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

// DEFAULTPATH is the PATH exported by the live init before any command runs.
const DEFAULTPATH = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// Init bootstraps the live environment. It runs exactly once, when the binary
// is PID 1. It never aborts the boot: problems are reported on the console and
// the TUI still starts.
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

	// Runtime directories a few tools expect; idempotent on the initramfs.
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

// logf writes a line to the console and best-effort mirrors it to the first
// serial port, so headless serial boots (and the QEMU e2e) observe live-init
// progress. The single grub entry boots console=ttyS0 only, so /dev/console
// IS the serial port. It is intended for the synchronous boot sequence that
// completes before the TUI starts; use logSerial for anything that may fire
// later.
func logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stdout, msg)
	writeSerial(msg)
}

// tuiOwnsSerial is set once the BubbleTea TUI owns the terminal. Background
// goroutines must not write raw bytes to /dev/ttyS0 afterwards when the TUI
// itself renders on the serial port (console=ttyS0 under
// qemu -nographic -serial stdio): it injects lines into the alt-screen and
// smears the footer. Logs still go to the file fallback below.
var tuiOwnsSerial bool

// SetTuiActive reports whether the TUI now owns the terminal. Call with true
// immediately before p.Run() and false afterwards.
func SetTuiActive(active bool) { tuiOwnsSerial = active }

// TuiActive reports whether the TUI currently owns the terminal.
func TuiActive() bool { return tuiOwnsSerial }

// serialConsoleOnly reports whether the kernel was booted with console=ttyS0
// and without console=tty0 (the single grub entry). Then the TUI's
// stdout IS /dev/ttyS0 and raw serial writes corrupt it.
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

// appendFileLog best-effort appends background status to a file so it stays
// debuggable after serial is muted (serial TUI owns the port).
func appendFileLog(msg string) {
	for _, path := range []string{"/run/live-net.log", "/tmp/live-net.log"} {
		if file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			_, _ = file.WriteString(msg + "\n")
			_ = file.Close()
			return
		}
	}
}

// logSerial writes a line to the first serial port only (never stdio), for
// log output produced after the live boot has handed the terminal to the TUI
// (e.g. the background DHCP bring-up). Muted once the TUI owns a
// serial-only console; the message is kept in the file log instead.
func logSerial(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	appendFileLog(msg)
	if tuiOwnsSerial && serialConsoleOnly() {
		return
	}
	writeSerial(msg)
}

// writeSerial best-effort mirrors msg to /dev/ttyS0 so headless serial consoles
// (and the QEMU e2e) observe live-init progress.
func writeSerial(msg string) {
	if tuiOwnsSerial && serialConsoleOnly() {
		return
	}
	if file, err := os.OpenFile("/dev/ttyS0", os.O_WRONLY, 0); err == nil {
		_, _ = file.WriteString(msg + "\n")
		_ = file.Close()
	}
}

// loadModules best-effort loads the storage drivers in NeedModules from the
// modules bundled into the initramfs by scripts/release.sh. Modules are
// injected with the init_module syscall directly because the initramfs has
// no modprobe; entries that are built into the kernel or were not bundled
// are skipped.
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

// logBlockDevices prints the discovered block devices so the headless e2e
// and users can see which disks are visible after module loading.
func logBlockDevices() {
	for _, dev := range sysinfo.Devices() {
		logf("live: block device %s", dev)
	}
}

// logTools reports which host utilities the install engine needs are present
// on the live rootfs, so the headless e2e and users can tell at a glance
// whether the Alpine-based userspace was bundled correctly.
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
// dhcpDone is set once each interface attempt has finished (succeeded or
// given up), so callers can tell whether the network may still be coming up.
var (
	dhcpMu      sync.Mutex
	dhcpDone    bool
	dhcpSucceed bool
)

func init() {
	// The default NetworkReady (externally visible) reports whether the live
	// DHCP bring-up has succeeded; it is consulted by the mirror indicator.
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

// logDHCPUp reports a successful DHCP bring-up on the serial console so headless
// e2e tests can assert the network (and /etc/resolv.conf) came up, mirroring
// what the tarball/mirror fetches depend on. Serial-only: this runs in the
// background after the TUI owns the terminal.
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
	// Bring the interface up first; busybox udhcpc relies on the interface
	// being admin-up to send DHCPDISCOVER. QEMU user-net's internal DHCP server
	// answers on 10.0.2.2 and hands out 10.0.2.15.
	_ = exec.Command("ip", "link", "set", iface, "up").Run()
	// Bound the attempts (-t/ -T) so a missing server gives up in a few seconds
	// instead of letting udhcpc idle for minutes; -n exits if no lease is
	// obtained rather than looping forever.
	cmd := exec.Command("udhcpc", "-i", iface, "-n", "-q", "-t", "3", "-T", "2",
		"-s", "/etc/udhcpc/default.script")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("udhcpc %s: %w: %s", iface, err, strings.TrimSpace(string(out)))
	}
	return nil
}
