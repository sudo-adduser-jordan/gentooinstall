# gentooinstall

[![Go](https://img.shields.io/github/go-mod/go-version/sudo-adduser-jordan/gentooinstall)](https://go.dev/doc/devel/release)
[![Release](https://img.shields.io/github/v/release/sudo-adduser-jordan/gentooinstall)](https://github.com/sudo-adduser-jordan/gentooinstall/releases)
[![Release workflow](https://github.com/sudo-adduser-jordan/gentooinstall/actions/workflows/release.yml/badge.svg)](https://github.com/sudo-adduser-jordan/gentooinstall/actions/workflows/release.yml)
[![Test](https://github.com/sudo-adduser-jordan/gentooinstall/actions/workflows/test.yml/badge.svg)](https://github.com/sudo-adduser-jordan/gentooinstall/actions/workflows/test.yml)

![gentooinstall demo](demo.gif)

## Build

```sh
make build        # -> ./bin/gentooinstall
make test         # go vet ./... && go test ./...
make fmt          # gofmt -l -w .
```

## End-to-end tests (QEMU)

Two opt-in test suites drive the real installer inside QEMU. Both are
local-only (never run in CI) and need host `qemu-system-x86_64`, `qemu-img`,
`grub-mkrescue`/`xorriso`, a build-host kernel and outbound access to the
Gentoo mirror:

```sh
make vm-test       # boots the live ISO to the TUI on the serial console.
make vm-install    # runs a FULL install for every builds/*.toml template.
```

`make vm-test` covers the boot path headlessly: the grub entry
"Gentoo Install" maps the serial port to `/dev/console`, so with
`-nographic -serial stdio` the TUI renders straight into the launching
terminal and `TestISOBoots` asserts the PID 1 banner and the
`live: tui starting` marker appear on the serial console.

`make vm-install` (`TestInstallInVM`) executes a real installation for each
shipped template. It stages the config (rewriting placeholder device paths to
the VM's disks), builds the ISO with `release.sh`'s
`GENTOOINSTALL_INSTALL_CFG` hook so the default grub entry passes a
`gentooinstall.install=builds/custom.toml` kernel flag, boots the ISO under
the config's firmware (OVMF for `efi`, SeaBIOS for `bios`) with user-mode NIC
and raw disks, and passes when the headless PID 1 install prints
`gentooinstall install: success` and powers the guest down. The
`existing-efi.toml` case pre-partitions its first disk (GPT ESP + swap + ext4)
and skips cleanly when the `losetup`/mkfs tools need root that isn't
available.

## Usage

```sh
make build
./bin/gentooinstall                    # interactive configurator (builds/custom.toml,
./bin/gentooinstall install            # install using builds/custom.toml (destructive!)
./bin/gentooinstall gif                # record ./demo.gif of the simulated install demo
./bin/gentooinstall -c myconf.toml     # alternate config path
./bin/gentooinstall chroot /mnt        # chroot into an existing system
```

### The TUI

| Key | Action |
|---|---|
| `1`–`6` | Switch tab: Disk · System · Network · Gentoo · Packages · Install |
| `↑/k ↓/j` | Navigate options |
| `Enter` | Edit option |
| `Space` | Toggle checkboxes / multi-select entries |
| `?` | Help text for the selected option |
| `s` / `S` | Save / Save as |
| `i` | Start the installation (asks for confirmation) |
| `d` | Run the installation demo (simulated, touches no disks) |
| `q` | Quit (prompts when unsaved) |

The **Install** tab shows a summary of the current configuration plus the
exact disk layout tree that would be applied. `gentooinstall gif` drives the real
TUI demo inside the external [charmbracelet/vhs](https://github.com/charmbracelet/vhs)
virtual terminal (no TTY, no disks) and records the frames into an animated
GIF:

```sh
go install github.com/charmbracelet/vhs@latest   # one-time setup
./bin/gentooinstall gif                                  # writes ./demo.gif
./bin/gentooinstall gif path/to/demo.gif
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
parted + gnupg + GNU tar, installed via apk), so the ISO can partition, format
and install for real — no separate live stick required. Storage and network
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
  -device e1000,netdev=net0 \
  -nographic -serial stdio -monitor none
```

The single grub entry maps the serial port to `/dev/console` (so the TUI
renders over `-nographic -serial stdio`, no framebuffer window).

Booting the same ISO without OVMF (plain SeaBIOS) is a legacy-BIOS boot —
pair it with `builds/bios.toml` (`disk.boot_type = "bios"`).

## Releases

Versioned releases are built with [GoReleaser](https://goreleaser.com) and
published to GitHub Releases on `v*` tags (`.goreleaser.yml` + `.github/workflows/release.yml`).
Every release ships three artifact kinds:

- **source** — the full source archive,
- **binary** — statically linked `gentooinstall` for linux/amd64 and linux/arm64,
- **live ISO** — the bootable `gentooinstall-live-*.iso` described above.

```sh
goreleaser release                # create a release for the current tag
goreleaser release --snapshot    # dry run into dist/ without publishing
```

## Project overview

The installer performs the following main steps (in roughly this order),
with some parts depending on the chosen configuration:

1. Partition disks (highly dependent on configuration)
2. Download and extract stage3 tarball (with cryptographic verification)
   \[Continues in chroot from here\]
3. Setup portage (initial rsync/git sync, run mirrorselect, create zz-autounmask files)
4. Base system configuration (hostname, timezone, keymap, locales)
5. Install required packages (git, kernel, ...)
6. Make system bootable (generate fstab, build initramfs, create efibootmgr/syslinux boot entry)
7. Ensure minimal working system (automatic wired networking, install eix, set root password)
   - (Optional) Install sshd with secure config (no password logins)
   - (Optional) Install additional packages provided in config

## Recommendations

### EFI vs BIOS

Use EFI. BIOS is old and deprecated for a long time now.

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