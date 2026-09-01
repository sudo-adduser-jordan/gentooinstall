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
func (l *Layout) Summary() []*SummaryNode {
	type nodeInfo struct {
		node     *SummaryNode
		children []string
	}
	nodes := map[string]*nodeInfo{}
	var order []string

	add := func(parent string, n *SummaryNode) {
		if _, ok := nodes[n.ID]; !ok {
			order = append(order, n.ID)
			nodes[n.ID] = &nodeInfo{node: n}
		}
		if _, ok := nodes[parent]; !ok && parent != "__root__" {
			nodes[parent] = &nodeInfo{node: &SummaryNode{ID: parent}}
			order = append(order, parent)
		}
		if nodes[parent] == nil {
			nodes[parent] = &nodeInfo{}
		}
		nodes[parent].children = append(nodes[parent].children, n.ID)
	}

	for _, a := range l.Actions {
		switch a.Action {
		case ActExisting:
			add("__root__", &SummaryNode{ID: a.NewID, Name: a.Device, Hint: "(no-format, existing)"})
		case ActCreateGPT:
			if a.ID != "" {
				add(a.ID, &SummaryNode{ID: a.NewID, Name: "gpt"})
			} else {
				add("__root__", &SummaryNode{ID: a.NewID, Name: a.Device, Desc: "(gpt)"})
			}
		case ActCreatePartition:
			add(a.ID, &SummaryNode{ID: a.NewID, Name: "part",
				Hint: "(" + a.Type + ")", Desc: fmt.Sprintf("size=%s", a.Size)})
		case ActCreateRaid:
			for _, m := range a.IDs {
				add(m, &SummaryNode{ID: "_" + a.NewID,
					Name: fmt.Sprintf("raid%d", a.Level),
					Desc: fmt.Sprintf("name=%s", a.Name)})
			}
			add("__root__", &SummaryNode{ID: a.NewID,
				Name: fmt.Sprintf("raid%d", a.Level),
				Desc: fmt.Sprintf("name=%s", a.Name)})
		case ActCreateLuks:
			if a.ID != "" {
				add(a.ID, &SummaryNode{ID: a.NewID, Name: "luks"})
			} else {
				add("__root__", &SummaryNode{ID: a.NewID, Name: a.Device, Desc: "(luks)"})
			}
		case ActCreateDummy:
			add("__root__", &SummaryNode{ID: a.NewID, Name: a.Device})
		case ActFormat:
			add(a.ID, &SummaryNode{ID: "__fs__" + a.ID, Name: a.Type, Hint: "(fs)",
				Desc: labelDesc(a.Label)})
		case ActFormatZFS:
			for _, m := range a.IDs {
				add(m, &SummaryNode{ID: "__fs__" + m, Name: "zfs", Hint: "(fs)"})
			}
		case ActFormatBtrfs:
			for _, m := range a.IDs {
				add(m, &SummaryNode{ID: "__fs__" + m, Name: "btrfs", Hint: "(fs)",
					Desc: labelDesc(a.Label)})
			}
		}
	}

	roleOf := func(id string) string {
		switch id {
		case l.BIOSID:
			return "bios"
		case l.EFIID:
			return "efi"
		case l.SwapID:
			return "swap"
		case l.RootID:
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
		n := len(info.children)
		for i, cid := range info.children {
			last := i == n-1
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
			child := nodes[cid].node
			out = append(out, &SummaryNode{
				ID:     child.ID,
				Name:   child.Name,
				Hint:   child.Hint,
				Desc:   child.Desc,
				Role:   roleOf(child.ID),
				Indent: ind,
			})
			walk(cid, childPrefix, false)
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
		return "" // synthetic fs/root markers stay anonymous
	default:
		return strings.TrimPrefix(id, "_")
	}
}

// SummaryPlain renders the tree as aligned plain text.
func (l *Layout) SummaryPlain() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%-40s %-24s %s\n", "NODE", "ID", "OPTIONS"))
	for _, n := range l.Summary() {
		ptr := ""
		switch n.Role {
		case "bios":
			ptr = "<- bios"
		case "efi":
			ptr = "<- efi"
		case "swap":
			ptr = "<- swap"
		case "root":
			ptr = "<- root"
		}
		name := n.Name
		if n.Hint != "" {
			name += " " + n.Hint
		}
		opts := n.Desc
		if opts == "" {
			opts = ptr
		} else if ptr != "" {
			opts += "  " + ptr
		}
		sb.WriteString(fmt.Sprintf("%s%-36s %-24s %s\n", n.Indent, name, displayID(n.ID), opts))
	}
	return sb.String()
}
