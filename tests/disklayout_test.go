package tests

import (
	"strings"
	"testing"

	"gentooinstall/internal/config"
	"gentooinstall/internal/disklayout"
)

func kinds(l *disklayout.Layout) []string {
	out := make([]string, 0, len(l.Actions))
	for _, a := range l.Actions {
		out = append(out, string(a.Action))
	}
	return out
}

func join(ks []string) string { return strings.Join(ks, ",") }

func TestClassicSingleDisk(t *testing.T) {
	c := classicCfg("/dev/sdX", true, false)
	l, err := disklayout.BuildFromConfig(c, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got := join(kinds(l))
	want := "create_gpt,create_partition,create_partition,create_partition,create_luks,format,format,format"
	if got != want {
		t.Fatalf("actions:\n got %s\nwant %s", got, want)
	}
	if l.RootID != "part_luks_root" || l.EFIID != "part_efi" || l.SwapID != "part_swap" {
		t.Fatalf("roles wrong: %+v", l)
	}
	if l.RootFSType != "ext4" || l.RootMountOpts != "defaults,noatime,errors=remount-ro,discard" {
		t.Fatalf("root fs opts wrong: %q %q", l.RootFSType, l.RootMountOpts)
	}

	u, ok := l.UUIDOf("part_luks_root")
	if !ok || u == "" {
		t.Fatalf("luks uuid missing")
	}
	found := false
	for _, dc := range l.DracutCmdline {
		if dc == "rd.luks.uuid="+u {
			found = true
		}
	}
	if !found {
		t.Fatalf("dracut cmdline missing luks uuid: %v", l.DracutCmdline)
	}
	if !l.Flags.UsedLuks || !l.Flags.UsedEncryption || l.Flags.UsedRaid || l.Flags.UsedZFS {
		t.Fatalf("flags wrong: %+v", l.Flags)
	}
}

func TestClassicNoLuksBtrfsNoSwap(t *testing.T) {
	c := classicCfg("/dev/sdX", false, true)
	c.Disk.UseSwap = false
	l, err := disklayout.BuildFromConfig(c, "")
	if err != nil {
		t.Fatal(err)
	}
	got := join(kinds(l))
	want := "create_gpt,create_partition,create_partition,format,format"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if l.SwapID != "" {
		t.Fatal("swap should be unset")
	}
	if l.RootFSType != "btrfs" ||
		l.RootMountOpts != "defaults,noatime,compress-force=zstd,subvol=/root" {
		t.Fatalf("btrfs opts: %q", l.RootMountOpts)
	}
	if !l.Flags.UsedBtrfs {
		t.Fatal("UsedBtrfs not set")
	}
}

func TestClassicBiosSingleDisk(t *testing.T) {
	c := classicCfg("/dev/sdX", false, false)
	c.Disk.BootType = "bios"
	l, err := disklayout.BuildFromConfig(c, "")
	if err != nil {
		t.Fatal(err)
	}
	got := join(kinds(l))
	want := "create_gpt,create_partition,create_partition,create_partition,format,format,format"
	if got != want {
		t.Fatalf("actions:\n got %s\nwant %s", got, want)
	}
	if l.EFIID != "" || l.BIOSID != "part_bios" {
		t.Fatalf("boot roles wrong: EFIID=%q BIOSID=%q", l.EFIID, l.BIOSID)
	}
	if l.RootID != "part_root" || l.SwapID != "part_swap" {
		t.Fatalf("roles wrong: %+v", l)
	}
	// The bios_grub partition is formatted as FAT32 like bash does.
	boot := l.Actions[len(l.Actions)-3]
	if boot.ID != "part_bios" || boot.Type != "bios" {
		t.Fatalf("bios boot action: %+v", boot)
	}
	if l.Flags.NoPartitioningOrFormatting {
		t.Fatal("classic must partition")
	}
}

func TestZfsCentricMultiDisk(t *testing.T) {
	c := config.Default(true)
	c.Disk.Scheme = config.SchemeZFSCentric
	c.Disk.Devices = []string{"/dev/sda", "/dev/sdb"}
	c.Disk.ZFSEncrypt = true
	c.Disk.ZFSUseCompress = true

	l, err := disklayout.BuildFromConfig(c, "")
	if err != nil {
		t.Fatal(err)
	}
	got := join(kinds(l))
	// Default config enables 8GiB swap, so a swap partition+format exists.
	want := "create_gpt,create_partition,create_partition,create_partition,create_dummy,format,format,format_zfs"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	zfs := l.Actions[len(l.Actions)-1]
	if len(zfs.IDs) != 2 || zfs.IDs[0] != "part_root_dev0" || zfs.IDs[1] != "root_dev1" {
		t.Fatalf("zfs members: %v", zfs.IDs)
	}
	if zfs.Compress != "zstd" || !zfs.Encrypt {
		t.Fatalf("zfs opts: %+v", zfs)
	}
	if l.RootID != "part_root_dev0" || l.RootFSType != "zfs" || l.EFIID != "part_efi_dev0" {
		t.Fatalf("roles: %+v", l)
	}
	if !l.Flags.UsedZFS || !l.Flags.UsedEncryption {
		t.Fatalf("flags: %+v", l.Flags)
	}
}

func TestBtrfsCentricWithLuks(t *testing.T) {
	c := config.Default(true)
	c.Disk.Scheme = config.SchemeBtrfs
	c.Disk.Devices = []string{"/dev/sda", "/dev/sdb"}
	c.Disk.UseLuks = true

	l, err := disklayout.BuildFromConfig(c, "")
	if err != nil {
		t.Fatal(err)
	}
	got := join(kinds(l))
	want := "create_gpt,create_partition,create_partition,create_partition,create_luks,create_luks,format,format,format_btrfs"
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	b := l.Actions[len(l.Actions)-1]
	if len(b.IDs) != 2 || b.IDs[0] != "luks_dev0" || b.IDs[1] != "luks_dev1" {
		t.Fatalf("btrfs members: %v", b.IDs)
	}
	if l.RootID != "luks_dev0" || l.SwapID != "part_swap_dev0" {
		t.Fatalf("roles: %+v", l)
	}
	if l.RootFSType != "btrfs" ||
		l.RootMountOpts != "defaults,noatime,compress=zstd,subvol=/root" {
		t.Fatalf("opts: %q", l.RootMountOpts)
	}
}

func TestRaid0AndRaid1(t *testing.T) {
	mk := func(scheme string) *disklayout.Layout {
		c := config.Default(true)
		c.Disk.Scheme = scheme
		c.Disk.Devices = []string{"/dev/sda", "/dev/sdb"}
		l, err := disklayout.BuildFromConfig(c, "")
		if err != nil {
			t.Fatal(err)
		}
		return l
	}

	r0 := mk(config.SchemeRaid0Luks)
	if l := join(kinds(r0)); !strings.HasPrefix(l,
		"create_gpt,create_partition,create_partition,create_partition,"+
			"create_gpt,create_partition,create_partition,create_partition,") {
		t.Fatalf("raid0 prefix: %s", l)
	}
	if r0.EFIID != "part_efi_dev0" || r0.RootID != "part_luks_root" || r0.SwapID != "part_raid_swap" {
		t.Fatalf("raid0 roles: efi=%q root=%q swap=%q", r0.EFIID, r0.RootID, r0.SwapID)
	}

	r1 := mk(config.SchemeRaid1Luks)
	var bootRaid *disklayout.Action
	for i := range r1.Actions {
		a := &r1.Actions[i]
		if a.Action == disklayout.ActCreateRaid && a.Name == "efi" {
			bootRaid = a
		}
	}
	if bootRaid == nil || bootRaid.Level != 1 || len(bootRaid.IDs) != 2 {
		t.Fatalf("boot raid: %+v", bootRaid)
	}
	if r1.EFIID != "part_raid_efi" {
		t.Fatalf("raid1 efi role: %q", r1.EFIID)
	}
	md := 0
	for _, dc := range r1.DracutCmdline {
		if strings.HasPrefix(dc, "rd.md.uuid=") {
			md++
			u := strings.TrimPrefix(dc, "rd.md.uuid=")
			if strings.Count(u, ":") != 3 {
				t.Fatalf("bad mduuid %q", u)
			}
		}
	}
	if md < 2 {
		t.Fatalf("expected >=2 md uuids for raid1+swap+root, got %d (%v)", md, r1.DracutCmdline)
	}
}

func TestExistingPartitions(t *testing.T) {
	c := config.Default(true)
	c.Disk.Scheme = config.SchemeExisting
	c.Disk.Device = "/dev/sdX"
	c.Disk.BootDevice = "/dev/sdA"
	c.Disk.SwapDevice = ""
	l, err := disklayout.BuildFromConfig(c, "")
	if err != nil {
		t.Fatal(err)
	}
	if !l.Flags.NoPartitioningOrFormatting {
		t.Fatal("must set NoPartitioningOrFormatting")
	}
	// Empty SwapDevice behaves like swap=false (bash ${SWAP_DEVICE:-false}).
	if got := join(kinds(l)); got != "existing,existing" {
		t.Fatalf("got %s", got)
	}
	if l.SwapID != "" {
		t.Fatalf("SwapID must be empty, got %q", l.SwapID)
	}
	if l.RootFSType != "" {
		t.Fatalf("RootFSType must stay empty, got %q", l.RootFSType)
	}

	// With a swap device present, all three are registered.
	c.Disk.SwapDevice = "/dev/sdB"
	l2, err := disklayout.BuildFromConfig(c, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := join(kinds(l2)); got != "existing,existing,existing" {
		t.Fatalf("with swap: got %s", got)
	}
	if l2.SwapID != "part_swap" {
		t.Fatalf("swap role: %q", l2.SwapID)
	}
}

func TestCustomScheme(t *testing.T) {
	c := config.Default(true)
	c.Disk.Scheme = config.SchemeCustom
	c.Disk.Custom = []config.CustomAction{
		{Action: "create_gpt", NewID: "gpt", Device: "/dev/vda"},
		{Action: "create_partition", NewID: "p1", ID: "gpt", Size: "512MiB", Type: "efi"},
		{Action: "create_partition", NewID: "p2", ID: "gpt", Size: "remaining", Type: "linux"},
		{Action: "format", ID: "p1", Type: "efi", Label: "efi"},
		{Action: "format", ID: "p2", Type: "ext4", Label: "root"},
	}
	l, err := disklayout.BuildFromConfig(c, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Actions) != 5 {
		t.Fatalf("actions: %d", len(l.Actions))
	}
	if ok := func() bool { _, found := l.UUIDOf("p1"); return found }(); !ok {
		t.Fatal("custom ids must be registered")
	}
}

func TestBuildValidationErrors(t *testing.T) {
	bad := []func(*config.Config){
		func(c *config.Config) { // unknown parent id
			c.Disk.Custom = []config.CustomAction{
				{Action: "create_partition", NewID: "p", ID: "nope", Size: "1GiB", Type: "efi"},
			}
		},
		func(c *config.Config) { // duplicate id
			c.Disk.Custom = []config.CustomAction{
				{Action: "create_gpt", NewID: "dup", Device: "/dev/x"},
				{Action: "create_gpt", NewID: "dup", Device: "/dev/x"},
			}
		},
		func(c *config.Config) { // invalid partition type
			c.Disk.Custom = []config.CustomAction{
				{Action: "create_gpt", NewID: "g", Device: "/dev/x"},
				{Action: "create_partition", NewID: "p", ID: "g", Size: "1GiB", Type: "ntfs"},
			}
		},
		func(c *config.Config) { // partition after remaining
			c.Disk.Custom = []config.CustomAction{
				{Action: "create_gpt", NewID: "g", Device: "/dev/x"},
				{Action: "create_partition", NewID: "a", ID: "g", Size: "remaining", Type: "linux"},
				{Action: "create_partition", NewID: "b", ID: "g", Size: "1GiB", Type: "linux"},
			}
		},
		func(c *config.Config) { // format with invalid type
			c.Disk.Custom = []config.CustomAction{
				{Action: "create_gpt", NewID: "g", Device: "/dev/x"},
				{Action: "create_partition", NewID: "p", ID: "g", Size: "1GiB", Type: "efi"},
				{Action: "format", ID: "p", Type: "zfs"},
			}
		},
		func(c *config.Config) { // raid needs >=1 member
			c.Disk.Custom = []config.CustomAction{
				{Action: "create_raid", NewID: "r", Level: "0", Name: "x", IDs: []string{}},
			}
		},
	}
	for i, mutate := range bad {
		c := config.Default(true)
		c.Disk.Scheme = config.SchemeCustom
		mutate(c)
		if _, err := disklayout.BuildFromConfig(c, ""); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
}

func TestUuidToMdUUID(t *testing.T) {
	u := "00000000-1111-2222-3333-444444444444"
	if got := disklayout.UuidToMdUUID(u); got != "00000000:11112222:33334444:44444444" {
		t.Fatalf("mduuid: %q", got)
	}
}

func TestSummaryTree(t *testing.T) {
	l, err := disklayout.BuildFromConfig(classicCfg("/dev/mydisk", false, false), "")
	if err != nil {
		t.Fatal(err)
	}
	rows := l.Summary()
	if len(rows) == 0 {
		t.Fatal("empty summary")
	}
	if rows[0].Name != "/dev/mydisk" || rows[0].Indent != "" {
		t.Fatalf("row0: %+v", rows[0])
	}
	if rows[1].Name != "part" || !strings.Contains(rows[1].Indent, "├─") {
		t.Fatalf("row1: %+v", rows[1])
	}
	// The final fs row under part_root must be a last child.
	last := rows[len(rows)-1]
	if !strings.Contains(last.Indent, "└─") {
		t.Fatalf("last row indent: %+v", last)
	}
	roles := 0
	for _, r := range rows {
		if r.Role != "" {
			roles++
		}
	}
	if roles != 3 { // efi + swap + root
		t.Fatalf("expected 3 role rows, got %d", roles)
	}
	s := l.SummaryPlain()
	for _, want := range []string{"NODE", "<- efi", "<- swap", "<- root"} {
		if !strings.Contains(s, want) {
			t.Fatalf("summary missing %q:\n%s", want, s)
		}
	}
}
