# gentooinstall

[![Go](https://img.shields.io/github/go-mod/go-version/sudo-adduser-jordan/gentooinstall)](https://go.dev/doc/devel/release)
[![Release](https://img.shields.io/github/v/release/sudo-adduser-jordan/gentooinstall)](https://github.com/sudo-adduser-jordan/gentooinstall/releases)
[![Release workflow](https://github.com/sudo-adduser-jordan/gentooinstall/actions/workflows/release.yml/badge.svg)](https://github.com/sudo-adduser-jordan/gentooinstall/actions/workflows/release.yml)
[![Test](https://github.com/sudo-adduser-jordan/gentooinstall/actions/workflows/test.yml/badge.svg)](https://github.com/sudo-adduser-jordan/gentooinstall/actions/workflows/test.yml)

![gentooinstall demo](demo.gif)

## Install

```sh
# make install
# go install
# curl binary
```

## Usage

```sh
gentooinstall
gentooinstall builds/custom.toml
gentooinstall demo demo.gif
```

## Configuration

Configurations are TOML and live in `builds/`. The repo ships templates for
every supported scheme: classic single disk with EFI (`default.toml`,
`openrc.toml`, `musl.toml`, `desktop-systemd.toml`) or legacy BIOS
(`bios.toml`), btrfs RAID (`btrfs-efi.toml`), and reuse of existing partitions
(`existing-efi.toml`).
Running `gentooinstall` with no arguments opens `builds/custom.toml` if it exists,
otherwise the shipped `builds/default.toml`; the first save always writes
`builds/custom.toml` (gitignored) so the templates are never overwritten.
Point the configurator at any file with `gentooinstall /path/to/config.toml`.

Example:

```toml
[disk]
scheme = "classic_single_disk"
boot_type = "efi"
device = "/dev/sdX"
use_swap = true
swap_size = "8GiB"
use_luks = true
root_fs = "ext4"

[system]
hostname = "gentoo"
timezone = "Europe/Berlin"
keymap = "de"
locales = ["en_US.UTF-8 UTF-8"]
locale = "en_US.UTF-8"

[gentoo]
arch = "amd64"
stage3_variant = "systemd"
mirror = "https://mirror.leaseweb.com/gentoo"
portage_sync_type = "git"

[packages]
enable_sshd = true
kernel_type = "bin"
ssh_authorized_keys = ["ssh-ed25519 AAAA..."]
```

Layouts (RAID0/1+LUKS, ZFS-centric, btrfs-centric, existing
partitions or fully custom stacks of disk actions) are selectable via
`scheme`; the custom scheme takes a declarative `[[disk.custom]]` action
list mirroring the original bash DSL.

## Live ISO

Releases ship a minimal bootable live ISO that boots straight into the
gentooinstall TUI (statically linked, running as PID 1). Build it yourself with:

```sh
scripts/release.sh
```

The rootfs is a trimmed-down Alpine userland (busybox + util-linux + gptfdisk +
parted + gnupg + GNU tar, installed via apk), so the ISO can partition. Storage and network
kernel modules are bundled alongside. ZFS schemes are not usable from the ISO
(the Alpine ZFS module cannot match the bundled build-host kernel). Building the
ISO fetches the Alpine base from dl-cdn.alpinelinux.org, so it needs network
access and produces an ISO of roughly 100–200 MB.

The ISO is hybrid BIOS+UEFI. The default configs use
`disk.boot_type = "efi"`, which requires a UEFI boot (so that
`/sys/firmware/efi` exists for `efivarfs`/`efibootmgr`):

```sh
cp /usr/share/OVMF/x64/OVMF_VARS.4m.fd /tmp/OVMF_VARS.fd
qemu-system-x86_64 \
  -enable-kvm \
  -cpu host \
  -smp $(nproc) \
  -m 4G \
  -cdrom bin/gentooinstall.iso \
  -boot d \
  -drive file=bin/gentoo-disk.img,format=qcow2,if=virtio,cache=writeback \
  -drive if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/x64/OVMF_CODE.4m.fd \
  -drive if=pflash,format=raw,file=/tmp/OVMF_VARS.fd \
  -netdev user,id=net0 \
  -device virtio-net-pci,netdev=net0 \
  -nographic -serial stdio -monitor none
```

The single grub entry maps the serial port to `/dev/console` (so the TUI
renders over `-nographic -serial stdio`, no framebuffer window).

Booting the same ISO without OVMF (plain SeaBIOS) is a legacy-BIOS boot —
pair it with `builds/bios.toml` (`disk.boot_type = "bios"`).

## Recommendations

### EFI vs BIOS

Use EFI. BIOS is old and deprecated for a long time now.

EFI installs created with this tool are directly bootable from removable media
(USB sticks): besides the regular `efibootmgr` entry, the installer writes a
self-contained unified EFI image (kernel + initramfs + command line wrapped by
the systemd-stub) at `/boot/efi/EFI/BOOT/BOOTX64.EFI` on the ESP. Firmware
picks that file up automatically when you select the USB in the boot menu, so
no persistent NVRAM entry is required (re-run `/boot/efi/EFI/BOOT/generate_bootx64.sh`
after a kernel update to refresh it). Building the fallback needs the UEFI
stub: `sys-apps/systemd` with the `boot` USE flag (systemd), or
`sys-apps/systemd-utils` with `boot kernel-install` (OpenRC).

### Modern file systems

I recommend using a modern file system like ZFS, both on desktops and servers.
It provides transparent block-level compression, instant snapshots and full-disk encryption.

### Systemd vs OpenRC

Both are fine init systems. If you cannot decide:
- OpenRC is a service manager — more manual setup, but you learn a lot.
- Systemd is an OS-level software suite — steep learning curve but huge feature set.

## Troubleshooting and FAQ

#### Q: `cannot mount efivarfs: the live system was not booted in UEFI mode (/sys/firmware/efi is missing)` (or `configuration uses an EFI boot partition but the live system was not booted in UEFI mode`)

**A:** The config requests an EFI install (`disk.boot_type = "efi"`) but the
live medium was booted in legacy-BIOS mode, where `efivarfs` cannot exist.
Either reboot the live ISO under UEFI (bare metal: enable UEFI boot; QEMU:
attach OVMF firmware as shown in [Live ISO](#live-iso)), or switch to a
BIOS install with `disk.boot_type = "bios"` (e.g. `builds/bios.toml`).

#### Q: ZFS cannot be installed in the chroot due to an unsupported kernel version

**A:** Switch to testing temporarily:

```
echo 'ACCEPT_KEYWORDS="~amd64"' >> /etc/portage/make.conf
emerge -v gentoo-kernel-bin
exit
# Now select 'retry' when asked about what to do next.
```

#### Q: I get errors after partitioning about blkid not being able to find a UUID

**A:** Ensure all devices are unmounted and not in use. Use `wipefs -a <DEVICE>` on your partitions before starting.

## References

* [Gentoo AMD64 Handbook](https://wiki.gentoo.org/wiki/Handbook:AMD64)
* [Sakaki's EFI Install Guide](https://wiki.gentoo.org/wiki/Sakaki%27s_EFI_Install_Guide)