package pkglists

import (
	"sort"
	"strings"

	"gentooinstall/data/repos"
)

func Has(name string) bool {
	if name == "" {
		return false
	}
	_, err := repos.ReadFile(name)
	return err == nil
}

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
