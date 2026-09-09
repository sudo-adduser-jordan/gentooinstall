// Command-line argument parsing (lib/cli).
package tests

import (
	"testing"

	"gentooinstall/lib/cli"
)

func TestParseArgsEmpty(t *testing.T) {
	p, err := cli.ParseArgs(nil)
	if err != nil {
		t.Fatalf("ParseArgs(nil): %v", err)
	}
	if p.Mode != "" || p.CfgPath != "" || p.ShowHelp || p.ShowVersion {
		t.Fatalf("unexpected parse: %+v", p)
	}
}

func TestParseArgsHelpVersion(t *testing.T) {
	for _, a := range [][]string{{"-h"}, {"--help"}, {"help"}} {
		p, err := cli.ParseArgs(a)
		if err != nil || !p.ShowHelp {
			t.Fatalf("ParseArgs(%v) = %+v, %v", a, p, err)
		}
	}
	for _, a := range [][]string{{"-v"}, {"--version"}} {
		p, err := cli.ParseArgs(a)
		if err != nil || !p.ShowVersion {
			t.Fatalf("ParseArgs(%v) = %+v, %v", a, p, err)
		}
	}
}

func TestParseArgsModes(t *testing.T) {
	p, err := cli.ParseArgs([]string{"install", "builds/desktop-systemd.toml"})
	if err != nil || p.Mode != "install" || p.CfgPath != "builds/desktop-systemd.toml" {
		t.Fatalf("install parse = %+v, %v", p, err)
	}
	p, err = cli.ParseArgs([]string{"-c", "my.toml", "install"})
	if err != nil || p.Mode != "install" || p.CfgPath != "my.toml" {
		t.Fatalf("-c install parse = %+v, %v", p, err)
	}
	p, err = cli.ParseArgs([]string{"gif", "out.gif"})
	if err != nil || p.Mode != "gif" || len(p.Rest) != 1 || p.Rest[0] != "out.gif" {
		t.Fatalf("gif parse = %+v, %v", p, err)
	}
	p, err = cli.ParseArgs([]string{"chroot", "/mnt/gentoo", "bash"})
	if err != nil || p.Mode != "chroot" || len(p.Rest) != 2 {
		t.Fatalf("chroot parse = %+v, %v", p, err)
	}
	p, err = cli.ParseArgs([]string{"--in-chroot"})
	if err != nil || p.Mode != "in-chroot" {
		t.Fatalf("in-chroot parse = %+v, %v", p, err)
	}
	p, err = cli.ParseArgs([]string{"custom.toml"})
	if err != nil || p.CfgPath != "custom.toml" {
		t.Fatalf("positional parse = %+v, %v", p, err)
	}
}

func TestParseArgsErrors(t *testing.T) {
	for _, a := range [][]string{
		{"-c"},
		{"--config"},
		{"--bogus"},
		{"install", "gif"},
		{"a.toml", "b.toml"},
	} {
		if _, err := cli.ParseArgs(a); err == nil {
			t.Fatalf("ParseArgs(%v) expected error", a)
		}
	}
}
