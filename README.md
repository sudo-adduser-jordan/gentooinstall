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

`scripts/run-vm-test.sh` runs the real, destructive install path entirely
inside QEMU — **no privileges needed at test time**, so it behaves identically
on a laptop and on a GitHub Actions runner (no privileged containers involved;
ubuntu-24.04 runners disable unprivileged user namespaces, so there is nothing
to fall back to):

```sh
sudo make build-testkit   # builds the Debian "testkit" live image (root only)
make vm-test              # fully unprivileged; uses KVM, falls back to TCG
```

How it works:

1. **Boot 1** — the testkit (built by `scripts/build-testkit.sh`) boots under
   OVMF/EFI firmware, fetches an inject payload over the user-mode network
   (the binary, a config and an optional stage3 seed), and runs
   `gentooinstall install` with `GENTOOINSTALL_ASSUME_YES=1` against a fresh
   `/dev/vdb`. Every interactive prompt is answered with its intended default
   and the pre-apply countdown is skipped.
2. **Boot 2** — the installed disk is booted for real: via OVMF reusing the
   same NVRAM file (so the `efibootmgr` entry persists) for EFI targets, or
   plain SeaBIOS for BIOS targets. Reaching the multi-user target (a serial
   `login:` prompt) is the pass criterion.

The stage3 tarball can be seeded from the host with `--stage3 file.tar.xz`,
renamed to match the mirror's current listing so the installer's `.verified`
resume path is exercised instead of a live download. `.e2e-images/` (work
dir, logs, disk snapshots) is gitignored.

`.github/workflows/install-test.yml` runs the default (EFI/systemd) build on
every push touching the installer, and `workflow_dispatch` can pick layout
variants: `default`, `bios`, `luks-efi` and `btrfs-efi`.

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
(`bios.toml`), btrfs RAID (`btrfs-efi.toml`), mdadm RAID0/RAID1 + LUKS
(`raid0-efi.toml`, `raid1-efi.toml`), reuse of existing partitions
(`existing-efi.toml`), and ZFS (`zfs-efi.toml`).
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

The ISO contains only the built binary plus a minimal initramfs/kernel —
no desktop, no login, no partitioning tooling.

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