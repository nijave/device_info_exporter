package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/VictoriaMetrics/metrics"
	zfs "github.com/bicomsystems/go-libzfs"
	udev "github.com/farjump/go-libudev"
	"github.com/prometheus/client_golang/prometheus"
	"io"
	"k8s.io/klog/v2"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var (
	Namespace = "device"

	allowedUdevPropertiesSimple = &map[string]string{
		"devname": "device",
		"devpath": "path",
		"major":   "",
		"minor":   "",
	}

	allowedUdevProperties = &map[string]string{
		"devname":   "device",
		"devpath":   "path",
		"major":     "",
		"minor":     "",
		"id_bus":    "bus",
		"scsi_type": "type",
		//"scsi_model":         "",
		//"scsi_ident_serial":  "",
		"id_model": "model",
		// This is the same as wwn for scsi devices
		//"id_serial":          "serial",
		"id_scsi_serial":     "serial",
		"id_path":            "id",
		"id_wwn":             "wwn",
		"id_fs_uuid":         "fs_uuid",
		"id_fs_type":         "fs_type",
		"id_part_table_type": "part_table_type",
	}
)

func metricString(namespace, subsystem, name string, labels prometheus.Labels) string {
	labelPairs := make([]string, 0)
	for k, v := range labels {
		labelPairs = append(labelPairs, fmt.Sprintf("%s=\"%s\"", k, v))
	}
	return fmt.Sprintf(`%s_%s_%s{%s}`, namespace, subsystem, name, strings.Join(labelPairs, ","))
}

func labelsForDevice(dev *udev.Device, labelMap *map[string]string) prometheus.Labels {
	allowedLabels := *labelMap
	labels := prometheus.Labels{}

	for k, newKey := range allowedLabels {
		if newKey != "" {
			k = newKey
		}
		labels[k] = ""
	}

	for k, v := range dev.Properties() {
		k = strings.ToLower(k)

		if _, ok := allowedLabels[k]; !ok {
			continue
		}

		if newKey, ok := allowedLabels[k]; ok && newKey != "" {
			k = newKey
		}

		labels[k] = v
	}

	return labels
}

func writeDeviceMapperGauges(w io.Writer) {
	ctx := context.Background()
	ctxTimeout, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()

	cmd, err := exec.CommandContext(
		ctxTimeout,
		"dmsetup", "info",
		"-co", "name,major,minor,attr,uuid",
		"--noheadings",
		"--sep", "*",
	).Output()

	if err != nil {
		klog.ErrorS(err, "error executing dmsetup info")
		return
	}

	output := strings.Trim(string(cmd), "\n\r ")

	if output == "No devices found" {
		klog.Warningln("no devices found")
	}

	devices := strings.Split(output, "\n")

	for _, deviceLine := range devices {
		device := strings.Split(deviceLine, "*")
		klog.InfoS("found device", "device", device)
		metrics.WriteGaugeUint64(
			w,
			metricString(Namespace, "devicemapper", "info", prometheus.Labels{
				"name":  device[0],
				"major": device[1],
				"minor": device[2],
				"attr":  device[3],
				"uuid":  device[4],
			}),
			1,
		)
	}
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
		metrics.WriteGaugeUint64(
			w,
			metricString(Namespace, "lsblk", "info", prometheus.Labels{
				"name":    dev.Name,
				"path":    dev.Path,
				"major":   strings.Split(dev.MajorMinor, ":")[0],
				"minor":   strings.Split(dev.MajorMinor, ":")[1],
				"type":    dev.Type,
				"fs_type": dev.FsType,
				"label":   dev.Label,
				"uuid":    dev.UUID,
				"serial":  dev.Serial,
				"wwn":     dev.WWN,
			}),
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
				metrics.WriteGaugeUint64(
					w,
					metricString(Namespace, "udev", "link_info", prometheus.Labels{
						"path":      dev.Devpath(),
						"device":    dev.Sysname(),
						"link":      link,
						"link_name": filepath.Base(link),
					}),
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

			for _, subTree := range vdevTree.Devices {
				devices = append([]zfs.VDevTree{subTree}, devices...)
			}

			if len(vdevTree.Devices) == 0 && vdevTree.GUID != 0 {
				deviceNameParts := strings.Split(vdevTree.Name, "/")
				metrics.WriteGaugeUint64(
					w,
					metricString(Namespace, "zfs", "info", prometheus.Labels{
						"type":   string(vdevTree.Type),
						"pool":   poolName,
						"path":   vdevTree.Name,
						"device": deviceNameParts[len(deviceNameParts)-1],
						"guid":   fmt.Sprintf("%d", vdevTree.GUID),
					}),
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
		//writeDeviceMapperGauges(w)
		writeLsblkGauges(w)
		writeUdevGauges(w)
		writeZfsGauges(w)
	})
	if err := http.ListenAndServe(":9133", nil); err != nil {
		klog.ErrorS(err, "failed to start http server")
	}
}
