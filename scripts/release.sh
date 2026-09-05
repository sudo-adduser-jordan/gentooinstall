#!/usr/bin/env bash
set -Eeuo pipefail

APP_SRC="./cmd/gentooinstall"
ISO_OUTPUT="${1:-bin/gentooinstall.iso}"
BUILD_DIR="$PWD/iso_build"
ROOTFS="$PWD/rootfs"
trap 'rm -rf "$BUILD_DIR" "$ROOTFS" init' EXIT

die()   { echo "Error: $*" >&2; exit 1; }
latest(){ curl -fsSL "$1/" | sed -n "s#.*href=\"\\($2\\)\".*#\\1#p" | sort -V | tail -n1; }

# Live rootfs = minimal Alpine so the ISO can actually install Gentoo (mirrors
# internal/live/live.go NeedModules and installer/prepare.go WantedPrograms).
# Building needs network to dl-cdn.alpinelinux.org; ZFS stays excluded (alpine
# zfs kmod cannot match the bundled build-host kernel).
ALPINE_BRANCH=v3.21
ALPINE_MIRROR=https://dl-cdn.alpinelinux.org/alpine
MODULES=(virtio virtio_ring virtio_pci virtio_blk virtio_scsi
    scsi_mod sd_mod libata ahci ata_piix ata_generic
    nvme_keyring nvme_auth nvme_core nvme
    usbcore usb_storage uas xhci_pci xhci_hcd
    virtio_net e1000 e1000e r8169 igb ixgbe tg3
    md_mod dm_mod dm_crypt btrfs)

for cmd in go cpio gzip grub-mkrescue curl tar modprobe file; do
    command -v "$cmd" >/dev/null 2>&1 || die "$cmd not found"
done
case "$(uname -m)" in
    x86_64|amd64)  GOARCH=amd64;  ALPINE_ARCH=x86_64 ;;
    aarch64|arm64) GOARCH=arm64; ALPINE_ARCH=aarch64 ;;
    *) die "unsupported arch" ;;
esac

echo "Bootstrapping Alpine rootfs ($ALPINE_ARCH)..."
mkdir -p "$ROOTFS"/{dev,proc,sys,tmp,etc} "$BUILD_DIR/boot/grub" "$(dirname "$ISO_OUTPUT")"

echo "Compiling init..."
CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build -trimpath -ldflags="-s -w" -o "$ROOTFS/init" "$APP_SRC"
chmod 0755 "$ROOTFS/init"

echo "Locating kernel..."
KERNEL="$(for d in /boot /boot/efi /lib/modules; do find "$d" -maxdepth 1 -type f -name 'vmlinuz-*' 2>/dev/null; done | sort -V | tail -n1)"
[[ -n "$KERNEL" ]] || die "no kernel found"
KERNEL_VER="$(file "$KERNEL" | sed -n 's/.*version \([^ ()]*\).*/\1/p')"
KERNEL_VER="${KERNEL_VER:-$(uname -r)}"
MODDIR="/lib/modules/$KERNEL_VER"

MINIROOTFS="$(latest "$ALPINE_MIRROR/$ALPINE_BRANCH/releases/$ALPINE_ARCH" "alpine-minirootfs-[^\"]*-$ALPINE_ARCH\\.tar\\.gz")"
[[ -n "$MINIROOTFS" ]] || die "could not resolve minirootfs"
curl -fsSL -o "$BUILD_DIR/$MINIROOTFS" "$ALPINE_MIRROR/$ALPINE_BRANCH/releases/$ALPINE_ARCH/$MINIROOTFS"
tar -xzf "$BUILD_DIR/$MINIROOTFS" -C "$ROOTFS"

APK_TOOLS="$(latest "$ALPINE_MIRROR/$ALPINE_BRANCH/main/$ALPINE_ARCH" "apk-tools-static-[^\"]*\\.apk")"
[[ -n "$APK_TOOLS" ]] || die "could not resolve apk-tools-static"
curl -fsSL -o "$BUILD_DIR/$APK_TOOLS" "$ALPINE_MIRROR/$ALPINE_BRANCH/main/$ALPINE_ARCH/$APK_TOOLS"
tar -xzOf "$BUILD_DIR/$APK_TOOLS" sbin/apk.static > "$BUILD_DIR/apk.static"
chmod +x "$BUILD_DIR/apk.static"

echo "Installing live toolset with apk..."
APK_OUT="$("$BUILD_DIR/apk.static" --root "$ROOTFS" --initdb --allow-untrusted --no-scripts \
    -X "$ALPINE_MIRROR/$ALPINE_BRANCH/main" -X "$ALPINE_MIRROR/$ALPINE_BRANCH/community" \
    add busybox util-linux e2fsprogs dosfstools sgdisk parted gnupg tar xz \
        btrfs-progs mdadm cryptsetup git 2>&1)" || true
printf '%s\n' "$APK_OUT"
grep -q '^OK: ' <<<"$APK_OUT" || die "apk install failed"

# Live init mounts devtmpfs and runs DHCP via busybox udhcpc; ship its script.
mkdir -p "$ROOTFS/etc/udhcpc"
cat > "$ROOTFS/etc/udhcpc/default.script" <<'EOF'
#!/bin/sh
RESOLV_CONF=/etc/resolv.conf
case "$1" in
    deconfig)
        ip addr flush dev "$interface" 2>/dev/null
        ;;
    bound)
        ip link set "$interface" up
        ip addr add "$ip/24" dev "$interface"
        for r in $router; do
            ip route del default 2>/dev/null
            ip route add default via "$r" dev "$interface"
        done
        : > "$RESOLV_CONF"
        for d in $dns; do
            echo "nameserver $d" >> "$RESOLV_CONF"
        done
        ;;
esac
exit 0
EOF
chmod +x "$ROOTFS/etc/udhcpc/default.script"

# Config templates so saving from the live ISO lands in the writable ramfs.
mkdir -p "$ROOTFS/builds"
cp builds/*.toml "$ROOTFS/builds/"

[[ -e /dev/console ]] && cp -a /dev/console "$ROOTFS/dev/console" 2>/dev/null || true
cp "$KERNEL" "$BUILD_DIR/boot/vmlinuz"

bundle_mod() {
    local base name
    base="$(basename "$1")"
    case "$base" in
        *.ko.zst) name="${base%.ko.zst}" ;;
        *.ko.gz)  name="${base%.ko.gz}" ;;
        *.ko)     name="${base%.ko}" ;;
        *)        return ;;
    esac
    name="${name//-/_}"
    local out="$ROOTFS/lib/modules/bundle/$name.ko"
    [[ -e "$out" ]] && return
    case "$base" in
        *.zst) command -v zstd >/dev/null 2>&1 && zstd -d -c "$1" > "$out" || echo "Warning: zstd missing, skip $name" >&2 ;;
        *.gz)  gzip -d -c "$1" > "$out" ;;
        *)     cp "$1" "$out" ;;
    esac
    echo "bundled module $name"
    return 0
}

mkdir -p "$ROOTFS/lib/modules/bundle"
bundle_find() {
    local alt f
    for alt in "$1" "${1//_/-}"; do
        f="$(find "$MODDIR/kernel/drivers" -name "$alt.ko*" 2>/dev/null | head -n1)"
        [[ -n "$f" ]] && { bundle_mod "$f"; return 0; }
    done
    return 0
}
if [[ -d "$MODDIR" ]]; then
    if command -v modprobe >/dev/null 2>&1; then
        while read -r line; do
            path="${line##* }"
            [[ "$path" == *.ko* ]] || continue
            bundle_mod "$path"
        done < <(modprobe --show-depends "${MODULES[@]}" 2>/dev/null || true)
    fi
    if ! compgen -G "$ROOTFS/lib/modules/bundle/*.ko" >/dev/null; then
        for m in "${MODULES[@]}"; do bundle_find "$m"; done
    fi
fi
compgen -G "$ROOTFS/lib/modules/bundle/*.ko" >/dev/null || \
    echo "Warning: no module tree for $KERNEL_VER; disks may need built-in drivers" >&2

echo "Building initramfs..."
(cd "$ROOTFS" && find . -print0 | cpio --null -o -H newc --quiet) | gzip -9 > "$BUILD_DIR/boot/initrd.img"

cat > "$BUILD_DIR/boot/grub/grub.cfg" <<'EOF'
set timeout=0
set default=0
insmod all_video
set gfxmode=1024x768,800x600,auto
insmod gfxterm
terminal_output gfxterm
set gfxpayload=keep

menuentry "Gentoo Install" {
    linux /boot/vmlinuz quiet console=ttyS0 console=tty0 loglevel=4 rdinit=/init
    initrd /boot/initrd.img
}
EOF

grub-mkrescue -o "$ISO_OUTPUT" "$BUILD_DIR"
echo "ISO ready: $ISO_OUTPUT"