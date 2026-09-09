// Disk layout construction from config (disklayout.BuildFromConfig).
package tests

import (
	"strings"
	"testing"

	"gentooinstall/lib/config"
	"gentooinstall/lib/disklayout"
)

func kinds(layout *disklayout.Layout) []string {
	out := make([]string, 0, len(layout.Actions))
	for _, action := range layout.Actions {
		out = append(out, string(action.Action))
	}
	return out
}

func join(ks []string) string { return strings.Join(ks, ",") }

func TestClassicSingleDisk(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", true, false)
	layout, err := disklayout.BuildFromConfig(cfg, testingT.TempDir())
	if err != nil {
		testingT.Fatal(err)
	}
	got := join(kinds(layout))
	want := "create_gpt,create_partition,create_partition,create_partition,create_luks,format,format,format"
	if got != want {
		testingT.Fatalf("actions:\n got %s\nwant %s", got, want)
	}
	if layout.RootID != "part_luks_root" || layout.EFIID != "part_efi" || layout.SwapID != "part_swap" {
		testingT.Fatalf("roles wrong: %+v", layout)
	}
	if layout.RootFSType != "ext4" || layout.RootMountOpts != "defaults,noatime,errors=remount-ro,discard" {
		testingT.Fatalf("root fs opts wrong: %q %q", layout.RootFSType, layout.RootMountOpts)
	}

	uuid, ok := layout.UUIDOf("part_luks_root")
	if !ok || uuid == "" {
		testingT.Fatalf("luks uuid missing")
	}
	found := false
	for _, dc := range layout.DracutCmdline {
		if dc == "rd.luks.uuid="+uuid {
			found = true
		}
	}
	if !found {
		testingT.Fatalf("dracut cmdline missing luks uuid: %v", layout.DracutCmdline)
	}
	if !layout.Flags.UsedLuks || !layout.Flags.UsedEncryption || layout.Flags.UsedRaid || layout.Flags.UsedZFS {
		testingT.Fatalf("flags wrong: %+v", layout.Flags)
	}
}

func TestClassicNoLuksBtrfsNoSwap(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, true)
	cfg.Disk.UseSwap = false
	layout, err := disklayout.BuildFromConfig(cfg, "")
	if err != nil {
		testingT.Fatal(err)
	}
	got := join(kinds(layout))
	want := "create_gpt,create_partition,create_partition,format,format"
	if got != want {
		testingT.Fatalf("got %s want %s", got, want)
	}
	if layout.SwapID != "" {
		testingT.Fatal("swap should be unset")
	}
	if layout.RootFSType != "btrfs" ||
		layout.RootMountOpts != "defaults,noatime,compress-force=zstd,subvol=/root" {
		testingT.Fatalf("btrfs opts: %q", layout.RootMountOpts)
	}
	if !layout.Flags.UsedBtrfs {
		testingT.Fatal("UsedBtrfs not set")
	}
}

func TestClassicBiosSingleDisk(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Disk.BootType = "bios"
	layout, err := disklayout.BuildFromConfig(cfg, "")
	if err != nil {
		testingT.Fatal(err)
	}
	got := join(kinds(layout))
	want := "create_gpt,create_partition,create_partition,create_partition,format,format,format"
	if got != want {
		testingT.Fatalf("actions:\n got %s\nwant %s", got, want)
	}
	if layout.EFIID != "" || layout.BIOSID != "part_bios" {
		testingT.Fatalf("boot roles wrong: EFIID=%q BIOSID=%q", layout.EFIID, layout.BIOSID)
	}
	if layout.RootID != "part_root" || layout.SwapID != "part_swap" {
		testingT.Fatalf("roles wrong: %+v", layout)
	}
	// The bios_grub partition is formatted as FAT32 like bash does.
	boot := layout.Actions[len(layout.Actions)-3]
	if boot.ID != "part_bios" || boot.Type != "bios" {
		testingT.Fatalf("bios boot action: %+v", boot)
	}
	if layout.Flags.NoPartitioningOrFormatting {
		testingT.Fatal("classic must partition")
	}
}

func TestZfsCentricMultiDisk(testingT *testing.T) {
	cfg := config.Default(true)
	cfg.Disk.Scheme = config.SchemeZFSCentric
	cfg.Disk.Devices = []string{"/dev/sda", "/dev/sdb"}
	cfg.Disk.ZFSEncrypt = true
	cfg.Disk.ZFSUseCompress = true

	layout, err := disklayout.BuildFromConfig(cfg, "")
	if err != nil {
		testingT.Fatal(err)
	}
	got := join(kinds(layout))
	// Default config enables 8GiB swap, so a swap partition+format exists.
	want := "create_gpt,create_partition,create_partition,create_partition,create_dummy,format,format,format_zfs"
	if got != want {
		testingT.Fatalf("got %s want %s", got, want)
	}
	zfs := layout.Actions[len(layout.Actions)-1]
	if len(zfs.IDs) != 2 || zfs.IDs[0] != "part_root_dev0" || zfs.IDs[1] != "root_dev1" {
		testingT.Fatalf("zfs members: %v", zfs.IDs)
	}
	if zfs.Compress != "zstd" || !zfs.Encrypt {
		testingT.Fatalf("zfs opts: %+v", zfs)
	}
	if layout.RootID != "part_root_dev0" || layout.RootFSType != "zfs" || layout.EFIID != "part_efi_dev0" {
		testingT.Fatalf("roles: %+v", layout)
	}
	if !layout.Flags.UsedZFS || !layout.Flags.UsedEncryption {
		testingT.Fatalf("flags: %+v", layout.Flags)
	}
}

func TestBtrfsCentricWithLuks(testingT *testing.T) {
	cfg := config.Default(true)
	cfg.Disk.Scheme = config.SchemeBtrfs
	cfg.Disk.Devices = []string{"/dev/sda", "/dev/sdb"}
	cfg.Disk.UseLuks = true

	layout, err := disklayout.BuildFromConfig(cfg, "")
	if err != nil {
		testingT.Fatal(err)
	}
	got := join(kinds(layout))
	want := "create_gpt,create_partition,create_partition,create_partition,create_luks,create_luks,format,format,format_btrfs"
	if got != want {
		testingT.Fatalf("got %s\nwant %s", got, want)
	}
	btrfs := layout.Actions[len(layout.Actions)-1]
	if len(btrfs.IDs) != 2 || btrfs.IDs[0] != "luks_dev0" || btrfs.IDs[1] != "luks_dev1" {
		testingT.Fatalf("btrfs members: %v", btrfs.IDs)
	}
	if layout.RootID != "luks_dev0" || layout.SwapID != "part_swap_dev0" {
		testingT.Fatalf("roles: %+v", layout)
	}
	if layout.RootFSType != "btrfs" ||
		layout.RootMountOpts != "defaults,noatime,compress=zstd,subvol=/root" {
		testingT.Fatalf("opts: %q", layout.RootMountOpts)
	}
}

func TestRaid0AndRaid1(testingT *testing.T) {
	mk := func(scheme string) *disklayout.Layout {
		cfg := config.Default(true)
		cfg.Disk.Scheme = scheme
		cfg.Disk.Devices = []string{"/dev/sda", "/dev/sdb"}
		layout, err := disklayout.BuildFromConfig(cfg, "")
		if err != nil {
			testingT.Fatal(err)
		}
		return layout
	}

	r0 := mk(config.SchemeRaid0Luks)
	if prefix := join(kinds(r0)); !strings.HasPrefix(prefix,
		"create_gpt,create_partition,create_partition,create_partition,"+
			"create_gpt,create_partition,create_partition,create_partition,") {
		testingT.Fatalf("raid0 prefix: %s", prefix)
	}
	if r0.EFIID != "part_efi_dev0" || r0.RootID != "part_luks_root" || r0.SwapID != "part_raid_swap" {
		testingT.Fatalf("raid0 roles: efi=%q root=%q swap=%q", r0.EFIID, r0.RootID, r0.SwapID)
	}

	r1 := mk(config.SchemeRaid1Luks)
	var bootRaid *disklayout.Action
	for idx := range r1.Actions {
		action := &r1.Actions[idx]
		if action.Action == disklayout.ActCreateRaid && action.Name == "efi" {
			bootRaid = action
		}
	}
	if bootRaid == nil || bootRaid.Level != 1 || len(bootRaid.IDs) != 2 {
		testingT.Fatalf("boot raid: %+v", bootRaid)
	}
	if r1.EFIID != "part_raid_efi" {
		testingT.Fatalf("raid1 efi role: %q", r1.EFIID)
	}
	md := 0
	for _, dc := range r1.DracutCmdline {
		if strings.HasPrefix(dc, "rd.md.uuid=") {
			md++
			uuid := strings.TrimPrefix(dc, "rd.md.uuid=")
			if strings.Count(uuid, ":") != 3 {
				testingT.Fatalf("bad mduuid %q", uuid)
			}
		}
	}
	if md < 2 {
		testingT.Fatalf("expected >=2 md uuids for raid1+swap+root, got %d (%v)", md, r1.DracutCmdline)
	}
}

func TestExistingPartitions(testingT *testing.T) {
	cfg := config.Default(true)
	cfg.Disk.Scheme = config.SchemeExisting
	cfg.Disk.Device = "/dev/sdX"
	cfg.Disk.BootDevice = "/dev/sdA"
	cfg.Disk.SwapDevice = ""
	layout, err := disklayout.BuildFromConfig(cfg, "")
	if err != nil {
		testingT.Fatal(err)
	}
	if !layout.Flags.NoPartitioningOrFormatting {
		testingT.Fatal("must set NoPartitioningOrFormatting")
	}
	// Empty SwapDevice behaves like swap=false (bash ${SWAP_DEVICE:-false}).
	if got := join(kinds(layout)); got != "existing,existing" {
		testingT.Fatalf("got %s", got)
	}
	if layout.SwapID != "" {
		testingT.Fatalf("SwapID must be empty, got %q", layout.SwapID)
	}
	if layout.RootFSType != "" {
		testingT.Fatalf("RootFSType must stay empty, got %q", layout.RootFSType)
	}

	// With a swap device present, all three are registered.
	cfg.Disk.SwapDevice = "/dev/sdB"
	layout2, err := disklayout.BuildFromConfig(cfg, "")
	if err != nil {
		testingT.Fatal(err)
	}
	if got := join(kinds(layout2)); got != "existing,existing,existing" {
		testingT.Fatalf("with swap: got %s", got)
	}
	if layout2.SwapID != "part_swap" {
		testingT.Fatalf("swap role: %q", layout2.SwapID)
	}
}

func TestCustomScheme(testingT *testing.T) {
	cfg := config.Default(true)
	cfg.Disk.Scheme = config.SchemeCustom
	cfg.Disk.Custom = []config.CustomAction{
		{Action: "create_gpt", NewID: "gpt", Device: "/dev/vda"},
		{Action: "create_partition", NewID: "p1", ID: "gpt", Size: "512MiB", Type: "efi"},
		{Action: "create_partition", NewID: "p2", ID: "gpt", Size: "remaining", Type: "linux"},
		{Action: "format", ID: "p1", Type: "efi", Label: "efi"},
		{Action: "format", ID: "p2", Type: "ext4", Label: "root"},
	}
	layout, err := disklayout.BuildFromConfig(cfg, "")
	if err != nil {
		testingT.Fatal(err)
	}
	if len(layout.Actions) != 5 {
		testingT.Fatalf("actions: %d", len(layout.Actions))
	}
	if ok := func() bool { _, found := layout.UUIDOf("p1"); return found }(); !ok {
		testingT.Fatal("custom ids must be registered")
	}
}

func TestBuildValidationErrors(testingT *testing.T) {
	bad := []func(*config.Config){
		func(cfg *config.Config) { // unknown parent id
			cfg.Disk.Custom = []config.CustomAction{
				{Action: "create_partition", NewID: "p", ID: "nope", Size: "1GiB", Type: "efi"},
			}
		},
		func(cfg *config.Config) { // duplicate id
			cfg.Disk.Custom = []config.CustomAction{
				{Action: "create_gpt", NewID: "dup", Device: "/dev/x"},
				{Action: "create_gpt", NewID: "dup", Device: "/dev/x"},
			}
		},
		func(cfg *config.Config) { // invalid partition type
			cfg.Disk.Custom = []config.CustomAction{
				{Action: "create_gpt", NewID: "g", Device: "/dev/x"},
				{Action: "create_partition", NewID: "p", ID: "g", Size: "1GiB", Type: "ntfs"},
			}
		},
		func(cfg *config.Config) { // partition after remaining
			cfg.Disk.Custom = []config.CustomAction{
				{Action: "create_gpt", NewID: "g", Device: "/dev/x"},
				{Action: "create_partition", NewID: "a", ID: "g", Size: "remaining", Type: "linux"},
				{Action: "create_partition", NewID: "b", ID: "g", Size: "1GiB", Type: "linux"},
			}
		},
		func(cfg *config.Config) { // format with invalid type
			cfg.Disk.Custom = []config.CustomAction{
				{Action: "create_gpt", NewID: "g", Device: "/dev/x"},
				{Action: "create_partition", NewID: "p", ID: "g", Size: "1GiB", Type: "efi"},
				{Action: "format", ID: "p", Type: "zfs"},
			}
		},
		func(cfg *config.Config) { // raid needs >=1 member
			cfg.Disk.Custom = []config.CustomAction{
				{Action: "create_raid", NewID: "r", Level: "0", Name: "x", IDs: []string{}},
			}
		},
	}
	for idx, mutate := range bad {
		cfg := config.Default(true)
		cfg.Disk.Scheme = config.SchemeCustom
		mutate(cfg)
		if _, err := disklayout.BuildFromConfig(cfg, ""); err == nil {
			testingT.Fatalf("case %d: expected error", idx)
		}
	}
}

func TestClassicNoSwapExt4(testingT *testing.T) {
	// Classic single disk, no LUKS, no btrfs, no swap: the minimal EFI setup.
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Disk.UseSwap = false
	layout, err := disklayout.BuildFromConfig(cfg, "")
	if err != nil {
		testingT.Fatal(err)
	}
	got := join(kinds(layout))
	want := "create_gpt,create_partition,create_partition,format,format"
	if got != want {
		testingT.Fatalf("got %s want %s", got, want)
	}
	if layout.SwapID != "" {
		testingT.Fatalf("swap must be unset, got %q", layout.SwapID)
	}
	if layout.RootFSType != "ext4" {
		testingT.Fatalf("root fs: %q", layout.RootFSType)
	}
	if layout.EFIID == "" {
		testingT.Fatalf("EFI role must be present: %+v", layout)
	}
}

func TestUuidToMdUUID(testingT *testing.T) {
	uuid := "00000000-1111-2222-3333-444444444444"
	if got := disklayout.UuidToMdUUID(uuid); got != "00000000:11112222:33334444:44444444" {
		testingT.Fatalf("mduuid: %q", got)
	}
}

func TestSummaryTree(testingT *testing.T) {
	layout, err := disklayout.BuildFromConfig(classicCfg("/dev/mydisk", false, false), "")
	if err != nil {
		testingT.Fatal(err)
	}
	rows := layout.Summary()
	if len(rows) == 0 {
		testingT.Fatal("empty summary")
	}
	if rows[0].Name != "/dev/mydisk" || rows[0].Indent != "" {
		testingT.Fatalf("row0: %+v", rows[0])
	}
	if rows[1].Name != "part" || !strings.Contains(rows[1].Indent, "├─") {
		testingT.Fatalf("row1: %+v", rows[1])
	}
	// The final fs row under part_root must be a last child.
	last := rows[len(rows)-1]
	if !strings.Contains(last.Indent, "└─") {
		testingT.Fatalf("last row indent: %+v", last)
	}
	roles := 0
	for _, row := range rows {
		if row.Role != "" {
			roles++
		}
	}
	if roles != 3 { // efi + swap + root
		testingT.Fatalf("expected 3 role rows, got %d", roles)
	}
	summary := layout.SummaryPlain()
	for _, want := range []string{"NODE", "<- efi", "<- swap", "<- root"} {
		if !strings.Contains(summary, want) {
			testingT.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
}

func TestBootTypeConsistency(testingT *testing.T) {
	efiCfg := classicCfg("/dev/sdX", false, false)
	efiLayout, err := disklayout.BuildFromConfig(efiCfg, "")
	if err != nil {
		testingT.Fatal(err)
	}
	if err := disklayout.CheckBootTypeConsistency(efiCfg, efiLayout); err != nil {
		testingT.Fatalf("matching efi should pass: %v", err)
	}

	biosCfg := classicCfg("/dev/sdX", false, false)
	biosCfg.Disk.BootType = "bios"
	biosLayout, err := disklayout.BuildFromConfig(biosCfg, "")
	if err != nil {
		testingT.Fatal(err)
	}
	if err := disklayout.CheckBootTypeConsistency(biosCfg, biosLayout); err != nil {
		testingT.Fatalf("matching bios should pass: %v", err)
	}

	// Stale EFI layout reused after switching the TUI to bios must be
	// rejected instead of failing late at MountEfiVars on a BIOS boot.
	if err := disklayout.CheckBootTypeConsistency(biosCfg, efiLayout); err == nil {
		testingT.Fatal("stale EFI layout with bios config should fail")
	} else if !strings.Contains(err.Error(), "EFIID") {
		testingT.Fatalf("stale error should name EFIID, got: %v", err)
	}
	if err := disklayout.CheckBootTypeConsistency(efiCfg, biosLayout); err == nil {
		testingT.Fatal("stale BIOS layout with efi config should fail")
	}

	custom := classicCfg("/dev/sdX", false, false)
	custom.Disk.Scheme = config.SchemeCustom
	if err := disklayout.CheckBootTypeConsistency(custom, biosLayout); err != nil {
		testingT.Fatalf("custom schemes are exempt: %v", err)
	}
}
