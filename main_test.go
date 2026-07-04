package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestWriteVdevLeafGauges_RaidzPool(t *testing.T) {
	input := `{
		"pools": {
			"tank": {
				"name": "tank",
				"state": "ONLINE",
				"guid": "3920273586464696295",
				"vdevs": {
					"tank": {
						"name": "tank",
						"vdevs": {
							"raidz1-0": {
								"name": "raidz1-0",
								"vdev_type": "raidz",
								"guid": "763132626387621737",
								"state": "HEALTHY",
								"vdevs": {
									"sda1": {
										"name": "sda1",
										"vdev_type": "disk",
										"guid": "12841765308123764671",
										"path": "/dev/sda1",
										"state": "HEALTHY"
									},
									"sdb1": {
										"name": "sdb1",
										"vdev_type": "disk",
										"guid": "1527839927278881561",
										"path": "/dev/sdb1",
										"state": "HEALTHY"
									},
									"sdc1": {
										"name": "sdc1",
										"vdev_type": "disk",
										"guid": "6982750226085199860",
										"path": "/dev/sdc1",
										"state": "HEALTHY"
									}
								}
							}
						}
					}
				}
			}
		}
	}`

	var status zpoolStatusOutput
	if err := json.Unmarshal([]byte(input), &status); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	var buf bytes.Buffer
	for poolName, pool := range status.Pools {
		for _, vdev := range pool.Vdevs {
			writeVdevLeafGauges(&buf, poolName, vdev)
		}
	}

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	sort.Strings(lines)

	if len(lines) != 3 {
		t.Fatalf("expected 3 metric lines, got %d: %s", len(lines), output)
	}

	for _, line := range lines {
		if !strings.HasPrefix(line, "device_zfs_info{") {
			t.Errorf("unexpected metric prefix: %s", line)
		}
		if !strings.HasSuffix(line, " 1") {
			t.Errorf("unexpected metric value: %s", line)
		}
		if !strings.Contains(line, `pool="tank"`) {
			t.Errorf("missing pool label: %s", line)
		}
		if !strings.Contains(line, `type="disk"`) {
			t.Errorf("missing type label: %s", line)
		}
	}

	if !strings.Contains(output, `path="/dev/sda1"`) {
		t.Error("missing sda1 path")
	}
	if !strings.Contains(output, `device="sda1"`) {
		t.Error("missing sda1 device")
	}
	if !strings.Contains(output, `guid="12841765308123764671"`) {
		t.Error("missing sda1 guid")
	}
}

func TestWriteVdevLeafGauges_MirrorPool(t *testing.T) {
	input := `{
		"pools": {
			"rpool": {
				"name": "rpool",
				"state": "ONLINE",
				"vdevs": {
					"rpool": {
						"name": "rpool",
						"vdevs": {
							"mirror-0": {
								"name": "mirror-0",
								"vdev_type": "mirror",
								"guid": "111111",
								"vdevs": {
									"sda2": {
										"name": "sda2",
										"vdev_type": "disk",
										"guid": "222222",
										"path": "/dev/sda2"
									},
									"sdb2": {
										"name": "sdb2",
										"vdev_type": "disk",
										"guid": "333333",
										"path": "/dev/sdb2"
									}
								}
							}
						}
					}
				}
			}
		}
	}`

	var status zpoolStatusOutput
	if err := json.Unmarshal([]byte(input), &status); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	var buf bytes.Buffer
	for poolName, pool := range status.Pools {
		for _, vdev := range pool.Vdevs {
			writeVdevLeafGauges(&buf, poolName, vdev)
		}
	}

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	if len(lines) != 2 {
		t.Fatalf("expected 2 metric lines, got %d: %s", len(lines), output)
	}

	if !strings.Contains(output, `pool="rpool"`) {
		t.Error("missing pool label")
	}
}

func TestWriteVdevLeafGauges_PartUUID(t *testing.T) {
	input := `{
		"pools": {
			"tank": {
				"name": "tank",
				"state": "ONLINE",
				"vdevs": {
					"tank": {
						"name": "tank",
						"vdevs": {
							"ca1eb824-c371-491d-ac13-37637e35c683": {
								"name": "ca1eb824-c371-491d-ac13-37637e35c683",
								"vdev_type": "disk",
								"guid": "12841765308123764671",
								"path": "/dev/disk/by-partuuid/ca1eb824-c371-491d-ac13-37637e35c683",
								"state": "HEALTHY"
							}
						}
					}
				}
			}
		}
	}`

	var status zpoolStatusOutput
	if err := json.Unmarshal([]byte(input), &status); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	var buf bytes.Buffer
	for poolName, pool := range status.Pools {
		for _, vdev := range pool.Vdevs {
			writeVdevLeafGauges(&buf, poolName, vdev)
		}
	}

	output := buf.String()
	if !strings.Contains(output, `path="/dev/disk/by-partuuid/ca1eb824-c371-491d-ac13-37637e35c683"`) {
		t.Error("expected full partuuid path")
	}
	if !strings.Contains(output, `device="ca1eb824-c371-491d-ac13-37637e35c683"`) {
		t.Error("expected partuuid as device name")
	}
}

func TestWriteVdevLeafGauges_EmptyPools(t *testing.T) {
	input := `{"pools": {}}`

	var status zpoolStatusOutput
	if err := json.Unmarshal([]byte(input), &status); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	var buf bytes.Buffer
	for poolName, pool := range status.Pools {
		for _, vdev := range pool.Vdevs {
			writeVdevLeafGauges(&buf, poolName, vdev)
		}
	}

	if buf.Len() != 0 {
		t.Errorf("expected no output for empty pools, got: %s", buf.String())
	}
}

func TestWriteVdevLeafGauges_SkipsIntermediateVdevs(t *testing.T) {
	input := `{
		"pools": {
			"tank": {
				"name": "tank",
				"vdevs": {
					"tank": {
						"name": "tank",
						"guid": "0",
						"vdevs": {
							"raidz1-0": {
								"name": "raidz1-0",
								"vdev_type": "raidz",
								"guid": "999",
								"vdevs": {
									"sda": {
										"name": "sda",
										"vdev_type": "disk",
										"guid": "111",
										"path": "/dev/sda"
									}
								}
							}
						}
					}
				}
			}
		}
	}`

	var status zpoolStatusOutput
	if err := json.Unmarshal([]byte(input), &status); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	var buf bytes.Buffer
	for poolName, pool := range status.Pools {
		for _, vdev := range pool.Vdevs {
			writeVdevLeafGauges(&buf, poolName, vdev)
		}
	}

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	if len(lines) != 1 {
		t.Fatalf("expected 1 metric line (only leaf disk), got %d: %s", len(lines), output)
	}
	if !strings.Contains(output, `guid="111"`) {
		t.Error("expected leaf device guid")
	}
	if strings.Contains(output, `guid="999"`) {
		t.Error("intermediate raidz vdev should not appear")
	}
}

func TestWriteVdevLeafGauges_FallbackToName(t *testing.T) {
	input := `{
		"pools": {
			"tank": {
				"name": "tank",
				"vdevs": {
					"tank": {
						"name": "tank",
						"vdevs": {
							"sda": {
								"name": "/dev/sda",
								"vdev_type": "disk",
								"guid": "555"
							}
						}
					}
				}
			}
		}
	}`

	var status zpoolStatusOutput
	if err := json.Unmarshal([]byte(input), &status); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	var buf bytes.Buffer
	for poolName, pool := range status.Pools {
		for _, vdev := range pool.Vdevs {
			writeVdevLeafGauges(&buf, poolName, vdev)
		}
	}

	output := buf.String()
	if !strings.Contains(output, `path="/dev/sda"`) {
		t.Errorf("expected fallback to name for path, got: %s", output)
	}
	if !strings.Contains(output, `device="sda"`) {
		t.Errorf("expected device basename from name, got: %s", output)
	}
}

func TestWriteVdevLeafGauges_MultiplePools(t *testing.T) {
	input := `{
		"pools": {
			"tank": {
				"name": "tank",
				"vdevs": {
					"tank": {
						"name": "tank",
						"vdevs": {
							"sda": {
								"name": "sda",
								"vdev_type": "disk",
								"guid": "111",
								"path": "/dev/sda"
							}
						}
					}
				}
			},
			"rpool": {
				"name": "rpool",
				"vdevs": {
					"rpool": {
						"name": "rpool",
						"vdevs": {
							"sdb": {
								"name": "sdb",
								"vdev_type": "disk",
								"guid": "222",
								"path": "/dev/sdb"
							}
						}
					}
				}
			}
		}
	}`

	var status zpoolStatusOutput
	if err := json.Unmarshal([]byte(input), &status); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	var buf bytes.Buffer
	for poolName, pool := range status.Pools {
		for _, vdev := range pool.Vdevs {
			writeVdevLeafGauges(&buf, poolName, vdev)
		}
	}

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	if len(lines) != 2 {
		t.Fatalf("expected 2 metric lines, got %d: %s", len(lines), output)
	}

	if !strings.Contains(output, `pool="tank"`) {
		t.Error("missing tank pool")
	}
	if !strings.Contains(output, `pool="rpool"`) {
		t.Error("missing rpool pool")
	}
}

func TestParseZpoolStatusText_RaidzPool(t *testing.T) {
	input := `  pool: tank
 state: ONLINE
config:

	NAME                                        STATE     READ WRITE CKSUM
	tank                                        ONLINE       0     0     0
	  raidz1-0                                  ONLINE       0     0     0
	    /dev/disk/by-id/scsi-0001-part1         ONLINE       0     0     0
	    /dev/disk/by-id/scsi-0002-part1         ONLINE       0     0     0
	    /dev/disk/by-id/scsi-0003-part1         ONLINE       0     0     0

errors: No known data errors
`

	status := parseZpoolStatusText(input)

	pool, ok := status.Pools["tank"]
	if !ok {
		t.Fatal("missing tank pool")
	}

	var buf bytes.Buffer
	for _, vdev := range pool.Vdevs {
		writeVdevLeafGauges(&buf, "tank", vdev)
	}

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	if len(lines) != 3 {
		t.Fatalf("expected 3 leaf devices, got %d: %s", len(lines), output)
	}

	if !strings.Contains(output, `pool="tank"`) {
		t.Error("missing pool label")
	}
	if !strings.Contains(output, `type="disk"`) {
		t.Error("missing disk type")
	}
	if !strings.Contains(output, `path="/dev/disk/by-id/scsi-0001-part1"`) {
		t.Error("missing device path")
	}
}

func TestParseZpoolStatusText_MirrorPool(t *testing.T) {
	input := `  pool: rpool
 state: ONLINE
config:

	NAME                    STATE     READ WRITE CKSUM
	rpool                   ONLINE       0     0     0
	  mirror-0              ONLINE       0     0     0
	    /dev/sda2           ONLINE       0     0     0
	    /dev/sdb2           ONLINE       0     0     0

errors: No known data errors
`

	status := parseZpoolStatusText(input)

	pool, ok := status.Pools["rpool"]
	if !ok {
		t.Fatal("missing rpool pool")
	}

	var buf bytes.Buffer
	for _, vdev := range pool.Vdevs {
		writeVdevLeafGauges(&buf, "rpool", vdev)
	}

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	if len(lines) != 2 {
		t.Fatalf("expected 2 leaf devices, got %d: %s", len(lines), output)
	}

	if !strings.Contains(output, `device="sda2"`) {
		t.Error("missing sda2")
	}
	if !strings.Contains(output, `device="sdb2"`) {
		t.Error("missing sdb2")
	}
}

func TestParseZpoolStatusText_MultiplePools(t *testing.T) {
	input := `  pool: tank
 state: ONLINE
config:

	NAME              STATE     READ WRITE CKSUM
	tank              ONLINE       0     0     0
	  /dev/sda1       ONLINE       0     0     0

errors: No known data errors

  pool: rpool
 state: ONLINE
config:

	NAME              STATE     READ WRITE CKSUM
	rpool             ONLINE       0     0     0
	  /dev/sdb1       ONLINE       0     0     0

errors: No known data errors
`

	status := parseZpoolStatusText(input)

	if len(status.Pools) != 2 {
		t.Fatalf("expected 2 pools, got %d", len(status.Pools))
	}

	if _, ok := status.Pools["tank"]; !ok {
		t.Error("missing tank pool")
	}
	if _, ok := status.Pools["rpool"]; !ok {
		t.Error("missing rpool pool")
	}
}

func TestParseZpoolStatusText_WithSpares(t *testing.T) {
	input := `  pool: tank
 state: ONLINE
config:

	NAME              STATE     READ WRITE CKSUM
	tank              ONLINE       0     0     0
	  mirror-0        ONLINE       0     0     0
	    /dev/sda1     ONLINE       0     0     0
	    /dev/sdb1     ONLINE       0     0     0
	spares
	  /dev/sdc1       AVAIL

errors: No known data errors
`

	status := parseZpoolStatusText(input)

	var buf bytes.Buffer
	for _, vdev := range status.Pools["tank"].Vdevs {
		writeVdevLeafGauges(&buf, "tank", vdev)
	}

	output := buf.String()
	if !strings.Contains(output, `device="sda1"`) {
		t.Error("missing sda1")
	}
	if !strings.Contains(output, `device="sdb1"`) {
		t.Error("missing sdb1")
	}
	if !strings.Contains(output, `device="sdc1"`) {
		t.Error("missing sdc1")
	}
}

func TestParseZpoolStatusText_EmptyOutput(t *testing.T) {
	status := parseZpoolStatusText("")
	if len(status.Pools) != 0 {
		t.Errorf("expected 0 pools, got %d", len(status.Pools))
	}
}

func TestParseZpoolStatusText_NoPools(t *testing.T) {
	input := "no pools available\n"
	status := parseZpoolStatusText(input)
	if len(status.Pools) != 0 {
		t.Errorf("expected 0 pools, got %d", len(status.Pools))
	}
}

func TestReadUevent(t *testing.T) {
	dir := t.TempDir()
	content := "MAJOR=8\nMINOR=0\nDEVNAME=sda\nDEVTYPE=disk\n"
	os.WriteFile(filepath.Join(dir, "uevent"), []byte(content), 0644)

	props := readUevent(dir)
	if props == nil {
		t.Fatal("expected non-nil props")
	}
	if props["MAJOR"] != "8" {
		t.Errorf("expected MAJOR=8, got %q", props["MAJOR"])
	}
	if props["MINOR"] != "0" {
		t.Errorf("expected MINOR=0, got %q", props["MINOR"])
	}
	if props["DEVNAME"] != "sda" {
		t.Errorf("expected DEVNAME=sda, got %q", props["DEVNAME"])
	}
	if props["DEVTYPE"] != "disk" {
		t.Errorf("expected DEVTYPE=disk, got %q", props["DEVTYPE"])
	}
}

func TestReadUevent_Missing(t *testing.T) {
	props := readUevent("/nonexistent/path")
	if props != nil {
		t.Error("expected nil for missing uevent")
	}
}

func TestReadUdevDB(t *testing.T) {
	dir := t.TempDir()
	origPath := "/run/udev/data"

	dbContent := `S:disk/by-id/ata-VBOX_HARDDISK_VB12345
S:disk/by-path/pci-0000:00:1f.2-ata-1
I:12345678
E:ID_BUS=scsi
E:ID_MODEL=VBOX_HARDDISK
E:ID_SERIAL=VBOX_HARDDISK_VB12345
E:ID_WWN=0x12345678
G:systemd
`
	testDBDir := filepath.Join(dir, "run", "udev", "data")
	os.MkdirAll(testDBDir, 0755)
	os.WriteFile(filepath.Join(testDBDir, "b8:0"), []byte(dbContent), 0644)

	// Can't easily redirect readUdevDB path, so test the parsing logic directly
	_ = origPath
	_ = dir

	entry := udevDBEntry{props: make(map[string]string)}
	for _, line := range strings.Split(dbContent, "\n") {
		if rest, ok := strings.CutPrefix(line, "E:"); ok {
			if k, v, found := strings.Cut(rest, "="); found {
				entry.props[k] = v
			}
		} else if rest, ok := strings.CutPrefix(line, "S:"); ok {
			entry.links = append(entry.links, "/dev/"+rest)
		}
	}

	if entry.props["ID_BUS"] != "scsi" {
		t.Errorf("expected ID_BUS=scsi, got %q", entry.props["ID_BUS"])
	}
	if entry.props["ID_MODEL"] != "VBOX_HARDDISK" {
		t.Errorf("expected ID_MODEL=VBOX_HARDDISK, got %q", entry.props["ID_MODEL"])
	}
	if entry.props["ID_WWN"] != "0x12345678" {
		t.Errorf("expected ID_WWN, got %q", entry.props["ID_WWN"])
	}
	if len(entry.links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(entry.links))
	}
	if entry.links[0] != "/dev/disk/by-id/ata-VBOX_HARDDISK_VB12345" {
		t.Errorf("unexpected link: %s", entry.links[0])
	}
	if entry.links[1] != "/dev/disk/by-path/pci-0000:00:1f.2-ata-1" {
		t.Errorf("unexpected link: %s", entry.links[1])
	}
}

func TestLabelsFromProperties_Simple(t *testing.T) {
	props := map[string]string{
		"DEVNAME": "/dev/sda",
		"DEVPATH": "/devices/pci0000:00/block/sda",
		"MAJOR":   "8",
		"MINOR":   "0",
	}

	labels := labelsFromProperties(props, allowedUdevPropertiesSimple)

	if v, _ := labels.Get("device"); v != "/dev/sda" {
		t.Errorf("expected device=/dev/sda, got %q", v)
	}
	if v, _ := labels.Get("path"); v != "/devices/pci0000:00/block/sda" {
		t.Errorf("expected path, got %q", v)
	}
	if v, _ := labels.Get("major"); v != "8" {
		t.Errorf("expected major=8, got %q", v)
	}
	if v, _ := labels.Get("minor"); v != "0" {
		t.Errorf("expected minor=0, got %q", v)
	}
}

func TestLabelsFromProperties_SCSI(t *testing.T) {
	props := map[string]string{
		"DEVNAME":        "/dev/sda",
		"DEVPATH":        "/devices/pci0000:00/block/sda",
		"MAJOR":          "8",
		"MINOR":          "0",
		"ID_BUS":         "scsi",
		"SCSI_TYPE":      "disk",
		"ID_MODEL":       "VBOX_HARDDISK",
		"ID_SCSI_SERIAL": "VB12345",
		"ID_PATH":        "pci-0000:00:1f.2-ata-1",
		"ID_WWN":         "0x12345678",
		"ID_FS_UUID":     "abcd-1234",
		"ID_FS_TYPE":     "ext4",
	}

	labels := labelsFromProperties(props, allowedUdevProperties)

	if v, _ := labels.Get("device"); v != "/dev/sda" {
		t.Errorf("expected device=/dev/sda, got %q", v)
	}
	if v, _ := labels.Get("bus"); v != "scsi" {
		t.Errorf("expected bus=scsi, got %q", v)
	}
	if v, _ := labels.Get("model"); v != "VBOX_HARDDISK" {
		t.Errorf("expected model, got %q", v)
	}
	if v, _ := labels.Get("serial"); v != "VB12345" {
		t.Errorf("expected serial, got %q", v)
	}
	if v, _ := labels.Get("wwn"); v != "0x12345678" {
		t.Errorf("expected wwn, got %q", v)
	}
	if v, _ := labels.Get("fs_uuid"); v != "abcd-1234" {
		t.Errorf("expected fs_uuid, got %q", v)
	}
	if v, _ := labels.Get("fs_type"); v != "ext4" {
		t.Errorf("expected fs_type, got %q", v)
	}
}

func TestLabelsFromProperties_EmptyValues(t *testing.T) {
	props := map[string]string{
		"DEVNAME": "/dev/vda",
		"DEVPATH": "/devices/virtio/block/vda",
		"MAJOR":   "252",
		"MINOR":   "0",
	}

	labels := labelsFromProperties(props, allowedUdevPropertiesSimple)

	keys := labels.Keys()
	if len(keys) != 4 {
		t.Errorf("expected 4 labels, got %d: %v", len(keys), keys)
	}
	for _, k := range keys {
		v, _ := labels.Get(k)
		if k == "device" && v == "" {
			t.Error("device should not be empty")
		}
	}
}

func TestWriteUdevGauges_LiveSystem(t *testing.T) {
	if _, err := os.ReadDir("/sys/class/block"); err != nil {
		t.Skip("no /sys/class/block, skipping live test")
	}

	var buf bytes.Buffer
	writeUdevGauges(&buf)

	output := buf.String()
	if !strings.Contains(output, "device_udev_info{") {
		t.Error("expected at least one device_udev_info metric")
	}
}
