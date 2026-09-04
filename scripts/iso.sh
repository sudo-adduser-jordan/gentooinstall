#!/usr/bin/env bash
set -Eeuo pipefail

APP_SRC="main.go"
ISO_OUTPUT="${1:-output.iso}"
BUILD_DIR="$PWD/iso_build"
ROOTFS="$PWD/rootfs"
trap 'rm -rf "$BUILD_DIR" "$ROOTFS" init' EXIT

for cmd in go cpio gzip grub-mkrescue; do
    command -v "$cmd" >/dev/null 2>&1 || { echo "Error: $cmd not found" >&2; exit 1; }
done

case "$(uname -m)" in
    x86_64)  GOARCH=amd64 ;; aarch64) GOARCH=arm64 ;;
    *)       echo "Unsupported arch" >&2; exit 1 ;;
esac

echo "Compiling init..."
CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build -trimpath -ldflags="-s -w" -o init "$APP_SRC"
chmod 0755 init

echo "Building initramfs..."
mkdir -p "$ROOTFS"/{dev,proc,sys,tmp,etc} "$BUILD_DIR/boot/grub"
mv init "$ROOTFS/init"
[[ -e /dev/console ]] && cp -a /dev/console "$ROOTFS/dev/console"
(cd "$ROOTFS" && find . -print0 | cpio --null -o -H newc --quiet) | gzip -9 > "$BUILD_DIR/boot/initrd.img"

echo "Locating kernel..."
KERNEL=""
for d in /boot /boot/efi /lib/modules; do
    [[ -d "$d" ]] || continue
    KERNEL="$(find "$d" -maxdepth 1 -type f -name 'vmlinuz-*' -print 2>/dev/null | sort -V | tail -n 1)" && break
done
[[ -n "$KERNEL" ]] || { echo "Error: no kernel found" >&2; exit 1; }
cp "$KERNEL" "$BUILD_DIR/boot/vmlinuz"

cat > "$BUILD_DIR/boot/grub/grub.cfg" <<'EOF'
set timeout=0
set default=0

menuentry "Gentoo Install" {
    linux /boot/vmlinuz quiet loglevel=4 rdinit=/init
    initrd /boot/initrd.img
}
EOF

grub-mkrescue -o "$ISO_OUTPUT" "$BUILD_DIR"
echo "ISO ready: $ISO_OUTPUT"