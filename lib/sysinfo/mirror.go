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

type MirrorStatus struct {
	OK   bool
	Note string
}

const probeTimeout = 5 * time.Second

const probePath = "/"

func MirrorHost(mirrorURL string) string {
	parsedURL, err := url.Parse(mirrorURL)
	if err != nil || parsedURL.Host == "" {
		return ""
	}
	return parsedURL.Host
}

func mirrorOrigin(mirrorURL string) string {
	parsedURL, err := url.Parse(mirrorURL)
	if err != nil || parsedURL.Host == "" {
		return ""
	}
	return (&url.URL{Scheme: parsedURL.Scheme, Host: parsedURL.Host}).String()
}

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

func probeErrNote(err error) string {
	for _, wrapped := range unwrapAll(err) {
		if _, ok := wrapped.(*net.DNSError); ok {
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

func unwrapAll(err error) []error {
	var out []error
	for err != nil {
		out = append(out, err)
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			break
		}
		err = unwrapped.Unwrap()
	}
	return out
}
