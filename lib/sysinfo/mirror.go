package sysinfo

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MirrorStatus reports whether a configured Gentoo mirror host is reachable
// over the network together with a short human-readable note for the TUI.
type MirrorStatus struct {
	OK   bool
	Note string
}

// probeTimeout bounds a single mirror reachability probe so the indicator
// never hangs the UI waiting on a dead or filtered host.
const probeTimeout = 5 * time.Second

// probePath is a small, cheap, always-present resource on the mirror host to
// HEAD. It only establishes reachability at the HTTP layer (what the stage3
// fetch actually needs); the full releases listing is still resolved by the
// installer against the configured subpath.
const probePath = "/"

// MirrorHost returns the host portion of a Gentoo mirror URL (no scheme), or
// "" when the value is not a parseable URL. The TUI shows this next to the
// mirror reachability indicator.
func MirrorHost(mirrorURL string) string {
	u, err := url.Parse(mirrorURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}

// mirrorOrigin strips scheme and any path/query from a mirror URL, returning
// just "scheme://host" (the endpoint the probe targets).
func mirrorOrigin(mirrorURL string) string {
	u, err := url.Parse(mirrorURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
}

// MirrorProbe checks whether the host named by the given Gentoo mirror URL is
// reachable over HTTP. It prefers a HEAD request, falling back to GET for
// servers that reject HEAD; a 2xx response counts as reachable. The result
// maps common failure kinds to a short note the TUI can show verbatim.
func MirrorProbe(ctx context.Context, mirrorURL string) MirrorStatus {
	origin := mirrorOrigin(mirrorURL)
	if origin == "" {
		return MirrorStatus{OK: false, Note: "invalid mirror"}
	}

	client := &http.Client{Timeout: probeTimeout}
	for attempt, method := 0, http.MethodHead; ; method = http.MethodGet {
		req, err := http.NewRequestWithContext(ctx, method, origin+probePath, nil)
		if err != nil {
			return MirrorStatus{OK: false, Note: "invalid request"}
		}
		resp, err := client.Do(req)
		// A 405/400 on HEAD means the server does not support it; retry with
		// GET before concluding the mirror is down.
		if method == http.MethodHead && err == nil &&
			(resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusBadRequest) {
			resp.Body.Close()
			attempt++
			if attempt < 2 {
				continue
			}
		}
		if err != nil {
			return MirrorStatus{OK: false, Note: probeErrNote(err)}
		}
		statusOK := resp.StatusCode >= 200 && resp.StatusCode < 300
		note := "ok"
		if !statusOK {
			note = "http " + resp.Status
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return MirrorStatus{OK: statusOK, Note: note}
	}
}

// probeErrNote turns a transport/dial error into the short diagnostic shown
// next to the shield. Unreachable is the main live-ISO case, where DHCP is
// still coming up or a proxy is required.
func probeErrNote(err error) string {
	for _, e := range unwrapAll(err) {
		if _, ok := e.(*net.DNSError); ok {
			return "dns failed"
		}
	}
	msg := strings.ToLower(fmt.Sprint(err))
	switch {
	case strings.Contains(msg, "no such host"):
		return "dns failed"
	case strings.Contains(msg, "no route to host"),
		strings.Contains(msg, "network is unreachable"),
		strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "client.timeout"),
		strings.Contains(msg, "context deadline exceeded"):
		return "no network"
	}
	return "unreachable"
}

// unwrapAll returns err and every error wrapped within it.
func unwrapAll(err error) []error {
	var out []error
	for err != nil {
		out = append(out, err)
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			break
		}
		err = u.Unwrap()
	}
	return out
}
