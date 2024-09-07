#!/bin/sh

cat <<'EOF'
podman run -it --rm \
  -v /dev/zfs:/dev/zfs:ro docker.io/library/golang:1.22 bash
EOF

set -e

sed -i -e 's/ main/ main contrib non-free/g' /etc/apt/sources.list.d/debian.sources || true
apt update
apt install -y libudev-dev libzfslinux-dev

go build -v -o device_info_exporter main.go