# Device Info Exporter
Exports additional metadata from ZFS pools and `lsblk` which can be joined to `node_exporter` metrics for additional context.
It is recommended to create recording rules to enrich data coming in.

## ZFS Example
Block device bandwidth by zpool devices
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