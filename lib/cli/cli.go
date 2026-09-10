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
	Mode        string // "", "install", "demo", "chroot", "in-chroot"
	CfgPath     string // -c/--config value or positional config path
	Rest        []string
	ShowHelp    bool
	ShowVersion bool
	// Demo marks a TUI run driven by the VHS demo recorder. It bypasses the
	// root gate (the simulation is non-destructive) so the GIF can be
	// produced by a regular user.
	Demo bool
}

// ParseArgs parses args (typically os.Args[1:]) without side effects:
// no printing, no os.Exit. Errors mirror the fatal messages in main.go.
func ParseArgs(args []string) (Parsed, error) {
	var parsed Parsed
	index := 0
	for index < len(args) {
		arg := args[index]
		switch arg {
		case "-h", "--help", "help":
			parsed.ShowHelp = true
			return parsed, nil
		case "-v", "--version":
			parsed.ShowVersion = true
			return parsed, nil
		case "-c", "--config":
			if index+1 >= len(args) {
				return parsed, fmt.Errorf("--config requires a path")
			}
			index++
			parsed.CfgPath = args[index]
		case "install":
			if parsed.Mode == "demo" {
				return parsed, fmt.Errorf("invalid argument '%s'", arg)
			}
			parsed.Mode = "install"
		case "demo":
			if parsed.Mode == "install" {
				return parsed, fmt.Errorf("invalid argument '%s'", arg)
			}
			parsed.Mode = "demo"
			parsed.Rest = args[index+1:]
			index = len(args)
		case "chroot":
			parsed.Mode = "chroot"
			parsed.Rest = args[index+1:]
			index = len(args)
		case "--in-chroot":
			parsed.Mode = "in-chroot"
		case "--demo":
			parsed.Demo = true
		default:
			if parsed.CfgPath == "" && !strings.HasPrefix(arg, "-") {
				parsed.CfgPath = arg // positional config for TUI/install mode
			} else {
				return parsed, fmt.Errorf("invalid argument '%s'", arg)
			}
		}
		index++
	}
	return parsed, nil
}
