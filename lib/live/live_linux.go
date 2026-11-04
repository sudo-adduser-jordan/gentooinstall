//go:build linux

package live

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"gentooinstall/lib/sysinfo"
)

const DEFAULTPATH = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

func Init() error {
	setDefaultEnv("PATH", DEFAULTPATH)
	setDefaultEnv("SHELL", "/bin/sh")
	setDefaultEnv("TERM", "linux")

	var errs []string
	for _, mount := range MountTable() {
		if isMounted(mount.Target) {
			continue
		}
		if err := syscall.Mount(mount.Device, mount.Target, mount.FSType, mount.Flags, mount.Data); err != nil {
			errs = append(errs, fmt.Sprintf("mount %s: %v", mount.Target, err))
		}
	}

	for _, dir := range []string{
		"/run",
		"/tmp",
		"/var/tmp",
		"/dev/pts",
		"/dev/shm",
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			errs = append(errs, fmt.Sprintf("mkdir %s: %v", dir, err))
		}
	}

	loadModules()
	logBlockDevices()
	logTools()
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

func logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stdout, msg)
	writeSerial(msg)
}

var tuiOwnsSerial bool

func SetTuiActive(active bool) { tuiOwnsSerial = active }

func TuiActive() bool { return tuiOwnsSerial }

func serialConsoleOnly() bool {
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return false
	}
	fields := strings.Fields(string(data))
	hasS0, hasTty0 := false, false
	for _, field := range fields {
		if field == "console=ttyS0" || strings.HasPrefix(field, "console=ttyS0,") {
			hasS0 = true
		}
		if field == "console=tty0" || strings.HasPrefix(field, "console=tty0") {
			hasTty0 = true
		}
	}
	return hasS0 && !hasTty0
}

func appendFileLog(msg string) {
	for _, path := range []string{"/run/live-net.log", "/tmp/live-net.log"} {
		if file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			_, _ = file.WriteString(msg + "\n")
			_ = file.Close()
			return
		}
	}
}

func logSerial(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	appendFileLog(msg)
	if tuiOwnsSerial && serialConsoleOnly() {
		return
	}
	writeSerial(msg)
}

func writeSerial(msg string) {
	if tuiOwnsSerial && serialConsoleOnly() {
		return
	}
	if file, err := os.OpenFile("/dev/ttyS0", os.O_WRONLY, 0); err == nil {
		_, _ = file.WriteString(msg + "\n")
		_ = file.Close()
	}
}

func loadModules() {
	for _, mod := range NeedModules {
		if moduleLoaded(mod) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(ModuleDir, mod+".ko"))
		if err != nil {
			logf("live: load %s: %v", mod, err)
			continue
		}
		if err := unix.InitModule(data, ""); err != nil {
			logf("live: init_module %s: %v", mod, err)
			continue
		}
		logf("live: loaded module %s", mod)
	}
}

func logBlockDevices() {
	for _, dev := range sysinfo.Devices() {
		logf("live: block device %s", dev)
	}
}

func logTools() {
	found := []string{}
	for _, program := range []string{"busybox", "gpg", "lsblk", "ntpd", "partprobe",
		"sgdisk", "mount", "tar", "udhcpc"} {
		if _, err := exec.LookPath(program); err == nil {
			found = append(found, program)
		}
	}
	logf("live: tools: %s", strings.Join(found, " "))
}

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

var (
	dhcpMu      sync.Mutex
	dhcpDone    bool
	dhcpSucceed bool
)

func init() {
	NetworkReady = func() bool {
		dhcpMu.Lock()
		defer dhcpMu.Unlock()
		return dhcpSucceed
	}
}

func tryDHCP() {
	for attempt := 1; attempt <= 3; attempt++ {
		ifaces := Interfaces()
		var last error
		for _, iface := range ifaces {
			last = dhcpUp(iface)
			if last == nil {
				dhcpMu.Lock()
				dhcpDone = true
				dhcpSucceed = true
				dhcpMu.Unlock()
				logDHCPUp(iface)
				return
			}
			logSerial("live: dhcp %s: %v", iface, last)
		}
		if len(ifaces) == 0 {
			logSerial("live: dhcp: no interfaces on attempt %d, retrying", attempt)
			time.Sleep(2 * time.Second)
			continue
		}
		time.Sleep(2 * time.Second)
	}
	dhcpMu.Lock()
	dhcpDone = true
	dhcpMu.Unlock()
}

func logDHCPUp(iface string) {
	logSerial("live: dhcp %s: up", iface)
	if data, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		conf := strings.TrimSpace(string(data))
		if conf != "" {
			logSerial("live: resolv.conf:\n%s", conf)
		} else {
			logSerial("live: resolv.conf: (empty)")
		}
	} else {
		logSerial("live: resolv.conf: %v", err)
	}
}

func dhcpUp(iface string) error {
	_ = exec.Command("ip", "link", "set", iface, "up").Run()
	cmd := exec.Command("udhcpc", "-i", iface, "-n", "-q", "-t", "3", "-T", "2",
		"-s", "/etc/udhcpc/default.script")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("udhcpc %s: %w: %s", iface, err, strings.TrimSpace(string(out)))
	}
	return nil
}
