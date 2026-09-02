package tests

import (
	"reflect"
	"testing"

	"gentooinstall/internal/installer"
)

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"vmlinuz-6.1.11-gentoo", "vmlinuz-6.10.0", true},
		{"vmlinuz-6.10.0", "vmlinuz-6.1.11-gentoo", false},
		{"vmlinuz-6.1.9", "vmlinuz-6.1.10", true},
		{"vmlinuz-6.1.10", "vmlinuz-6.1.9", false},
		{"vmlinuz-4.19.0", "vmlinuz-5.4.0", true},
		{"vmlinuz-6.1", "vmlinuz-6.1", false},
		{"vmlinuz-6.1.11", "vmlinuz-6.1.11-gentoo", true},
		{"vmlinuz-6.1.11-gentoo", "vmlinuz-6.1.11", false},
		{"kernel-5.15.7", "kernel-5.15.6", false},
	}
	for _, tc := range cases {
		if got := installer.VersionLess(tc.a, tc.b); got != tc.want {
			t.Fatalf("VersionLess(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestEfiBootmgrArgs(t *testing.T) {
	got := installer.EfiBootmgrArgs("/dev/sda", "1", "root=UUID=abc rd.vconsole.keymap=us")
	want := []string{
		"--verbose", "--create",
		"--disk", "/dev/sda", "--part", "1",
		"--label", "gentoo",
		"--loader", `\vmlinuz.efi`,
		"--unicode", `initrd=\initramfs.img root=UUID=abc rd.vconsole.keymap=us`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EfiBootmgrArgs = %#v, want %#v", got, want)
	}
}

func TestDiskNames(t *testing.T) {
	cases := []struct {
		name    string
		entries []installer.RaidMember
		want    string
	}{
		{"empty", nil, ""},
		{"single", []installer.RaidMember{{Disk: "/dev/sdb"}}, "/dev/sdb"},
		{"multiple", []installer.RaidMember{{Disk: "/dev/sdb"}, {Disk: "/dev/sdc"}}, "/dev/sdb /dev/sdc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := installer.DiskNames(tc.entries); got != tc.want {
				t.Fatalf("DiskNames = %q, want %q", got, tc.want)
			}
		})
	}
}
