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

// WantedPrograms returns the host programs required for cfg. Only programs
// the Go engine actually shells out to are listed: downloads (http), sha512
// digests and guid generation are implemented in Go, so wget/sha512sum/
// uuidgen/python3 from the bash port are deliberately absent.
func WantedPrograms(ctx *Context) (required, wanted []string) {
	required = []string{"gpg", "hwclock", "lsblk", "ntpd", "partprobe",
		"sgdisk"}
	wanted = []string{}
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
	if !HasProgram("rhash") {
		wanted = append(wanted, "rhash") // optional, sha512sum suffices
	}
	return required, wanted
}

// CheckPrograms verifies all required programs are present.
func CheckPrograms(ctx *Context) error {
	req, want := WantedPrograms(ctx)
	var missing []string
	for _, program := range req {
		if !HasProgram(program) {
			missing = append(missing, program)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required programs: %s", strings.Join(missing, " "))
	}
	if len(want) > 0 {
		var mw []string
		for _, program := range want {
			if !HasProgram(program) {
				mw = append(mw, program)
			}
		}
		if len(mw) > 0 {
			ctx.Runner.logf("Missing optional programs: %s", strings.Join(mw, " "))
		}
	}
	return nil
}

// SyncTime synchronizes the system clock (port of sync_time).
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
		// Fall back to the Date header of a plain http request, bounded
		// so a blackholed network cannot hang the install forever.
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

// PrepareEnvironment checks programs and syncs the clock
// (port of prepare_installation_environment).
func PrepareEnvironment(ctx *Context) error {
	ctx.Runner.log("Preparing installation environment")
	if err := CheckPrograms(ctx); err != nil {
		return err
	}
	return SyncTime(ctx)
}

// EncryptionKeyEnv is read before prompting.
const EncryptionKeyEnv = "GENTOO_INSTALL_ENCRYPTION_KEY"

// EnsureEncryptionKey resolves the luks/zfs key from env or interactive
// prompt (port of check_encryption_key).
func EnsureEncryptionKey(ctx *Context, stdin io.Reader) error {
	if !ctx.Layout.Flags.UsedEncryption {
		return nil
	}
	key := os.Getenv(EncryptionKeyEnv)
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
	// Terminal echo suppression requires term handling; for the live ISO
	// context we accept plain input like the bash fallback path did.
	fmt.Print(prompt)
	line, err := rd.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// SHA512File hex-digests a file without loading it into memory.
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
