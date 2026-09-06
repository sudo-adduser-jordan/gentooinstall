// Package cli holds the gentooinstall command-line parser in importable
// form so the external test suite (tests/, which may only exercise
// exported identifiers) can cover argument handling without invoking
// package main.
package cli

import (
	"fmt"
	"strings"
)

// Parsed is the result of parsing os.Args[1:].
type Parsed struct {
	Mode        string // "", "install", "gif", "chroot", "in-chroot"
	CfgPath     string // -c/--config value or positional config path
	Rest        []string
	ShowHelp    bool
	ShowVersion bool
}

// ParseArgs parses args (typically os.Args[1:]) without side effects:
// no printing, no os.Exit. Errors mirror the fatal messages in
// cmd/gentooinstall/main.go.
func ParseArgs(args []string) (Parsed, error) {
	var p Parsed
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "-h", "--help", "help":
			p.ShowHelp = true
			return p, nil
		case "-v", "--version":
			p.ShowVersion = true
			return p, nil
		case "-c", "--config":
			if i+1 >= len(args) {
				return p, fmt.Errorf("--config requires a path")
			}
			i++
			p.CfgPath = args[i]
		case "install":
			if p.Mode == "gif" {
				return p, fmt.Errorf("invalid argument '%s'", a)
			}
			p.Mode = "install"
		case "gif":
			if p.Mode == "install" {
				return p, fmt.Errorf("invalid argument '%s'", a)
			}
			p.Mode = "gif"
			p.Rest = args[i+1:]
			i = len(args)
		case "chroot":
			p.Mode = "chroot"
			p.Rest = args[i+1:]
			i = len(args)
		case "--in-chroot":
			p.Mode = "in-chroot"
		default:
			if p.CfgPath == "" && !strings.HasPrefix(a, "-") {
				p.CfgPath = a // positional config for TUI/install mode
			} else {
				return p, fmt.Errorf("invalid argument '%s'", a)
			}
		}
		i++
	}
	return p, nil
}
