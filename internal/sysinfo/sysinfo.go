// Package sysinfo discovers runtime system information: block devices,
// keymaps, timezones, locales and EFI support.
package sysinfo

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// HasEFI reports whether the running system supports EFI.
func HasEFI() bool {
	_, err := os.Stat("/sys/firmware/efi")
	return err == nil
}

// DefaultBootType returns "efi" or "bios" depending on system support.
func DefaultBootType() string {
	if HasEFI() {
		return "efi"
	}
	return "bios"
}

// NProc returns the number of processors (nproc equivalent).
func NProc() int {
	n := runtime_numCPU()
	return n
}

// Devices lists all entries below /dev/disk/by-id, sorted.
func Devices() []string {
	entries, err := os.ReadDir("/dev/disk/by-id")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, filepath.Join("/dev/disk/by-id", e.Name()))
	}
	sort.Strings(out)
	return out
}

// ShortenDevice strips the /dev/disk/by-id/ prefix if present.
func ShortenDevice(dev string) string {
	return strings.TrimPrefix(dev, "/dev/disk/by-id/")
}

// CanonicalizeDevice returns the matching /dev/disk/by-id path for dev,
// or dev unchanged when no by-id entry resolves to it.
func CanonicalizeDevice(dev string) string {
	given, err := filepath.EvalSymlinks(dev)
	if err != nil {
		given = dev
	}
	entries, err := os.ReadDir("/dev/disk/by-id")
	if err != nil {
		return dev
	}
	for _, e := range entries {
		p := filepath.Join("/dev/disk/by-id", e.Name())
		real, err := filepath.EvalSymlinks(p)
		if err == nil && real == given {
			return p
		}
	}
	return dev
}

// CurrentTimezone determines the timezone from /etc/localtime.
func CurrentTimezone() string {
	link, err := os.Readlink("/etc/localtime")
	if err != nil {
		// Fall through to content comparison below.
		return compareTimezone()
	}
	const marker = "zoneinfo/"
	if i := strings.LastIndex(link, marker); i >= 0 {
		return link[i+len(marker):]
	}
	return "Europe/London"
}

func compareTimezone() string {
	target, err := os.ReadFile("/etc/localtime")
	if err != nil {
		return "Europe/London"
	}
	var found string
	root := "/usr/share/zoneinfo"
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			if found != "" {
				return filepath.SkipAll
			}
			return nil
		}
		data, err := os.ReadFile(path)
		if err == nil && bytes_equal(data, target) {
			found = strings.TrimPrefix(path, root+"/")
			return filepath.SkipAll
		}
		return nil
	})
	if found == "" {
		return "Europe/London"
	}
	return found
}

// Timezones lists all regular files below /usr/share/zoneinfo, relative
// and sorted (find -type f -printf %P | sort -u).
func Timezones() []string {
	root := "/usr/share/zoneinfo"
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return out
}

// Keymaps scans common locations for console keymaps (*.map.gz).
func Keymaps() []string {
	seen := map[string]bool{}
	var out []string
	for _, root := range []string{"/usr/share/keymaps", "/usr/share/kbd/keymaps"} {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".map.gz") {
				return nil
			}
			m := strings.TrimSuffix(name, ".map.gz")
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
			return nil
		})
	}
	sort.Strings(out)
	return out
}

// FallbackKeymaps is used when no keymap directory exists on the host.
var FallbackKeymaps = []string{
	"us", "de", "de-latin1", "fr", "uk", "es", "it", "nl", "be-latin1",
	"sv-latin1", "no-latin1", "dk-latin1", "fi", "pl", "pt-latin1",
	"br-abnt2", "cz-lat2", "hu", "ru", "sg-latin1", "cf", "jp106",
}

// DefaultKeymap reads KEYMAP from /etc/vconsole.conf and validates it
// against known keymaps, falling back to "us".
func DefaultKeymap(known []string) string {
	data, err := os.ReadFile("/etc/vconsole.conf")
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "KEYMAP=") {
				k := strings.Trim(line[len("KEYMAP="):], `"'`)
				for _, cand := range known {
					if cand == k {
						return k
					}
				}
			}
		}
	}
	return "us"
}

// SystemLocales runs `locale -a`.
func SystemLocales() ([]string, error) {
	out, err := runCapture("locale", "-a")
	if err != nil {
		return nil, err
	}
	lines := strings.FieldsFunc(string(out), func(r rune) bool { return r == '\n' })
	res := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			res = append(res, l)
		}
	}
	sort.Strings(res)
	return res, nil
}
