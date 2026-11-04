package cli

import (
	"fmt"
	"strings"
)

type Parsed struct {
	Mode        string
	CfgPath     string
	Rest        []string
	ShowHelp    bool
	ShowVersion bool
	Demo        bool
}

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
				parsed.CfgPath = arg
			} else {
				return parsed, fmt.Errorf("invalid argument '%s'", arg)
			}
		}
		index++
	}
	return parsed, nil
}
