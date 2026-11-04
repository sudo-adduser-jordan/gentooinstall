package assets

import (
	_ "embed"
)

//go:embed fstab
var Fstab string

//go:embed sshd_config
var SSHDConfig string

//go:embed i18n_supported
var I18NSupported string

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
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}
