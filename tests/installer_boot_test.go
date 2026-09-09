// Pure bootloader helpers (VersionLess, EfiBootmgrArgs, DiskNames).
package tests

import (
	"reflect"
	"testing"

	"gentooinstall/lib/installer"
)

func TestVersionLess(testingT *testing.T) {
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
			testingT.Fatalf("VersionLess(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestEfiBootmgrArgs(testingT *testing.T) {
	got := installer.EfiBootmgrArgs("/dev/sda", "1", "root=UUID=abc rd.vconsole.keymap=us")
	want := []string{
		"--verbose", "--create",
		"--disk", "/dev/sda", "--part", "1",
		"--label", "gentoo",
		"--loader", `\vmlinuz.efi`,
		"--unicode", `initrd=\initramfs.img root=UUID=abc rd.vconsole.keymap=us`,
	}
	if !reflect.DeepEqual(got, want) {
		testingT.Fatalf("EfiBootmgrArgs = %#v, want %#v", got, want)
	}
}

func TestDiskNames(testingT *testing.T) {
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
		testingT.Run(tc.name, func(testingT *testing.T) {
			if got := installer.DiskNames(tc.entries); got != tc.want {
				testingT.Fatalf("DiskNames = %q, want %q", got, tc.want)
			}
		})
	}
}
