# Device Info Exporter

Device Info Exporter is a Prometheus metric enrichment tool for Linux. It exports stable device metadata that can be joined to metrics from other exporters, adding the context that operational metrics often do not carry on their own.

Use it to connect `node_exporter` disk metrics with:

- ZFS pools and vdevs
- LVM physical volumes and volume groups
- `lsblk` filesystem, partition, parent-device, serial, and WWN metadata
- udev identifiers, persistent links, bus/model information, and filesystem details

The exporter listens on port `9133` and exposes one gauge per discovered relationship. Labels are intended for Prometheus joins; `major` and `minor` are the most reliable correlation keys when they are available on both sides.

## Metrics

The `/metrics` endpoint currently exports:

- `device_lsblk_info`: block-device name, path, parent, type, filesystem, label, UUID, serial, WWN, and major/minor numbers
- `device_pv_info`: LVM physical-volume path, volume group, and major/minor numbers
- `device_udev_info`: udev device path, name, major/minor numbers, and selected hardware/filesystem identifiers
- `device_udev_link_info`: persistent udev links associated with a device
- `device_zfs_info`: leaf ZFS vdev path, device, pool, vdev type, and GUID

Metadata is read at scrape time from Linux tools and interfaces including `lsblk`, `pvs`, `zpool status`, `/sys/class/block`, and the udev database. Run the exporter with sufficient permissions to read the metadata needed in your environment.

## Usage

```sh
# Listens on 9133/tcp
./device_info_exporter
```

Configure Prometheus to scrape the exporter, alongside the exporters whose metrics you want to enrich:

```yaml
scrape_configs:
  - job_name: device_info
    static_configs:
      - targets: ["my-node:9133"]
```

The enrichment metrics and source metrics must have compatible target labels. The examples below use `label_replace` to normalize `instance`; adapt the target and label filters to your scrape configuration.

## Enrichment examples

### Join node_exporter disk I/O with block-device metadata

This adds the device name, path, parent, WWN, and major/minor labels from `device_lsblk_info` to a node_exporter disk metric. Keep the multiplication by `1` as the join operation; the info metric supplies labels while preserving the source value.

```promql
max by (cluster, instance, job, device, path, parent, wwn, major, minor) (
  label_replace(
    rate(node_disk_writes_completed_total[$__rate_interval]),
    "node", "$1", "instance", "(.+):.*"
  )
  * on (node, job, device) group_left (path, parent, wwn, major, minor)
    label_replace(
      device_lsblk_info,
      "node", "$1", "instance", "(.+):.*"
    )
)
```

### Join node_exporter disk bandwidth to ZFS pools

`device_zfs_info` identifies the leaf devices that make up each pool. `device_udev_link_info` bridges persistent ZFS paths to the device names reported by node_exporter. Filter links for the naming scheme used by your host, and include `instance` when multiple targets are combined.

```promql
sort_desc(
  sum by (instance, pool, device) (
    rate(node_disk_read_bytes_total{instance="my-node:9100"}[5m])
    * on (device) group_left (pool)
      label_replace(
        label_replace(
          device_zfs_info,
          "link", "$1", "path", "(.*)"
        )
        * on (link, instance) group_left (device)
          device_udev_link_info{
            link!~"/dev/disk/by-(part)?label/.*",
            link!~"/dev/disk/by-(part)?uuid/.*"
          },
        "device", "$1", "device", "([a-z]+)[0-9]*"
      )
  )
)
```

### Join node_exporter with LVM volume-group information

Use `major` and `minor` to associate node_exporter disk or filesystem series with `device_pv_info`, then group or alert by `volume_group`. The exact source metric and join labels depend on whether you are enriching a disk, partition, or filesystem series.

```promql
node_filesystem_size_bytes
  * on (instance, major, minor) group_left (path, name, volume_group)
    device_pv_info
```

### Add hardware details to disk metrics

Udev metadata is useful when dashboards or alerts need model, bus, WWN, serial, filesystem, or persistent-link context without duplicating that discovery logic in every query.

```promql
node_disk_read_bytes_total
  * on (instance, major, minor) group_left (model, bus, wwn, serial, id)
    device_udev_info
```

Prometheus joins require matching label values and cardinality. Check the labels emitted by your node_exporter version and adjust `on(...)`, `group_left(...)`, and any `label_replace` expressions accordingly. Recording rules are recommended for frequently used joins so dashboards and alerts do not repeat the same enrichment expressions.

## Development

Run the test suite with:

```sh
go test ./...
```
