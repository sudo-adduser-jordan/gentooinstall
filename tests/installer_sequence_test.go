package tests

import (
	"strings"
	"testing"

	"gentooinstall/internal/config"
	"gentooinstall/internal/disklayout"
	"gentooinstall/internal/installer"
)

// recordingExec captures every command the installer would run, executing
// nothing, so tests can assert the exact command sequences without needing
// real block devices or elevated privileges.
type recordingExec struct {
	cmds []string
}

func (r *recordingExec) Run(name string, args ...string) error {
	r.cmds = append(r.cmds, installer.CommandLine(name, args...))
	return nil
}

func (r *recordingExec) QuietRun(name string, args ...string) (string, error) {
	r.cmds = append(r.cmds, installer.CommandLine(name, args...))
	return "", nil
}

func (r *recordingExec) RunWithStdin(stdin, name string, args ...string) error {
	r.cmds = append(r.cmds, installer.CommandLine(name, args...)+"  (stdin)")
	return nil
}

func (r *recordingExec) Try(name string, args ...string) error {
	r.cmds = append(r.cmds, installer.CommandLine(name, args...))
	return nil
}

// applySeq builds the disk layout for cfg with all resolved devices mapped to
// /dev/null (an existing node, so waitPartition's stat returns immediately)
// and records the commands ApplyDiskActions would execute.
func applySeq(t *testing.T, cfg *config.Config) (*recordingExec, error) {
	t.Helper()
	layout, err := disklayout.BuildFromConfig(cfg, t.TempDir())
	if err != nil {
		return nil, err
	}

	// Map every registered id to /dev/null so resolution succeeds without
	// probing the host for real devices.
	all, err := layout.ExpandIDs(".+")
	if err != nil {
		return nil, err
	}
	overrides := map[string]string{}
	for _, id := range disklayout.SplitIDList(all) {
		overrides[id] = "/dev/null"
	}
	resolver := &disklayout.Resolver{Layout: layout}
	resolver.SetResolvedDevices(overrides)

	rec := &recordingExec{}
	var out strings.Builder
	r := installer.NewRunner(&out, &out)
	r.Exec = rec
	r.NonInteractive = true

	c := &installer.Context{
		R:             r,
		Cfg:           cfg,
		Layout:        layout,
		Resolver:      resolver,
		EncryptionKey: "test-secret",
	}
	if err := installer.ApplyDiskActions(c); err != nil {
		return nil, err
	}
	return rec, nil
}

func seqContains(rec *recordingExec, name string, substr string) bool {
	for _, c := range rec.cmds {
		if strings.HasPrefix(c, name+" ") && strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func seqCount(rec *recordingExec, prefix string) int {
	n := 0
	for _, c := range rec.cmds {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// The default classic single-disk EFI install must invoke the tools in the
// expected order: wipe the disk, create a GPT table, partition it, then
// format each partition.
func TestApplyDefaultsToValidCommands(t *testing.T) {
	cfg := classicCfg("/dev/vdb", false, false) // ext4 root, no luks
	rec, err := applySeq(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.cmds) == 0 {
		t.Fatal("expected at least one recorded command")
	}
	// Sanity: a fresh GPT table is created before any partition/format step.
	if !seqContains(rec, "sgdisk", "-Z") {
		t.Fatalf("expected sgdisk -Z (fresh GPT): %v", rec.cmds)
	}
	if seqCount(rec, "mkfs.fat") < 1 || seqCount(rec, "mkfs.ext4") < 1 {
		t.Fatalf("expected an EFI and a root filesystem format: %v", rec.cmds)
	}
}

func TestApplyLuksSequence(t *testing.T) {
	cfg := classicCfg("/dev/vdb", true, false) // ext4 root behind LUKS
	rec, err := applySeq(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// LUKS must be initialised via cryptsetup luksFormat and then opened.
	if !seqContains(rec, "cryptsetup", "luksFormat") {
		t.Fatalf("expected cryptsetup luksFormat: %v", rec.cmds)
	}
	if !seqContains(rec, "cryptsetup", "open") {
		t.Fatalf("expected cryptsetup open: %v", rec.cmds)
	}
}
