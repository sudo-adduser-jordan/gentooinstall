package disklayout

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gentooinstall/lib/config"
)

// UUIDStore generates stable uuids per id, persisted below Dir.
// It ports load_or_generate_uuid.
type UUIDStore struct {
	Dir string // empty disables persistence (tests)
}

func randomUUID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	raw[6] = (raw[6] & 0x0f) | 0x40 // version 4
	raw[8] = (raw[8] & 0x3f) | 0x80 // variant 10xx
	hex := fmt.Sprintf("%x", raw[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", hex[0:8], hex[8:12], hex[12:16], hex[16:20], hex[20:32])
}

func uuidFileName(id string) string {
	return base64.StdEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(id))
}

// Get returns the stored uuid for id, generating and persisting one if needed.
func (store *UUIDStore) Get(id string) string {
	if store.Dir == "" {
		return randomUUID()
	}
	path := filepath.Join(store.Dir, uuidFileName(id))
	if data, err := os.ReadFile(path); err == nil {
		if uuid := strings.TrimSpace(string(data)); uuid != "" {
			return uuid
		}
	}
	uuid := randomUUID()
	_ = os.MkdirAll(store.Dir, 0o755)
	_ = os.WriteFile(path, []byte(uuid), 0o644)
	return uuid
}

// UuidToMdUUID converts a hyphenated uuid to the mdadm colon notation.
func UuidToMdUUID(uuid string) string {
	normalized := strings.ReplaceAll(strings.ToLower(uuid), "-", "")
	if len(normalized) != 32 {
		return uuid
	}
	return fmt.Sprintf("%s:%s:%s:%s", normalized[0:8], normalized[8:16], normalized[16:24], normalized[24:32])
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

func (builder *Builder) verifyExisting(field, id string) error {
	return builder.layout.verifyExisting(field, id)
}

func (builder *Builder) verifyOption(opt, arg string, allowed ...string) error {
	return builder.layout.verifyOption(opt, arg, allowed...)
}

func (builder *Builder) createNewID(field, id string) (string, error) {
	if strings.Contains(id, ";") {
		return "", fmt.Errorf("%s=%q contains invalid character ';'", field, id)
	}
	if _, exists := builder.layout.uuids[id]; exists {
		return "", fmt.Errorf("identifier %q already exists", id)
	}
	uuid := builder.store.Get(id)
	builder.layout.uuids[id] = uuid
	builder.layout.order = append(builder.layout.order, id)
	return id, nil
}

func (builder *Builder) resolveEntry(id, typ, arg string) {
	builder.layout.resolvable[id] = ResolveEntry{Type: typ, Arg: arg}
}

// RegisterExisting registers an already-formatted device (register_existing).
func (builder *Builder) RegisterExisting(newID, device string) error {
	if newID == "" || device == "" {
		return fmt.Errorf("existing: new_id and device are required")
	}
	if _, err := builder.createNewID("new_id", newID); err != nil {
		return err
	}
	builder.resolveEntry(newID, "device", device)
	builder.add(Action{Action: ActExisting, NewID: newID, Device: device})
	return nil
}

// CreateGPT creates a new GPT table on device or on the operand id.
func (builder *Builder) CreateGPT(newID, device, id string) error {
	if err := onlyOneOf(device, id); err != nil {
		return err
	}
	if newID == "" {
		return fmt.Errorf("create_gpt: new_id required")
	}
	if id != "" {
		if err := builder.verifyExisting("id", id); err != nil {
			return err
		}
	}
	if _, err := builder.createNewID("new_id", newID); err != nil {
		return err
	}
	builder.resolveEntry(newID, "ptuuid", builder.layout.uuids[newID])
	builder.add(Action{Action: ActCreateGPT, NewID: newID, Device: device, ID: id})
	return nil
}

// CreatePartition adds a partition of size ("1GiB" or "remaining").
func (builder *Builder) CreatePartition(newID, gptID, size, typ string) error {
	if err := builder.verifyExisting("id", gptID); err != nil {
		return err
	}
	if err := builder.verifyOption("type", typ,
		"bios", "efi", "swap", "raid", "luks", "linux"); err != nil {
		return err
	}
	if builder.layout.hadRemaining[gptID] {
		return fmt.Errorf("cannot add another partition to table (%s) after size=remaining was used", gptID)
	}
	if _, err := builder.createNewID("new_id", newID); err != nil {
		return err
	}
	if size == "remaining" {
		builder.layout.hadRemaining[gptID] = true
	} else if size == "" {
		return fmt.Errorf("create_partition: size required")
	}
	builder.layout.partGPT[newID] = gptID
	builder.resolveEntry(newID, "partuuid", builder.layout.uuids[newID])
	builder.add(Action{Action: ActCreatePartition, NewID: newID, ID: gptID, Size: size, Type: typ})
	return nil
}

// CreateRaid creates an mdadm array from member ids.
func (builder *Builder) CreateRaid(newID string, level int, name, idsJoined string) error {
	builder.flags.UsedRaid = true
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
		if err := builder.verifyExisting("ids", id); err != nil {
			return err
		}
	}
	if _, err := builder.createNewID("new_id", newID); err != nil {
		return err
	}
	uuid := builder.layout.uuids[newID]
	builder.resolveEntry(newID, "mdadm", uuid)
	builder.layout.DracutCmdline = append(builder.layout.DracutCmdline,
		fmt.Sprintf("rd.md.uuid=%s", UuidToMdUUID(uuid)))
	builder.add(Action{Action: ActCreateRaid, NewID: newID, Level: level, Name: name, IDs: ids})
	return nil
}

// CreateLuks wraps device or id into a LUKS2 container named name.
func (builder *Builder) CreateLuks(newID, name, device, id string) error {
	builder.flags.UsedLuks = true
	builder.flags.UsedEncryption = true
	if err := onlyOneOf(device, id); err != nil {
		return err
	}
	if id != "" {
		if err := builder.verifyExisting("id", id); err != nil {
			return err
		}
	}
	if name == "" {
		return fmt.Errorf("create_luks: name required")
	}
	if _, err := builder.createNewID("new_id", newID); err != nil {
		return err
	}
	uuid := builder.layout.uuids[newID]
	builder.resolveEntry(newID, "luks", name)
	builder.layout.DracutCmdline = append(builder.layout.DracutCmdline, "rd.luks.uuid="+uuid)
	builder.add(Action{Action: ActCreateLuks, NewID: newID, Name: name, Device: device, ID: id})
	return nil
}

// CreateDummy registers a plain device without any action (zfs/btrfs members).
func (builder *Builder) CreateDummy(newID, device string) error {
	if _, err := builder.createNewID("new_id", newID); err != nil {
		return err
	}
	builder.resolveEntry(newID, "device", device)
	builder.add(Action{Action: ActCreateDummy, NewID: newID, Device: device})
	return nil
}

// Format formats the device identified by idisk.
func (builder *Builder) Format(id, typ, label string) error {
	if err := builder.verifyExisting("id", id); err != nil {
		return err
	}
	if err := builder.verifyOption("type", typ, "bios", "efi", "swap", "ext4", "btrfs"); err != nil {
		return err
	}
	if typ == "btrfs" {
		builder.flags.UsedBtrfs = true
	}
	builder.add(Action{Action: ActFormat, ID: id, Type: typ, Label: label})
	return nil
}

// FormatZFS creates a zfs pool over all member devices.
func (builder *Builder) FormatZFS(idsJoined, poolType string, encrypt bool, compress string) error {
	builder.flags.UsedZFS = true
	ids := SplitIDList(idsJoined)
	if err := validUniqueIDs(ids); err != nil {
		return fmt.Errorf("ids=%s %w", idsJoined, err)
	}
	for _, id := range ids {
		if err := builder.verifyExisting("ids", id); err != nil {
			return err
		}
	}
	if poolType == "" {
		poolType = "standard"
	}
	if err := builder.verifyOption("pool_type", poolType, "standard", "custom"); err != nil {
		return err
	}
	builder.flags.UsedEncryption = encrypt
	builder.add(Action{Action: ActFormatZFS, IDs: ids, PoolType: poolType, Encrypt: encrypt, Compress: compress})
	return nil
}

// FormatBtrfs creates a (possibly multi-device) btrfs filesystem.
func (builder *Builder) FormatBtrfs(idsJoined, raidType, label string) error {
	builder.flags.UsedBtrfs = true
	ids := SplitIDList(idsJoined)
	if err := validUniqueIDs(ids); err != nil {
		return fmt.Errorf("ids=%s %w", idsJoined, err)
	}
	for _, id := range ids {
		if err := builder.verifyExisting("ids", id); err != nil {
			return err
		}
	}
	if raidType != "" {
		if err := builder.verifyOption("raid_type", raidType, "raid0", "raid1"); err != nil {
			return err
		}
	}
	builder.add(Action{Action: ActFormatBtrfs, IDs: ids, RaidType: raidType, Label: label})
	return nil
}

func (builder *Builder) add(action Action) {
	builder.layout.Actions = append(builder.layout.Actions, action)
}

// Finish returns the built layout after preset-specific role assignment.
func (builder *Builder) Finish() *Layout { return &builder.layout }

// BuildFromConfig constructs the layout described by cfg.Disk
// (port of all create_*_layout functions).
func BuildFromConfig(cfg *config.Config, uuidDir string) (*Layout, error) {
	disk := &cfg.Disk
	builder := NewBuilder(uuidDir)
	swapArg := func() string {
		if disk.UseSwap {
			return disk.SwapSize
		}
		return "false"
	}
	useSwap := func() bool { return disk.UseSwap && swapArg() != "false" && disk.SwapSize != "" }

	setRootFS := func(fs string, forceCompress bool) error {
		switch fs {
		case "btrfs":
			opts := "defaults,noatime,compress=zstd,subvol=/root"
			if forceCompress {
				opts = "defaults,noatime,compress-force=zstd,subvol=/root"
			}
			builder.layout.RootFSType = "btrfs"
			builder.layout.RootMountOpts = opts
		case "ext4":
			builder.layout.RootFSType = "ext4"
			builder.layout.RootMountOpts = "defaults,noatime,errors=remount-ro,discard"
		default:
			return fmt.Errorf("unsupported root filesystem type %q", fs)
		}
		return nil
	}

	switch disk.Scheme {
	case config.SchemeClassic:
		bt := disk.BootType
		rootFS := disk.RootFS
		if rootFS == "" {
			rootFS = "ext4"
		}
		if err := builder.CreateGPT("gpt", disk.Device, ""); err != nil {
			return nil, err
		}
		if err := builder.CreatePartition("part_"+bt, "gpt", "1GiB", bt); err != nil {
			return nil, err
		}
		if useSwap() {
			if err := builder.CreatePartition("part_swap", "gpt", swapArg(), "swap"); err != nil {
				return nil, err
			}
		}
		if err := builder.CreatePartition("part_root", "gpt", "remaining", "linux"); err != nil {
			return nil, err
		}
		rootID := "part_root"
		if disk.UseLuks {
			if err := builder.CreateLuks("part_luks_root", "root", "", "part_root"); err != nil {
				return nil, err
			}
			rootID = "part_luks_root"
		}
		if err := builder.Format("part_"+bt, bt, bt); err != nil {
			return nil, err
		}
		if useSwap() {
			if err := builder.Format("part_swap", "swap", "swap"); err != nil {
				return nil, err
			}
		}
		if err := builder.Format(rootID, rootFS, "root"); err != nil {
			return nil, err
		}
		if bt == "efi" {
			builder.layout.EFIID = "part_" + bt
		} else {
			builder.layout.BIOSID = "part_" + bt
		}
		if useSwap() {
			builder.layout.SwapID = "part_swap"
		}
		builder.layout.RootID = rootID
		if err := setRootFS(rootFS, true); err != nil {
			return nil, err
		}

	case config.SchemeExisting:
		builder.flags.NoPartitioningOrFormatting = true
		bt := disk.BootType
		if err := builder.RegisterExisting("part_"+bt, disk.BootDevice); err != nil {
			return nil, err
		}
		if useSwap() && disk.SwapDevice != "" {
			if err := builder.RegisterExisting("part_swap", disk.SwapDevice); err != nil {
				return nil, err
			}
			builder.layout.SwapID = "part_swap"
		}
		if err := builder.RegisterExisting("part_root", disk.Device); err != nil {
			return nil, err
		}
		if bt == "efi" {
			builder.layout.EFIID = "part_" + bt
		} else {
			builder.layout.BIOSID = "part_" + bt
		}
		builder.layout.RootID = "part_root"
		// RootFSType stays empty: unknown, skip fstab entry.

	case config.SchemeZFSCentric:
		bt := disk.BootType
		compress := ""
		if disk.ZFSUseCompress {
			compress = disk.ZFSCompression
		}
		if len(disk.Devices) < 1 {
			return nil, fmt.Errorf("expected at least one device")
		}
		if err := builder.CreateGPT("gpt_dev0", disk.Devices[0], ""); err != nil {
			return nil, err
		}
		if err := builder.CreatePartition("part_"+bt+"_dev0", "gpt_dev0", "1GiB", bt); err != nil {
			return nil, err
		}
		if useSwap() {
			if err := builder.CreatePartition("part_swap_dev0", "gpt_dev0", swapArg(), "swap"); err != nil {
				return nil, err
			}
		}
		if err := builder.CreatePartition("part_root_dev0", "gpt_dev0", "remaining", "linux"); err != nil {
			return nil, err
		}
		rootIDs := []string{"part_root_dev0"}
		for index := 1; index < len(disk.Devices); index++ {
			id := fmt.Sprintf("root_dev%d", index)
			if err := builder.CreateDummy(id, disk.Devices[index]); err != nil {
				return nil, err
			}
			rootIDs = append(rootIDs, id)
		}
		if err := builder.Format("part_"+bt+"_dev0", bt, bt); err != nil {
			return nil, err
		}
		if useSwap() {
			if err := builder.Format("part_swap_dev0", "swap", "swap"); err != nil {
				return nil, err
			}
		}
		if err := builder.FormatZFS(strings.Join(rootIDs, ";"), disk.ZFSPoolType, disk.ZFSEncrypt, compress); err != nil {
			return nil, err
		}
		if bt == "efi" {
			builder.layout.EFIID = "part_" + bt + "_dev0"
		} else {
			builder.layout.BIOSID = "part_" + bt + "_dev0"
		}
		if useSwap() {
			builder.layout.SwapID = "part_swap_dev0"
		}
		builder.layout.RootID = "part_root_dev0"
		builder.layout.RootFSType = "zfs"

	case config.SchemeBtrfs:
		bt := disk.BootType
		raidType := disk.BtrfsRaidType
		if raidType == "" {
			raidType = "raid0"
		}
		if len(disk.Devices) < 1 {
			return nil, fmt.Errorf("expected at least one device")
		}
		if err := builder.CreateGPT("gpt_dev0", disk.Devices[0], ""); err != nil {
			return nil, err
		}
		if err := builder.CreatePartition("part_"+bt+"_dev0", "gpt_dev0", "1GiB", bt); err != nil {
			return nil, err
		}
		if useSwap() {
			if err := builder.CreatePartition("part_swap_dev0", "gpt_dev0", swapArg(), "swap"); err != nil {
				return nil, err
			}
		}
		if err := builder.CreatePartition("part_root_dev0", "gpt_dev0", "remaining", "linux"); err != nil {
			return nil, err
		}
		rootID := "part_root_dev0"
		rootIDs := []string{"part_root_dev0"}
		if disk.UseLuks {
			if err := builder.CreateLuks("luks_dev0", "luks_root_0", "", "part_root_dev0"); err != nil {
				return nil, err
			}
			rootID = "luks_dev0"
			rootIDs = []string{"luks_dev0"}
			for index := 1; index < len(disk.Devices); index++ {
				id := fmt.Sprintf("luks_dev%d", index)
				if err := builder.CreateLuks(id, fmt.Sprintf("luks_root_%d", index), disk.Devices[index], ""); err != nil {
					return nil, err
				}
				rootIDs = append(rootIDs, id)
			}
		} else {
			for index := 1; index < len(disk.Devices); index++ {
				id := fmt.Sprintf("root_dev%d", index)
				if err := builder.CreateDummy(id, disk.Devices[index]); err != nil {
					return nil, err
				}
				rootIDs = append(rootIDs, id)
			}
		}
		if err := builder.Format("part_"+bt+"_dev0", bt, bt); err != nil {
			return nil, err
		}
		if useSwap() {
			if err := builder.Format("part_swap_dev0", "swap", "swap"); err != nil {
				return nil, err
			}
		}
		if err := builder.FormatBtrfs(strings.Join(rootIDs, ";"), raidType, "root"); err != nil {
			return nil, err
		}
		if bt == "efi" {
			builder.layout.EFIID = "part_" + bt + "_dev0"
		} else {
			builder.layout.BIOSID = "part_" + bt + "_dev0"
		}
		if useSwap() {
			builder.layout.SwapID = "part_swap_dev0"
		}
		builder.layout.RootID = rootID
		if err := setRootFS("btrfs", false); err != nil {
			return nil, err
		}

	case config.SchemeRaid0Luks, config.SchemeRaid1Luks:
		bt := disk.BootType
		rootFS := disk.RootFS
		if rootFS == "" {
			rootFS = "ext4"
		}
		if len(disk.Devices) < 2 {
			return nil, fmt.Errorf("scheme %s needs at least 2 devices", disk.Scheme)
		}
		for index := range disk.Devices {
			gpt := fmt.Sprintf("gpt_dev%d", index)
			if err := builder.CreateGPT(gpt, disk.Devices[index], ""); err != nil {
				return nil, err
			}
			if err := builder.CreatePartition(fmt.Sprintf("part_%s_dev%d", bt, index), gpt, "1GiB", bt); err != nil {
				return nil, err
			}
			if useSwap() {
				if err := builder.CreatePartition(fmt.Sprintf("part_swap_dev%d", index), gpt, swapArg(), "raid"); err != nil {
					return nil, err
				}
			}
			if err := builder.CreatePartition(fmt.Sprintf("part_root_dev%d", index), gpt, "remaining", "raid"); err != nil {
				return nil, err
			}
		}

		bootPartID := fmt.Sprintf("part_%s_dev0", bt)
		if disk.Scheme == config.SchemeRaid1Luks {
			ids, err := builder.layout.ExpandIDs(fmt.Sprintf(`^part_%s_dev[0-9]+$`, bt))
			if err != nil {
				return nil, err
			}
			if err := builder.CreateRaid("part_raid_"+bt, 1, bt, ids); err != nil {
				return nil, err
			}
			bootPartID = "part_raid_" + bt
		}
		swapID := ""
		if useSwap() {
			ids, err := builder.layout.ExpandIDs(`^part_swap_dev[0-9]+$`)
			if err != nil {
				return nil, err
			}
			level := 0
			if disk.Scheme == config.SchemeRaid1Luks {
				level = 1
			}
			if err := builder.CreateRaid("part_raid_swap", level, "swap", ids); err != nil {
				return nil, err
			}
			swapID = "part_raid_swap"
		}
		ids, err := builder.layout.ExpandIDs(`^part_root_dev[0-9]+$`)
		if err != nil {
			return nil, err
		}
		rootLevel := 0
		if disk.Scheme == config.SchemeRaid1Luks {
			rootLevel = 1
		}
		if err := builder.CreateRaid("part_raid_root", rootLevel, "root", ids); err != nil {
			return nil, err
		}
		rootID := "part_raid_root"
		if disk.UseLuks {
			if err := builder.CreateLuks("part_luks_root", "root", "", "part_raid_root"); err != nil {
				return nil, err
			}
			rootID = "part_luks_root"
		}
		if err := builder.Format(bootPartID, bt, bt); err != nil {
			return nil, err
		}
		if swapID != "" {
			if err := builder.Format(swapID, "swap", "swap"); err != nil {
				return nil, err
			}
		}
		if err := builder.Format(rootID, rootFS, "root"); err != nil {
			return nil, err
		}
		if bt == "efi" {
			builder.layout.EFIID = bootPartID
		} else {
			builder.layout.BIOSID = bootPartID
		}
		builder.layout.SwapID = swapID
		builder.layout.RootID = rootID
		if err := setRootFS(rootFS, false); err != nil {
			return nil, err
		}

	case config.SchemeCustom:
		if err := buildCustom(builder, disk.Custom); err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unknown scheme %q", disk.Scheme)
	}

	builder.layout.Flags = builder.flags
	return &builder.layout, nil
}

// CheckBootTypeConsistency verifies the built layout roles agree with the
// configured boot type. Preset schemes derive EFIID/BIOSID directly from
// Disk.BootType, so any disagreement means the install would run with a
// different firmware path than the UI shows (e.g. a stale EFI layout after
// the user switched the TUI to bios and hit Retry instead of starting a
// fresh install). Custom schemes replay raw actions and never consult
// BootType, so they are exempt here and validated by their actions.
func CheckBootTypeConsistency(cfg *config.Config, layout *Layout) error {
	if cfg.Disk.Scheme == config.SchemeCustom {
		return nil
	}
	switch cfg.Disk.BootType {
	case "efi":
		if layout.EFIID == "" {
			return fmt.Errorf("disk.boot_type = \"efi\" but the layout has no EFI partition (BIOSID=%q); rebuild the layout from the current configuration instead of retrying a stale run", layout.BIOSID)
		}
		if layout.BIOSID != "" {
			return fmt.Errorf("disk.boot_type = \"efi\" but the layout also has BIOS partition %q; rebuild the layout from the current configuration", layout.BIOSID)
		}
	case "bios":
		if layout.BIOSID == "" {
			return fmt.Errorf("disk.boot_type = \"bios\" but the layout has no BIOS partition (EFIID=%q); it was built from a stale EFI configuration — abort and start a fresh install instead of retrying", layout.EFIID)
		}
		if layout.EFIID != "" {
			return fmt.Errorf("disk.boot_type = \"bios\" but the layout still has EFI partition %q; it was built from a stale EFI configuration — abort and start a fresh install instead of retrying", layout.EFIID)
		}
	default:
		return fmt.Errorf("invalid boot type %q (want \"efi\" or \"bios\")", cfg.Disk.BootType)
	}
	return nil
}

func buildCustom(builder *Builder, actions []config.CustomAction) error {
	for index, customAction := range actions {
		var err error
		switch customAction.Action {
		case "existing":
			err = builder.RegisterExisting(customAction.NewID, customAction.Device)
		case "create_gpt":
			err = builder.CreateGPT(customAction.NewID, customAction.Device, customAction.ID)
		case "create_partition":
			err = builder.CreatePartition(customAction.NewID, customAction.ID, customAction.Size, customAction.Type)
		case "create_raid":
			err = builder.CreateRaid(customAction.NewID, atoiDefault(customAction.Level, 0), customAction.Name, strings.Join(customAction.IDs, ";"))
		case "create_luks":
			err = builder.CreateLuks(customAction.NewID, customAction.Name, customAction.Device, customAction.ID)
		case "create_dummy":
			err = builder.CreateDummy(customAction.NewID, customAction.Device)
		case "format":
			err = builder.Format(customAction.ID, customAction.Type, customAction.Label)
		case "format_zfs":
			err = builder.FormatZFS(strings.Join(customAction.IDs, ";"), customAction.PoolType, customAction.Encrypt, customAction.Compress)
		case "format_btrfs":
			err = builder.FormatBtrfs(strings.Join(customAction.IDs, ";"), customAction.RaidType, customAction.Label)
		default:
			return fmt.Errorf("[disk.custom] #%d: unknown action %q", index+1, customAction.Action)
		}
		if err != nil {
			return fmt.Errorf("[disk.custom] #%d (%s): %w", index+1, customAction.Action, err)
		}
	}
	return nil
}

func atoiDefault(str string, def int) int {
	num := 0
	if str == "" {
		return def
	}
	for _, digit := range str {
		if digit < '0' || digit > '9' {
			return def
		}
		num = num*10 + int(digit-'0')
	}
	return num
}
