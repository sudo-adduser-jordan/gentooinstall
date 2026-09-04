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

	"gentooinstall/internal/config"
	"gentooinstall/internal/disklayout"
	"gentooinstall/internal/installer"
)

// discardWriter satisfies io.Writer while surfacing unexpected writes.
type discardWriter struct{ t *testing.T }

func (w discardWriter) Write(p []byte) (int, error) {
	w.t.Logf("runner output: %s", strings.TrimSpace(string(p)))
	return len(p), nil
}

// Call records a single command invocation seen by ExecStub.
type Call struct {
	Name     string
	Args     []string
	Stdin    string
	QuietRun bool
}

// Line renders the invocation like the installer would log it.
func (c Call) Line() string { return installer.CommandLine(c.Name, c.Args...) }

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

func (s *ExecStub) record(name string, args []string, quiet bool, stdin string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	line := installer.CommandLine(name, args...)
	s.calls = append(s.calls, Call{
		Name: name, Args: append([]string{}, args...), Stdin: stdin, QuietRun: quiet,
	})
	if len(s.queue) > 0 {
		err := s.queue[0]
		s.queue = s.queue[1:]
		if err != nil {
			return err
		}
	}
	for _, sub := range s.FailOn {
		if strings.Contains(line, sub) {
			return fmt.Errorf("scripted failure for %s", line)
		}
	}
	return nil
}

// FailNext scripted the next invocation to return err.
func (s *ExecStub) FailNext(err error) { s.queue = append(s.queue, err) }

// Run records an invocation.
func (s *ExecStub) Run(name string, args ...string) error {
	return s.record(name, args, false, "")
}

// QuietRun records an invocation and returns scripted output.
func (s *ExecStub) QuietRun(name string, args ...string) (string, error) {
	line := installer.CommandLine(name, args...)
	if err := s.record(name, args, true, ""); err != nil {
		return "", err
	}
	return s.QuietOuts[line], nil
}

// RunWithStdin records an invocation carrying stdin.
func (s *ExecStub) RunWithStdin(stdin, name string, args ...string) error {
	return s.record(name, args, false, stdin)
}

// Try records an invocation.
func (s *ExecStub) Try(name string, args ...string) error {
	return s.record(name, args, false, "")
}

// Calls returns a copy of all recorded invocations.
func (s *ExecStub) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call{}, s.calls...)
}

// Lines returns all recorded commandlines in order.
func (s *ExecStub) Lines() []string {
	var out []string
	for _, c := range s.Calls() {
		out = append(out, c.Line())
	}
	return out
}

// assertCmds requires the recorded commandlines to match want exactly.
func assertCmds(t *testing.T, s *ExecStub, want ...string) {
	t.Helper()
	got := s.Lines()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("command sequence:\n  got  %q\n  want %q", got, want)
	}
}

// assertCmdContains requires want to appear as a contiguous run of recorded
// commandlines.
func assertCmdContains(t *testing.T, s *ExecStub, want []string) {
	t.Helper()
	got := s.Lines()
outer:
	for i := 0; i+len(want) <= len(got); i++ {
		for j := range want {
			if got[i+j] != want[j] {
				continue outer
			}
		}
		return
	}
	t.Fatalf("command sequence %q missing run %q;\n  got %q", want, want, got)
}

// seedUUID pre-writes a fixed uuid for an id into a UUIDStore dir so
// BuildFromConfig produces deterministic sgdisk/mdadm/cryptsetup uuids.
func seedUUID(t *testing.T, dir, id, u string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := base64.StdEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(id))
	if err := os.WriteFile(filepath.Join(dir, name), []byte(u+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// layoutOverrides maps every layout id to a fictitious device path.
func layoutOverrides(l *disklayout.Layout) map[string]string {
	m := map[string]string{}
	add := func(id string) {
		if id == "" {
			return
		}
		m[id] = "/dev/fake-" + strings.ReplaceAll(id, "/", "-")
	}
	for _, a := range l.Actions {
		add(a.NewID)
		add(a.ID)
		for _, id := range a.IDs {
			add(id)
		}
	}
	add(l.RootID)
	add(l.EFIID)
	add(l.BIOSID)
	add(l.SwapID)
	return m
}

// testContext builds an installer.Context wired to an ExecStub, a mock
// resolver (id -> /dev/fake-*), a BlkidUUID stub and a scratch Root so the
// engine can run entirely in-process without touching the host.
func testContext(t *testing.T, cfg *config.Config, uuidSeeds map[string]string) (*installer.Context, *ExecStub) {
	t.Helper()
	stub := NewExecStub()
	r := installer.NewRunner(io.Discard, io.Discard)
	r.Exec = stub
	r.OnFailure = installer.DefaultOnFailure
	r.NonInteractive = true
	r.LookPath = func(string) bool { return false }

	uuidDir := t.TempDir()
	for id, u := range uuidSeeds {
		seedUUID(t, uuidDir, id, u)
	}
	layout, err := disklayout.BuildFromConfig(cfg, uuidDir)
	if err != nil {
		t.Fatalf("BuildFromConfig: %v", err)
	}
	res := &disklayout.Resolver{Layout: layout}
	res.SetResolvedDevices(layoutOverrides(layout))

	c := &installer.Context{
		R:             r,
		Cfg:           cfg,
		Layout:        layout,
		Resolver:      res,
		EncryptionKey: "test-passphrase",
		Root:          t.TempDir(),
		BlkidUUID: func(string) (string, error) {
			return "00000000-1111-2222-3333-444444444444", nil
		},
		IsMountpoint: func(string) bool { return false },
		NProc:        8,
	}
	return c, stub
}

// readScratch returns the raw content of a file the installer wrote under the
// context root.
func readScratch(t *testing.T, c *installer.Context, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(c.Root, path))
	if err != nil {
		t.Fatalf("read scratch %s: %v", path, err)
	}
	return string(data)
}

// writeScratch plants a fixture file under the context root.
func writeScratch(t *testing.T, c *installer.Context, path, content string) {
	t.Helper()
	full := filepath.Join(c.Root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mkScratchDir creates a directory under the context root.
func mkScratchDir(t *testing.T, c *installer.Context, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(c.Root, path), 0o755); err != nil {
		t.Fatal(err)
	}
}

// symlinkScratch plants a symbolic link under the context root.
func symlinkScratch(t *testing.T, c *installer.Context, target, path string) {
	t.Helper()
	full := filepath.Join(c.Root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, full); err != nil {
		t.Fatal(err)
	}
}
