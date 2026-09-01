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

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if n := len(line); n > 0 && line[n-1] == '\r' {
				line = line[:n-1]
			}
			out = append(out, line)
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	// Trim trailing empty lines.
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}
