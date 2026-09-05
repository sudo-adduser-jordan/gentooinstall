//go:build linux

package live

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// DefaultPath is the PATH exported by the live init before any command runs.
const DefaultPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// Init bootstraps the live environment. It runs exactly once, when the binary
// is PID 1. It never aborts the boot: problems are reported on the console and
// the TUI still starts.
func Init() error {
	setDefaultEnv("PATH", DefaultPath)
	setDefaultEnv("SHELL", "/bin/sh")
	setDefaultEnv("TERM", "linux")

	var errs []string
	for _, m := range MountTable() {
		if isMounted(m.Target) {
			continue
		}
		if err := syscall.Mount(m.Device, m.Target, m.FSType, m.Flags, m.Data); err != nil {
			errs = append(errs, fmt.Sprintf("mount %s: %v", m.Target, err))
		}
	}

	// Runtime directories a few tools expect; idempotent on the initramfs.
	for _, dir := range []string{"/run", "/tmp", "/var/tmp", "/dev/pts", "/dev/shm"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			errs = append(errs, fmt.Sprintf("mkdir %s: %v", dir, err))
		}
	}

	loadModules()
	go tryDHCP()

	if len(errs) > 0 {
		return fmt.Errorf("live init: %s", strings.Join(errs, "; "))
	}
	return nil
}

func setDefaultEnv(key, val string) {
	if os.Getenv(key) == "" {
		_ = os.Setenv(key, val)
	}
}

// isMounted reports whether target appears in /proc/mounts (empty until proc
// itself is mounted, which is the first entry of the mount table).
func isMounted(target string) bool {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == target {
			return true
		}
	}
	return false
}

// loadModules best-effort loads the storage drivers in NeedModules.
func loadModules() {
	for _, mod := range NeedModules {
		if moduleLoaded(mod) {
			continue
		}
		cmd := exec.Command("modprobe", mod)
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stdout, "live: modprobe %s: %v %s\n", mod, err,
				strings.TrimSpace(string(out)))
		}
	}
}

// moduleLoaded reports whether name is listed in /proc/modules.
func moduleLoaded(name string) bool {
	data, err := os.ReadFile("/proc/modules")
	if err != nil {
		return false
	}
	prefix := name + " "
	for _, line := range bytes.Split(data, []byte("\n")) {
		if bytes.HasPrefix(line, []byte(prefix)) {
			return true
		}
	}
	return false
}

// tryDHCP brings up the first physical interface with udhcpc, retrying a few
// times because the network driver may still be probing. It runs in the
// background so it never delays the TUI.
func tryDHCP() {
	for attempt := 1; attempt <= 3; attempt++ {
		ifaces := Interfaces()
		var last error
		for _, iface := range ifaces {
			last = dhcpUp(iface)
			if last == nil {
				return
			}
		}
		if last == nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func dhcpUp(iface string) error {
	cmd := exec.Command("udhcpc", "-i", iface, "-n", "-q", "-s", "/etc/udhcpc/default.script")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("udhcpc %s: %w: %s", iface, err, strings.TrimSpace(string(out)))
	}
	return nil
}
