package installer

import (
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const gentooReleaseKeyURL = "https://gentoo.org/.well-known/openpgpkey/hu/wtktzo4gyuhzu8a4z5fdj3fgmr1u6tob?l=releng"

// Stage3Info describes the resolved stage3 tarball.
type Stage3Info struct {
	Basename string // e.g. stage3-amd64-systemd-20240121T123456Z.tar.xz
	Path     string // absolute path in TmpDir
}

func httpDownload(r *Runner, url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %s while downloading %s", resp.Status, url)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		return err
	}
	r.logf("downloaded %s (%d bytes)", filepath.Base(dest), n)
	return nil
}

func httpGetBody(r *Runner, url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http %s while fetching %s", resp.Status, url)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// ResolveStage3 determines the current tarball filename from the mirror
// listing (port of download_stage3's listing parsing).
func ResolveStage3(c *Context) (Stage3Info, error) {
	basename := c.Cfg.Stage3BaseNameFinal()
	releasesURL := fmt.Sprintf("%s/releases/%s/autobuilds/current-%s/",
		c.Cfg.Gentoo.Mirror, c.Cfg.Gentoo.Arch, basename)

	c.R.logf("Fetching list of current tarballs from %s", releasesURL)
	body, err := httpGetBody(c.R, releasesURL)
	if err != nil {
		return Stage3Info{}, fmt.Errorf("could not retrieve list of tarballs: %w", err)
	}

	// Decode URL-encoded strings (parity with the python unquote step).
	if dec, err := urlUnescape(body); err == nil {
		body = dec
	}

	re := regexp.MustCompile(`"` + regexp.QuoteMeta(basename) + `-[0-9A-Z]*\.tar\.xz"`)
	set := map[string]bool{}
	for _, m := range re.FindAllString(body, -1) {
		set[strings.Trim(m, `"`)] = true
	}
	var names []string
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return Stage3Info{}, fmt.Errorf("could not parse list of tarballs for %s", basename)
	}
	name := names[0]
	return Stage3Info{
		Basename: name,
		Path:     filepath.Join(TmpDir, name),
	}, nil
}

// DownloadStage3 downloads and cryptographically verifies the stage3
// tarball, resuming via a .verified marker (port of download_stage3).
func DownloadStage3(c *Context) (Stage3Info, error) {
	info, err := ResolveStage3(c)
	if err != nil {
		return info, err
	}
	basename := info.Basename
	releasesURL := fmt.Sprintf("%s/releases/%s/autobuilds/current-%s/",
		c.Cfg.Gentoo.Mirror, c.Cfg.Gentoo.Arch, c.Cfg.Stage3BaseNameFinal())
	verifiedMarker := info.Path + ".verified"

	if _, err := os.Stat(verifiedMarker); err == nil {
		c.R.logf("%s tarball already downloaded and verified", basename)
		c.Stage3File = info.Path
		return info, nil
	}

	c.R.logf("Downloading %s tarball", basename)
	tarballURL := strings.TrimSuffix(releasesURL, "/") + "/" + basename
	if err := httpDownload(c.R, tarballURL, info.Path); err != nil {
		return info, fmt.Errorf("could not download %s: %w", basename, err)
	}
	digestsPath := info.Path + ".DIGESTS"
	if err := httpDownload(c.R, tarballURL+".DIGESTS", digestsPath); err != nil {
		return info, fmt.Errorf("could not download DIGESTS: %w", err)
	}

	c.R.log("Importing gentoo gpg key")
	keyPath := filepath.Join(TmpDir, "gentoo-keys.gpg")
	if err := httpDownload(c.R, gentooReleaseKeyURL, keyPath); err != nil {
		return info, fmt.Errorf("could not retrieve gentoo gpg key: %w", err)
	}
	if out, err := c.R.QuietRun("gpg", "--quiet", "--import", keyPath); err != nil {
		return info, fmt.Errorf("could not import gentoo gpg key:\n%s", out)
	}

	c.R.log("Verifying tarball signature")
	prevDir := c.R.Dir
	c.R.Dir = TmpDir
	_, err = c.R.QuietRun("gpg", "--quiet", "--verify", digestsPath)
	c.R.Dir = prevDir
	if err != nil {
		return info, fmt.Errorf("signature of '%s' invalid", filepath.Base(digestsPath))
	}

	c.R.log("Verifying tarball integrity")
	want, err := sha512FromDigests(digestsPath)
	if err != nil {
		return info, err
	}
	got, err := SHA512File(info.Path)
	if err != nil {
		return info, err
	}
	if !strings.EqualFold(want, got) {
		return info, fmt.Errorf("checksum mismatch!\n want %s\n got  %s", want, got)
	}

	if err := os.WriteFile(verifiedMarker, nil, 0o644); err != nil {
		return info, err
	}
	c.Stage3File = info.Path
	return info, nil
}

// sha512FromDigests extracts the SHA512 line for our tarball from a
// .DIGESTS file (grep 'tar.xz$' + sed 's/  .*stage3-/  stage3-/').
func sha512FromDigests(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasSuffix(line, ".tar.xz") && !strings.HasSuffix(line, ".tar.xz ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || len(fields[0]) != sha512.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			continue
		}
		return fields[0], nil
	}
	return "", fmt.Errorf("no SHA512 line found in %s", path)
}

// urlUnescape decodes percent-encodings without touching '+'.
func urlUnescape(s string) (string, error) {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi, ok1 := unhex(s[i+1])
			lo, ok2 := unhex(s[i+2])
			if ok1 && ok2 {
				sb.WriteByte(hi<<4 | lo)
				i += 2
				continue
			}
		}
		sb.WriteByte(s[i])
	}
	return sb.String(), nil
}

func unhex(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}
