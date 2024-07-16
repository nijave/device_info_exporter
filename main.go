package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/VictoriaMetrics/metrics"
	zfs "github.com/bicomsystems/go-libzfs"
	udev "github.com/farjump/go-libudev"
	"io"
	"k8s.io/klog/v2"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type OrderedDict struct {
	keys   []string
	values map[string]string
}

func NewOrderedDict() *OrderedDict {
	return &OrderedDict{
		keys:   []string{},
		values: make(map[string]string),
	}
}

func (od *OrderedDict) Set(key string, value string) {
	if _, exists := od.values[key]; !exists {
		od.keys = append(od.keys, key)
	}
	od.values[key] = value
}

func (od *OrderedDict) Get(key string) (string, bool) {
	value, exists := od.values[key]
	return value, exists
}

func (od *OrderedDict) Keys() []string {
	return od.keys
}

func allowedUdevPropertiesSimpleInit() *OrderedDict {
	od := NewOrderedDict()

	od.Set("devname", "device")
	od.Set("devpath", "path")
	od.Set("major", "")
	od.Set("minor", "")

	return od
}

func allowedUdevPropertiesInit() *OrderedDict {
	od := NewOrderedDict()

	// Adding key-value pairs to OrderedDict
	od.Set("devname", "device")
	od.Set("devpath", "path")
	od.Set("major", "")
	od.Set("minor", "")
	od.Set("id_bus", "bus")
	od.Set("scsi_type", "type")
	//od.Set("scsi_model", "")
	//od.Set("scsi_ident_serial", "")
	od.Set("id_model", "model")
	// This is the same as wwn for scsi devices
	//od.Set("id_serial", "serial")
	od.Set("id_scsi_serial", "serial")
	od.Set("id_path", "id")
	od.Set("id_wwn", "wwn")
	od.Set("id_fs_uuid", "fs_uuid")
	od.Set("id_fs_type", "fs_type")
	od.Set("id_part_table_type", "part_table_type")

	return od
}

var (
	Namespace                   = "device"
	allowedUdevPropertiesSimple = allowedUdevPropertiesSimpleInit()
	allowedUdevProperties       = allowedUdevPropertiesInit()
)

func metricString(namespace, subsystem, name string, labels *OrderedDict) string {
	labelPairs := make([]string, 0)
	for _, k := range labels.Keys() {
		v, _ := labels.Get(k)
		labelPairs = append(labelPairs, fmt.Sprintf("%s=\"%s\"", k, v))
	}
	return fmt.Sprintf(`%s_%s_%s{%s}`, namespace, subsystem, name, strings.Join(labelPairs, ","))
}

func labelsForDevice(dev *udev.Device, labelMap *OrderedDict) *OrderedDict {
	allowedLabels := *labelMap
	labels := NewOrderedDict()

	for _, k := range allowedLabels.Keys() {
		newKey, _ := allowedLabels.Get(k)
		if newKey != "" {
			k = newKey
		}
		labels.Set(k, "")
	}

	for k, v := range dev.Properties() {
		k = strings.ToLower(k)

		if _, ok := allowedLabels.Get(k); !ok {
			continue
		}

		if newKey, ok := allowedLabels.Get(k); ok && newKey != "" {
			k = newKey
		}

		labels.Set(k, v)
	}

	return labels
}

type blockDevice struct {
	Name       string `json:"kname"`
	Path       string `json:"path"`
	MajorMinor string `json:"maj:min"`
	Type       string `json:"type"`
	FsType     string `json:"fstype"`
	Label      string `json:"label"`
	UUID       string `json:"uuid"`
	Serial     string `json:"serial"`
	WWN        string `json:"wwn"`
}

type blockDeviceList struct {
	BlockDevices []blockDevice `json:"blockdevices"`
}

func writeLsblkGauges(w io.Writer) {
	ctx := context.Background()
	timeout, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()

	cmd, err := exec.CommandContext(timeout,
		"lsblk", "-OJ", "--list",
		//"-o", "KNAME,PATH,MAJ:MIN,FSTYPE,LABEL,UUID,WWN,SERIAL",
	).Output()

	if err != nil {
		klog.ErrorS(err, "error executing lsblk")
		return
	}

	var blockDevices blockDeviceList
	err = json.Unmarshal(cmd, &blockDevices)
	if err != nil {
		klog.ErrorS(err, "error unmarshalling lsblk output", "stdout", cmd)
		return
	}

	for _, dev := range blockDevices.BlockDevices {
		labels := NewOrderedDict()
		labels.Set("device", dev.Name)
		labels.Set("path", dev.Path)
		labels.Set("name", filepath.Base(dev.Path))
		labels.Set("major", strings.Split(dev.MajorMinor, ":")[0])
		labels.Set("minor", strings.Split(dev.MajorMinor, ":")[1])
		labels.Set("type", dev.Type)
		labels.Set("fs_type", dev.FsType)
		labels.Set("label", dev.Label)
		labels.Set("uuid", dev.UUID)
		labels.Set("serial", dev.Serial)
		labels.Set("wwn", dev.WWN)
		metrics.WriteGaugeUint64(
			w,
			metricString(Namespace, "lsblk", "info", labels),
			1,
		)
	}

}

func writeUdevGauges(w io.Writer) {
	ud := &udev.Udev{}
	dsp, _ := ud.NewEnumerate().DeviceSyspaths()
	for _, path := range dsp {
		dev := ud.NewDeviceFromSyspath(path)

		// TODO: filter with the udev interface
		if dev.Subsystem() == "block" {
			labelMap := allowedUdevPropertiesSimple
			if dev.PropertyValue("ID_BUS") == "scsi" {
				labelMap = allowedUdevProperties
			}
			labels := labelsForDevice(dev, labelMap)
			metrics.WriteGaugeUint64(
				w,
				metricString(Namespace, "udev", "info", labels),
				1,
			)

			for link, _ := range dev.Devlinks() {
				labels := NewOrderedDict()
				labels.Set("path", dev.Devpath())
				labels.Set("device", dev.Sysname())
				labels.Set("link", link)
				labels.Set("link_name", filepath.Base(link))
				metrics.WriteGaugeUint64(
					w,
					metricString(Namespace, "udev", "link_info", labels),
					1,
				)
			}
		}

	}
}

func writeZfsGauges(w io.Writer) {
	pools, err := zfs.PoolOpenAll()
	if err != nil {
		klog.ErrorS(err, "zfs pool open failed")
		return
	}

	for _, pool := range pools {
		poolName, err := pool.Name()
		if err != nil {
			klog.ErrorS(err, "zfs pool name failed")
			pool.Close()
			continue
		}

		devices := make([]zfs.VDevTree, 1)

		vdevTree, err := pool.VDevTree()
		if err != nil {
			klog.ErrorS(err, "zfs pool root vdev tree failed", "pool", poolName)
			pool.Close()
			continue
		}

		devices = append(devices, vdevTree)

		for {
			vdevTree = devices[0]
			devices = devices[1:]

			var subTree zfs.VDevTree

			//if vdevTree.Path == "" {
			//	vdevTree.Path = poolName
			//}

			for _, subTree = range vdevTree.Devices {
				//subTree.Path = strings.Join([]string{vdevTree.Path, subTree.Name}, "/")
				devices = append([]zfs.VDevTree{subTree}, devices...)
			}

			for _, subTree = range vdevTree.L2Cache {
				//subTree.Path = strings.Join([]string{vdevTree.Path, subTree.Name}, "/")
				devices = append([]zfs.VDevTree{subTree}, devices...)
			}

			for _, subTree = range vdevTree.Spares {
				//subTree.Path = strings.Join([]string{vdevTree.Path, subTree.Name}, "/")
				devices = append([]zfs.VDevTree{subTree}, devices...)
			}

			if vdevTree.Logs != nil {
				//vdevTree.Logs.Path = strings.Join([]string{vdevTree.Path, vdevTree.Logs.Name}, "/")
				devices = append([]zfs.VDevTree{*vdevTree.Logs}, devices...)
			}

			if len(vdevTree.Devices) == 0 && vdevTree.GUID != 0 {
				deviceNameParts := strings.Split(vdevTree.Name, "/")
				labels := NewOrderedDict()
				labels.Set("type", string(vdevTree.Type))
				labels.Set("pool", poolName)
				labels.Set("path", vdevTree.Name)
				labels.Set("device", deviceNameParts[len(deviceNameParts)-1])
				labels.Set("guid", fmt.Sprintf("%d", vdevTree.GUID))
				metrics.WriteGaugeUint64(
					w,
					metricString(Namespace, "zfs", "info", labels),
					1,
				)
			}

			if len(devices) < 1 {
				break
			}
		}

		pool.Close()
	}
}

func main() {
	http.HandleFunc("/metrics", func(w http.ResponseWriter, req *http.Request) {
		klog.InfoS("handling request", "src", req.RemoteAddr)
		writeLsblkGauges(w)
		writeUdevGauges(w)
		writeZfsGauges(w)
	})
	if err := http.ListenAndServe(":9133", nil); err != nil {
		klog.ErrorS(err, "failed to start http server")
	}
}
