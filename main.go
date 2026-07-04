package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/VictoriaMetrics/metrics"
	"io"
	"k8s.io/klog/v2"
	"net/http"
	"os"
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

func labelsFromProperties(props map[string]string, labelMap *OrderedDict) *OrderedDict {
	labels := NewOrderedDict()

	for _, k := range labelMap.Keys() {
		newKey, _ := labelMap.Get(k)
		if newKey != "" {
			labels.Set(newKey, "")
		} else {
			labels.Set(k, "")
		}
	}

	for k, v := range props {
		k = strings.ToLower(k)
		if _, ok := labelMap.Get(k); !ok {
			continue
		}
		if newKey, ok := labelMap.Get(k); ok && newKey != "" {
			k = newKey
		}
		labels.Set(k, v)
	}

	return labels
}

func readUevent(sysPath string) map[string]string {
	data, err := os.ReadFile(filepath.Join(sysPath, "uevent"))
	if err != nil {
		return nil
	}
	props := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			props[k] = v
		}
	}
	return props
}

type udevDBEntry struct {
	props map[string]string
	links []string
}

func readUdevDB(major, minor string) udevDBEntry {
	entry := udevDBEntry{props: make(map[string]string)}
	data, err := os.ReadFile(fmt.Sprintf("/run/udev/data/b%s:%s", major, minor))
	if err != nil {
		return entry
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "E:"); ok {
			if k, v, found := strings.Cut(rest, "="); found {
				entry.props[k] = v
			}
		} else if rest, ok := strings.CutPrefix(line, "S:"); ok {
			entry.links = append(entry.links, "/dev/"+rest)
		}
	}
	return entry
}

type blockDevice struct {
	Name       string `json:"kname"`
	Parent     string `json:"pkname"`
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
		// If the device is already top-level, set parent to itself
		// so these series can join to partitions and top-level devices
		// without extra work
		if dev.Parent == "" {
			dev.Parent = dev.Name
		}

		labels := NewOrderedDict()
		labels.Set("device", dev.Name)
		labels.Set("path", dev.Path)
		labels.Set("name", filepath.Base(dev.Path))
		labels.Set("parent", dev.Parent)
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

type Pv struct {
	Path        string `json:"pv_name"`
	VolumeGroup string `json:"vg_name"`
	Major       string `json:"pv_major"`
	Minor       string `json:"pv_minor"`
}

type PvList struct {
	Pvs []Pv `json:"pv"`
}

type LvmReport struct {
	Report []PvList `json:"report"`
}

func writeLvmGauges(w io.Writer) {
	ctx := context.Background()
	timeout, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()

	cmd, err := exec.CommandContext(timeout,
		"pvs", "-o", "name,vg_name,major,minor", "--reportformat", "json",
	).Output()

	if err != nil {
		klog.ErrorS(err, "error executing pvs")
		return
	}

	var report LvmReport
	err = json.Unmarshal(cmd, &report)
	if err != nil {
		klog.ErrorS(err, "error unmarshalling pvs output", "stdout", cmd)
		return
	}

	for _, dev := range report.Report[0].Pvs {
		labels := NewOrderedDict()
		labels.Set("path", dev.Path)
		labels.Set("name", filepath.Base(dev.Path))
		labels.Set("volume_group", dev.VolumeGroup)
		labels.Set("major", dev.Major)
		labels.Set("minor", dev.Minor)
		metrics.WriteGaugeUint64(
			w,
			metricString(Namespace, "pv", "info", labels),
			1,
		)
	}
}

func writeUdevGauges(w io.Writer) {
	entries, err := os.ReadDir("/sys/class/block")
	if err != nil {
		klog.ErrorS(err, "error reading /sys/class/block")
		return
	}

	for _, entry := range entries {
		sysPath := filepath.Join("/sys/class/block", entry.Name())

		uevent := readUevent(sysPath)
		if uevent == nil {
			continue
		}

		major := uevent["MAJOR"]
		minor := uevent["MINOR"]

		realPath, err := filepath.EvalSymlinks(sysPath)
		if err != nil {
			continue
		}
		devpath := strings.TrimPrefix(realPath, "/sys")

		udevDB := readUdevDB(major, minor)

		allProps := make(map[string]string)
		for k, v := range uevent {
			allProps[k] = v
		}
		for k, v := range udevDB.props {
			allProps[k] = v
		}

		devname := allProps["DEVNAME"]
		if !strings.HasPrefix(devname, "/") {
			devname = "/dev/" + devname
		}
		allProps["DEVNAME"] = devname
		allProps["DEVPATH"] = devpath

		labelMap := allowedUdevPropertiesSimple
		if strings.EqualFold(allProps["ID_BUS"], "scsi") {
			labelMap = allowedUdevProperties
		}
		labels := labelsFromProperties(allProps, labelMap)
		metrics.WriteGaugeUint64(
			w,
			metricString(Namespace, "udev", "info", labels),
			1,
		)

		for _, link := range udevDB.links {
			linkLabels := NewOrderedDict()
			linkLabels.Set("path", devpath)
			linkLabels.Set("device", entry.Name())
			linkLabels.Set("link", link)
			linkLabels.Set("link_name", filepath.Base(link))
			metrics.WriteGaugeUint64(
				w,
				metricString(Namespace, "udev", "link_info", linkLabels),
				1,
			)
		}
	}
}

type zpoolStatusOutput struct {
	Pools map[string]zpoolPool `json:"pools"`
}

type zpoolPool struct {
	Name  string               `json:"name"`
	Vdevs map[string]zpoolVdev `json:"vdevs"`
}

type zpoolVdev struct {
	Name     string               `json:"name"`
	VdevType string               `json:"vdev_type"`
	GUID     string               `json:"guid"`
	Path     string               `json:"path"`
	Vdevs    map[string]zpoolVdev `json:"vdevs"`
}

func writeVdevLeafGauges(w io.Writer, poolName string, vdev zpoolVdev) {
	for _, child := range vdev.Vdevs {
		writeVdevLeafGauges(w, poolName, child)
	}

	if len(vdev.Vdevs) == 0 && vdev.VdevType != "root" && vdev.GUID != "0" {
		path := vdev.Path
		if path == "" {
			path = vdev.Name
		}
		deviceNameParts := strings.Split(path, "/")
		labels := NewOrderedDict()
		labels.Set("type", vdev.VdevType)
		labels.Set("pool", poolName)
		labels.Set("path", path)
		labels.Set("device", deviceNameParts[len(deviceNameParts)-1])
		labels.Set("guid", vdev.GUID)
		metrics.WriteGaugeUint64(
			w,
			metricString(Namespace, "zfs", "info", labels),
			1,
		)
	}
}

type vdevNode struct {
	name     string
	vdevType string
	children []*vdevNode
}

func (n *vdevNode) toZpoolVdev() zpoolVdev {
	vdevs := make(map[string]zpoolVdev, len(n.children))
	for _, child := range n.children {
		vdevs[child.name] = child.toZpoolVdev()
	}
	return zpoolVdev{
		Name:     n.name,
		VdevType: n.vdevType,
		Path:     n.name,
		Vdevs:    vdevs,
	}
}

func classifyVdevName(name, poolName string) string {
	switch {
	case strings.HasPrefix(name, "mirror"):
		return "mirror"
	case strings.HasPrefix(name, "raidz"):
		return "raidz"
	case strings.HasPrefix(name, "spare"):
		return "spare"
	case strings.HasPrefix(name, "log"):
		return "log"
	case strings.HasPrefix(name, "cache"):
		return "cache"
	case strings.HasPrefix(name, "special"):
		return "special"
	case strings.HasPrefix(name, "dedup"):
		return "dedup"
	case name == poolName:
		return "root"
	default:
		return "disk"
	}
}

func parseZpoolStatusText(output string) zpoolStatusOutput {
	result := zpoolStatusOutput{Pools: make(map[string]zpoolPool)}
	var currentPool string
	inConfig := false

	type stackEntry struct {
		indent int
		node   *vdevNode
	}
	var stack []stackEntry
	var roots []*vdevNode

	flushPool := func() {
		if currentPool == "" {
			return
		}
		pool := zpoolPool{
			Name:  currentPool,
			Vdevs: make(map[string]zpoolVdev, len(roots)),
		}
		for _, root := range roots {
			pool.Vdevs[root.name] = root.toZpoolVdev()
		}
		result.Pools[currentPool] = pool
		roots = nil
		stack = nil
	}

	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "pool:") {
			flushPool()
			currentPool = strings.TrimSpace(strings.TrimPrefix(trimmed, "pool:"))
			inConfig = false
			continue
		}

		if strings.HasPrefix(trimmed, "config:") {
			inConfig = true
			continue
		}

		if !inConfig || currentPool == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "NAME") || trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "errors:") {
			inConfig = false
			continue
		}

		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}

		name := fields[0]
		node := &vdevNode{
			name:     name,
			vdevType: classifyVdevName(name, currentPool),
		}

		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}

		if len(stack) == 0 {
			roots = append(roots, node)
		} else {
			stack[len(stack)-1].node.children = append(stack[len(stack)-1].node.children, node)
		}
		stack = append(stack, stackEntry{indent: indent, node: node})
	}

	flushPool()
	return result
}

func writeZfsGauges(w io.Writer) {
	ctx := context.Background()
	timeout, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()

	cmd, err := exec.CommandContext(timeout,
		"zpool", "status", "-j", "-P",
	).Output()

	if err != nil {
		klog.V(2).InfoS("zpool status -j failed, trying text mode", "err", err)
		cmd, err = exec.CommandContext(timeout,
			"zpool", "status", "-P",
		).Output()
		if err != nil {
			klog.ErrorS(err, "error executing zpool status")
			return
		}
		status := parseZpoolStatusText(string(cmd))
		for poolName, pool := range status.Pools {
			for _, vdev := range pool.Vdevs {
				writeVdevLeafGauges(w, poolName, vdev)
			}
		}
		return
	}

	var status zpoolStatusOutput
	err = json.Unmarshal(cmd, &status)
	if err != nil {
		klog.ErrorS(err, "error unmarshalling zpool status output", "stdout", cmd)
		return
	}

	for poolName, pool := range status.Pools {
		for _, vdev := range pool.Vdevs {
			writeVdevLeafGauges(w, poolName, vdev)
		}
	}
}

func main() {
	http.HandleFunc("/metrics", func(w http.ResponseWriter, req *http.Request) {
		klog.InfoS("handling request", "src", req.RemoteAddr)
		writeLsblkGauges(w)
		writeLvmGauges(w)
		writeUdevGauges(w)
		writeZfsGauges(w)
	})
	if err := http.ListenAndServe(":9133", nil); err != nil {
		klog.ErrorS(err, "failed to start http server")
	}
}
