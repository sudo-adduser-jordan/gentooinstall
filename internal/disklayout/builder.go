package disklayout

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gentooinstall/internal/config"
)

// UUIDStore generates stable uuids per id, persisted below Dir.
// It ports load_or_generate_uuid.
type UUIDStore struct {
	Dir string // empty disables persistence (tests)
}

func randomUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10xx
	h := fmt.Sprintf("%x", b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32])
}

func uuidFileName(id string) string {
	return base64.StdEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(id))
}

// Get returns the stored uuid for id, generating and persisting one if needed.
func (s *UUIDStore) Get(id string) string {
	if s.Dir == "" {
		return randomUUID()
	}
	f := filepath.Join(s.Dir, uuidFileName(id))
	if data, err := os.ReadFile(f); err == nil {
		if u := strings.TrimSpace(string(data)); u != "" {
			return u
		}
	}
	u := randomUUID()
	_ = os.MkdirAll(s.Dir, 0o755)
	_ = os.WriteFile(f, []byte(u), 0o644)
	return u
}

// UuidToMdUUID converts a hyphenated uuid to the mdadm colon notation.
func UuidToMdUUID(uuid string) string {
	u := strings.ReplaceAll(strings.ToLower(uuid), "-", "")
	if len(u) != 32 {
		return uuid
	}
	return fmt.Sprintf("%s:%s:%s:%s", u[0:8], u[8:16], u[16:24], u[24:32])
}

// Builder incrementally constructs a Layout, validating as it goes
// (port of scripts/config.sh).
type Builder struct {
	store  UUIDStore
	flags  Flags
	layout Layout
}

// NewBuilder creates a builder using the given uuid persistence directory.
func NewBuilder(uuidDir string) *Builder {
	return &Builder{
		store: UUIDStore{Dir: uuidDir},
		layout: Layout{
			uuids:        map[string]string{},
			resolvable:   map[string]ResolveEntry{},
			partGPT:      map[string]string{},
			hadRemaining: map[string]bool{},
		},
	}
}

func (b *Builder) verifyExisting(field, id string) error {
	return b.layout.verifyExisting(field, id)
}

func (b *Builder) verifyOption(opt, arg string, allowed ...string) error {
	return b.layout.verifyOption(opt, arg, allowed...)
}

func (b *Builder) createNewID(field, id string) (string, error) {
	if strings.Contains(id, ";") {
		return "", fmt.Errorf("%s=%q contains invalid character ';'", field, id)
	}
	if _, exists := b.layout.uuids[id]; exists {
		return "", fmt.Errorf("identifier %q already exists", id)
	}
	u := b.store.Get(id)
	b.layout.uuids[id] = u
	b.layout.order = append(b.layout.order, id)
	return id, nil
}

func (b *Builder) resolveEntry(id, typ, arg string) {
	b.layout.resolvable[id] = ResolveEntry{Type: typ, Arg: arg}
}

// RegisterExisting registers an already-formatted device (register_existing).
func (b *Builder) RegisterExisting(newID, device string) error {
	if newID == "" || device == "" {
		return fmt.Errorf("existing: new_id and device are required")
	}
	if _, err := b.createNewID("new_id", newID); err != nil {
		return err
	}
	b.resolveEntry(newID, "device", device)
	b.add(Action{Action: ActExisting, NewID: newID, Device: device})
	return nil
}

// CreateGPT creates a new GPT table on device or on the operand id.
func (b *Builder) CreateGPT(newID, device, id string) error {
	if err := onlyOneOf(device, id); err != nil {
		return err
	}
	if newID == "" {
		return fmt.Errorf("create_gpt: new_id required")
	}
	if id != "" {
		if err := b.verifyExisting("id", id); err != nil {
			return err
		}
	}
	if _, err := b.createNewID("new_id", newID); err != nil {
		return err
	}
	b.resolveEntry(newID, "ptuuid", b.layout.uuids[newID])
	b.add(Action{Action: ActCreateGPT, NewID: newID, Device: device, ID: id})
	return nil
}

// CreatePartition adds a partition of size ("1GiB" or "remaining").
func (b *Builder) CreatePartition(newID, gptID, size, typ string) error {
	if err := b.verifyExisting("id", gptID); err != nil {
		return err
	}
	if err := b.verifyOption("type", typ,
		"bios", "efi", "swap", "raid", "luks", "linux"); err != nil {
		return err
	}
	if b.layout.hadRemaining[gptID] {
		return fmt.Errorf("cannot add another partition to table (%s) after size=remaining was used", gptID)
	}
	if _, err := b.createNewID("new_id", newID); err != nil {
		return err
	}
	if size == "remaining" {
		b.layout.hadRemaining[gptID] = true
	} else if size == "" {
		return fmt.Errorf("create_partition: size required")
	}
	b.layout.partGPT[newID] = gptID
	b.resolveEntry(newID, "partuuid", b.layout.uuids[newID])
	b.add(Action{Action: ActCreatePartition, NewID: newID, ID: gptID, Size: size, Type: typ})
	return nil
}

// CreateRaid creates an mdadm array from member ids.
func (b *Builder) CreateRaid(newID string, level int, name, idsJoined string) error {
	b.flags.UsedRaid = true
	switch level {
	case 0, 1, 5, 6:
	default:
		return fmt.Errorf("invalid option level=%d, must be one of (0 1 5 6)", level)
	}
	ids := SplitIDList(idsJoined)
	if err := validUniqueIDs(ids); err != nil {
		return fmt.Errorf("ids=%s %w", idsJoined, err)
	}
	for _, id := range ids {
		if err := b.verifyExisting("ids", id); err != nil {
			return err
		}
	}
	if _, err := b.createNewID("new_id", newID); err != nil {
		return err
	}
	uuid := b.layout.uuids[newID]
	b.resolveEntry(newID, "mdadm", uuid)
	b.layout.DracutCmdline = append(b.layout.DracutCmdline,
		fmt.Sprintf("rd.md.uuid=%s", UuidToMdUUID(uuid)))
	b.add(Action{Action: ActCreateRaid, NewID: newID, Level: level, Name: name, IDs: ids})
	return nil
}

// CreateLuks wraps device or id into a LUKS2 container named name.
func (b *Builder) CreateLuks(newID, name, device, id string) error {
	b.flags.UsedLuks = true
	b.flags.UsedEncryption = true
	if err := onlyOneOf(device, id); err != nil {
		return err
	}
	if id != "" {
		if err := b.verifyExisting("id", id); err != nil {
			return err
		}
	}
	if name == "" {
		return fmt.Errorf("create_luks: name required")
	}
	if _, err := b.createNewID("new_id", newID); err != nil {
		return err
	}
	uuid := b.layout.uuids[newID]
	b.resolveEntry(newID, "luks", name)
	b.layout.DracutCmdline = append(b.layout.DracutCmdline, "rd.luks.uuid="+uuid)
	b.add(Action{Action: ActCreateLuks, NewID: newID, Name: name, Device: device, ID: id})
	return nil
}

// CreateDummy registers a plain device without any action (zfs/btrfs members).
func (b *Builder) CreateDummy(newID, device string) error {
	if _, err := b.createNewID("new_id", newID); err != nil {
		return err
	}
	b.resolveEntry(newID, "device", device)
	b.add(Action{Action: ActCreateDummy, NewID: newID, Device: device})
	return nil
}

// Format formats the device identified by id.
func (b *Builder) Format(id, typ, label string) error {
	if err := b.verifyExisting("id", id); err != nil {
		return err
	}
	if err := b.verifyOption("type", typ, "bios", "efi", "swap", "ext4", "btrfs"); err != nil {
		return err
	}
	if typ == "btrfs" {
		b.flags.UsedBtrfs = true
	}
	b.add(Action{Action: ActFormat, ID: id, Type: typ, Label: label})
	return nil
}

// FormatZFS creates a zfs pool over all member devices.
func (b *Builder) FormatZFS(idsJoined, poolType string, encrypt bool, compress string) error {
	b.flags.UsedZFS = true
	ids := SplitIDList(idsJoined)
	if err := validUniqueIDs(ids); err != nil {
		return fmt.Errorf("ids=%s %w", idsJoined, err)
	}
	for _, id := range ids {
		if err := b.verifyExisting("ids", id); err != nil {
			return err
		}
	}
	if poolType == "" {
		poolType = "standard"
	}
	if err := b.verifyOption("pool_type", poolType, "standard", "custom"); err != nil {
		return err
	}
	b.flags.UsedEncryption = encrypt
	b.add(Action{Action: ActFormatZFS, IDs: ids, PoolType: poolType, Encrypt: encrypt, Compress: compress})
	return nil
}

// FormatBtrfs creates a (possibly multi-device) btrfs filesystem.
func (b *Builder) FormatBtrfs(idsJoined, raidType, label string) error {
	b.flags.UsedBtrfs = true
	ids := SplitIDList(idsJoined)
	if err := validUniqueIDs(ids); err != nil {
		return fmt.Errorf("ids=%s %w", idsJoined, err)
	}
	for _, id := range ids {
		if err := b.verifyExisting("ids", id); err != nil {
			return err
		}
	}
	if raidType != "" {
		if err := b.verifyOption("raid_type", raidType, "raid0", "raid1"); err != nil {
			return err
		}
	}
	b.add(Action{Action: ActFormatBtrfs, IDs: ids, RaidType: raidType, Label: label})
	return nil
}

func (b *Builder) add(a Action) { b.layout.Actions = append(b.layout.Actions, a) }

// Finish returns the built layout after preset-specific role assignment.
func (b *Builder) Finish() *Layout { return &b.layout }

// BuildFromConfig constructs the layout described by cfg.Disk
// (port of all create_*_layout functions).
func BuildFromConfig(cfg *config.Config, uuidDir string) (*Layout, error) {
	d := &cfg.Disk
	b := NewBuilder(uuidDir)
	swapArg := func() string {
		if d.UseSwap {
			return d.SwapSize
		}
		return "false"
	}
	useSwap := func() bool { return d.UseSwap && swapArg() != "false" && d.SwapSize != "" }

	setRootFS := func(fs string, forceCompress bool) error {
		switch fs {
		case "btrfs":
			opts := "defaults,noatime,compress=zstd,subvol=/root"
			if forceCompress {
				opts = "defaults,noatime,compress-force=zstd,subvol=/root"
			}
			b.layout.RootFSType = "btrfs"
			b.layout.RootMountOpts = opts
		case "ext4":
			b.layout.RootFSType = "ext4"
			b.layout.RootMountOpts = "defaults,noatime,errors=remount-ro,discard"
		default:
			return fmt.Errorf("unsupported root filesystem type %q", fs)
		}
		return nil
	}

	switch d.Scheme {
	case config.SchemeClassic:
		bt := d.BootType
		rootFS := d.RootFS
		if rootFS == "" {
			rootFS = "ext4"
		}
		if err := b.CreateGPT("gpt", d.Device, ""); err != nil {
			return nil, err
		}
		if err := b.CreatePartition("part_"+bt, "gpt", "1GiB", bt); err != nil {
			return nil, err
		}
		if useSwap() {
			if err := b.CreatePartition("part_swap", "gpt", swapArg(), "swap"); err != nil {
				return nil, err
			}
		}
		if err := b.CreatePartition("part_root", "gpt", "remaining", "linux"); err != nil {
			return nil, err
		}
		rootID := "part_root"
		if d.UseLuks {
			if err := b.CreateLuks("part_luks_root", "root", "", "part_root"); err != nil {
				return nil, err
			}
			rootID = "part_luks_root"
		}
		if err := b.Format("part_"+bt, bt, bt); err != nil {
			return nil, err
		}
		if useSwap() {
			if err := b.Format("part_swap", "swap", "swap"); err != nil {
				return nil, err
			}
		}
		if err := b.Format(rootID, rootFS, "root"); err != nil {
			return nil, err
		}
		if bt == "efi" {
			b.layout.EFIID = "part_" + bt
		} else {
			b.layout.BIOSID = "part_" + bt
		}
		if useSwap() {
			b.layout.SwapID = "part_swap"
		}
		b.layout.RootID = rootID
		if err := setRootFS(rootFS, true); err != nil {
			return nil, err
		}

	case config.SchemeExisting:
		b.flags.NoPartitioningOrFormatting = true
		bt := d.BootType
		if err := b.RegisterExisting("part_"+bt, d.BootDevice); err != nil {
			return nil, err
		}
		if useSwap() && d.SwapDevice != "" {
			if err := b.RegisterExisting("part_swap", d.SwapDevice); err != nil {
				return nil, err
			}
			b.layout.SwapID = "part_swap"
		}
		if err := b.RegisterExisting("part_root", d.Device); err != nil {
			return nil, err
		}
		if bt == "efi" {
			b.layout.EFIID = "part_" + bt
		} else {
			b.layout.BIOSID = "part_" + bt
		}
		b.layout.RootID = "part_root"
		// RootFSType stays empty: unknown, skip fstab entry.

	case config.SchemeZFSCentric:
		bt := d.BootType
		compress := ""
		if d.ZFSUseCompress {
			compress = d.ZFSCompression
		}
		if len(d.Devices) < 1 {
			return nil, fmt.Errorf("expected at least one device")
		}
		if err := b.CreateGPT("gpt_dev0", d.Devices[0], ""); err != nil {
			return nil, err
		}
		if err := b.CreatePartition("part_"+bt+"_dev0", "gpt_dev0", "1GiB", bt); err != nil {
			return nil, err
		}
		if useSwap() {
			if err := b.CreatePartition("part_swap_dev0", "gpt_dev0", swapArg(), "swap"); err != nil {
				return nil, err
			}
		}
		if err := b.CreatePartition("part_root_dev0", "gpt_dev0", "remaining", "linux"); err != nil {
			return nil, err
		}
		rootIDs := []string{"part_root_dev0"}
		for i := 1; i < len(d.Devices); i++ {
			id := fmt.Sprintf("root_dev%d", i)
			if err := b.CreateDummy(id, d.Devices[i]); err != nil {
				return nil, err
			}
			rootIDs = append(rootIDs, id)
		}
		if err := b.Format("part_"+bt+"_dev0", bt, bt); err != nil {
			return nil, err
		}
		if useSwap() {
			if err := b.Format("part_swap_dev0", "swap", "swap"); err != nil {
				return nil, err
			}
		}
		if err := b.FormatZFS(strings.Join(rootIDs, ";"), d.ZFSPoolType, d.ZFSEncrypt, compress); err != nil {
			return nil, err
		}
		if bt == "efi" {
			b.layout.EFIID = "part_" + bt + "_dev0"
		} else {
			b.layout.BIOSID = "part_" + bt + "_dev0"
		}
		if useSwap() {
			b.layout.SwapID = "part_swap_dev0"
		}
		b.layout.RootID = "part_root_dev0"
		b.layout.RootFSType = "zfs"

	case config.SchemeBtrfs:
		bt := d.BootType
		raidType := d.BtrfsRaidType
		if raidType == "" {
			raidType = "raid0"
		}
		if len(d.Devices) < 1 {
			return nil, fmt.Errorf("expected at least one device")
		}
		if err := b.CreateGPT("gpt_dev0", d.Devices[0], ""); err != nil {
			return nil, err
		}
		if err := b.CreatePartition("part_"+bt+"_dev0", "gpt_dev0", "1GiB", bt); err != nil {
			return nil, err
		}
		if useSwap() {
			if err := b.CreatePartition("part_swap_dev0", "gpt_dev0", swapArg(), "swap"); err != nil {
				return nil, err
			}
		}
		if err := b.CreatePartition("part_root_dev0", "gpt_dev0", "remaining", "linux"); err != nil {
			return nil, err
		}
		rootID := "part_root_dev0"
		rootIDs := []string{"part_root_dev0"}
		if d.UseLuks {
			if err := b.CreateLuks("luks_dev0", "luks_root_0", "", "part_root_dev0"); err != nil {
				return nil, err
			}
			rootID = "luks_dev0"
			rootIDs = []string{"luks_dev0"}
			for i := 1; i < len(d.Devices); i++ {
				id := fmt.Sprintf("luks_dev%d", i)
				if err := b.CreateLuks(id, fmt.Sprintf("luks_root_%d", i), d.Devices[i], ""); err != nil {
					return nil, err
				}
				rootIDs = append(rootIDs, id)
			}
		} else {
			for i := 1; i < len(d.Devices); i++ {
				id := fmt.Sprintf("root_dev%d", i)
				if err := b.CreateDummy(id, d.Devices[i]); err != nil {
					return nil, err
				}
				rootIDs = append(rootIDs, id)
			}
		}
		if err := b.Format("part_"+bt+"_dev0", bt, bt); err != nil {
			return nil, err
		}
		if useSwap() {
			if err := b.Format("part_swap_dev0", "swap", "swap"); err != nil {
				return nil, err
			}
		}
		if err := b.FormatBtrfs(strings.Join(rootIDs, ";"), raidType, "root"); err != nil {
			return nil, err
		}
		if bt == "efi" {
			b.layout.EFIID = "part_" + bt + "_dev0"
		} else {
			b.layout.BIOSID = "part_" + bt + "_dev0"
		}
		if useSwap() {
			b.layout.SwapID = "part_swap_dev0"
		}
		b.layout.RootID = rootID
		b.layout.RootFSType = "btrfs"
		b.layout.RootMountOpts = "defaults,noatime,compress=zstd,subvol=/root"

	case config.SchemeRaid0Luks, config.SchemeRaid1Luks:
		bt := d.BootType
		rootFS := d.RootFS
		if rootFS == "" {
			rootFS = "ext4"
		}
		if len(d.Devices) < 2 {
			return nil, fmt.Errorf("scheme %s needs at least 2 devices", d.Scheme)
		}
		for i := range d.Devices {
			gpt := fmt.Sprintf("gpt_dev%d", i)
			if err := b.CreateGPT(gpt, d.Devices[i], ""); err != nil {
				return nil, err
			}
			if err := b.CreatePartition(fmt.Sprintf("part_%s_dev%d", bt, i), gpt, "1GiB", bt); err != nil {
				return nil, err
			}
			if useSwap() {
				if err := b.CreatePartition(fmt.Sprintf("part_swap_dev%d", i), gpt, swapArg(), "raid"); err != nil {
					return nil, err
				}
			}
			if err := b.CreatePartition(fmt.Sprintf("part_root_dev%d", i), gpt, "remaining", "raid"); err != nil {
				return nil, err
			}
		}

		bootPartID := fmt.Sprintf("part_%s_dev0", bt)
		if d.Scheme == config.SchemeRaid1Luks {
			ids, err := b.layout.ExpandIDs(fmt.Sprintf(`^part_%s_dev[0-9]+$`, bt))
			if err != nil {
				return nil, err
			}
			if err := b.CreateRaid("part_raid_"+bt, 1, bt, ids); err != nil {
				return nil, err
			}
			bootPartID = "part_raid_" + bt
		}
		swapID := ""
		if useSwap() {
			ids, err := b.layout.ExpandIDs(`^part_swap_dev[0-9]+$`)
			if err != nil {
				return nil, err
			}
			level := 0
			if d.Scheme == config.SchemeRaid1Luks {
				level = 1
			}
			if err := b.CreateRaid("part_raid_swap", level, "swap", ids); err != nil {
				return nil, err
			}
			swapID = "part_raid_swap"
		}
		ids, err := b.layout.ExpandIDs(`^part_root_dev[0-9]+$`)
		if err != nil {
			return nil, err
		}
		rootLevel := 0
		if d.Scheme == config.SchemeRaid1Luks {
			rootLevel = 1
		}
		if err := b.CreateRaid("part_raid_root", rootLevel, "root", ids); err != nil {
			return nil, err
		}
		rootID := "part_raid_root"
		if d.UseLuks {
			if err := b.CreateLuks("part_luks_root", "root", "", "part_raid_root"); err != nil {
				return nil, err
			}
			rootID = "part_luks_root"
		}
		if err := b.Format(bootPartID, bt, bt); err != nil {
			return nil, err
		}
		if swapID != "" {
			if err := b.Format(swapID, "swap", "swap"); err != nil {
				return nil, err
			}
		}
		if err := b.Format(rootID, rootFS, "root"); err != nil {
			return nil, err
		}
		if bt == "efi" {
			b.layout.EFIID = bootPartID
		} else {
			b.layout.BIOSID = bootPartID
		}
		b.layout.SwapID = swapID
		b.layout.RootID = rootID
		if err := setRootFS(rootFS, false); err != nil {
			return nil, err
		}

	case config.SchemeCustom:
		if err := buildCustom(b, d.Custom); err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unknown scheme %q", d.Scheme)
	}

	b.layout.Flags = b.flags
	return &b.layout, nil
}

func buildCustom(b *Builder, actions []config.CustomAction) error {
	for i, ca := range actions {
		var err error
		switch ca.Action {
		case "existing":
			err = b.RegisterExisting(ca.NewID, ca.Device)
		case "create_gpt":
			err = b.CreateGPT(ca.NewID, ca.Device, ca.ID)
		case "create_partition":
			err = b.CreatePartition(ca.NewID, ca.ID, ca.Size, ca.Type)
		case "create_raid":
			err = b.CreateRaid(ca.NewID, atoiDefault(ca.Level, 0), ca.Name, strings.Join(ca.IDs, ";"))
		case "create_luks":
			err = b.CreateLuks(ca.NewID, ca.Name, ca.Device, ca.ID)
		case "create_dummy":
			err = b.CreateDummy(ca.NewID, ca.Device)
		case "format":
			err = b.Format(ca.ID, ca.Type, ca.Label)
		case "format_zfs":
			err = b.FormatZFS(strings.Join(ca.IDs, ";"), ca.PoolType, ca.Encrypt, ca.Compress)
		case "format_btrfs":
			err = b.FormatBtrfs(strings.Join(ca.IDs, ";"), ca.RaidType, ca.Label)
		default:
			return fmt.Errorf("[disk.custom] #%d: unknown action %q", i+1, ca.Action)
		}
		if err != nil {
			return fmt.Errorf("[disk.custom] #%d (%s): %w", i+1, ca.Action, err)
		}
	}
	return nil
}

func atoiDefault(s string, def int) int {
	n := 0
	if s == "" {
		return def
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
	}
	return n
}
