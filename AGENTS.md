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
make iso        # builds the live ISO (scripts/release.sh -> ./bin/gentooinstall.iso)
make vm-test    # QEMU boot e2e for the live ISO (skips if qemu is absent)
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
qemu-system-x86_64 -cdrom bin/gentooinstall.iso

```

Full install loop: create a drive image, boot the ISO with it attached to
install onto, then boot the installed OS from the drive (both with keyboard +
monitor):


```sh
qemu-img create -f qcow2 bin/gentoo-disk.img 20G

qemu-system-x86_64 -cdrom bin/gentooinstall.iso -drive file=bin/gentoo-disk.img,format=qcow2

qemu-system-x86_64 -drive file=bin/gentoo-disk.img,format=qcow2

qemu-system-x86_64 -drive file=bin/gentoo-disk.img,format=qcow2 -m 512

```

