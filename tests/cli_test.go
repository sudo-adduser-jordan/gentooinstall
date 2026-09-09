// Command-line argument parsing (lib/cli).
package tests

import (
	"testing"

	"gentooinstall/lib/cli"
)

func TestParseArgsEmpty(testingT *testing.T) {
	parsed, err := cli.ParseArgs(nil)
	if err != nil {
		testingT.Fatalf("ParseArgs(nil): %v", err)
	}
	if parsed.Mode != "" || parsed.CfgPath != "" || parsed.ShowHelp || parsed.ShowVersion {
		testingT.Fatalf("unexpected parse: %+v", parsed)
	}
}

func TestParseArgsHelpVersion(testingT *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}, {"help"}} {
		parsed, err := cli.ParseArgs(args)
		if err != nil || !parsed.ShowHelp {
			testingT.Fatalf("ParseArgs(%v) = %+v, %v", args, parsed, err)
		}
	}
	for _, args := range [][]string{{"-v"}, {"--version"}} {
		parsed, err := cli.ParseArgs(args)
		if err != nil || !parsed.ShowVersion {
			testingT.Fatalf("ParseArgs(%v) = %+v, %v", args, parsed, err)
		}
	}
}

func TestParseArgsModes(testingT *testing.T) {
	parsed, err := cli.ParseArgs([]string{"install", "builds/desktop-systemd.toml"})
	if err != nil || parsed.Mode != "install" || parsed.CfgPath != "builds/desktop-systemd.toml" {
		testingT.Fatalf("install parse = %+v, %v", parsed, err)
	}
	parsed, err = cli.ParseArgs([]string{"-c", "my.toml", "install"})
	if err != nil || parsed.Mode != "install" || parsed.CfgPath != "my.toml" {
		testingT.Fatalf("-c install parse = %+v, %v", parsed, err)
	}
	parsed, err = cli.ParseArgs([]string{"gif", "out.gif"})
	if err != nil || parsed.Mode != "gif" || len(parsed.Rest) != 1 || parsed.Rest[0] != "out.gif" {
		testingT.Fatalf("gif parse = %+v, %v", parsed, err)
	}
	parsed, err = cli.ParseArgs([]string{"chroot", "/mnt/gentoo", "bash"})
	if err != nil || parsed.Mode != "chroot" || len(parsed.Rest) != 2 {
		testingT.Fatalf("chroot parse = %+v, %v", parsed, err)
	}
	parsed, err = cli.ParseArgs([]string{"--in-chroot"})
	if err != nil || parsed.Mode != "in-chroot" {
		testingT.Fatalf("in-chroot parse = %+v, %v", parsed, err)
	}
	parsed, err = cli.ParseArgs([]string{"custom.toml"})
	if err != nil || parsed.CfgPath != "custom.toml" {
		testingT.Fatalf("positional parse = %+v, %v", parsed, err)
	}
}

func TestParseArgsErrors(testingT *testing.T) {
	for _, args := range [][]string{
		{"-c"},
		{"--config"},
		{"--bogus"},
		{"install", "gif"},
		{"a.toml", "b.toml"},
	} {
		if _, err := cli.ParseArgs(args); err == nil {
			testingT.Fatalf("ParseArgs(%v) expected error", args)
		}
	}
}
