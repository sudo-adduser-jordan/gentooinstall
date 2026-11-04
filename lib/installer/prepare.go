package installer

import (
	"bufio"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func WantedPrograms(ctx *Context) (required, wanted []string) {
	required = []string{"gpg", "hwclock", "lsblk", "ntpd", "partprobe",
		"sgdisk"}
	wanted = []string{}
	if ctx.Layout != nil {
		if ctx.Layout.Flags.UsedBtrfs {
			required = append(required, "btrfs")
		}
		if ctx.Layout.Flags.UsedZFS {
			required = append(required, "zfs")
		}
		if ctx.Layout.Flags.UsedRaid {
			required = append(required, "mdadm")
		}
		if ctx.Layout.Flags.UsedLuks {
			required = append(required, "cryptsetup")
		}
	}
	if !HasProgram("rhash") {
		wanted = append(wanted, "rhash")
	}
	return required, wanted
}

func MissingPrograms(ctx *Context) []string {
	required, _ := WantedPrograms(ctx)
	var missing []string
	for _, program := range required {
		if !ctx.Runner.HasProgram(program) {
			missing = append(missing, program)
		}
	}
	return missing
}

func CheckPrograms(ctx *Context) error {
	if missing := MissingPrograms(ctx); len(missing) > 0 {
		return fmt.Errorf("missing required programs: %s", strings.Join(missing, " "))
	}
	_, want := WantedPrograms(ctx)
	if len(want) > 0 {
		var mw []string
		for _, program := range want {
			if !ctx.Runner.HasProgram(program) {
				mw = append(mw, program)
			}
		}
		if len(mw) > 0 {
			ctx.Runner.logf("Missing optional programs: %s", strings.Join(mw, " "))
		}
	}
	return nil
}

var apkPackages = map[string]string{
	"gpg":        "gnupg",
	"hwclock":    "busybox",
	"lsblk":      "util-linux",
	"ntpd":       "ntp",
	"partprobe":  "parted",
	"btrfs":      "btrfs-progs",
	"zfs":        "zfs",
	"mdadm":      "mdadm",
	"cryptsetup": "cryptsetup",
	"rhash":      "rhash",
}

func ProgramPackages(programs []string) []string {
	packages := make([]string, len(programs))
	for index, program := range programs {
		if pkg, ok := apkPackages[program]; ok {
			packages[index] = pkg
		} else {
			packages[index] = program
		}
	}
	return packages
}

func InstallProgramsCmdline(programs []string) string {
	return "apk " + CommandLine("add", append([]string{"--no-cache"}, ProgramPackages(programs)...)...)
}

func InstallMissingPrograms(ctx *Context) error {
	missing := MissingPrograms(ctx)
	if len(missing) == 0 {
		return nil
	}
	if !ctx.Runner.HasProgram("apk") {
		return fmt.Errorf("no apk found to install missing programs; install the required "+
			"packages on this system yourself: %s", strings.Join(ProgramPackages(missing), " "))
	}
	ctx.Runner.logf("Installing missing programs: %s", InstallProgramsCmdline(missing))
	args := append([]string{"add", "--no-cache"}, ProgramPackages(missing)...)
	if err := ctx.Runner.Try("apk", args...); err != nil {
		return err
	}
	if still := MissingPrograms(ctx); len(still) > 0 {
		return fmt.Errorf("still missing required programs: %s",
			strings.Join(still, " "))
	}
	return nil
}

func SyncTime(ctx *Context) error {
	ctx.Runner.log("Syncing time")
	switch {
	case HasProgram("ntpd"):
		if err := ctx.Runner.Try("ntpd", "-g", "-q"); err != nil {
			return err
		}
	case HasProgram("chronyd"):
		if err := ctx.Runner.Try("chronyd", "-q"); err != nil {
			return err
		}
	default:
		httpClient := &http.Client{Timeout: 15 * time.Second}
		resp, err := httpClient.Head("http://example.com")
		if err != nil {
			return fmt.Errorf("could not fetch time over http: %w", err)
		}
		defer resp.Body.Close()
		date := resp.Header.Get("Date")
		if date == "" {
			return fmt.Errorf("no Date header received")
		}
		if err := ctx.Runner.Try("date", "-s", date); err != nil {
			return err
		}
	}
	out, _ := ctx.Runner.QuietRun("date")
	ctx.Runner.logf("Current date: %s", out)
	ctx.Runner.log("Writing time to hardware clock")
	return ctx.Runner.Try("hwclock", "--systohc", "--utc")
}

func PrepareEnvironment(ctx *Context) error {
	ctx.Runner.log("Preparing installation environment")
	if err := CheckPrograms(ctx); err != nil {
		return err
	}
	return SyncTime(ctx)
}

const EncryptionKeyEnv = "GENTOO_INSTALL_ENCRYPTION_KEY"

func EnsureEncryptionKey(ctx *Context, stdin io.Reader) error {
	if !ctx.Layout.Flags.UsedEncryption {
		return nil
	}
	key := os.Getenv(EncryptionKeyEnv)
	if key == "" && ctx.Runner.NonInteractive {
		return fmt.Errorf("encryption is enabled but no key was supplied; set %s (or gentooinstall.install-key= on the kernel command line)", EncryptionKeyEnv)
	}
	if key == "" {
		fmt.Fprintln(ctx.Runner.stderr(),
			"[+] You have enabled encryption, but haven't specified a key in the environment variable "+EncryptionKeyEnv+".")
		ok, err := AskYesNo(ctx.Runner, "Do you want to enter an encryption key now?", true)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("please export %s with the desired key", EncryptionKeyEnv)
		}

		rd := bufio.NewReader(stdinOrReader(stdin))
		for {
			key, err = readSecret(rd, "Enter encryption key: ")
			if err != nil {
				return err
			}
			if len(key) < 8 {
				fmt.Fprintln(ctx.Runner.stderr(), "[!] Your encryption key must be at least 8 characters long.")
				continue
			}
			again, err := readSecret(rd, "Repeat encryption key: ")
			if err != nil {
				return err
			}
			if again != key {
				fmt.Fprintln(ctx.Runner.stderr(), "[!] Encryption keys mismatch.")
				continue
			}
			break
		}
		_ = os.Setenv(EncryptionKeyEnv, key)
	}
	if len(key) < 8 {
		return fmt.Errorf("your encryption key must be at least 8 characters long")
	}
	ctx.EncryptionKey = key
	return nil
}

func stdinOrReader(reader io.Reader) io.Reader {
	if reader != nil {
		return reader
	}
	return os.Stdin
}

func readSecret(rd *bufio.Reader, prompt string) (string, error) {
	fmt.Print(prompt)
	line, err := rd.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func SHA512File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha512.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
