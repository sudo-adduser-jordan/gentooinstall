package installer

import (
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const gentooReleaseKeyURL = "https://gentoo.org/.well-known/openpgpkey/hu/wtktzo4gyuhzu8a4z5fdj3fgmr1u6tob?l=releng"

const stage3DownloadAttempts = 3

const stage3RetryBackoff = time.Second

const stage3SpaceSlack = 64 * 1024 * 1024

var errNotPublished = errors.New("not published")

var errInsufficientSpace = errors.New("not enough free space on the target root filesystem")

type Stage3Info struct {
	Basename string
	Path     string
	Size     int64
}

const stage3MetaTimeout = 30 * time.Second

func httpClientFor(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		return http.DefaultClient
	}
	return &http.Client{Timeout: timeout}
}

func httpDownload(runner *Runner, url, dest string) error {
	return httpDownloadTimeout(runner, url, dest, 0)
}

func httpDownloadTimeout(runner *Runner, url, dest string, timeout time.Duration) error {
	resp, err := httpClientFor(timeout).Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %s while downloading %s", resp.Status, url)
	}
	file, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer file.Close()
	bytesCopied, err := io.Copy(file, resp.Body)
	if err != nil {
		_ = os.Remove(dest)
		return err
	}
	runner.logf("downloaded %s (%d bytes)", filepath.Base(dest), bytesCopied)
	return nil
}

func httpGetBody(runner *Runner, url string) (string, error) {
	resp, err := httpClientFor(stage3MetaTimeout).Get(url)
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

func resolveFromLatest(ctx *Context, releasesURL, basename string) (string, int64, error) {
	latestURL := strings.TrimSuffix(releasesURL, "/") + "/latest-" + basename + ".txt"
	ctx.Runner.logf("Fetching current tarball name from %s", latestURL)
	body, err := httpGetBody(ctx.Runner, latestURL)
	if errors.Is(err, errNotPublished) {
		ctx.Runner.logf("%s does not publish a latest-%s.txt listing; scanning the index", latestURL, basename)
		return "", 0, nil
	}
	if err != nil {
		return "", 0, fmt.Errorf("could not fetch current tarball name from %s: %w", latestURL, err)
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
		var size int64
		if len(fields) > 1 {
			if parsedSize, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
				size = parsedSize
			}
		}
		return fields[0], size, nil
	}
	return "", 0, fmt.Errorf("no tarball name found in %s", latestURL)
}

func resolveFromIndex(ctx *Context, releasesURL, basename string) (string, int64, error) {
	body, err := httpGetBody(ctx.Runner, releasesURL)
	if err != nil {
		return "", 0, fmt.Errorf("could not retrieve list of tarballs from %s: %w", releasesURL, err)
	}

	if dec, err := url.PathUnescape(body); err == nil {
		body = dec
	}

	re := regexp.MustCompile(`"` + regexp.QuoteMeta(basename) + `-[0-9A-Z]*\.tar\.xz"`)
	set := map[string]bool{}
	for _, match := range re.FindAllString(body, -1) {
		set[strings.Trim(match, `"`)] = true
	}
	var names []string
	for name := range set {
		names = append(names, name)
	}
	if len(names) == 0 {
		return "", 0, fmt.Errorf("could not parse list of tarballs for %s at %s", basename, releasesURL)
	}
	sort.Strings(names)
	return names[len(names)-1], 0, nil
}

func ResolveStage3(ctx *Context) (Stage3Info, error) {
	basename := ctx.Cfg.Stage3BaseNameFinal()
	releasesURL := fmt.Sprintf("%s/releases/%s/autobuilds/current-%s/",
		ctx.Cfg.Gentoo.Mirror, ctx.Cfg.Gentoo.Arch, basename)

	var (
		name string
		size int64
		err  error
	)
	ctx.Runner.logf("Fetching list of current tarballs from %s", releasesURL)
	if name, size, err = resolveFromLatest(ctx, releasesURL, basename); err != nil {
		return Stage3Info{}, err
	}
	if name == "" {
		if name, size, err = resolveFromIndex(ctx, releasesURL, basename); err != nil {
			return Stage3Info{}, err
		}
	}
	return Stage3Info{
		Basename: name,
		Path:     filepath.Join(Stage3ScratchDir, name),
		Size:     size,
	}, nil
}

func DownloadStage3(ctx *Context) (Stage3Info, error) {
	info, err := downloadStage3Once(ctx)
	if err == nil {
		return info, nil
	}
	if !isTransientFailure(err) {
		return info, err
	}
	for attempt := 1; attempt < stage3DownloadAttempts; attempt++ {
		ctx.Runner.logf("Stage3 download failed (%v); retrying (%d/%d)", err,
			attempt+1, stage3DownloadAttempts)
		time.Sleep(stage3RetryBackoff)
		if info, err = downloadStage3Once(ctx); err == nil {
			return info, nil
		}
		if !isTransientFailure(err) {
			break
		}
	}
	return info, err
}

func isTransientFailure(err error) bool {
	if errors.Is(err, syscall.ENOSPC) || errors.Is(err, errInsufficientSpace) {
		return false
	}
	return true
}

func checkStage3Space(ctx *Context, needed int64) error {
	free, err := freeSpaceBytes(ctx.path(Stage3ScratchDir))
	if err != nil {
		return nil
	}
	if free < needed+stage3SpaceSlack {
		return fmt.Errorf("%w: need at least %d bytes, only %d free",
			errInsufficientSpace, needed+stage3SpaceSlack, free)
	}
	return nil
}

func freeSpaceBytes(dir string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

func downloadStage3Once(ctx *Context) (Stage3Info, error) {
	info, err := ResolveStage3(ctx)
	if err != nil {
		return info, err
	}
	basename := info.Basename
	releasesURL := fmt.Sprintf("%s/releases/%s/autobuilds/current-%s/",
		ctx.Cfg.Gentoo.Mirror, ctx.Cfg.Gentoo.Arch, ctx.Cfg.Stage3BaseNameFinal())

	if err := ctx.mkdirAll(Stage3ScratchDir, 0o755); err != nil {
		return info, fmt.Errorf("could not create stage3 scratch dir: %w", err)
	}
	if info.Size > 0 {
		if err := checkStage3Space(ctx, info.Size); err != nil {
			return info, err
		}
	}

	dst := ctx.path(info.Path)
	verifiedMarker := dst + ".verified"

	if marker, err := os.ReadFile(verifiedMarker); err == nil &&
		strings.TrimSpace(string(marker)) == basename {
		if _, terr := os.Stat(dst); terr == nil {
			ctx.Runner.logf("%s tarball already downloaded and verified", basename)
			ctx.Stage3File = info.Path
			return info, nil
		}
	}
	_ = os.Remove(verifiedMarker)

	ctx.Runner.logf("Downloading %s tarball", basename)
	tarballURL := strings.TrimSuffix(releasesURL, "/") + "/" + basename
	if err := httpDownload(ctx.Runner, tarballURL, dst); err != nil {
		return info, fmt.Errorf("could not download %s: %w", basename, err)
	}
	digestsPath := dst + ".DIGESTS"
	if err := httpDownloadTimeout(ctx.Runner, tarballURL+".DIGESTS", digestsPath, stage3MetaTimeout); err != nil {
		return info, fmt.Errorf("could not download DIGESTS: %w", err)
	}

	ctx.Runner.log("Importing gentoo gpg key")
	keyPath := ctx.path(filepath.Join(TmpDir, "gentoo-keys.gpg"))
	if err := httpDownloadTimeout(ctx.Runner, gentooReleaseKeyURL, keyPath, stage3MetaTimeout); err != nil {
		return info, fmt.Errorf("could not retrieve gentoo gpg key: %w", err)
	}
	if out, err := ctx.Runner.QuietRun("gpg", "--quiet", "--import", keyPath); err != nil {
		return info, fmt.Errorf("could not import gentoo gpg key:\n%s", out)
	}

	ctx.Runner.log("Verifying tarball signature")
	prevDir := ctx.Runner.Dir
	ctx.Runner.Dir = TmpDir
	_, err = ctx.Runner.QuietRun("gpg", "--quiet", "--verify", digestsPath)
	ctx.Runner.Dir = prevDir
	if err != nil {
		return info, fmt.Errorf("signature of '%s' invalid", filepath.Base(digestsPath))
	}

	ctx.Runner.log("Verifying tarball integrity")
	want, err := sha512FromDigests(digestsPath, basename)
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

	if err := ctx.writeFile(info.Path+".verified", []byte(basename+"\n"), 0o644); err != nil {
		return info, err
	}
	ctx.Stage3File = info.Path
	return info, nil
}

var digestsSectionHeader = regexp.MustCompile(`^#\s*([0-9A-Za-z]+)\s*HASH\s*$`)

func sha512FromDigests(path, basename string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var fallback string
	section := ""
	seenMarker := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if match := digestsSectionHeader.FindStringSubmatch(line); match != nil {
			seenMarker = true
			section = strings.ToLower(match[1])
			continue
		}
		if section != "" && section != "sha512" {
			continue
		}
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
		if fallback == "" {
			fallback = fields[0]
		}
		name := fields[len(fields)-1]
		if name == basename || strings.HasSuffix(name, "/"+basename) {
			return fields[0], nil
		}
	}
	if fallback != "" && !seenMarker {
		return fallback, nil
	}
	return "", fmt.Errorf("no SHA512 line found in %s", path)
}
