package installer

import (
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const gentooReleaseKeyURL = "https://gentoo.org/.well-known/openpgpkey/hu/wtktzo4gyuhzu8a4z5fdj3fgmr1u6tob?l=releng"

// stage3DownloadAttempts is how many times DownloadStage3 tries to resolve
// and fetch the tarball (plus its DIGESTS and the gpg key) before giving up.
// Transient network faults, mirror hiccups and checksum slips are retried
// with a short backoff so a flaky connection does not fail the whole install.
const stage3DownloadAttempts = 3

// stage3RetryBackoff is the pause between download attempts.
const stage3RetryBackoff = time.Second

// errNotPublished is returned when a mirror answers 404, so callers can
// distinguish "this listing does not exist" from a network or server fault.
var errNotPublished = errors.New("not published")

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
		if resp.StatusCode == http.StatusNotFound {
			return "", fmt.Errorf("%s: %w", url, errNotPublished)
		}
		return "", fmt.Errorf("http %s while fetching %s", resp.Status, url)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// resolveFromLatest reads the authoritative "latest-<basename>.txt" listing
// that Gentoo publishes in every current-* directory (e.g.
// "stage3-amd64-systemd-20260830T151604Z.tar.xz 290005580"). It returns the
// current tarball name. A mirror that does not publish the file (404) yields
// ("", nil) so the caller can scan the HTML index instead; network or server
// errors are returned as-is so the failure is not silently masked.
func resolveFromLatest(c *Context, releasesURL, basename string) (string, error) {
	latestURL := strings.TrimSuffix(releasesURL, "/") + "/latest-" + basename + ".txt"
	c.R.logf("Fetching current tarball name from %s", latestURL)
	body, err := httpGetBody(c.R, latestURL)
	if errors.Is(err, errNotPublished) {
		c.R.logf("%s does not publish a latest-%s.txt listing; scanning the index", latestURL, basename)
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("could not fetch current tarball name from %s: %w", latestURL, err)
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 1 || !strings.HasSuffix(fields[0], ".tar.xz") {
			continue
		}
		return fields[0], nil
	}
	return "", fmt.Errorf("no tarball name found in %s", latestURL)
}

// resolveFromIndex scans the HTML index for tarball names and returns the
// newest one (parity with the bash listing parse, but preferring the newest
// build rather than the oldest).
func resolveFromIndex(c *Context, releasesURL, basename string) (string, error) {
	body, err := httpGetBody(c.R, releasesURL)
	if err != nil {
		return "", fmt.Errorf("could not retrieve list of tarballs from %s: %w", releasesURL, err)
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
	if len(names) == 0 {
		return "", fmt.Errorf("could not parse list of tarballs for %s at %s", basename, releasesURL)
	}
	sort.Strings(names)
	return names[len(names)-1], nil
}

// ResolveStage3 determines the current tarball filename from the mirror
// listing (port of download_stage3's listing parsing). It prefers the
// authoritative "latest-<basename>.txt" file Gentoo publishes, falling back
// to scanning the HTML index only when the .txt is absent (HTTP 404);
// network or server errors on the preferred listing fail the resolution so
// the real cause is not hidden.
func ResolveStage3(c *Context) (Stage3Info, error) {
	basename := c.Cfg.Stage3BaseNameFinal()
	releasesURL := fmt.Sprintf("%s/releases/%s/autobuilds/current-%s/",
		c.Cfg.Gentoo.Mirror, c.Cfg.Gentoo.Arch, basename)

	var (
		name string
		err  error
	)
	c.R.logf("Fetching list of current tarballs from %s", releasesURL)
	if name, err = resolveFromLatest(c, releasesURL, basename); err != nil {
		return Stage3Info{}, err
	}
	if name == "" {
		if name, err = resolveFromIndex(c, releasesURL, basename); err != nil {
			return Stage3Info{}, err
		}
	}
	return Stage3Info{
		Basename: name,
		Path:     filepath.Join(TmpDir, name),
	}, nil
}

// DownloadStage3 downloads and cryptographically verifies the stage3
// tarball, resuming via a .verified marker (port of download_stage3). The
// whole resolve+fetch+verify pipeline is retried a few times with a short
// backoff so transient network or mirror failures do not abort the install;
// each attempt re-resolves the tarball name and re-fetches everything.
func DownloadStage3(c *Context) (Stage3Info, error) {
	info, err := downloadStage3Once(c)
	if err == nil {
		return info, nil
	}
	for attempt := 1; attempt < stage3DownloadAttempts; attempt++ {
		c.R.logf("Stage3 download failed (%v); retrying (%d/%d)", err,
			attempt+1, stage3DownloadAttempts)
		time.Sleep(stage3RetryBackoff)
		if info, err = downloadStage3Once(c); err == nil {
			return info, nil
		}
	}
	return info, err
}

// downloadStage3Once performs a single resolve + download + verify pass.
func downloadStage3Once(c *Context) (Stage3Info, error) {
	info, err := ResolveStage3(c)
	if err != nil {
		return info, err
	}
	basename := info.Basename
	releasesURL := fmt.Sprintf("%s/releases/%s/autobuilds/current-%s/",
		c.Cfg.Gentoo.Mirror, c.Cfg.Gentoo.Arch, c.Cfg.Stage3BaseNameFinal())

	// File operations below resolve against the context root; the logical
	// TmpDir paths are kept for c.Stage3File and the gpg working directory.
	dst := c.path(info.Path)
	verifiedMarker := dst + ".verified"

	if _, err := os.Stat(verifiedMarker); err == nil {
		c.R.logf("%s tarball already downloaded and verified", basename)
		c.Stage3File = info.Path
		return info, nil
	}

	c.R.logf("Downloading %s tarball", basename)
	tarballURL := strings.TrimSuffix(releasesURL, "/") + "/" + basename
	if err := httpDownload(c.R, tarballURL, dst); err != nil {
		return info, fmt.Errorf("could not download %s: %w", basename, err)
	}
	digestsPath := dst + ".DIGESTS"
	if err := httpDownload(c.R, tarballURL+".DIGESTS", digestsPath); err != nil {
		return info, fmt.Errorf("could not download DIGESTS: %w", err)
	}

	c.R.log("Importing gentoo gpg key")
	keyPath := c.path(filepath.Join(TmpDir, "gentoo-keys.gpg"))
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
	got, err := SHA512File(dst)
	if err != nil {
		return info, err
	}
	if !strings.EqualFold(want, got) {
		return info, fmt.Errorf("checksum mismatch!\n want %s\n got  %s", want, got)
	}

	if err := c.writeFile(info.Path+".verified", nil, 0o644); err != nil {
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
