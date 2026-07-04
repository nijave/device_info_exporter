#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
VM_DIR="$SCRIPT_DIR/.vm-state"
IMAGE_URL="https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img"
IMAGE_FILE="$VM_DIR/jammy-base.img"
DISK_FILE="$VM_DIR/boot.qcow2"
CIDATA_ISO="$VM_DIR/cidata.iso"
SSH_PORT="${SSH_PORT:-2222}"
METRICS_PORT="${METRICS_PORT:-9133}"

mkdir -p "$VM_DIR"

# Download Ubuntu 22.04 cloud image if needed
if [ ! -f "$IMAGE_FILE" ]; then
    echo "Downloading Ubuntu 22.04 cloud image..."
    curl -L -o "$IMAGE_FILE" "$IMAGE_URL"
fi

# Create boot disk from base image
if [ ! -f "$DISK_FILE" ]; then
    echo "Creating boot disk..."
    qemu-img create -f qcow2 -b "$IMAGE_FILE" -F qcow2 "$DISK_FILE" 20G
fi

# Create virtual disks for ZFS pool
for i in 0 1 2; do
    ZPOOL_DISK="$VM_DIR/zfs_disk_${i}.qcow2"
    if [ ! -f "$ZPOOL_DISK" ]; then
        qemu-img create -f qcow2 "$ZPOOL_DISK" 1G
    fi
done

# Build the binary for the VM (linux/amd64)
echo "Building binary..."
(cd "$PROJECT_DIR" && CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o "$VM_DIR/zfs_devices" .)

# Create cloud-init config
cat > "$VM_DIR/user-data" << 'USERDATA'
#cloud-config
password: ubuntu
chpasswd: { expire: false }
ssh_pwauth: true
packages:
  - zfsutils-linux
  - lvm2
runcmd:
  - ["zpool", "create", "testpool", "raidz", "/dev/vdb", "/dev/vdc", "/dev/vdd"]
  - ["zpool", "status"]
USERDATA

cat > "$VM_DIR/meta-data" << METADATA
instance-id: zfs-test-vm
local-hostname: zfs-test
METADATA

# Generate cloud-init ISO
genisoimage -output "$CIDATA_ISO" -volid cidata -joliet -rock \
    "$VM_DIR/user-data" "$VM_DIR/meta-data" 2>/dev/null

echo ""
echo "Starting VM..."
echo "  SSH:     ssh -p $SSH_PORT ubuntu@localhost  (password: ubuntu)"
echo "  Metrics: http://localhost:$METRICS_PORT/metrics (after starting binary)"
echo ""
echo "  Copy binary: sshpass -p ubuntu scp -P $SSH_PORT $VM_DIR/zfs_devices ubuntu@localhost:~/"
echo "  Inside VM:"
echo "    sudo ~/zfs_devices   # run the exporter"
echo "    zpool status          # verify pool"
echo ""

exec qemu-system-x86_64 \
    -m 2G \
    -smp 2 \
    -cpu host \
    -enable-kvm \
    -nographic \
    -drive file="$DISK_FILE",format=qcow2,if=virtio \
    -drive file="$VM_DIR/zfs_disk_0.qcow2",format=qcow2,if=virtio \
    -drive file="$VM_DIR/zfs_disk_1.qcow2",format=qcow2,if=virtio \
    -drive file="$VM_DIR/zfs_disk_2.qcow2",format=qcow2,if=virtio \
    -cdrom "$CIDATA_ISO" \
    -netdev user,id=net0,hostfwd=tcp::"$SSH_PORT"-:22,hostfwd=tcp::"$METRICS_PORT"-:9133 \
    -device virtio-net-pci,netdev=net0
