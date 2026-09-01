// Package pkglists provides complete, statically embedded per-repo package
// lists for the "Additional packages" picker. The lists are downloaded into
// data/repos/*.packages by scripts/repo.sh and embedded at build
// time, so gentooinstall always has a full catalog to search without network access.
package pkglists

import (
	"embed"
	"sort"
	"strings"
)

//go:embed data/repos/*.packages
var files embed.FS

// Has reports whether a static package list exists for the given repo name.
func Has(name string) bool {
	if name == "" {
		return false
	}
	_, err := files.ReadFile("data/repos/" + name + ".packages")
	return err == nil
}

// Atoms returns the complete, deduplicated, sorted list of package atoms for
// a repo, or nil if no static list exists for it.
func Atoms(name string) []string {
	b, err := files.ReadFile("data/repos/" + name + ".packages")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// pkg_desc_index lines are "<category/name> <versions>: <desc>".
		atom := line
		if i := strings.IndexByte(line, ' '); i > 0 {
			atom = line[:i]
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
