// Package disklayout models disk configurations as an ordered list of
// actions, mirroring scripts/config.sh of the original bash installer.
package disklayout

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// ActionKind enumerates all supported disk action types.
type ActionKind string

const (
	ActExisting        ActionKind = "existing"
	ActCreateGPT       ActionKind = "create_gpt"
	ActCreatePartition ActionKind = "create_partition"
	ActCreateRaid      ActionKind = "create_raid"
	ActCreateLuks      ActionKind = "create_luks"
	ActCreateDummy     ActionKind = "create_dummy"
	ActFormat          ActionKind = "format"
	ActFormatZFS       ActionKind = "format_zfs"
	ActFormatBtrfs     ActionKind = "format_btrfs"
)

// Partition type codes understood by sgdisk.
var PartitionTypeCodes = map[string]string{
	"bios":  "ef02",
	"efi":   "ef00",
	"swap":  "8200",
	"raid":  "fd00",
	"luks":  "8309",
	"linux": "8300",
}

// ResolveEntry describes how to find a device for an id at apply time.
type ResolveEntry struct {
	Type string // device | ptuuid | partuuid | uuid | mdadm | luks
	Arg  string
}

// Action is a single step of the disk configuration.
type Action struct {
	Action   ActionKind `json:"action"`
	NewID    string     `json:"new_id,omitempty"`
	ID       string     `json:"id,omitempty"`
	Device   string     `json:"device,omitempty"`
	Size     string     `json:"size,omitempty"`
	Type     string     `json:"type,omitempty"`
	Label    string     `json:"label,omitempty"`
	Level    int        `json:"level,omitempty"`
	Name     string     `json:"name,omitempty"`
	IDs      []string   `json:"ids,omitempty"`
	PoolType string     `json:"pool_type,omitempty"`
	Encrypt  bool       `json:"encrypt,omitempty"`
	Compress string     `json:"compress,omitempty"` // "" = disabled
	RaidType string     `json:"raid_type,omitempty"`
}

// Flags tracks which subsystems are used by the layout.
type Flags struct {
	UsedRaid                   bool
	UsedLuks                   bool
	UsedZFS                    bool
	UsedBtrfs                  bool
	UsedEncryption             bool
	NoPartitioningOrFormatting bool
}

// Layout is a fully built disk configuration.
type Layout struct {
	Flags Flags

	Actions []Action

	// Role ids.
	RootID string
	EFIID  string
	BIOSID string
	SwapID string

	RootFSType    string // ext4 | btrfs | zfs | "" (unknown/existing)
	RootMountOpts string

	DracutCmdline []string

	uuids        map[string]string
	resolvable   map[string]ResolveEntry
	partGPT      map[string]string // partition id -> parent gpt id
	order        []string          // registration order for expand_ids
	hadRemaining map[string]bool
}

// UUIDOf returns the generated uuid for a registered id.
func (l *Layout) UUIDOf(id string) (string, bool) {
	u, ok := l.uuids[id]
	return u, ok
}

// Resolvable returns the resolve entry for an id.
func (l *Layout) Resolvable(id string) (ResolveEntry, bool) {
	e, ok := l.resolvable[id]
	return e, ok
}

// ParentGPTOf returns the gpt table id a partition belongs to.
func (l *Layout) ParentGPTOf(partID string) (string, bool) {
	g, ok := l.partGPT[partID]
	return g, ok
}

// ExpandIDs returns all registered ids matching regex, joined with ';'
// (port of expand_ids).
func (l *Layout) ExpandIDs(regex string) (string, error) {
	re, err := regexp.Compile(regex)
	if err != nil {
		return "", err
	}
	var out []string
	for _, id := range l.order {
		if re.MatchString(id) {
			out = append(out, id)
		}
	}
	return strings.Join(out, ";"), nil
}

// SplitIDList splits a ';'-separated id list, dropping empties.
func SplitIDList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ";") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func validUniqueIDs(ids []string) error {
	if len(ids) == 0 {
		return fmt.Errorf("must contain at least one entry")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return fmt.Errorf("contains duplicate identifiers")
		}
		seen[id] = true
	}
	return nil
}

// onlyOneOf mirrors only_one_of from bash.
func onlyOneOf(device, id string) error {
	if device != "" && id != "" {
		return fmt.Errorf("only one of (device, id) can be given")
	}
	return nil
}

func (l *Layout) verifyExisting(field, id string) error {
	if _, ok := l.uuids[id]; !ok {
		return fmt.Errorf("%s=%q not found", field, id)
	}
	return nil
}

func (l *Layout) verifyOption(opt, arg string, allowed ...string) error {
	if slices.Contains(allowed, arg) {
		return nil
	}
	return fmt.Errorf("invalid option %s=%q, must be one of (%s)", opt, arg, strings.Join(allowed, " "))
}
