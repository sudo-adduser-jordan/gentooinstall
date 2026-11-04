package disklayout

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var errBlkidNoMatch = errors.New("blkid: no matching device found yet")

type Resolver struct {
	Layout *Layout

	cachedLsblk string

	overrides map[string]string
}

func (resolver *Resolver) SetResolvedDevices(devices map[string]string) {
	if resolver.overrides == nil && len(devices) > 0 {
		resolver.overrides = map[string]string{}
	}
	for key, value := range devices {
		resolver.overrides[key] = value
	}
}

func runOut(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

func GetBlkidField(field, device string) (string, error) {
	if err := exec.Command("blkid", "-g", "-c", "/dev/null").Run(); err != nil {
		return "", fmt.Errorf("error while executing blkid: %w", err)
	}
	if err := exec.Command("partprobe").Run(); err != nil {
		_ = err
	}
	out, err := runOut("blkid", "-c", "/dev/null", "-o", "export", device)
	if err != nil {
		return "", fmt.Errorf("error while executing blkid %q: %w", device, err)
	}
	for _, line := range strings.Split(out, "\n") {
		if value, ok := strings.CutPrefix(line, field+"="); ok {
			return value, nil
		}
	}
	return "", fmt.Errorf("could not find %s=... in blkid output for %s", field, device)
}

func (resolver *Resolver) deviceByPartuuid(partUUID string) (string, error) {
	path := filepath.Join("/dev/disk/by-partuuid", partUUID)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	return DeviceByBlkidField("PARTUUID", partUUID)
}

func DeviceByUuid(uuid string) (string, error) {
	path := filepath.Join("/dev/disk/by-uuid", uuid)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	return DeviceByBlkidField("UUID", uuid)
}

func DeviceByBlkidField(field, value string) (string, error) {
	if err := exec.Command("blkid", "-g", "-c", "/dev/null").Run(); err != nil {
		return "", fmt.Errorf("error while executing blkid: %w", err)
	}
	if _, err := exec.LookPath("partprobe"); err == nil {
		_ = exec.Command("partprobe").Run()
	}
	out, err := runOut("blkid", "-c", "/dev/null", "-o", "export", "-t", field+"="+value)
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 2 {
			return "", fmt.Errorf("could not find device with %s=%s yet: %w",
				field, value, errBlkidNoMatch)
		}
		return "", fmt.Errorf("error while executing blkid to find %s=%s: %w", field, value, err)
	}
	for _, line := range strings.Split(out, "\n") {
		if value, ok := strings.CutPrefix(line, "DEVNAME="); ok {
			return value, nil
		}
	}
	return "", fmt.Errorf("could not find DEVNAME=... in blkid output")
}

func (resolver *Resolver) lsblkOutput() (string, error) {
	if resolver.cachedLsblk != "" {
		return resolver.cachedLsblk, nil
	}
	out, err := runOut("lsblk", "--all", "--path", "--pairs", "--output", "NAME,PTUUID,PARTUUID")
	if err != nil {
		return "", fmt.Errorf("error while executing lsblk: %w", err)
	}
	resolver.cachedLsblk = out
	return out, nil
}

func (resolver *Resolver) CacheLsblkOutput() error {
	out, err := runOut("lsblk", "--all", "--path", "--pairs", "--output", "NAME,PTUUID,PARTUUID")
	if err != nil {
		return fmt.Errorf("error while executing lsblk to cache output: %w", err)
	}
	resolver.cachedLsblk = out
	return nil
}

func (resolver *Resolver) SetCachedLsblk(value string) {
	if strings.TrimSpace(value) != "" {
		resolver.cachedLsblk = value
	}
}

func (resolver *Resolver) CachedEnvValue() string { return resolver.cachedLsblk }

func (resolver *Resolver) DeviceByPtUuid(ptuuid string) (string, error) {
	ptuuid = strings.ToLower(ptuuid)
	out, err := resolver.lsblkOutput()
	if err != nil {
		return "", err
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.ToLower(sc.Text())
		if strings.Contains(line, fmt.Sprintf("ptuuid=%q partuuid=\"\"", ptuuid)) ||
			strings.Contains(line, fmt.Sprintf("ptuuid=%q partuuid=%q", ptuuid, "")) {
			if rest, ok := strings.CutPrefix(line, "name=\""); ok {
				if dev, _, found := strings.Cut(rest, "\""); found {
					return dev, nil
				}
			}
		}
	}
	if dev, err := deviceByPtUuidFromBlkid(ptuuid); err == nil {
		return dev, nil
	}
	return "", fmt.Errorf("could not find PTUUID=%s in lsblk output", ptuuid)
}

func deviceByPtUuidFromBlkid(ptuuid string) (string, error) {
	blocks, err := os.ReadDir("/sys/block")
	if err != nil {
		return "", err
	}
	for _, block := range blocks {
		if !block.Type().IsDir() {
			continue
		}
		dev := filepath.Join("/dev", block.Name())
		out, err := runOut("blkid", "-p", "-o", "export", dev)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			if value, ok := strings.CutPrefix(line, "PTUUID="); ok &&
				strings.ToLower(strings.TrimSpace(value)) == ptuuid {
				return dev, nil
			}
		}
	}
	return "", fmt.Errorf("could not find PTUUID=%s in blkid probes", ptuuid)
}

func DeviceByMdadmUuid(uuid string) (string, error) {
	mduuid := UuidToMdUUID(uuid)
	out, err := runOut("mdadm", "--examine", "--scan")
	if err != nil {
		return "", fmt.Errorf("error while executing mdadm: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		low := strings.ToLower(line)
		if strings.Contains(low, "uuid="+mduuid) && strings.HasPrefix(low, "array") {
			dev := strings.TrimPrefix(line, "ARRAY")
			if index := strings.Index(dev, "metadata="); index >= 0 {
				dev = dev[:index]
			}
			return strings.TrimSpace(dev), nil
		}
	}
	return "", fmt.Errorf("could not find UUID=%s in mdadm output", mduuid)
}

func (resolver *Resolver) ResolveDevice(id string) (string, error) {
	if resolver.overrides != nil {
		if dev, ok := resolver.overrides[id]; ok {
			return dev, nil
		}
	}
	entry, ok := resolver.Layout.resolvable[id]
	if !ok {
		return "", fmt.Errorf("cannot resolve id=%q to a block device (no table entry)", id)
	}

	var dev string
	var err error
	switch entry.Type {
	case "partuuid":
		dev, err = resolver.deviceByPartuuid(entry.Arg)
	case "ptuuid":
		dev, err = resolver.DeviceByPtUuid(entry.Arg)
	case "uuid":
		dev, err = DeviceByUuid(entry.Arg)
	case "mdadm":
		dev, err = DeviceByMdadmUuid(resolver.Layout.uuids[id])
	case "luks":
		dev = "/dev/mapper/" + entry.Arg
	case "device":
		dev = entry.Arg
	default:
		return "", fmt.Errorf("cannot resolve '%s:%s' to device (unknown type)", entry.Type, entry.Arg)
	}
	if err != nil {
		return "", err
	}
	return Canonicalize(dev), nil
}

func Canonicalize(dev string) string {
	given, err := filepath.EvalSymlinks(dev)
	if err != nil {
		given = dev
	}
	entries, err := os.ReadDir("/dev/disk/by-id")
	if err != nil {
		return dev
	}
	best := ""
	for _, entry := range entries {
		path := filepath.Join("/dev/disk/by-id", entry.Name())
		real, err := filepath.EvalSymlinks(path)
		if err == nil && real == given {
			if best == "" || path < best {
				best = path
			}
		}
	}
	if best != "" {
		return best
	}
	return dev
}
