# AGENTS.md

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

Notes:
- The stage3 tarball is staged on the **target root filesystem**, not the
  RAM-backed `/tmp` of the live ISO, so the install itself works on machines
  with as little as 512MB. Give the full-install QEMU run at least 1G
  (`-m 1024`) so the in-chroot work (portage sync, kernel build) has room.
- `make iso` (scripts/release.sh) needs network to dl-cdn.alpinelinux.org to
  bootstrap the live rootfs, and host cpio/gzip/grub-mkrescue/xorriso/modprobe.
- The NIC must be one the live initramfs actually bundles: the ISO ships module
  files for the build-host kernel. QEMU's user-mode DHCP serves 10.0.2.2/3 and
  the live init writes /etc/resolv.conf.
- Prefer `-device e1000` in the QEMU examples: although virtio-net's ring
  buffer is faster than the emulated e1000 NIC, QEMU's default `-netdev user`
  (SLIRP) backend is a userspace TCP/IP stack that becomes the throughput
  bottleneck either way, and in practice e1000 delivers better real-world
  download speed there. The live initramfs bundles both `e1000` and
  `virtio_net` (as long as the build-host kernel ships standalone `.ko` files)
  and always tries DHCP on whichever NIC QEMU attaches. Use `virtio-net-pci`
  with the tap-device example below (or another non-SLIRP backend) where the
  ring-buffer virtio path actually pays off. `make vm-test`'s
  TestISOBoot/TestISOBootNetwork boot-test the `e1000` + DHCP + DNS +
  mirror-reachability path on the serial console deterministically.
- FAT/vfat (`fat`, `vfat`, `nls_cp437`, `nls_ascii`) are bundled for the ESP
  and the FAT32 bios_grub partition only when the build-host kernel ships them
  as modules; when `CONFIG_VFAT_FS=y` the ISO relies on the built-in driver.
  Both module lists (scripts/release.sh `MODULES` + live.NeedModules) stay in
  sync; a release host with `=m` but no installed `.ko` produces an ISO that
  cannot mount `/boot/efi` or `/boot/bios` ("unknown filesystem type vfat").

