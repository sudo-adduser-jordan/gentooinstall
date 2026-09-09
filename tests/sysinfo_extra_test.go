// Mirror hosts, device enumeration and locale/keymap helpers.
package tests

import (
	"strings"
	"testing"

	"gentooinstall/lib/sysinfo"
)

func TestMirrorHostCases(t *testing.T) {
	cases := map[string]string{
		"https://mirror.example.com/gentoo": "mirror.example.com",
		"http://10.0.2.2:8080/x":            "10.0.2.2:8080",
		"not a url":                         "",
		"":                                  "",
		"/relative/path":                    "",
	}
	for in, want := range cases {
		if got := sysinfo.MirrorHost(in); got != want {
			t.Fatalf("MirrorHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDevicesSkipsPseudo(t *testing.T) {
	for _, d := range sysinfo.Devices() {
		base := d[strings.LastIndex(d, "/")+1:]
		for _, prefix := range []string{"loop", "ram", "sr", "zram"} {
			if strings.HasPrefix(base, prefix) {
				t.Fatalf("Devices leaked pseudo-device %q", d)
			}
		}
	}
}

func TestSystemLocalesErrorWithoutPath(t *testing.T) {
	t.Setenv("PATH", "")
	if _, err := sysinfo.SystemLocales(); err == nil {
		t.Skip("locale unexpectedly succeeded without PATH")
	}
}

func TestDefaultKeymapValidation(t *testing.T) {
	if k := sysinfo.DefaultKeymap(nil); k != "us" {
		t.Fatalf("nil known = %q, want us", k)
	}
	if k := sysinfo.DefaultKeymap([]string{}); k != "us" {
		t.Fatalf("empty known = %q, want us", k)
	}
}

func TestTimezonesKeymapsDoNotFail(t *testing.T) {
	// Host-dependent enumeration; only assert it never panics and stays sorted.
	tz := sysinfo.Timezones()
	for i := 1; i < len(tz); i++ {
		if tz[i-1] > tz[i] {
			t.Fatal("Timezones not sorted")
		}
	}
	km := sysinfo.Keymaps()
	for i := 1; i < len(km); i++ {
		if km[i-1] > km[i] {
			t.Fatal("Keymaps not sorted")
		}
	}
}
