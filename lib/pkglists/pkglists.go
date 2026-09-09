// Package pkglists exposes the static per-repository package lists
// embedded from data/repos (refreshed daily by scripts/packages.sh).
package pkglists

import (
	"sort"
	"strings"

	"gentooinstall/data/repos"
)

// Has reports whether a static package list exists for the given repo name.
func Has(name string) bool {
	if name == "" {
		return false
	}
	_, err := repos.ReadFile(name)
	return err == nil
}

// Atoms returns the complete, deduplicated, sorted list of package atoms for
// a repo, or nil if no static list exists for it.
func Atoms(name string) []string {
	content, err := repos.ReadFile(name)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// pkg_desc_index lines are "<category/name> <versions>: <desc>".
		atom := line
		if index := strings.IndexByte(line, ' '); index > 0 {
			atom = line[:index]
		}
		if atom == "" || seen[atom] {
			continue
		}
		seen[atom] = true
		out = append(out, atom)
	}
	sort.Strings(out)
	return out
}
