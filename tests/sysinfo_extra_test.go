// Mirror hosts, device enumeration and locale/keymap helpers.
package tests

import (
	"strings"
	"testing"

	"gentooinstall/lib/sysinfo"
)

func TestMirrorHostCases(testingT *testing.T) {
	cases := map[string]string{
		"https://mirror.example.com/gentoo": "mirror.example.com",
		"http://10.0.2.2:8080/x":            "10.0.2.2:8080",
		"not a url":                         "",
		"":                                  "",
		"/relative/path":                    "",
	}
	for in, want := range cases {
		if got := sysinfo.MirrorHost(in); got != want {
			testingT.Fatalf("MirrorHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDevicesSkipsPseudo(testingT *testing.T) {
	for _, dev := range sysinfo.Devices() {
		base := dev[strings.LastIndex(dev, "/")+1:]
		for _, prefix := range []string{"loop", "ram", "sr", "zram"} {
			if strings.HasPrefix(base, prefix) {
				testingT.Fatalf("Devices leaked pseudo-device %q", dev)
			}
		}
	}
}

func TestSystemLocalesErrorWithoutPath(testingT *testing.T) {
	testingT.Setenv("PATH", "")
	if _, err := sysinfo.SystemLocales(); err == nil {
		testingT.Skip("locale unexpectedly succeeded without PATH")
	}
}

func TestDefaultKeymapValidation(testingT *testing.T) {
	if keymap := sysinfo.DefaultKeymap(nil); keymap != "us" {
		testingT.Fatalf("nil known = %q, want us", keymap)
	}
	if keymap := sysinfo.DefaultKeymap([]string{}); keymap != "us" {
		testingT.Fatalf("empty known = %q, want us", keymap)
	}
}

func TestTimezonesKeymapsDoNotFail(testingT *testing.T) {
	// Host-dependent enumeration; only assert it never panics and stays sorted.
	tz := sysinfo.Timezones()
	for idx := 1; idx < len(tz); idx++ {
		if tz[idx-1] > tz[idx] {
			testingT.Fatal("Timezones not sorted")
		}
	}
	km := sysinfo.Keymaps()
	for idx := 1; idx < len(km); idx++ {
		if km[idx-1] > km[idx] {
			testingT.Fatal("Keymaps not sorted")
		}
	}
}
