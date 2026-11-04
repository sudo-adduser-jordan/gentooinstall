package disklayout

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

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

var PartitionTypeCodes = map[string]string{
	"bios":  "ef02",
	"efi":   "ef00",
	"swap":  "8200",
	"raid":  "fd00",
	"luks":  "8309",
	"linux": "8300",
}

type ResolveEntry struct {
	Type string
	Arg  string
}

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
	Compress string     `json:"compress,omitempty"`
	RaidType string     `json:"raid_type,omitempty"`
}

type Flags struct {
	UsedRaid                   bool
	UsedLuks                   bool
	UsedZFS                    bool
	UsedBtrfs                  bool
	UsedEncryption             bool
	NoPartitioningOrFormatting bool
}

type Layout struct {
	Flags Flags

	Actions []Action

	RootID string
	EFIID  string
	BIOSID string
	SwapID string

	RootFSType    string
	RootMountOpts string

	DracutCmdline []string

	uuids        map[string]string
	resolvable   map[string]ResolveEntry
	partGPT      map[string]string
	order        []string
	hadRemaining map[string]bool
}

func (layout *Layout) UUIDOf(id string) (string, bool) {
	uuid, exists := layout.uuids[id]
	return uuid, exists
}

func (layout *Layout) ParentGPTOf(partID string) (string, bool) {
	gptID, exists := layout.partGPT[partID]
	return gptID, exists
}

func (layout *Layout) ExpandIDs(regex string) (string, error) {
	re, err := regexp.Compile(regex)
	if err != nil {
		return "", err
	}
	var out []string
	for _, id := range layout.order {
		if re.MatchString(id) {
			out = append(out, id)
		}
	}
	return strings.Join(out, ";"), nil
}

func SplitIDList(list string) []string {
	var out []string
	for _, part := range strings.Split(list, ";") {
		if part != "" {
			out = append(out, part)
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

func onlyOneOf(device, id string) error {
	if device != "" && id != "" {
		return fmt.Errorf("only one of (device, id) can be given")
	}
	return nil
}

func (layout *Layout) verifyExisting(field, id string) error {
	if _, ok := layout.uuids[id]; !ok {
		return fmt.Errorf("%s=%q not found", field, id)
	}
	return nil
}

func (layout *Layout) verifyOption(opt, arg string, allowed ...string) error {
	if slices.Contains(allowed, arg) {
		return nil
	}
	return fmt.Errorf("invalid option %s=%q, must be one of (%s)", opt, arg, strings.Join(allowed, " "))
}
