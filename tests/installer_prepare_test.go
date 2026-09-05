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

	"gentooinstall/internal/installer"
)

func TestSHA512File(t *testing.T) {
	content := "stage3 payload\n"
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha512.Sum512([]byte(content))
	got, err := installer.SHA512File(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != hex.EncodeToString(sum[:]) {
		t.Fatalf("SHA512File = %s", got)
	}
}

func TestWantedPrograms(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	c, _ := testContext(t, cfg, nil)

	req, _ := installer.WantedPrograms(c)
	wantReq := []string{"gpg", "hwclock", "lsblk", "ntpd", "partprobe", "sgdisk"}
	if fmt.Sprint(req) != fmt.Sprint(wantReq) {
		t.Fatalf("required programs = %v, want %v", req, wantReq)
	}
	// rhash is optional (sha512sum suffices); it may never be required.
	for _, r := range req {
		if r == "rhash" {
			t.Fatalf("rhash must stay optional, got required: %v", req)
		}
	}
}

func TestSHA512FileMissing(t *testing.T) {
	if _, err := installer.SHA512File(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestResolveStage3(t *testing.T) {
	var hits int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch {
		case strings.HasSuffix(r.URL.Path, "latest-stage3-amd64-systemd.txt"):
			fmt.Fprint(w, "# Latest as of now\nstage3-amd64-systemd-20240121T123456Z.tar.xz 123456\n")
		case strings.HasSuffix(r.URL.Path, "/"):
			fmt.Fprint(w,
				`<a href="stage3-amd64-systemd-20240121T120000Z.tar.xz">`+
					`<a href="stage3-amd64-systemd-20240121T123456Z.tar.xz">`+
					`<a href="stage3%20with%25es%2Finside">`+
					`<a href="other-artifact">`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = ts.URL
	c, _ := testContext(t, cfg, nil)

	info, err := installer.ResolveStage3(c)
	if err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("expected 1 listing request, got %d", hits)
	}
	// The authoritative latest-*.txt names the newest tarball, even though
	// the index also lists an older one.
	if info.Basename != "stage3-amd64-systemd-20240121T123456Z.tar.xz" {
		t.Fatalf("ResolveStage3 = %q (want name from latest-*.txt)", info.Basename)
	}
	if info.Path != "/tmp/gentoo-install/"+info.Basename {
		t.Fatalf("ResolveStage3 path = %q", info.Path)
	}
}

func TestResolveStage3FallsBackToIndex(t *testing.T) {
	var hits int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch {
		case strings.HasSuffix(r.URL.Path, "latest-stage3-amd64-systemd.txt"):
			http.NotFound(w, r)
		case strings.HasSuffix(r.URL.Path, "/"):
			fmt.Fprint(w,
				`<a href="stage3-amd64-systemd-20240121T120000Z.tar.xz">`+
					`<a href="stage3-amd64-systemd-20240121T123456Z.tar.xz">`+
					`<a href="other-artifact">`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = ts.URL
	c, _ := testContext(t, cfg, nil)

	info, err := installer.ResolveStage3(c)
	if err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Fatalf("expected 2 requests (.txt then index), got %d", hits)
	}
	// Fallback picks the newest tarball from the index.
	if info.Basename != "stage3-amd64-systemd-20240121T123456Z.tar.xz" {
		t.Fatalf("ResolveStage3 fallback = %q", info.Basename)
	}
}

func TestResolveStage3NoMatch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "latest-stage3-amd64-systemd.txt") {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, "<a href=\"bogus-file\">")
	}))
	defer ts.Close()

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = ts.URL
	c, _ := testContext(t, cfg, nil)

	if _, err := installer.ResolveStage3(c); err == nil {
		t.Fatal("expected parse error for empty listing")
	}
}

func TestResolveStage3SurfacesLatestErrors(t *testing.T) {
	// A server error (not a 404) on the authoritative latest-*.txt listing
	// must fail with the real cause instead of silently falling back to the
	// index the way an absent listing (404) legitimately does.
	var hits int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if strings.HasSuffix(r.URL.Path, "latest-stage3-amd64-systemd.txt") {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = ts.URL
	c, _ := testContext(t, cfg, nil)

	_, err := installer.ResolveStage3(c)
	if err == nil {
		t.Fatal("expected error for failing latest listing")
	}
	if !strings.Contains(err.Error(), "latest-stage3-amd64-systemd.txt") {
		t.Fatalf("error should name the listing URL: %v", err)
	}
	if hits != 1 {
		t.Fatalf("expected no index fallback after a real error, got %d requests", hits)
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
}

func newStage3Mirror(t *testing.T, payload []byte) *stage3Mirror {
	t.Helper()
	m := &stage3Mirror{payload: payload, hits: map[string]int{}}
	sum := sha512.Sum512(payload)
	m.hash = hex.EncodeToString(sum[:])
	basename := "stage3-amd64-systemd-20240121T123456Z.tar.xz"
	digests := m.hash + "  " + basename + "\n" +
		strings.Repeat("0", 128) + "  irrelevant.tar.xz\n"
	m.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.hits[r.URL.Path]++
		m.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, basename+".DIGESTS"):
			fmt.Fprint(w, digests)
		case strings.HasSuffix(r.URL.Path, basename):
			m.mu.Lock()
			if m.failTarballN > 0 {
				m.failTarballN--
				m.mu.Unlock()
				http.Error(w, "transient failure", http.StatusInternalServerError)
				return
			}
			w.Write(m.payload)
			m.mu.Unlock()
		case strings.HasSuffix(r.URL.Path, "latest-stage3-amd64-systemd.txt"):
			fmt.Fprintf(w, "# Latest\n%s 123456\n", basename)
		case strings.HasSuffix(r.URL.Path, "/"):
			fmt.Fprintf(w, `"<a href="%s">`, basename)
		case strings.Contains(r.URL.Path, "releng") || strings.Contains(r.URL.Path, "openpgpkey"):
			fmt.Fprintln(w, "-----BEGIN PGP PUBLIC KEY BLOCK-----")
		default:
			http.NotFound(w, r)
		}
	}))
	return m
}

func (m *stage3Mirror) count(sub string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	for p, n := range m.hits {
		if strings.Contains(p, sub) {
			return n
		}
	}
	return 0
}

// countTarball returns the number of requests for exactly the tarball path
// (the .DIGESTS sibling ends in basename+".DIGESTS", so a substring match
// against basename would count it too).
func (m *stage3Mirror) countTarball(basename string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for p, hits := range m.hits {
		if strings.HasSuffix(p, basename) && !strings.HasSuffix(p, basename+".DIGESTS") {
			n += hits
		}
	}
	return n
}

func TestDownloadStage3(t *testing.T) {
	payload := []byte("the tarball bytes")
	mirror := newStage3Mirror(t, payload)
	defer mirror.ts.Close()

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = mirror.ts.URL
	c, s := testContext(t, cfg, nil)
	mkScratchDir(t, c, "/tmp/gentoo-install")

	info, err := installer.DownloadStage3(c)
	if err != nil {
		t.Fatal(err)
	}
	if info.Basename != "stage3-amd64-systemd-20240121T123456Z.tar.xz" {
		t.Fatalf("basename = %q", info.Basename)
	}
	if c.Stage3File != info.Path {
		t.Fatalf("c.Stage3File = %q", c.Stage3File)
	}
	stored := readScratch(t, c, info.Path)
	if stored != string(payload) {
		t.Fatalf("stored tarball = %q", stored)
	}
	if _, err := os.Stat(filepath.Join(c.Root, info.Path+".verified")); err != nil {
		t.Fatalf("verified marker missing: %v", err)
	}
	assertCmdContains(t, s, []string{
		"gpg --quiet --import " + filepath.Join(c.Root, "/tmp/gentoo-install/gentoo-keys.gpg"),
		"gpg --quiet --verify " + filepath.Join(c.Root, "/tmp/gentoo-install/"+info.Basename+".DIGESTS"),
	})
}

func TestDownloadStage3ResumesFromVerifiedMarker(t *testing.T) {
	payload := []byte("the tarball bytes")
	mirror := newStage3Mirror(t, payload)
	defer mirror.ts.Close()

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = mirror.ts.URL
	c, _ := testContext(t, cfg, nil)
	mkScratchDir(t, c, "/tmp/gentoo-install")
	path := "/tmp/gentoo-install/stage3-amd64-systemd-20240121T123456Z.tar.xz"
	writeScratch(t, c, path+".verified", "")

	info, err := installer.DownloadStage3(c)
	if err != nil {
		t.Fatal(err)
	}
	if info.Basename != "stage3-amd64-systemd-20240121T123456Z.tar.xz" {
		t.Fatalf("basename = %q", info.Basename)
	}
	if mirror.count("current-stage3-amd64-systemd") != 1 {
		t.Fatal("listing must be resolved once even when resuming")
	}
	if mirror.count("DIGESTS") != 0 {
		t.Fatal("resume path must not fetch DIGESTS")
	}
	for p := range mirror.hits {
		if strings.HasSuffix(p, ".tar.xz") {
			t.Fatalf("resume path must not re-download tarball, hit %s", p)
		}
	}
}

func TestDownloadStage3ChecksumMismatch(t *testing.T) {
	payload := []byte("different bytes than digest")
	mirror := newStage3Mirror(t, payload)
	defer mirror.ts.Close()
	// Corrupt the served tarball after computing the digests.
	mirror.mu.Lock()
	mirror.payload = []byte("tampered")
	mirror.mu.Unlock()

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = mirror.ts.URL
	c, _ := testContext(t, cfg, nil)
	mkScratchDir(t, c, "/tmp/gentoo-install")

	if _, err := installer.DownloadStage3(c); err == nil {
		t.Fatal("expected checksum mismatch error")
	}
}

func TestDownloadStage3RetriesTransientFailures(t *testing.T) {
	payload := []byte("the tarball bytes")
	mirror := newStage3Mirror(t, payload)
	defer mirror.ts.Close()
	mirror.mu.Lock()
	mirror.failTarballN = 2 // first two tarball fetches fail with a 500
	mirror.mu.Unlock()

	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Mirror = mirror.ts.URL
	c, s := testContext(t, cfg, nil)
	mkScratchDir(t, c, "/tmp/gentoo-install")

	info, err := installer.DownloadStage3(c)
	if err != nil {
		t.Fatalf("DownloadStage3 should survive transient failures: %v", err)
	}
	if info.Basename != "stage3-amd64-systemd-20240121T123456Z.tar.xz" {
		t.Fatalf("basename = %q", info.Basename)
	}
	if got := mirror.countTarball(info.Basename); got < 3 {
		t.Fatalf("tarball fetched %d times, want 3 (2 failures + success)", got)
	}
	stored := readScratch(t, c, info.Path)
	if stored != string(payload) {
		t.Fatalf("stored tarball = %q, want payload", stored)
	}
	assertCmdContains(t, s, []string{
		"gpg --quiet --import " + filepath.Join(c.Root, "/tmp/gentoo-install/gentoo-keys.gpg"),
		"gpg --quiet --verify " + filepath.Join(c.Root, "/tmp/gentoo-install/"+info.Basename+".DIGESTS"),
	})
}

func TestClearRoot(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Disk.UseSwap = false
	c, _ := testContext(t, cfg, nil)
	mkScratchDir(t, c, "/tmp/gentoo-install/root")
	writeScratch(t, c, "/tmp/gentoo-install/root/etc/os-release", "id=gentoo\n")
	writeScratch(t, c, "/tmp/gentoo-install/root/partial", "x")
	mkScratchDir(t, c, "/tmp/gentoo-install/root/lost+found")

	if err := installer.ClearRoot(c); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(c.Root, "/tmp/gentoo-install/root/etc")); !os.IsNotExist(err) {
		t.Fatalf("etc must be cleared after ClearRoot (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(c.Root, "/tmp/gentoo-install/root/partial")); !os.IsNotExist(err) {
		t.Fatalf("partial must be cleared after ClearRoot (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(c.Root, "/tmp/gentoo-install/root/lost+found")); err != nil {
		t.Fatalf("lost+found must survive ClearRoot: %v", err)
	}
}

func TestClearRootEmpty(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Disk.UseSwap = false
	c, _ := testContext(t, cfg, nil)
	mkScratchDir(t, c, "/tmp/gentoo-install/root")

	if err := installer.ClearRoot(c); err != nil {
		t.Fatal(err)
	}
}

func TestExtractStage3(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Disk.UseSwap = false
	c, s := testContext(t, cfg, nil)
	mkScratchDir(t, c, "/tmp/gentoo-install")
	tarPath := "/tmp/gentoo-install/stage3.tar.xz"
	writeScratch(t, c, tarPath, "tarball")

	info := installer.Stage3Info{Basename: "stage3.tar.xz", Path: tarPath}
	if err := installer.ExtractStage3(c, info); err != nil {
		t.Fatal(err)
	}
	// Root device mounted into the (empty) root mountpoint, then extracted.
	assertCmds(t, s,
		"mount /dev/fake-part_root /tmp/gentoo-install/root",
		"tar -xpf /tmp/gentoo-install/stage3.tar.xz --xattrs --numeric-owner",
	)
}

func TestExtractStage3SkipsLostFound(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Disk.UseSwap = false
	c, s := testContext(t, cfg, nil)
	mkScratchDir(t, c, "/tmp/gentoo-install")
	mkScratchDir(t, c, "/tmp/gentoo-install/root/lost+found")
	tarPath := "/tmp/gentoo-install/stage3.tar.xz"
	writeScratch(t, c, tarPath, "tarball")

	info := installer.Stage3Info{Basename: "stage3.tar.xz", Path: tarPath}
	if err := installer.ExtractStage3(c, info); err != nil {
		t.Fatal(err)
	}
	assertCmdContains(t, s, []string{
		"tar -xpf /tmp/gentoo-install/stage3.tar.xz --xattrs --numeric-owner",
	})
}

func TestExtractStage3NonEmptyRoot(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Disk.UseSwap = false
	c, _ := testContext(t, cfg, nil)
	mkScratchDir(t, c, "/tmp/gentoo-install")
	mkScratchDir(t, c, "/tmp/gentoo-install/root/etc")
	tarPath := "/tmp/gentoo-install/stage3.tar.xz"
	writeScratch(t, c, tarPath, "tarball")

	info := installer.Stage3Info{Basename: "stage3.tar.xz", Path: tarPath}
	err := installer.ExtractStage3(c, info)
	if err == nil || !strings.Contains(err.Error(), "root directory") {
		t.Fatalf("expected non-empty root error, got %v", err)
	}
}

func TestExtractStage3MissingTarball(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Disk.UseSwap = false
	c, _ := testContext(t, cfg, nil)

	info := installer.Stage3Info{Basename: "nope.tar.xz",
		Path: "/tmp/gentoo-install/nope.tar.xz"}
	err := installer.ExtractStage3(c, info)
	if err == nil || !strings.Contains(err.Error(), "stage3 file not found") {
		t.Fatalf("expected missing stage3 error, got %v", err)
	}
}
