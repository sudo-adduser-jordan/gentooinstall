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

// errBlkidNoMatch reports that blkid found no device for a token yet
// (blkid exits 2 in that case). Callers like waitPartition retry on it.
var errBlkidNoMatch = errors.New("blkid: no matching device found yet")

// Resolver maps layout ids to concrete device paths, mirroring
// resolve_device_by_id and its helpers from utils.sh.
type Resolver struct {
	Layout *Layout

	// cached lsblk output (needed because lsblk misbehaves in chroot)
	cachedLsblk string
}

func runOut(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

// GetBlkidField returns a field from blkid -o export for the device.
func GetBlkidField(field, device string) (string, error) {
	if err := exec.Command("blkid", "-g", "-c", "/dev/null").Run(); err != nil {
		return "", fmt.Errorf("error while executing blkid: %w", err)
	}
	if err := exec.Command("partprobe").Run(); err != nil {
		// best effort, like bash (errors ignored there too)
		_ = err
	}
	out, err := runOut("blkid", "-c", "/dev/null", "-o", "export", device)
	if err != nil {
		return "", fmt.Errorf("error while executing blkid %q: %w", device, err)
	}
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, field+"="); ok {
			return v, nil
		}
	}
	return "", fmt.Errorf("could not find %s=... in blkid output for %s", field, device)
}

// deviceByPartuuid resolves a partition id via the udev symlink, falling
// back to a direct blkid scan of the device nodes.
func (r *Resolver) deviceByPartuuid(u string) (string, error) {
	p := filepath.Join("/dev/disk/by-partuuid", u)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	return DeviceByBlkidField("PARTUUID", u)
}

// DeviceByUuid resolves via /dev/disk/by-uuid or blkid.
func DeviceByUuid(u string) (string, error) {
	p := filepath.Join("/dev/disk/by-uuid", u)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	return DeviceByBlkidField("UUID", u)
}

// DeviceByBlkidField searches all block devices for a matching field.
func DeviceByBlkidField(field, value string) (string, error) {
	if err := exec.Command("blkid", "-g", "-c", "/dev/null").Run(); err != nil {
		return "", fmt.Errorf("error while executing blkid: %w", err)
	}
	if _, err := exec.LookPath("partprobe"); err == nil {
		_ = exec.Command("partprobe").Run()
	}
	out, err := runOut("blkid", "-c", "/dev/null", "-o", "export", "-t", field+"="+value)
	if err != nil {
		// blkid exits 2 when no device carries the token yet (e.g. right
		// after partprobe); that is "not found yet", not a failure — report
		// it as such so waitPartition can retry.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 2 {
			return "", fmt.Errorf("could not find device with %s=%s yet: %w",
				field, value, errBlkidNoMatch)
		}
		return "", fmt.Errorf("error while executing blkid to find %s=%s: %w", field, value, err)
	}
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, "DEVNAME="); ok {
			return v, nil
		}
	}
	return "", fmt.Errorf("could not find DEVNAME=... in blkid output")
}

func (r *Resolver) lsblkOutput() (string, error) {
	if r.cachedLsblk != "" {
		return r.cachedLsblk, nil
	}
	out, err := runOut("lsblk", "--all", "--path", "--pairs", "--output", "NAME,PTUUID,PARTUUID")
	if err != nil {
		return "", fmt.Errorf("error while executing lsblk: %w", err)
	}
	r.cachedLsblk = out
	return out, nil
}

// CacheLsblkOutput pre-caches lsblk output before entering a chroot.
func (r *Resolver) CacheLsblkOutput() error {
	out, err := runOut("lsblk", "--all", "--path", "--pairs", "--output", "NAME,PTUUID,PARTUUID")
	if err != nil {
		return fmt.Errorf("error while executing lsblk to cache output: %w", err)
	}
	r.cachedLsblk = out
	return nil
}

// SetCachedLsblk seeds the cache from the parent environment
// (GENTOO_CACHED_LSBLK passthrough into the chroot).
func (r *Resolver) SetCachedLsblk(v string) {
	if strings.TrimSpace(v) != "" {
		r.cachedLsblk = v
	}
}

// CachedEnvValue exposes CACHED_LSBLK_OUTPUT for chroot env passthrough.
func (r *Resolver) CachedEnvValue() string { return r.cachedLsblk }

// DeviceByPtUuid finds the whole-disk device with the given PTUUID.
func (r *Resolver) DeviceByPtUuid(ptuuid string) (string, error) {
	ptuuid = strings.ToLower(ptuuid)
	out, err := r.lsblkOutput()
	if err != nil {
		return "", err
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.ToLower(sc.Text())
		if strings.Contains(line, fmt.Sprintf("ptuuid=%q partuuid=\"\"", ptuuid)) ||
			strings.Contains(line, fmt.Sprintf("ptuuid=%q partuuid=%q", ptuuid, "")) {
			// name="..." is the first pair on the line.
			if rest, ok := strings.CutPrefix(line, "name=\""); ok {
				if dev, _, found := strings.Cut(rest, "\""); found {
					return dev, nil
				}
			}
		}
	}
	// Fallback: probe every /sys/block device directly. This is immune to
	// a missing or stale udev database (docker containers) and works from
	// the live system alike.
	if dev, err := deviceByPtUuidFromBlkid(ptuuid); err == nil {
		return dev, nil
	}
	return "", fmt.Errorf("could not find PTUUID=%s in lsblk output", ptuuid)
}

func deviceByPtUuidFromBlkid(u string) (string, error) {
	blocks, err := os.ReadDir("/sys/block")
	if err != nil {
		return "", err
	}
	for _, b := range blocks {
		if !b.Type().IsDir() {
			continue
		}
		dev := filepath.Join("/dev", b.Name())
		out, err := runOut("blkid", "-p", "-o", "export", dev)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			if v, ok := strings.CutPrefix(line, "PTUUID="); ok &&
				strings.ToLower(strings.TrimSpace(v)) == u {
				return dev, nil
			}
		}
	}
	return "", fmt.Errorf("could not find PTUUID=%s in blkid probes", u)
}

// DeviceByMdadmUuid resolves an array uuid to its /dev/md/ device.
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
			if i := strings.Index(dev, "metadata="); i >= 0 {
				dev = dev[:i]
			}
			return strings.TrimSpace(dev), nil
		}
	}
	return "", fmt.Errorf("could not find UUID=%s in mdadm output", mduuid)
}

// ResolveDevice resolves the given id to a canonicalized device path.
func (r *Resolver) ResolveDevice(id string) (string, error) {
	entry, ok := r.Layout.resolvable[id]
	if !ok {
		return "", fmt.Errorf("cannot resolve id=%q to a block device (no table entry)", id)
	}

	var dev string
	var err error
	switch entry.Type {
	case "partuuid":
		dev, err = r.deviceByPartuuid(entry.Arg)
	case "ptuuid":
		dev, err = r.DeviceByPtUuid(entry.Arg)
	case "uuid":
		dev, err = DeviceByUuid(entry.Arg)
	case "mdadm":
		dev, err = DeviceByMdadmUuid(r.Layout.uuids[id])
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

// Canonicalize prefers a matching /dev/disk/by-id path.
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
	for _, e := range entries {
		p := filepath.Join("/dev/disk/by-id", e.Name())
		real, err := filepath.EvalSymlinks(p)
		if err == nil && real == given {
			// Prefer entries without a partition suffix for whole disks,
			// but any match is acceptable; keep the first sorted one.
			if best == "" || p < best {
				best = p
			}
		}
	}
	if best != "" {
		return best
	}
	return dev
}
