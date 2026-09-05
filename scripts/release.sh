#!/usr/bin/env bash
set -Eeuo pipefail

APP_SRC="./cmd/gentooinstall"
ISO_OUTPUT="${1:-bin/gentooinstall.iso}"
BUILD_DIR="$PWD/iso_build"
ROOTFS="$PWD/rootfs"
trap 'rm -rf "$BUILD_DIR" "$ROOTFS" init' EXIT

# Storage drivers bundled into the initramfs so disks appear under /dev even
# without udev; keep in sync with internal/live/live.go (NeedModules). The
# live init loads them from /lib/modules/bundle via the init_module syscall.
MODULES=(
    virtio virtio_ring virtio_pci virtio_blk virtio_scsi
    scsi_mod sd_mod libata ahci ata_piix ata_generic
    nvme_keyring nvme_auth nvme_core nvme
    usbcore usb_storage uas xhci_pci xhci_hcd
)

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

echo "Locating kernel..."
KERNEL=""
for d in /boot /boot/efi /lib/modules; do
    [[ -d "$d" ]] || continue
    KERNEL="$(find "$d" -maxdepth 1 -type f -name 'vmlinuz-*' -print 2>/dev/null | sort -V | tail -n 1)" && break
done
[[ -n "$KERNEL" ]] || { echo "Error: no kernel found" >&2; exit 1; }

# Resolve the kernel release so its matching module tree can be bundled. The
# version string survives in the bzImage header (`file` knows it) even though
# the payload is compressed; uname -r is the fallback when parsing fails.
KERNEL_VER=""
if command -v file >/dev/null 2>&1; then
    KERNEL_VER="$(file "$KERNEL" | sed -n 's/.*version \([^ ()]*\).*/\1/p')"
fi
[[ -n "$KERNEL_VER" ]] || KERNEL_VER="$(uname -r)"
MODDIR="/lib/modules/$KERNEL_VER"

echo "Building initramfs..."
mkdir -p "$ROOTFS"/{dev,proc,sys,tmp,etc} "$ROOTFS/builds" "$BUILD_DIR/boot/grub" "$(dirname "$ISO_OUTPUT")"
# Ship the config templates so the live ISO mirrors the repo layout: saving
# the default redirects to /builds/custom.toml (writable ramfs).
cp builds/*.toml "$ROOTFS/builds/"
mv init "$ROOTFS/init"
[[ -e /dev/console ]] && cp -a /dev/console "$ROOTFS/dev/console" 2>/dev/null || true
cp "$KERNEL" "$BUILD_DIR/boot/vmlinuz"

# Resolve the module file paths: prefer modprobe --show-depends (gives the
# exact names and the dependency closure), fall back to a name search for the
# curated list (still ordered so dependencies load first).
MODULE_FILES=""
if [[ -d "$MODDIR" ]] && command -v modprobe >/dev/null 2>&1; then
    for m in "${MODULES[@]}"; do
        while IFS= read -r line; do
            path="${line##* }" # last whitespace field (bare path or "insmod <path>")
            case "$path" in
                *".ko"*) MODULE_FILES="$MODULE_FILES $path" ;;
            esac
        done < <(modprobe --show-depends "$m" 2>/dev/null)
    done
fi
if [[ -z "$MODULE_FILES" && -d "$MODDIR" ]]; then
    for m in "${MODULES[@]}"; do
        for alt in "$m" "${m//_/-}"; do
            f="$(find "$MODDIR/kernel/drivers" \( -name "$alt.ko" -o -name "$alt.ko.zst" -o -name "$alt.ko.gz" \) -print 2>/dev/null | head -n 1)"
            [[ -n "$f" ]] && { MODULE_FILES="$MODULE_FILES $f"; break; }
        done
    done
fi

# Copy each module into the initramfs as a plain, decompressed .ko named
# <module-with-underscores>.ko so the live init can look it up by name.
if [[ -n "$MODULE_FILES" ]]; then
    mkdir -p "$ROOTFS/lib/modules/bundle"
    for f in $MODULE_FILES; do
        [[ -f "$f" ]] || continue
        base="$(basename "$f")"
        case "$base" in
            *.ko.zst) name="${base%.ko.zst}" ;;
            *.ko.gz)  name="${base%.ko.gz}" ;;
            *.ko)     name="${base%.ko}" ;;
            *)        continue ;;
        esac
        name="${name//-/_}"
        out="$ROOTFS/lib/modules/bundle/$name.ko"
        [[ -e "$out" ]] && continue
        case "$base" in
            *.zst)
                if command -v zstd >/dev/null 2>&1; then
                    zstd -d -c "$f" > "$out"
                else
                    echo "Warning: zstd missing, skipping module $name" >&2
                    continue
                fi
                ;;
            *.gz) gzip -d -c "$f" > "$out" ;;
            *)    cp "$f" "$out" ;;
        esac
        echo "bundled module $name"
    done
else
    echo "Warning: no module tree for $KERNEL_VER; disks may need built-in drivers" >&2
fi

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