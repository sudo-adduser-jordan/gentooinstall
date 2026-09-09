// Shared test harness: recording ExecStub and scratch-filesystem helpers.
package tests

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"gentooinstall/lib/config"
	"gentooinstall/lib/disklayout"
	"gentooinstall/lib/installer"
)

// discardWriter satisfies io.Writer while surfacing unexpected writes.
type discardWriter struct{ t *testing.T }

func (writer discardWriter) Write(data []byte) (int, error) {
	writer.t.Logf("runner output: %s", strings.TrimSpace(string(data)))
	return len(data), nil
}

// Call records a single command invocation seen by ExecStub.
type Call struct {
	Name     string
	Args     []string
	Stdin    string
	QuietRun bool
}

// Line renders the invocation like the installer would log it.
func (call Call) Line() string { return installer.CommandLine(call.Name, call.Args...) }

// ExecStub implements installer.CommandExecutor in memory, recording every
// invocation so tests can assert exact command sequences without running
// anything on the host.
type ExecStub struct {
	mu    sync.Mutex
	calls []Call
	queue []error

	// QuietOuts returns output for QuietRun calls, keyed by commandline.
	QuietOuts map[string]string

	// FailOn aborts any call whose commandline contains one of these
	// substrings with a synthetic error.
	FailOn []string
}

// NewExecStub returns an empty recording stub.
func NewExecStub() *ExecStub {
	return &ExecStub{QuietOuts: map[string]string{}}
}

func (stub *ExecStub) record(name string, args []string, quiet bool, stdin string) error {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	line := installer.CommandLine(name, args...)
	stub.calls = append(stub.calls, Call{
		Name: name, Args: append([]string{}, args...), Stdin: stdin, QuietRun: quiet,
	})
	if len(stub.queue) > 0 {
		err := stub.queue[0]
		stub.queue = stub.queue[1:]
		if err != nil {
			return err
		}
	}
	for _, sub := range stub.FailOn {
		if strings.Contains(line, sub) {
			return fmt.Errorf("scripted failure for %s", line)
		}
	}
	return nil
}

// FailNext scripted the next invocation to return err.
func (stub *ExecStub) FailNext(err error) { stub.queue = append(stub.queue, err) }

// Run records an invocation.
func (stub *ExecStub) Run(name string, args ...string) error {
	return stub.record(name, args, false, "")
}

// QuietRun records an invocation and returns scripted output.
func (stub *ExecStub) QuietRun(name string, args ...string) (string, error) {
	line := installer.CommandLine(name, args...)
	if err := stub.record(name, args, true, ""); err != nil {
		return "", err
	}
	return stub.QuietOuts[line], nil
}

// RunWithStdin records an invocation carrying stdin.
func (stub *ExecStub) RunWithStdin(stdin, name string, args ...string) error {
	return stub.record(name, args, false, stdin)
}

// Try records an invocation.
func (stub *ExecStub) Try(name string, args ...string) error {
	return stub.record(name, args, false, "")
}

// Calls returns a copy of all recorded invocations.
func (stub *ExecStub) Calls() []Call {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return append([]Call{}, stub.calls...)
}

// Lines returns all recorded commandlines in order.
func (stub *ExecStub) Lines() []string {
	var out []string
	for _, call := range stub.Calls() {
		out = append(out, call.Line())
	}
	return out
}

// assertCmds requires the recorded commandlines to match want exactly.
func assertCmds(testingT *testing.T, stub *ExecStub, want ...string) {
	testingT.Helper()
	got := stub.Lines()
	if !reflect.DeepEqual(got, want) {
		testingT.Fatalf("command sequence:\n  got  %q\n  want %q", got, want)
	}
}

// assertCmdContains requires want to appear as a contiguous run of recorded
// commandlines.
func assertCmdContains(testingT *testing.T, stub *ExecStub, want []string) {
	testingT.Helper()
	got := stub.Lines()
outer:
	for start := 0; start+len(want) <= len(got); start++ {
		for off := range want {
			if got[start+off] != want[off] {
				continue outer
			}
		}
		return
	}
	testingT.Fatalf("command sequence %q missing run %q;\n  got %q", want, want, got)
}

// seedUUID pre-writes a fixed uuid for an id into a UUIDStore dir so
// BuildFromConfig produces deterministic sgdisk/mdadm/cryptsetup uuids.
func seedUUID(testingT *testing.T, dir, id, uuid string) {
	testingT.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		testingT.Fatal(err)
	}
	name := base64.StdEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(id))
	if err := os.WriteFile(filepath.Join(dir, name), []byte(uuid+"\n"), 0o644); err != nil {
		testingT.Fatal(err)
	}
}

// layoutOverrides maps every layout id to a fictitious device path.
func layoutOverrides(layout *disklayout.Layout) map[string]string {
	mapping := map[string]string{}
	add := func(id string) {
		if id == "" {
			return
		}
		mapping[id] = "/dev/fake-" + strings.ReplaceAll(id, "/", "-")
	}
	for _, action := range layout.Actions {
		add(action.NewID)
		add(action.ID)
		for _, id := range action.IDs {
			add(id)
		}
	}
	add(layout.RootID)
	add(layout.EFIID)
	add(layout.BIOSID)
	add(layout.SwapID)
	return mapping
}

// testContext builds an installer.Context wired to an ExecStub, a mock
// resolver (id -> /dev/fake-*), a BlkidUUID stub and a scratch Root so the
// engine can run entirely in-process without touching the host.
func testContext(testingT *testing.T, cfg *config.Config, uuidSeeds map[string]string) (*installer.Context, *ExecStub) {
	testingT.Helper()
	stub := NewExecStub()
	runner := installer.NewRunner(io.Discard, io.Discard)
	runner.Exec = stub
	runner.OnFailure = installer.DefaultOnFailure
	runner.NonInteractive = true
	runner.LookPath = func(string) bool { return false }

	uuidDir := testingT.TempDir()
	for id, uuid := range uuidSeeds {
		seedUUID(testingT, uuidDir, id, uuid)
	}
	layout, err := disklayout.BuildFromConfig(cfg, uuidDir)
	if err != nil {
		testingT.Fatalf("BuildFromConfig: %v", err)
	}
	res := &disklayout.Resolver{Layout: layout}
	res.SetResolvedDevices(layoutOverrides(layout))

	ctx := &installer.Context{
		Runner:        runner,
		Cfg:           cfg,
		Layout:        layout,
		Resolver:      res,
		EncryptionKey: "test-passphrase",
		Root:          testingT.TempDir(),
		BlkidUUID: func(string) (string, error) {
			return "00000000-1111-2222-3333-444444444444", nil
		},
		IsMountpoint: func(string) bool { return false },
		Stat:         func(string) (os.FileInfo, error) { return nil, nil },
		NProc:        8,
	}
	return ctx, stub
}

// readScratch returns the raw content of a file the installer wrote under the
// context root.
func readScratch(testingT *testing.T, ctx *installer.Context, path string) string {
	testingT.Helper()
	data, err := os.ReadFile(filepath.Join(ctx.Root, path))
	if err != nil {
		testingT.Fatalf("read scratch %s: %v", path, err)
	}
	return string(data)
}

// writeScratch plants a fixture file under the context root.
func writeScratch(testingT *testing.T, ctx *installer.Context, path, content string) {
	testingT.Helper()
	full := filepath.Join(ctx.Root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		testingT.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		testingT.Fatal(err)
	}
}

// mkScratchDir creates a directory under the context root.
func mkScratchDir(testingT *testing.T, ctx *installer.Context, path string) {
	testingT.Helper()
	if err := os.MkdirAll(filepath.Join(ctx.Root, path), 0o755); err != nil {
		testingT.Fatal(err)
	}
}

// symlinkScratch plants a symbolic link under the context root.
func symlinkScratch(testingT *testing.T, ctx *installer.Context, target, path string) {
	testingT.Helper()
	full := filepath.Join(ctx.Root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		testingT.Fatal(err)
	}
	if err := os.Symlink(target, full); err != nil {
		testingT.Fatal(err)
	}
}
