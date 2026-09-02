#!/usr/bin/env bash
# Build the gentooinstall live ISO: a bootable image that contains nothing but the
# statically linked gentooinstall binary (running as PID 1), a busybox fallback
# shell and a Linux kernel. There is no login, no desktop and no disk
# tooling - booting it drops you straight into the gentooinstall TUI.
#
# Requires: grub-mkrescue (grub-pc-bin + grub-efi + xorriso), cpio,
# busybox-static, and the repository's Go toolchain.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/dist/gentooinstall-live-amd64.iso"
BINSRC="$ROOT/bin/gentooinstall"
KERN="/boot/vmlinuz-$(uname -r)"

for CMD in grub-mkrescue cpio gzip busybox; do
  command -v "$CMD" >/dev/null 2>&1 || { echo "required tool not found: $CMD" >&2; exit 1; }
done

[[ -e "$KERN" ]] || { echo "no kernel found at $KERN" >&2; exit 1; }

# Build the fresh binary the ISO must boot.
make -C "$ROOT" build

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/iso/boot/grub" "$WORK/root/sbin" "$WORK/root/bin"

cp "$BINSRC" "$WORK/root/sbin/gentooinstall"
ln -s /sbin/busybox "$WORK/root/bin/sh"
cp "$(command -v busybox)" "$WORK/root/sbin/busybox"

cat >"$WORK/root/init" <<'EOF'
#!/bin/sh
export PATH=/sbin:/bin TERM=linux
mount -t proc proc /proc
mount -t sysfs sysfs /sys
mount -t devtmpfs dev /dev || mount -t tmpfs tmpfs /dev
mkdir -p /dev/pts /dev/shm /run /tmp
mount -t devpts devpts /dev/pts
mount -t tmpfs tmpfs /dev/shm
hostname gentooinstall
cd /
exec /sbin/gentooinstall
EOF
chmod +x "$WORK/root/init"

# Pack the initramfs: only the kernel, the shell and the one binary.
( cd "$WORK/root" && find . -print0 | cpio --null -o --format=newc | gzip -9 ) \
  >"$WORK/iso/boot/initrd.img"
cp "$KERN" "$WORK/iso/boot/vmlinuz"

cat >"$WORK/iso/boot/grub/grub.cfg" <<'EOF'
set timeout=0
set default=0
menuentry 'gentooinstall live' {
    linux /boot/vmlinuz quiet console=ttyS0,115200n8
    initrd /boot/initrd.img
}
EOF

mkdir -p "$(dirname "$OUT")"
grub-mkrescue -o "$OUT" "$WORK/iso" >/dev/null
echo "wrote $OUT"
