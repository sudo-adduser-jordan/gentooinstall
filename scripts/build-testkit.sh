#!/usr/bin/env bash
# Build the QEMU "testkit" used by the end-to-end install test harness
# (scripts/run-vm-test.sh).
#
# The testkit is a small Debian live/boot environment that:
#   * runs as the installer's host inside QEMU (OVMF/EFI firmware always),
#   * contains every host program the installer may need (see
#     internal/installer/prepare.go WantedPrograms),
#   * boots to serial console ttyS0 and, on first boot, fetches the inject
#     payload (gentooinstall binary + config + optional stage3 seed) from the
#     host via HTTP and runs it non-interactively (GENTOOINSTALL_ASSUME_YES).
#
# The image build needs root (debootstrap chroot + grub-install). Only the
# build is privileged; running the actual test (run-vm-test.sh) is fully
# unprivileged. GitHub Actions runners are fine with the sudo apt-get phase;
# no privileged containers are required (those are not usable on ubuntu-24.04
# runners anyway due to disabled unprivileged user namespaces).
#
# Requirements (host): root, debootstrap, losetup, sgdisk, mkfs.vfat,
# mkfs.ext4, blkid and network access to the Debian mirror.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${OUT:-$ROOT/dist/testkit.raw}"
SUITE="${SUITE:-bookworm}"
MIRROR="${MIRROR:-http://deb.debian.org/debian/}"
DISK_SIZE="${TESTKIT_SIZE:-6G}"

# Host tools the installer requires + the extras needed for the disk-layout
# matrix (btrfs / raid / luks): match internal/installer/prepare.go and
# internal/installer/disks.go.
PKGS="systemd udev kmod iproute2 isc-dhcp-client linux-image-amd64 grub-efi-amd64 \
 xz-utils ca-certificates util-linux gdisk parted dosfstools e2fsprogs btrfs-progs \
 mdadm cryptsetup gnupg wget python3 ntp"

[[ "$(id -u)" -eq 0 ]] || { echo "build-testkit.sh must run as root (image build)" >&2; exit 1; }
for CMD in debootstrap losetup sgdisk mkfs.vfat mkfs.ext4 blkid; do
  command -v "$CMD" >/dev/null 2>&1 || { echo "required tool not found: $CMD" >&2; exit 1; }
done

WORK="$(mktemp -d)"
LOOP=""
cleanup() {
  if [[ -n "$LOOP" ]]; then
    for mp in "$WORK/root/proc" "$WORK/root/sys" "$WORK/root/dev/pts" \
              "$WORK/root/dev" "$WORK/root/boot/efi" "$WORK/root"; do
      mountpoint -q "$mp" 2>/dev/null && umount "$mp" || true
    done
    losetup -d "$LOOP" 2>/dev/null || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

mkdir -p "$(dirname "$OUT")" "$WORK/root/boot/efi"
truncate -s "$DISK_SIZE" "$OUT"

sgdisk --zap-all \
  --new=1:2048:+1G --typecode=1:ef00 --change-name=1:testkit-esp \
  --new=2:0:0      --typecode=2:8300 --change-name=2:testkit-root \
  "$OUT"

LOOP="$(losetup --find --show -P "$OUT")"
mkfs.vfat -F 32 -n TESTKIT-ESP "${LOOP}p1"
mkfs.ext4 -q -L testkit-root "${LOOP}p2"

ROOTUUID="$(blkid -s UUID -o value "${LOOP}p2")"
ESPUUID="$(blkid -s UUID -o value "${LOOP}p1")"

mount "${LOOP}p2" "$WORK/root"
mkdir -p "$WORK/root/boot/efi"
mount "${LOOP}p1" "$WORK/root/boot/efi"

debootstrap --arch=amd64 --variant=minbase "$SUITE" "$WORK/root" "$MIRROR"

mount --bind /dev "$WORK/root/dev"
mount --bind /dev/pts "$WORK/root/dev/pts"
mount -t proc proc "$WORK/root/proc"
mount -t sysfs sysfs "$WORK/root/sys"

cat >"$WORK/root/etc/fstab" <<EOF
UUID=$ROOTUUID  /            ext4      errors=remount-ro 0 1
UUID=$ESPUUID   /boot/efi    vfat      umask=0077        0 1
proc            /proc        proc      defaults          0 0
sysfs           /sys         sysfs     defaults          0 0
devtmpfs        /dev         devtmpfs  mode=0755,nosuid  0 0
EOF

chroot "$WORK/root" /bin/env PKGS="$PKGS" /bin/bash -s <<'INNER'
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
export LANG=C
apt-get update -qq
# shellcheck disable=SC2086
apt-get install -y -qq --no-install-recommends $PKGS
apt-get clean

echo testkit > /etc/hostname
echo "nameserver 10.0.2.3" > /etc/resolv.conf

cat >> /etc/default/grub <<'GRUB'
GRUB_CMDLINE_LINUX="console=ttyS0,115200n8 net.ifnames=0 biosdevname=0"
GRUB_TIMEOUT=1
GRUB

cat > /etc/rc.local <<'RC'
#!/bin/sh
# gentooinstall e2e harness: fetch and run the install payload from the host,
# then power off the VM. Boot 2 of the test boots the installed target disk.
mount -o remount,rw / 2>/dev/null || true
printf 'nameserver 10.0.2.3\n' > /etc/resolv.conf
ip link set eth0 up 2>/dev/null || true
dhclient -1 -q -t 10 eth0 2>/dev/null || true
mkdir -p /mnt/payload
if [ ! -e /mnt/payload/payload.tgz ]; then
  wget -q -T 30 -O /mnt/payload/payload.tgz http://10.0.2.2:8000/payload.tgz || {
    echo "GI_TEST payload-fetch-failed" > /dev/ttyS0; systemctl poweroff --force; }
  tar xzf /mnt/payload/payload.tgz -C /mnt/payload
fi
sh /mnt/payload/run-install.sh > /dev/ttyS0 2>&1
st=$?
echo "GI_INSTALL exit=$st" > /dev/ttyS0
echo "GI_TEST_DONE" > /dev/ttyS0
systemctl poweroff --force
exit 0
RC
chmod 0755 /etc/rc.local

cat > /etc/systemd/system/gentooinstall-e2e.service <<'UNIT'
[Unit]
Description=gentooinstall e2e installer (boot 1)
After=multi-user.target

[Service]
Type=oneshot
RemainAfterExit=no
ExecStart=/etc/rc.local
StandardOutput=null
StandardError=null

[Install]
WantedBy=multi-user.target
UNIT
systemctl enable gentooinstall-e2e.service

# No getty on the (invisible) display; serial is the only console.
systemctl disable getty@tty1.service 2>/dev/null || true

grub-install --target=x86_64-efi --efi-directory=/boot/efi \
  --bootloader-id=testkit --removable --no-nvram

rm -f /etc/machine-id /var/lib/systemd/random-seed
exit 0
INNER

# Write grub.cfg host-side: it templates in the partition UUIDs and the
# kernel version installed by the linux-image package.
KVER="$(ls -1 "$WORK/root"/lib/modules | sort -V | tail -1)"
[[ -n "$KVER" ]] || { echo "no kernel installed in testkit" >&2; exit 1; }
cat >"$WORK/root/boot/grub/grub.cfg" <<'GRUBCFG'
set timeout=1
set default=0
insmod part_gpt
insmod ext2
menuentry 'testkit' {
    linux /boot/vmlinuz-__KVER__ root=UUID=__ROOTUUID__ console=ttyS0,115200n8 net.ifnames=0
    initrd /boot/initrd.img-__KVER__
}
GRUBCFG
sed -i -e "s/__KVER__/$KVER/g" -e "s/__ROOTUUID__/$ROOTUUID/g" \
  "$WORK/root/boot/grub/grub.cfg"

sync
echo "wrote $OUT (root=$ROOTUUID esp=$ESPUUID kernel=$KVER)"

if [[ "${COMPRESS:-0}" == "1" ]]; then
  gzip -c "$OUT" > "$OUT.gz"
  rm -f "$OUT"
  echo "compressed to $OUT.gz"
fi
