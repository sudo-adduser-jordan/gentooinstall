package installer

import (
	"bufio"
	"fmt"
	"strings"
)

// AskYesNo prompts the user until a valid answer is given
// (port of ask). Empty input yields def. A non-interactive runner
// answers def without reading stdin.
func AskYesNo(r *Runner, question string, def bool) (bool, error) {
	suffix := "(Y/n)"
	if !def {
		suffix = "(y/N)"
	}
	if r.NonInteractive {
		ans := "no"
		if def {
			ans = "yes"
		}
		fmt.Fprintf(r.stderr(), "%s %s %s\n", question, suffix, ans)
		return def, nil
	}
	rd := bufio.NewReader(stdinOrReader(r.Stdin))
	for {
		fmt.Fprintf(r.stderr(), "%s %s ", question, suffix)
		line, err := rd.ReadString('\n')
		if err != nil && line == "" {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
	}
}

// PromptLine reads one line from stdin with a prompt.
func PromptLine(r *Runner, prompt string) (string, error) {
	fmt.Fprint(r.stderr(), prompt)
	rd := bufio.NewReader(stdinOrReader(r.Stdin))
	line, err := rd.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// InteractiveOnFailure builds an OnFailure handler asking the user for
// shell/retry/abort/continue/print (the try() prompt).
func InteractiveOnFailure(r *Runner) func(string, error) FailAction {
	return func(cmdline string, err error) FailAction {
		for {
			ans, e := PromptLine(r,
				"Specify next action ([1mS[mhell/[1mr[metry/[1ma[mbort/[1mc[montinue/[1mp[mrint) ")
			if e != nil && ans == "" {
				return FailAbort
			}
			switch strings.ToLower(strings.TrimSpace(ans)) {
			case "", "s", "shell":
				return FailShell
			case "r", "retry":
				return FailRetry
			case "a", "abort":
				return FailAbort
			case "c", "continue":
				return FailContinue
			case "p", "print":
				return FailPrint
			}
		}
	}
}
