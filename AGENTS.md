# AGENTS.md

Guidance for AI coding agents working in this repository.

## Repository layout

```
.
├── main.go                 # entrypoint & CLI modes (install, demo, chroot)
├── assets/               # embedded static files (fstab, sshd_config, locales)
├── builds/               # shipped config templates (default/openrc/musl/…)
├── data/                 # static per-repo package lists (data/repos/*.packages)
├── lib/
│   ├── config/           # TOML model, defaults, validation, template paths
│   ├── sysinfo/          # devices, keymaps, timezones, locales, EFI detection
│   ├── disklayout/       # declarative disk actions, presets, id->device resolver
│   ├── installer/        # install engine (partitioning, stage3, chroot, ...)
│   └── tui/              # Bubble Tea TUI (numbered tabs, pVPN-style)
├── scripts/              # release.sh (live ISO), packages.sh (package lists)
├── tests/                # ALL Go tests (external package)
├── .goreleaser.yml       # releases: source archive + static binaries
├── .github/workflows/    # CI (release.yml attaches the live ISO)
├── example/              # Original bash implementation kept as reference
│   ├── configure         # dialog-based configurator
│   ├── install           # installer driver
│   └── scripts/          # utils.sh config.sh functions.sh main.sh ...
└── README.md             # project overview
```

## Conventions

- **Go entrypoint is `main.go`** at the repo root, library code under `lib/` and
  all tests in `tests/`. Do not modify anything in `example/` except to fix
  factual comments; it is a frozen reference of the legacy bash behavior used
  to verify the port.
- All Go tests go into `tests/` (`package tests`). They must exercise
  only exported identifiers; do not add `_test.go` files next to production
  packages.
- Config format is TOML and build configs are sourced from `builds/`. Only
  `builds/custom.toml` (gitignored) is ever written by the TUI; shipped
  templates (`default.toml` + variants) are read-only. Keep round-tripping
  lossless: loading + saving a file must preserve all fields.
- Disk layouts are built via `disklayout.BuildFromConfig`; presets must stay
  byte-compatible with the bash behavior in
  `example/scripts/config.sh` (same action order, same ids).
- External commands are executed through `lib/installer` helpers that
  log every invocation; never use `os/exec` ad hoc from other packages.
- The TUI can be driven headlessly through exported `Update`/`View`
  (`lib/tui` model). `gentooinstall demo` records the interactive demo by
  generating a VHS tape and running the external `charmbracelet/vhs` CLI
  (see `demoTape` in `main.go`); install vhs with
  `go install github.com/charmbracelet/vhs@latest`.

## Build & test

```sh
make build      # produces ./bin/gentooinstall
make test       # go vet ./... && go test ./...
make fmt        # gofmt -l -w .
make iso        # builds the live ISO (scripts/release.sh -> ./bin/gentooinstall.iso;
                # needs network to dl-cdn.alpinelinux.org and host cpio/gzip/
                # grub-mkrescue/xorriso/modprobe)
make vm-test    # QEMU e2e: TestISOBoots (boot to the TUI on the serial console).
                # Needs qemu-system, qemu-img, grub-mkrescue/xorriso, a
                # build-host kernel and outbound access to the Gentoo mirror.
make vm-install # QEMU e2e: TestInstallInVM runs a FULL install for every
                # builds/*.toml inside a VM (headless install via the
                # gentooinstall.install= kernel flag). Very slow; opt-in.
```

## Reference material when porting

| Bash source | Go counterpart |
|---|---|
| `example/scripts/config.sh` | `lib/disklayout/builder.go` |
| `example/scripts/utils.sh` (device resolution) | `lib/disklayout/resolver.go` |
| `example/scripts/functions.sh` | `lib/installer/*.go` |
| `example/scripts/main.sh` | `lib/installer/inchroot.go`, `kernel.go`, `network.go` |
| `example/configure` | `lib/tui/` |

## Notes for agents

- Run `make test` before declaring work complete.
- Never commit secrets; this repo has none, keep it that way.
- Commit messages: short imperative subject line, lowercase.

## Notes

```sh
make iso
qemu-img create -f qcow2 bin/gentoo-disk.img 20G

# UEFI boot (required for the default EFI configs: disk.boot_type = "efi"):
# VARS must be a writable copy.
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



# Legacy-BIOS boot (use with builds/bios.toml: disk.boot_type = "bios"):
# no OVMF -> SeaBIOS -> no /sys/firmware/efi; an EFI config fails here.
# NOTE: if=virtio shows up as /dev/vda (use that for Disk > Device);
# if your config expects /dev/sda, use if=ide instead of if=virtio.
qemu-system-x86_64 \
  -enable-kvm \
  -cpu host \
  -smp $(nproc) \
  -m 4G \
  -cdrom bin/gentooinstall.iso \
  -boot d \
  -drive file=bin/gentoo-disk.img,format=qcow2,if=virtio,cache=writeback \
  -netdev user,id=net0 \
  -device e1000,netdev=net0 \
  -nographic -serial stdio -monitor none


make iso
qemu-img create -f qcow2 bin/gentoo-disk.img 20G

# UEFI install (default EFI configs, disk.boot_type = "efi"):
# attach OVMF firmware + NIC + disk. VARS must be a writable copy.
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

# BIOS install (ONLY with builds/bios.toml, disk.boot_type = "bios"):
# no OVMF -> SeaBIOS -> no /sys/firmware/efi; an EFI config fails here.
# NOTE: if=virtio shows up as /dev/vda (use that for Disk > Device);
# if your config expects /dev/sda, use if=ide instead of if=virtio.
make iso
qemu-img create -f qcow2 bin/gentoo-disk.img 20G
qemu-system-x86_64 \
  -enable-kvm \
  -cpu host \
  -smp $(nproc) \
  -m 8G \
  -cdrom bin/gentooinstall.iso \
  -boot d \
  -drive file=bin/gentoo-disk.img,format=qcow2,if=virtio,cache=writeback \
  -netdev user,id=net0 \
  -device e1000,netdev=net0 \
  -nographic -serial stdio -monitor none

# Boot the installed OS (firmware must match the installed boot type; for EFI
# targets reuse the same writable VARS file so the efibootmgr entry persists):
# BIOS:
qemu-system-x86_64 \
  -enable-kvm \
  -cpu host \
  -smp $(nproc) \
  -m 4G \
  -drive file=bin/gentoo-disk.img,format=qcow2,if=virtio,cache=writeback \
  -netdev user,id=net0 \
  -device e1000,netdev=net0 \
  -nographic -serial stdio -monitor none



# UEFI: same as above plus the two pflash drives:
# -drive if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/x64/OVMF_CODE.4m.fd \
# -drive if=pflash,format=raw,file=/tmp/OVMF_VARS.fd \

# Dev/CI: same BIOS fast command boots headless into the TUI inside the
# terminal. The single grub entry "Gentoo Install" sets console=ttyS0 only,
# so /dev/console IS the serial port: with -nographic -serial stdio the TUI
# renders directly in the terminal that launched QEMU.
# make vm-test covers this path headlessly (TestISOBoots asserts the PID 1
# banner and the "live: tui starting" marker appear on the serial console).

# Headless full install (the o/vm-install driver): stage a config as
# builds/custom.toml and add the gentooinstall.install kernel flag so PID 1
# runs `gentooinstall install` non-interactively, prints
# "gentooinstall install: success" on the serial console and powers off. The
# VM install test (tests/install_vm_test.go) drives this for every template:
# GENTOOINSTALL_INSTALL_CFG=$PWD/builds/bios.toml make iso
# qemu-img create -f qcow2 bin/gentoo-disk.img 20G
# qemu-system-x86_64 \
#   -enable-kvm \
#   -cpu host \
#   -smp $(nproc) \
#   -m 4G \
#   -cdrom bin/gentooinstall.iso \
#   -boot d \
#   -drive file=bin/gentoo-disk.img,format=qcow2,if=virtio,cache=writeback \
#   -netdev user,id=net0 \
#   -device e1000,netdev=net0 \
#   -nographic -serial stdio -monitor none

```

Notes:
- The stage3 tarball is staged on the **target root filesystem**, not the
  RAM-backed `/tmp` of the live ISO, so the install itself works on machines
  with as little as 512MB. Give the full-install QEMU run at least 1G
  (`-m 1024`) so the in-chroot work (portage sync, kernel build) has room.
- `make iso` (scripts/release.sh) needs network to dl-cdn.alpinelinux.org to
  bootstrap the live rootfs, and host cpio/gzip/grub-mkrescue/xorriso/modprobe.
- The NIC must be one the live initramfs actually bundles: the ISO ships module
  files for the build-host kernel, and `e1000` is reliably present. QEMU's
  user-mode DHCP serves 10.0.2.2/3 and the live init writes /etc/resolv.conf.
- `virtio-net-pci` works only if the build-host kernel ships a standalone
  `virtio_net.ko`; distro kernels often build it in, so prefer `e1000`.
  `make vm-test`'s TestISOBootNetwork boot-tests exactly this e1000 + DHCP +
  DNS + mirror-reachability path on the serial console.
- FAT/vfat (`fat`, `vfat`, `nls_cp437`, `nls_ascii`) are bundled for the ESP
  and the FAT32 bios_grub partition only when the build-host kernel ships them
  as modules; when `CONFIG_VFAT_FS=y` the ISO relies on the built-in driver.
  Both module lists (scripts/release.sh `MODULES` + live.NeedModules) stay in
  sync; a release host with `=m` but no installed `.ko` produces an ISO that
  cannot mount `/boot/efi` or `/boot/bios` ("unknown filesystem type vfat").

