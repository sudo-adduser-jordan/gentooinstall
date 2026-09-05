# AGENTS.md

Guidance for AI coding agents working in this repository.

## Repository layout

```
.
├── cmd/gentooinstall/            # entrypoint & CLI modes (install, gif, chroot)
├── assets/               # embedded static files (fstab, sshd_config, locales)
├── builds/               # shipped config templates (default/openrc/musl/…)
├── internal/
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

- **Go entrypoint lives in `cmd/gentooinstall`**, library code under `internal/` and
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
- External commands are executed through `internal/installer` helpers that
  log every invocation; never use `os/exec` ad hoc from other packages.
- The TUI can be driven headlessly through exported `Update`/`View`
  (`internal/tui` model). `gentooinstall gif` records the interactive demo by
  generating a VHS tape and running the external `charmbracelet/vhs` CLI
  (see `gifTape` in `cmd/gentooinstall/main.go`); install vhs with
  `go install github.com/charmbracelet/vhs@latest`.

## Build & test

```sh
make build      # produces ./bin/gentooinstall
make test       # go vet ./... && go test ./...
make fmt        # gofmt -l -w .
make iso        # builds the live ISO (scripts/release.sh -> ./bin/gentooinstall.iso;
                # needs network to dl-cdn.alpinelinux.org and host cpio/gzip/
                # grub-mkrescue/xorriso/modprobe)
make vm-test    # QEMU e2e: TestISOBoots + TestISOBootNetwork. Needs qemu-system,
                # qemu-img, grub-mkrescue/xorriso, a build-host kernel and
                # outbound access to the Gentoo mirror.
```

## Reference material when porting

| Bash source | Go counterpart |
|---|---|
| `example/scripts/config.sh` | `internal/disklayout/builder.go` |
| `example/scripts/utils.sh` (device resolution) | `internal/disklayout/resolver.go` |
| `example/scripts/functions.sh` | `internal/installer/*.go` |
| `example/scripts/main.sh` | `internal/installer/inchroot.go`, `kernel.go`, `network.go` |
| `example/configure` | `internal/tui/` |

## Notes for agents

- Run `make test` before declaring work complete.
- Never commit secrets; this repo has none, keep it that way.
- Commit messages: short imperative subject line, lowercase.

## Notes
Interactive boot (keyboard + monitor, opens the TUI in the window):

```sh
make iso
qemu-system-x86_64 -cdrom bin/gentooinstall.iso
```

Full install loop: create a drive image, boot the ISO with it attached to
install onto, then boot the installed OS from the drive (both with keyboard +
monitor). Without a NIC (and the DHCP/DNS it brings up) the tarball/mirror
fetches fail, so always attach a user-mode NIC:

```sh
make iso
qemu-img create -f qcow2 bin/gentoo-disk.img 20G

qemu-system-x86_64 \
  -cdrom bin/gentooinstall.iso \
  -drive file=bin/gentoo-disk.img,format=qcow2 \
  -netdev user,id=net0 \
  -device e1000,netdev=net0 -m 512

qemu-system-x86_64 -drive file=bin/gentoo-disk.img,format=qcow2 -m 512
```

Notes:
- `make iso` (scripts/release.sh) needs network to dl-cdn.alpinelinux.org to
  bootstrap the live rootfs, and host cpio/gzip/grub-mkrescue/xorriso/modprobe.
- The NIC must be one the live initramfs actually bundles: the ISO ships module
  files for the build-host kernel, and `e1000` is reliably present. QEMU's
  user-mode DHCP serves 10.0.2.2/3 and the live init writes /etc/resolv.conf.
- `virtio-net-pci` works only if the build-host kernel ships a standalone
  `virtio_net.ko`; distro kernels often build it in, so prefer `e1000`.
  `make vm-test`'s TestISOBootNetwork boot-tests exactly this e1000 + DHCP +
  DNS + mirror-reachability path on the serial console.

