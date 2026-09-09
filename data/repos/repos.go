// Package repos embeds the static per-repository package lists refreshed
// daily by scripts/packages.sh.
package repos

import "embed"

//go:embed *.packages
var files embed.FS

// ReadFile returns the raw package list for the given repo name.
func ReadFile(name string) ([]byte, error) {
	return files.ReadFile(name + ".packages")
}
