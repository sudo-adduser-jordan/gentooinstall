// Stage3 resolution, download, resume and verification.
package tests

import (
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gentooinstall/lib/installer"
)

func TestSHA512File(testingT *testing.T) {
	content := "stage3 payload\n"
	path := filepath.Join(testingT.TempDir(), "payload")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		testingT.Fatal(err)
	}
	sum := sha512.Sum512([]byte(content))
	got, err := installer.SHA512File(path)
	if err != nil {
		testingT.Fatal(err)
	}
	if got != hex.EncodeToString(sum[:]) {
		testingT.Fatalf("SHA512File = %s", got)
	}
}

func TestWantedPrograms(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, _ := testContext(testingT, cfg, nil)

	req, _ := installer.WantedPrograms(ctx)
	wantReq := []string{"gpg", "hwclock", "lsblk", "ntpd", "partprobe", "sgdisk"}
	if fmt.Sprint(req) != fmt.Sprint(wantReq) {
		testingT.Fatalf("required programs = %v, want %v", req, wantReq)
	}
	// rhash is optional (sha512sum suffices); it may never be required.
	for _, prog := range req {
		if prog == "rhash" {
			testingT.Fatalf("rhash must stay optional, got required: %v", req)
		}
	}
}

func TestSHA512FileMissing(testingT *testing.T) {
	if _, err := installer.SHA512File(filepath.Join(testingT.TempDir(), "nope")); err == nil {
		testingT.Fatal("expected error for missing file")
	}
}

func TestResolveStage3(testingT *testing.T) {
	var hits int
	ts := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits++
		switch {
		case strings.HasSuffix(request.URL.Path, "latest-stage3-amd64-systemd.txt"):
			fmt.Fprint(writer, "# Latest as of now\nstage3-amd64-systemd-20240121T123456Z.tar.xz 123456\n")
		case strings.HasSuffix(request.URL.Path, "/"):
			fmt.Fprintf(writer, "%s",
				`<a href="stage3-amd64-systemd-20240121T120000Z.tar.xz">`+
					`<a href="stage3-amd64-systemd-20240121T123456Z.tar.xz">`+
					`<a href="stage3%20with%25es%2Finside">`+
					`<a href="other-artifact">`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer ts.Close()

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = ts.URL
	ctx, _ := testContext(testingT, cfg, nil)

	info, err := installer.ResolveStage3(ctx)
	if err != nil {
		testingT.Fatal(err)
	}
	if hits != 1 {
		testingT.Fatalf("expected 1 listing request, got %d", hits)
	}
	// The authoritative latest-*.txt names the newest tarball, even though
	// the index also lists an older one.
	if info.Basename != "stage3-amd64-systemd-20240121T123456Z.tar.xz" {
		testingT.Fatalf("ResolveStage3 = %q (want name from latest-*.txt)", info.Basename)
	}
	if info.Path != "/tmp/gentoo-install/root/.gentoo-stage3/"+info.Basename {
		testingT.Fatalf("ResolveStage3 path = %q", info.Path)
	}
}

func TestResolveStage3FallsBackToIndex(testingT *testing.T) {
	var hits int
	ts := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits++
		switch {
		case strings.HasSuffix(request.URL.Path, "latest-stage3-amd64-systemd.txt"):
			http.NotFound(writer, request)
		case strings.HasSuffix(request.URL.Path, "/"):
			fmt.Fprint(writer,
				`<a href="stage3-amd64-systemd-20240121T120000Z.tar.xz">`+
					`<a href="stage3-amd64-systemd-20240121T123456Z.tar.xz">`+
					`<a href="other-artifact">`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer ts.Close()

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = ts.URL
	ctx, _ := testContext(testingT, cfg, nil)

	info, err := installer.ResolveStage3(ctx)
	if err != nil {
		testingT.Fatal(err)
	}
	if hits != 2 {
		testingT.Fatalf("expected 2 requests (.txt then index), got %d", hits)
	}
	// Fallback picks the newest tarball from the index.
	if info.Basename != "stage3-amd64-systemd-20240121T123456Z.tar.xz" {
		testingT.Fatalf("ResolveStage3 fallback = %q", info.Basename)
	}
}

func TestResolveStage3NoMatch(testingT *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "latest-stage3-amd64-systemd.txt") {
			http.NotFound(writer, request)
			return
		}
		fmt.Fprint(writer, "<a href=\"bogus-file\">")
	}))
	defer ts.Close()

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = ts.URL
	ctx, _ := testContext(testingT, cfg, nil)

	if _, err := installer.ResolveStage3(ctx); err == nil {
		testingT.Fatal("expected parse error for empty listing")
	}
}

func TestResolveStage3SurfacesLatestErrors(testingT *testing.T) {
	// A server error (not a 404) on the authoritative latest-*.txt listing
	// must fail with the real cause instead of silently falling back to the
	// index the way an absent listing (404) legitimately does.
	var hits int
	ts := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits++
		if strings.HasSuffix(request.URL.Path, "latest-stage3-amd64-systemd.txt") {
			http.Error(writer, "boom", http.StatusInternalServerError)
			return
		}
		http.NotFound(writer, request)
	}))
	defer ts.Close()

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = ts.URL
	ctx, _ := testContext(testingT, cfg, nil)

	_, err := installer.ResolveStage3(ctx)
	if err == nil {
		testingT.Fatal("expected error for failing latest listing")
	}
	if !strings.Contains(err.Error(), "latest-stage3-amd64-systemd.txt") {
		testingT.Fatalf("error should name the listing URL: %v", err)
	}
	if hits != 1 {
		testingT.Fatalf("expected no index fallback after a real error, got %d requests", hits)
	}
}

// stage3Mirror serves the tarball, its DIGESTS file and the release gpg key,
// recording request counts.
type stage3Mirror struct {
	ts      *httptest.Server
	mu      sync.Mutex
	hits    map[string]int
	payload []byte
	hash    string
	// failTarball rejects the next failTarballN requests for the tarball
	// (before serving them) with an HTTP error, so retries kick in.
	failTarballN int
	// latestSize is the published byte size written into the latest-*.txt
	// listing; the download space pre-check compares it with free space.
	latestSize int64
	// digestsFirstIrrelevant serves the DIGESTS file with a valid-looking
	// but foreign .tar.xz line first, so tests can prove the verifier
	// picks the line naming our tarball instead of the first match.
	digestsFirstIrrelevant bool
	// digestsBlake2bFirst serves the current upstream layout: a "# BLAKE2B
	// HASH" section (128-hex, same length as SHA512) naming our tarball
	// before the real "# SHA512 HASH" section. Tests prove the verifier
	// honors the section markers instead of returning the BLAKE2B digest.
	digestsBlake2bFirst bool
	// digestsBlake2bOnly serves a DIGESTS file with only a "# BLAKE2B
	// HASH" section (no SHA512 at all), so tests can prove a listing
	// without any SHA512 digest is rejected.
	digestsBlake2bOnly bool
}

func newStage3Mirror(testingT *testing.T, payload []byte) *stage3Mirror {
	testingT.Helper()
	mirror := &stage3Mirror{payload: payload, hits: map[string]int{}, latestSize: 123456}
	sum := sha512.Sum512(payload)
	mirror.hash = hex.EncodeToString(sum[:])
	basename := "stage3-amd64-systemd-20240121T123456Z.tar.xz"
	mirror.ts = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mirror.mu.Lock()
		mirror.hits[request.URL.Path]++
		mirror.mu.Unlock()
		switch {
		case strings.HasSuffix(request.URL.Path, basename+".DIGESTS"):
			ours := mirror.hash + "  " + basename + "\n"
			foreign := strings.Repeat("0", 128) + "  irrelevant.tar.xz\n"
			switch {
			case mirror.digestsBlake2bFirst || mirror.digestsBlake2bOnly:
				blake2b := strings.Repeat("a", 128) + "  " + basename + "\n"
				if mirror.digestsBlake2bOnly {
					fmt.Fprint(writer, "# BLAKE2B HASH\n"+blake2b)
					break
				}
				fmt.Fprint(writer, "# BLAKE2B HASH\n"+blake2b+"# SHA512 HASH\n"+ours+foreign)
			case mirror.digestsFirstIrrelevant:
				fmt.Fprint(writer, foreign+ours)
			default:
				fmt.Fprint(writer, ours+foreign)
			}
		case strings.HasSuffix(request.URL.Path, basename):
			mirror.mu.Lock()
			if mirror.failTarballN > 0 {
				mirror.failTarballN--
				mirror.mu.Unlock()
				http.Error(writer, "transient failure", http.StatusInternalServerError)
				return
			}
			writer.Write(mirror.payload)
			mirror.mu.Unlock()
		case strings.HasSuffix(request.URL.Path, "latest-stage3-amd64-systemd.txt"):
			mirror.mu.Lock()
			fmt.Fprintf(writer, "# Latest\n%s %d\n", basename, mirror.latestSize)
			mirror.mu.Unlock()
		case strings.HasSuffix(request.URL.Path, "/"):
			fmt.Fprintf(writer, `"<a href="%s">`, basename)
		case strings.Contains(request.URL.Path, "releng") || strings.Contains(request.URL.Path, "openpgpkey"):
			fmt.Fprintln(writer, "-----BEGIN PGP PUBLIC KEY BLOCK-----")
		default:
			http.NotFound(writer, request)
		}
	}))
	return mirror
}

func (mirror *stage3Mirror) count(sub string) int {
	mirror.mu.Lock()
	defer mirror.mu.Unlock()
	for path, hits := range mirror.hits {
		if strings.Contains(path, sub) {
			return hits
		}
	}
	return 0
}

// countTarball returns the number of requests for exactly the tarball path
// (the .DIGESTS sibling ends in basename+".DIGESTS", so a substring match
// against basename would count it too).
func (mirror *stage3Mirror) countTarball(basename string) int {
	mirror.mu.Lock()
	defer mirror.mu.Unlock()
	total := 0
	for path, hits := range mirror.hits {
		if strings.HasSuffix(path, basename) && !strings.HasSuffix(path, basename+".DIGESTS") {
			total += hits
		}
	}
	return total
}

func TestDownloadStage3(testingT *testing.T) {
	payload := []byte("the tarball bytes")
	mirror := newStage3Mirror(testingT, payload)
	defer mirror.ts.Close()

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = mirror.ts.URL
	ctx, stub := testContext(testingT, cfg, nil)
	mkScratchDir(testingT, ctx, "/tmp/gentoo-install")

	info, err := installer.DownloadStage3(ctx)
	if err != nil {
		testingT.Fatal(err)
	}
	if info.Basename != "stage3-amd64-systemd-20240121T123456Z.tar.xz" {
		testingT.Fatalf("basename = %q", info.Basename)
	}
	if ctx.Stage3File != info.Path {
		testingT.Fatalf("c.Stage3File = %q", ctx.Stage3File)
	}
	stored := readScratch(testingT, ctx, info.Path)
	if stored != string(payload) {
		testingT.Fatalf("stored tarball = %q", stored)
	}
	if _, err := os.Stat(filepath.Join(ctx.Root, info.Path+".verified")); err != nil {
		testingT.Fatalf("verified marker missing: %v", err)
	}
	assertCmdContains(testingT, stub, []string{
		"gpg --quiet --import " + filepath.Join(ctx.Root, "/tmp/gentoo-install/gentoo-keys.gpg"),
		"gpg --quiet --verify " + filepath.Join(ctx.Root, info.Path+".DIGESTS"),
	})
}

func TestDownloadStage3ResumesFromVerifiedMarker(testingT *testing.T) {
	payload := []byte("the tarball bytes")
	mirror := newStage3Mirror(testingT, payload)
	defer mirror.ts.Close()

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = mirror.ts.URL
	ctx, _ := testContext(testingT, cfg, nil)
	mkScratchDir(testingT, ctx, "/tmp/gentoo-install/root/.gentoo-stage3")
	path := "/tmp/gentoo-install/root/.gentoo-stage3/stage3-amd64-systemd-20240121T123456Z.tar.xz"
	// A resume needs the verified tarball the marker was written for.
	writeScratch(testingT, ctx, path, string(payload))
	writeScratch(testingT, ctx, path+".verified",
		"stage3-amd64-systemd-20240121T123456Z.tar.xz\n")

	info, err := installer.DownloadStage3(ctx)
	if err != nil {
		testingT.Fatal(err)
	}
	if info.Basename != "stage3-amd64-systemd-20240121T123456Z.tar.xz" {
		testingT.Fatalf("basename = %q", info.Basename)
	}
	if mirror.count("current-stage3-amd64-systemd") != 1 {
		testingT.Fatal("listing must be resolved once even when resuming")
	}
	if mirror.count("DIGESTS") != 0 {
		testingT.Fatal("resume path must not fetch DIGESTS")
	}
	for path := range mirror.hits {
		if strings.HasSuffix(path, ".tar.xz") {
			testingT.Fatalf("resume path must not re-download tarball, hit %s", path)
		}
	}
}

func TestDownloadStage3IgnoresStaleMarker(testingT *testing.T) {
	payload := []byte("the tarball bytes")
	mirror := newStage3Mirror(testingT, payload)
	defer mirror.ts.Close()

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = mirror.ts.URL

	testingT.Run("marker without tarball re-downloads", func(testingT *testing.T) {
		ctx, _ := testContext(testingT, cfg, nil)
		mkScratchDir(testingT, ctx, "/tmp/gentoo-install/root/.gentoo-stage3")
		path := "/tmp/gentoo-install/root/.gentoo-stage3/stage3-amd64-systemd-20240121T123456Z.tar.xz"
		writeScratch(testingT, ctx, path+".verified",
			"stage3-amd64-systemd-20240121T123456Z.tar.xz\n")

		if _, err := installer.DownloadStage3(ctx); err != nil {
			testingT.Fatal(err)
		}
		if got := mirror.countTarball("stage3-amd64-systemd-20240121T123456Z.tar.xz"); got == 0 {
			testingT.Fatal("stale marker must trigger a re-download")
		}
		if got := readScratch(testingT, ctx, path); got != string(payload) {
			testingT.Fatalf("re-downloaded tarball = %q", got)
		}
	})

	testingT.Run("marker for another basename re-downloads", func(testingT *testing.T) {
		ctx, _ := testContext(testingT, cfg, nil)
		mkScratchDir(testingT, ctx, "/tmp/gentoo-install/root/.gentoo-stage3")
		path := "/tmp/gentoo-install/root/.gentoo-stage3/stage3-amd64-systemd-20240121T123456Z.tar.xz"
		writeScratch(testingT, ctx, path, string(payload))
		writeScratch(testingT, ctx, path+".verified", "stage3-amd64-systemd-OLDER.tar.xz\n")

		before := mirror.countTarball("stage3-amd64-systemd-20240121T123456Z.tar.xz")
		if _, err := installer.DownloadStage3(ctx); err != nil {
			testingT.Fatal(err)
		}
		if got := mirror.countTarball("stage3-amd64-systemd-20240121T123456Z.tar.xz"); got == before {
			testingT.Fatal("foreign marker must trigger a re-download")
		}
	})
}

func TestDownloadStage3ChecksumMismatch(testingT *testing.T) {
	payload := []byte("different bytes than digest")
	mirror := newStage3Mirror(testingT, payload)
	defer mirror.ts.Close()
	// Corrupt the served tarball after computing the digests.
	mirror.mu.Lock()
	mirror.payload = []byte("tampered")
	mirror.mu.Unlock()

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = mirror.ts.URL
	ctx, _ := testContext(testingT, cfg, nil)
	mkScratchDir(testingT, ctx, "/tmp/gentoo-install")

	if _, err := installer.DownloadStage3(ctx); err == nil {
		testingT.Fatal("expected checksum mismatch error")
	}
}

func TestDownloadStage3PrefersBasenameDigest(testingT *testing.T) {
	payload := []byte("the tarball bytes")
	mirror := newStage3Mirror(testingT, payload)
	defer mirror.ts.Close()
	mirror.digestsFirstIrrelevant = true

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = mirror.ts.URL
	ctx, _ := testContext(testingT, cfg, nil)
	mkScratchDir(testingT, ctx, "/tmp/gentoo-install")

	// The first .tar.xz line names a foreign file; the verifier must use
	// the line naming our tarball instead of failing the checksum.
	if _, err := installer.DownloadStage3(ctx); err != nil {
		testingT.Fatalf("DownloadStage3 with reordered DIGESTS: %v", err)
	}
}

func TestDownloadStage3SkipsBlake2bDigests(testingT *testing.T) {
	payload := []byte("the tarball bytes")
	mirror := newStage3Mirror(testingT, payload)
	defer mirror.ts.Close()
	mirror.digestsBlake2bFirst = true

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = mirror.ts.URL
	ctx, stub := testContext(testingT, cfg, nil)
	mkScratchDir(testingT, ctx, "/tmp/gentoo-install")

	// Current upstream DIGESTS files list a 128-hex BLAKE2B digest before
	// the SHA512 digest for the same tarball; the verifier must pick the
	// SHA512 section instead of failing the checksum against the BLAKE2B
	// value (same length, same basename).
	info, err := installer.DownloadStage3(ctx)
	if err != nil {
		testingT.Fatalf("DownloadStage3 with BLAKE2B-first DIGESTS: %v", err)
	}
	if got := readScratch(testingT, ctx, info.Path); got != string(payload) {
		testingT.Fatalf("stored tarball = %q", got)
	}
	assertCmdContains(testingT, stub, []string{
		"gpg --quiet --verify " + filepath.Join(ctx.Root, info.Path+".DIGESTS"),
	})
}

func TestDownloadStage3RejectsBlake2bOnlyDigests(testingT *testing.T) {
	payload := []byte("the tarball bytes")
	mirror := newStage3Mirror(testingT, payload)
	defer mirror.ts.Close()
	mirror.digestsBlake2bOnly = true

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = mirror.ts.URL
	ctx, _ := testContext(testingT, cfg, nil)
	mkScratchDir(testingT, ctx, "/tmp/gentoo-install")

	// A listing with only a BLAKE2B section carries no SHA512 digest, so the
	// verification must fail rather than trust a foreign algorithm's hash.
	_, err := installer.DownloadStage3(ctx)
	if err == nil {
		testingT.Fatal("expected failure with BLAKE2B-only DIGESTS")
	}
	if !strings.Contains(err.Error(), "no SHA512 line found") {
		testingT.Fatalf("unexpected error: %v", err)
	}
}

func TestDownloadStage3RetriesTransientFailures(testingT *testing.T) {
	payload := []byte("the tarball bytes")
	mirror := newStage3Mirror(testingT, payload)
	defer mirror.ts.Close()
	mirror.mu.Lock()
	mirror.failTarballN = 2 // first two tarball fetches fail with a 500
	mirror.mu.Unlock()

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = mirror.ts.URL
	ctx, stub := testContext(testingT, cfg, nil)
	mkScratchDir(testingT, ctx, "/tmp/gentoo-install")

	info, err := installer.DownloadStage3(ctx)
	if err != nil {
		testingT.Fatalf("DownloadStage3 should survive transient failures: %v", err)
	}
	if info.Basename != "stage3-amd64-systemd-20240121T123456Z.tar.xz" {
		testingT.Fatalf("basename = %q", info.Basename)
	}
	if got := mirror.countTarball(info.Basename); got < 3 {
		testingT.Fatalf("tarball fetched %d times, want 3 (2 failures + success)", got)
	}
	stored := readScratch(testingT, ctx, info.Path)
	if stored != string(payload) {
		testingT.Fatalf("stored tarball = %q, want payload", stored)
	}
	assertCmdContains(testingT, stub, []string{
		"gpg --quiet --import " + filepath.Join(ctx.Root, "/tmp/gentoo-install/gentoo-keys.gpg"),
		"gpg --quiet --verify " + filepath.Join(ctx.Root, info.Path+".DIGESTS"),
	})
}

func TestDownloadStage3FailsFastOnNoSpace(testingT *testing.T) {
	payload := []byte("the tarball bytes")
	mirror := newStage3Mirror(testingT, payload)
	defer mirror.ts.Close()
	// Lie about the published size: the target root filesystem in the test
	// has nowhere near this much free space, so the pre-download check must
	// fail fast without touching the tarball.
	mirror.mu.Lock()
	mirror.latestSize = 1 << 49
	mirror.mu.Unlock()

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = mirror.ts.URL
	ctx, _ := testContext(testingT, cfg, nil)
	mkScratchDir(testingT, ctx, "/tmp/gentoo-install")

	_, err := installer.DownloadStage3(ctx)
	if err == nil {
		testingT.Fatal("expected insufficient-space error")
	}
	if !strings.Contains(err.Error(), "not enough free space") {
		testingT.Fatalf("expected space error, got %v", err)
	}
	if got := mirror.countTarball("stage3-amd64-systemd-20240121T123456Z.tar.xz"); got != 0 {
		testingT.Fatalf("tarball fetched %d times; a space failure must not download", got)
	}
	if got := mirror.count("DIGESTS"); got != 0 {
		testingT.Fatalf("DIGESTS fetched %d times; a space failure must not download", got)
	}
}

func TestClearRoot(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Disk.UseSwap = false
	ctx, _ := testContext(testingT, cfg, nil)
	mkScratchDir(testingT, ctx, "/tmp/gentoo-install/root")
	writeScratch(testingT, ctx, "/tmp/gentoo-install/root/etc/os-release", "id=gentoo\n")
	writeScratch(testingT, ctx, "/tmp/gentoo-install/root/partial", "x")
	mkScratchDir(testingT, ctx, "/tmp/gentoo-install/root/lost+found")
	mkScratchDir(testingT, ctx, "/tmp/gentoo-install/root/.gentoo-stage3")
	writeScratch(testingT, ctx, "/tmp/gentoo-install/root/.gentoo-stage3/stage3.tar.xz", "tarball")

	if err := installer.ClearRoot(ctx); err != nil {
		testingT.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ctx.Root, "/tmp/gentoo-install/root/etc")); !os.IsNotExist(err) {
		testingT.Fatalf("etc must be cleared after ClearRoot (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(ctx.Root, "/tmp/gentoo-install/root/partial")); !os.IsNotExist(err) {
		testingT.Fatalf("partial must be cleared after ClearRoot (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(ctx.Root, "/tmp/gentoo-install/root/lost+found")); err != nil {
		testingT.Fatalf("lost+found must survive ClearRoot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ctx.Root, "/tmp/gentoo-install/root/.gentoo-stage3/stage3.tar.xz")); err != nil {
		testingT.Fatalf("staged tarball must survive ClearRoot so a re-extract can reuse it: %v", err)
	}
}

func TestClearRootEmpty(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Disk.UseSwap = false
	ctx, _ := testContext(testingT, cfg, nil)
	mkScratchDir(testingT, ctx, "/tmp/gentoo-install/root")

	if err := installer.ClearRoot(ctx); err != nil {
		testingT.Fatal(err)
	}
}

func TestExtractStage3(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Disk.UseSwap = false
	ctx, stub := testContext(testingT, cfg, nil)
	mkScratchDir(testingT, ctx, "/tmp/gentoo-install/root/.gentoo-stage3")
	tarPath := "/tmp/gentoo-install/root/.gentoo-stage3/stage3.tar.xz"
	writeScratch(testingT, ctx, tarPath, "tarball")

	info := installer.Stage3Info{Basename: "stage3.tar.xz", Path: tarPath}
	if err := installer.ExtractStage3(ctx, info); err != nil {
		testingT.Fatal(err)
	}
	// Root device mounted into the (empty) root mountpoint, staged tarball
	// extracted, then the scratch dir cleaned up.
	assertCmds(testingT, stub,
		"mount /dev/fake-part_root /tmp/gentoo-install/root",
		"tar -xpf /tmp/gentoo-install/root/.gentoo-stage3/stage3.tar.xz --xattrs --numeric-owner",
	)
	if _, err := os.Stat(filepath.Join(ctx.Root, "/tmp/gentoo-install/root/.gentoo-stage3")); !os.IsNotExist(err) {
		testingT.Fatalf("stage3 scratch dir must be removed after a successful extract (err=%v)", err)
	}
}

func TestExtractStage3SkipsLostFound(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Disk.UseSwap = false
	ctx, stub := testContext(testingT, cfg, nil)
	mkScratchDir(testingT, ctx, "/tmp/gentoo-install/root/lost+found")
	mkScratchDir(testingT, ctx, "/tmp/gentoo-install/root/.gentoo-stage3")
	tarPath := "/tmp/gentoo-install/root/.gentoo-stage3/stage3.tar.xz"
	writeScratch(testingT, ctx, tarPath, "tarball")

	info := installer.Stage3Info{Basename: "stage3.tar.xz", Path: tarPath}
	if err := installer.ExtractStage3(ctx, info); err != nil {
		testingT.Fatal(err)
	}
	assertCmdContains(testingT, stub, []string{
		"tar -xpf /tmp/gentoo-install/root/.gentoo-stage3/stage3.tar.xz --xattrs --numeric-owner",
	})
}

func TestExtractStage3NonEmptyRoot(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Disk.UseSwap = false
	ctx, _ := testContext(testingT, cfg, nil)
	mkScratchDir(testingT, ctx, "/tmp/gentoo-install/root/.gentoo-stage3")
	mkScratchDir(testingT, ctx, "/tmp/gentoo-install/root/etc")
	tarPath := "/tmp/gentoo-install/root/.gentoo-stage3/stage3.tar.xz"
	writeScratch(testingT, ctx, tarPath, "tarball")

	info := installer.Stage3Info{Basename: "stage3.tar.xz", Path: tarPath}
	err := installer.ExtractStage3(ctx, info)
	if err == nil || !strings.Contains(err.Error(), "root directory") {
		testingT.Fatalf("expected non-empty root error, got %v", err)
	}
}

func TestExtractStage3MissingTarball(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Disk.UseSwap = false
	ctx, _ := testContext(testingT, cfg, nil)

	info := installer.Stage3Info{Basename: "nope.tar.xz",
		Path: "/tmp/gentoo-install/nope.tar.xz"}
	err := installer.ExtractStage3(ctx, info)
	if err == nil || !strings.Contains(err.Error(), "stage3 file not found") {
		testingT.Fatalf("expected missing stage3 error, got %v", err)
	}
}
