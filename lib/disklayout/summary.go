package disklayout

import (
	"fmt"
	"strings"
)

// SummaryNode is one row of the disk layout summary tree.
type SummaryNode struct {
	ID     string // layout id, may be synthetic (__root__, _raid..., __fs__...)
	Name   string
	Hint   string
	Desc   string
	Role   string // "", "bios", "efi", "swap", "root"
	Indent string // rendered tree prefix, e.g. "│  ├─ "
}

// Summary builds the configured-disk-layout tree shown before applying
// (port of summarize_disk_actions / print_summary_tree).
func (layout *Layout) Summary() []*SummaryNode {
	type nodeInfo struct {
		node     *SummaryNode
		children []string
	}
	nodes := map[string]*nodeInfo{}
	var order []string

	add := func(parent string, node *SummaryNode) {
		if _, ok := nodes[node.ID]; !ok {
			order = append(order, node.ID)
			nodes[node.ID] = &nodeInfo{node: node}
		}
		if _, ok := nodes[parent]; !ok && parent != "__root__" {
			nodes[parent] = &nodeInfo{node: &SummaryNode{ID: parent}}
			order = append(order, parent)
		}
		if nodes[parent] == nil {
			nodes[parent] = &nodeInfo{}
		}
		nodes[parent].children = append(nodes[parent].children, node.ID)
	}

	for _, action := range layout.Actions {
		switch action.Action {
		case ActExisting:
			add("__root__", &SummaryNode{ID: action.NewID, Name: action.Device, Hint: "(no-format, existing)"})
		case ActCreateGPT:
			if action.ID != "" {
				add(action.ID, &SummaryNode{ID: action.NewID, Name: "gpt"})
			} else {
				add("__root__", &SummaryNode{ID: action.NewID, Name: action.Device, Desc: "(gpt)"})
			}
		case ActCreatePartition:
			add(action.ID, &SummaryNode{ID: action.NewID, Name: "part",
				Hint: "(" + action.Type + ")", Desc: fmt.Sprintf("size=%s", action.Size)})
		case ActCreateRaid:
			for _, member := range action.IDs {
				add(member, &SummaryNode{ID: "_" + action.NewID,
					Name: fmt.Sprintf("raid%d", action.Level),
					Desc: fmt.Sprintf("name=%s", action.Name)})
			}
			add("__root__", &SummaryNode{ID: action.NewID,
				Name: fmt.Sprintf("raid%d", action.Level),
				Desc: fmt.Sprintf("name=%s", action.Name)})
		case ActCreateLuks:
			if action.ID != "" {
				add(action.ID, &SummaryNode{ID: action.NewID, Name: "luks"})
			} else {
				add("__root__", &SummaryNode{ID: action.NewID, Name: action.Device, Desc: "(luks)"})
			}
		case ActCreateDummy:
			add("__root__", &SummaryNode{ID: action.NewID, Name: action.Device})
		case ActFormat:
			add(action.ID, &SummaryNode{ID: "__fs__" + action.ID, Name: action.Type, Hint: "(fs)",
				Desc: labelDesc(action.Label)})
		case ActFormatZFS:
			for _, member := range action.IDs {
				add(member, &SummaryNode{ID: "__fs__" + member, Name: "zfs", Hint: "(fs)"})
			}
		case ActFormatBtrfs:
			for _, member := range action.IDs {
				add(member, &SummaryNode{ID: "__fs__" + member, Name: "btrfs", Hint: "(fs)",
					Desc: labelDesc(action.Label)})
			}
		}
	}

	roleOf := func(id string) string {
		switch id {
		case layout.BIOSID:
			return "bios"
		case layout.EFIID:
			return "efi"
		case layout.SwapID:
			return "swap"
		case layout.RootID:
			return "root"
		}
		return ""
	}

	var out []*SummaryNode
	var walk func(id, prefix string, suppressConnector bool)
	walk = func(id, prefix string, suppressConnector bool) {
		info := nodes[id]
		if info == nil {
			return
		}
		count := len(info.children)
		for index, childID := range info.children {
			last := index == count-1
			var ind, childPrefix string
			if !suppressConnector {
				conn := "├─ "
				childPrefix = prefix + "│  "
				if last {
					conn = "└─ "
					childPrefix = prefix + "   "
				}
				ind = prefix + conn
			}
			child := nodes[childID].node
			out = append(out, &SummaryNode{
				ID:     child.ID,
				Name:   child.Name,
				Hint:   child.Hint,
				Desc:   child.Desc,
				Role:   roleOf(child.ID),
				Indent: ind,
			})
			walk(childID, childPrefix, false)
		}
	}
	walk("__root__", "", true)
	return out
}

func labelDesc(label string) string {
	if label != "" {
		return fmt.Sprintf("label=%s", label)
	}
	return ""
}

func displayID(id string) string {
	switch {
	case strings.HasPrefix(id, "__"):
		return ""
	default:
		return strings.TrimPrefix(id, "_")
	}
}

// SummaryPlain renders the tree as aligned plain text.
func (layout *Layout) SummaryPlain() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%-40s %-24s %s\n", "NODE", "ID", "OPTIONS"))
	for _, node := range layout.Summary() {
		ptr := ""
		switch node.Role {
		case "bios":
			ptr = "<- bios"
		case "efi":
			ptr = "<- efi"
		case "swap":
			ptr = "<- swap"
		case "root":
			ptr = "<- root"
		}
		name := node.Name
		if node.Hint != "" {
			name += " " + node.Hint
		}
		opts := node.Desc
		if opts == "" {
			opts = ptr
		} else if ptr != "" {
			opts += "  " + ptr
		}
		sb.WriteString(fmt.Sprintf("%s%-36s %-24s %s\n", node.Indent, name, displayID(node.ID), opts))
	}
	return sb.String()
}
