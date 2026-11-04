package repos

import "embed"

//go:embed *.packages
var files embed.FS

func ReadFile(name string) ([]byte, error) {
	return files.ReadFile(name + ".packages")
}
