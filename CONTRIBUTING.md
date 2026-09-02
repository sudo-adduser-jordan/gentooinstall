# Contributing

Entrypoint and library code live under `cmd/` and `internal/` (the Go/Bubble
Tea implementation). Read [AGENTS.md](AGENTS.md) for repository conventions
before your first change; this document covers day-to-day workflow and how
to develop against the terminal UI.

## Build & test

```sh
make build     # -> ./bin/gentooinstall
make test      # go vet ./... && go test ./...
make fmt       # gofmt -l -w .
make iso       # -> dist/gentooinstall-live-amd64.iso
```

All Go tests live in `tests/` (`package tests`) and exercise only
exported APIs — do not add `_test.go` files next to production packages.

## Developing the TUI

### Run the real UI

```sh
make build
./bin/gentooinstall builds/custom.toml  # any path; created on first save
./bin/gentooinstall                     # opens builds/custom.toml if present,
                                # else the shipped builds/default.toml
```

Requirements:

- An **interactive terminal** (plain SSH, tmux, alacritty, …). Bubble Tea
  will not render without a TTY.
- For vivid styling set `TERM=xterm-256color COLORTERM=truecolor`.

**Safety:** the configurator only edits TOML files. Destructive paths are
triple-guarded — Install-tab confirm modal → root check → countdown
before partitioning — so you can explore every tab as your normal user
without any risk to your system.

### Headless iteration (CI-friendly, no terminal)

The model API is exported (`tui.New`, `Update`, `View`, `Config`,
`ActiveTab`, `Dirty`), so interaction can be driven by injecting
`tea.KeyMsg` values — see `tests/tui_test.go`:

```sh
go test ./tests/ -run Tui -v
```

`./bin/gentooinstall gif` renders a recording of the simulated install demo into an
animated GIF without a TTY or any disk access. Changing the demo steps or the
captured layout is done in the gif tape code (`cmd/gentooinstall/main.go`),
not in the interactive flow.

To *see* rendered frames while developing, drop a throwaway test in
`tests/` that prints the view directly:

```go
m := tui.New(config.Default(true), "/tmp/x.toml")
mm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
fmt.Println(mm.(*tui.Model).View()) // full ANSI frame
```

### Fast rebuild loop

```sh
find . -name '*.go' | entr -crs 'make build && ./bin/gentooinstall'
```

### Testing the install path

The installer is destructive by design — never run `install` against your
host disks. There is no automated in-CI test that drives the real
block-device install (partitioning, LUKS, mdadm, btrfs, stage3 extraction,
chroot). CI runs only the hermetic unit test suite via `make test`, which
covers the pure planning and configuration logic; `make test` stays hermetic
and does not require any containers or elevated privileges.
