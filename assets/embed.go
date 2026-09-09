// Package assets embeds static files used by the installer.
package assets

import (
	_ "embed"
)

// Fstab is the base /etc/fstab written to the target system.
//
//go:embed fstab
var Fstab string

// SSHDConfig is the hardened sshd configuration installed on the target.
//
//go:embed sshd_config
var SSHDConfig string

// I18NSupported lists all locales supported by locale-gen, one per line.
//
//go:embed i18n_supported
var I18NSupported string

// SupportedLocales returns the embedded locale list.
func SupportedLocales() []string {
	return splitLines(I18NSupported)
}

func splitLines(str string) []string {
	var out []string
	start := 0
	for index := 0; index < len(str); index++ {
		if str[index] == '\n' {
			line := str[start:index]
			if length := len(line); length > 0 && line[length-1] == '\r' {
				line = line[:length-1]
			}
			out = append(out, line)
			start = index + 1
		}
	}
	if start < len(str) {
		out = append(out, str[start:])
	}
	// Trim trailing empty lines.
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}
