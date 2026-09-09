// Mirror reachability probing and the TUI mirror indicator.
package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gentooinstall/lib/config"
	"gentooinstall/lib/sysinfo"
	"gentooinstall/lib/tui"
)

func TestMirrorProbe(testingT *testing.T) {
	testingT.Run("http 200 is reachable", func(testingT *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			rw.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()
		st := sysinfo.MirrorProbe(context.Background(), ts.URL+"/gentoo")
		if !st.OK || st.Note != "ok" {
			testingT.Fatalf("probe = %+v, want OK/ok", st)
		}
	})

	testingT.Run("head 405 falls back to get", func(testingT *testing.T) {
		var heads, gets int
		ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			switch req.Method {
			case http.MethodHead:
				heads++
				http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			case http.MethodGet:
				gets++
				rw.WriteHeader(http.StatusOK)
			}
		}))
		defer ts.Close()
		st := sysinfo.MirrorProbe(context.Background(), ts.URL+"/")
		if !st.OK {
			testingT.Fatalf("probe = %+v, want OK via GET fallback", st)
		}
		if heads != 1 || gets != 1 {
			testingT.Fatalf("head=%d get=%d, want 1/1", heads, gets)
		}
	})

	testingT.Run("http 500 is down", func(testingT *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			http.Error(rw, "boom", http.StatusInternalServerError)
		}))
		defer ts.Close()
		st := sysinfo.MirrorProbe(context.Background(), ts.URL+"/")
		if st.OK {
			testingT.Fatalf("probe = %+v, want down", st)
		}
		if !strings.Contains(st.Note, "500") {
			testingT.Fatalf("note = %q, want mention of http 500", st.Note)
		}
	})

	testingT.Run("connection refused is no network", func(testingT *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {}))
		downURL := ts.URL
		ts.Close() // now connection refused
		st := sysinfo.MirrorProbe(context.Background(), downURL)
		if st.OK {
			testingT.Fatalf("probe = %+v, want down", st)
		}
		if !strings.Contains(st.Note, "no network") {
			testingT.Fatalf("note = %q, want no network", st.Note)
		}
	})

	testingT.Run("invalid mirror url", func(testingT *testing.T) {
		st := sysinfo.MirrorProbe(context.Background(), "not a url")
		if st.OK || !strings.Contains(st.Note, "invalid") {
			testingT.Fatalf("probe = %+v, want invalid mirror", st)
		}
	})
}

func TestTuiMirrorIndicator(testingT *testing.T) {
	newModel := func(mirror string) *tui.Model {
		cfg := config.Default(true)
		cfg.Gentoo.Mirror = mirror
		return tui.New(cfg, "/tmp/test-gentoo.toml")
	}

	testingT.Run("reachable shows check", func(testingT *testing.T) {
		model := newModel("https://mirror.example.com/gentoo")
		mm, _ := model.Update(tui.MirrorProbeMsg{OK: true, Note: "ok"})
		model = mm.(*tui.Model)
		view := model.View()
		for _, want := range []string{"mirror.example.com"} {
			if !strings.Contains(view, want) {
				testingT.Fatalf("reachable view missing %q:\n%s", want, view)
			}
		}
	})

	testingT.Run("down shows note", func(testingT *testing.T) {
		model := newModel("https://mirror.example.com/gentoo")
		mm, _ := model.Update(tui.MirrorProbeMsg{OK: false, Note: "no network"})
		model = mm.(*tui.Model)
		view := model.View()
		for _, want := range []string{"mirror.example.com", "✗", "no network"} {
			if !strings.Contains(view, want) {
				testingT.Fatalf("down view missing %q:\n%s", want, view)
			}
		}
	})

	testingT.Run("editing mirror re-enters checking", func(testingT *testing.T) {
		model := newModel("https://old.example.com/gentoo")
		mm, _ := model.Update(tui.MirrorProbeMsg{OK: true, Note: "ok"})
		model = mm.(*tui.Model)

		// Gentoo tab, move the cursor onto the "Gentoo mirror" text row.
		mm, _ = model.Update(keyRunes('4'))
		model = mm.(*tui.Model)
		model = rowDownN(model, 5)
		mm, _ = model.Update(keyEnter())
		model = mm.(*tui.Model)

		for range "https://old.example.com/gentoo" {
			mm, _ = model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
			model = mm.(*tui.Model)
		}
		for _, ch := range "https://new.example.com/gentoo" {
			mm, _ = model.Update(keyRunes(ch))
			model = mm.(*tui.Model)
		}
		mm, _ = model.Update(keyEnter())
		model = mm.(*tui.Model)

		if got := model.Config().Gentoo.Mirror; got != "https://new.example.com/gentoo" {
			testingT.Fatalf("mirror = %q", got)
		}
		if !model.Dirty() {
			testingT.Fatal("editing mirror must mark config dirty")
		}
		if !strings.Contains(model.View(), "...") {
			testingT.Fatalf("editing mirror must re-enter the checking state:\n%s", model.View())
		}
	})
}
