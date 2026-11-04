package sysinfo

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func HasEFI() bool {
	_, err := os.Stat("/sys/firmware/efi")
	return err == nil
}

func SupportsFilesystemPath(path, fs string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[len(fields)-1] == fs {
			return true
		}
	}
	return false
}

func SupportsFilesystem(fs string) bool {
	return SupportsFilesystemPath("/proc/filesystems", fs)
}

func Devices() []string {
	seen := map[string]bool{}
	var out []string
	add := func(path string, real string) {
		if path == "" || seen[real] {
			return
		}
		seen[real] = true
		out = append(out, path)
	}
	for _, path := range byIDDevices() {
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			real = path
		}
		add(path, real)
	}
	for _, path := range sysBlockDevices() {
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			real = path
		}
		add(path, real)
	}
	sort.Strings(out)
	return out
}

func byIDDevices() []string {
	entries, err := os.ReadDir("/dev/disk/by-id")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, filepath.Join("/dev/disk/by-id", entry.Name()))
	}
	return out
}

func sysBlockDevices() []string {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		if pseudoBlockDevice(name) {
			continue
		}
		dev := filepath.Join("/dev", name)
		info, err := os.Stat(dev)
		if err != nil || !isBlockDevice(info) {
			continue
		}
		out = append(out, dev)
	}
	return out
}

func pseudoBlockDevice(name string) bool {
	for _, prefix := range []string{"loop", "ram", "sr", "zram"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func isBlockDevice(info os.FileInfo) bool {
	return info.Mode()&os.ModeDevice != 0 && info.Mode()&os.ModeCharDevice == 0
}

func CanonicalizeDevice(dev string) string {
	given, err := filepath.EvalSymlinks(dev)
	if err != nil {
		given = dev
	}
	entries, err := os.ReadDir("/dev/disk/by-id")
	if err != nil {
		return dev
	}
	for _, entry := range entries {
		path := filepath.Join("/dev/disk/by-id", entry.Name())
		real, err := filepath.EvalSymlinks(path)
		if err == nil && real == given {
			return path
		}
	}
	return dev
}

func CurrentTimezone() string {
	link, err := os.Readlink("/etc/localtime")
	if err != nil {
		return compareTimezone()
	}
	const marker = "zoneinfo/"
	if index := strings.LastIndex(link, marker); index >= 0 {
		return link[index+len(marker):]
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
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || found != "" {
			if found != "" {
				return filepath.SkipAll
			}
			return nil
		}
		data, err := os.ReadFile(path)
		if err == nil && bytes.Equal(data, target) {
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

func Timezones() []string {
	root := "/usr/share/zoneinfo"
	var out []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
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

func Keymaps() []string {
	seen := map[string]bool{}
	var out []string
	for _, root := range []string{"/usr/share/keymaps", "/usr/share/kbd/keymaps"} {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".map.gz") {
				return nil
			}
			keymapName := strings.TrimSuffix(name, ".map.gz")
			if !seen[keymapName] {
				seen[keymapName] = true
				out = append(out, keymapName)
			}
			return nil
		})
	}
	sort.Strings(out)
	return out
}

var FallbackKeymaps = []string{
	"us", "de", "de-latin1", "fr", "uk", "es", "it", "nl", "be-latin1",
	"sv-latin1", "no-latin1", "dk-latin1", "fi", "pl", "pt-latin1",
	"br-abnt2", "cz-lat2", "hu", "ru", "sg-latin1", "cf", "jp106",
}

func DefaultKeymap(known []string) string {
	data, err := os.ReadFile("/etc/vconsole.conf")
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "KEYMAP=") {
				keymap := strings.Trim(line[len("KEYMAP="):], `"'`)
				for _, cand := range known {
					if cand == keymap {
						return keymap
					}
				}
			}
		}
	}
	return "us"
}

func SystemLocales() ([]string, error) {
	out, err := exec.Command("locale", "-a").Output()
	if err != nil {
		return nil, err
	}
	lines := strings.FieldsFunc(string(out), func(ch rune) bool { return ch == '\n' })
	res := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			res = append(res, line)
		}
	}
	sort.Strings(res)
	return res, nil
}
