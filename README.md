# Device Info Exporter
Exports additional metadata from ZFS pools, `lsblk`, and `udev` which can be joined to `node_exporter` metrics for additional context.
It is recommended to create recording rules to enrich data coming in. Using `major:minor` is the most reliable way to correlate data.

## Usage
```
# Listens on 9133/tcp
./device_info_exporter
```

## Disk I/O with devicemapper name
```
max(
    rate((
        label_replace(node_disk_writes_completed_total, "node", "$1", "instance", "(.+):.*") 
        * on(node, job, device) group_left(name, major, minor, wwn, path) 
            label_replace(device_lsblk_info, "node", "$1", "instance", "(.+):.*")
    )[$__rate_interval:])
) by (cluster, instance, job, major, minor, name, path)
```

## ZFS Example
Block device bandwidth by zpool devices. Additional filtering may be needed on `link` or `instance` to prevent overlapping series.
```
sort_desc(sum(rate(
    (node_disk_read_bytes_total{instance="my-node:9100"}
    * on(device) group_left(pool) label_replace(
        label_replace(
            device_zfs_info,
            "link",
            "$1",
            "path",
            "(.*)"
        ) * on(link, instance) group_left(device) (device_udev_link_info{link!~"/dev/disk/by-(part)?label/.*", link!~"/dev/disk/by-(part)?uuid/.*"}),
        "device",
        "$1",
        "device",
        "([a-z]+)[0-9]*"
    )
    )[5m:]
)) by (instance, pool, device))
```