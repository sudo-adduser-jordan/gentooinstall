package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gentooinstall/internal/config"
	"gentooinstall/internal/sysinfo"
	"gentooinstall/internal/tui"
)

func TestMirrorProbe(t *testing.T) {
	t.Run("http 200 is reachable", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()
		st := sysinfo.MirrorProbe(context.Background(), ts.URL+"/gentoo")
		if !st.OK || st.Note != "ok" {
			t.Fatalf("probe = %+v, want OK/ok", st)
		}
	})

	t.Run("head 405 falls back to get", func(t *testing.T) {
		var heads, gets int
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodHead:
				heads++
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			case http.MethodGet:
				gets++
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer ts.Close()
		st := sysinfo.MirrorProbe(context.Background(), ts.URL+"/")
		if !st.OK {
			t.Fatalf("probe = %+v, want OK via GET fallback", st)
		}
		if heads != 1 || gets != 1 {
			t.Fatalf("head=%d get=%d, want 1/1", heads, gets)
		}
	})

	t.Run("http 500 is down", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer ts.Close()
		st := sysinfo.MirrorProbe(context.Background(), ts.URL+"/")
		if st.OK {
			t.Fatalf("probe = %+v, want down", st)
		}
		if !strings.Contains(st.Note, "500") {
			t.Fatalf("note = %q, want mention of http 500", st.Note)
		}
	})

	t.Run("connection refused is no network", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		downURL := ts.URL
		ts.Close() // now connection refused
		st := sysinfo.MirrorProbe(context.Background(), downURL)
		if st.OK {
			t.Fatalf("probe = %+v, want down", st)
		}
		if !strings.Contains(st.Note, "no network") {
			t.Fatalf("note = %q, want no network", st.Note)
		}
	})

	t.Run("invalid mirror url", func(t *testing.T) {
		st := sysinfo.MirrorProbe(context.Background(), "not a url")
		if st.OK || !strings.Contains(st.Note, "invalid") {
			t.Fatalf("probe = %+v, want invalid mirror", st)
		}
	})
}

func TestTuiMirrorIndicator(t *testing.T) {
	newModel := func(mirror string) *tui.Model {
		cfg := config.Default(true)
		cfg.Gentoo.Mirror = mirror
		return tui.New(cfg, "/tmp/test-gentoo.toml")
	}

	t.Run("reachable shows check", func(t *testing.T) {
		m := newModel("https://mirror.example.com/gentoo")
		mm, _ := m.Update(tui.MirrorProbeMsg{OK: true, Note: "ok"})
		m = mm.(*tui.Model)
		view := m.View()
		for _, want := range []string{"Mirror", "mirror.example.com", "✓"} {
			if !strings.Contains(view, want) {
				t.Fatalf("reachable view missing %q:\n%s", want, view)
			}
		}
	})

	t.Run("down shows note", func(t *testing.T) {
		m := newModel("https://mirror.example.com/gentoo")
		mm, _ := m.Update(tui.MirrorProbeMsg{OK: false, Note: "no network"})
		m = mm.(*tui.Model)
		view := m.View()
		for _, want := range []string{"Mirror", "✗", "no network"} {
			if !strings.Contains(view, want) {
				t.Fatalf("down view missing %q:\n%s", want, view)
			}
		}
	})

	t.Run("editing mirror re-enters checking", func(t *testing.T) {
		m := newModel("https://old.example.com/gentoo")
		mm, _ := m.Update(tui.MirrorProbeMsg{OK: true, Note: "ok"})
		m = mm.(*tui.Model)

		// Gentoo tab, move the cursor onto the "Gentoo mirror" text row.
		mm, _ = m.Update(keyRunes('4'))
		m = mm.(*tui.Model)
		m = rowDownN(m, 5)
		mm, _ = m.Update(keyEnter())
		m = mm.(*tui.Model)

		for range "https://old.example.com/gentoo" {
			mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
			m = mm.(*tui.Model)
		}
		for _, r := range "https://new.example.com/gentoo" {
			mm, _ = m.Update(keyRunes(r))
			m = mm.(*tui.Model)
		}
		mm, _ = m.Update(keyEnter())
		m = mm.(*tui.Model)

		if got := m.Config().Gentoo.Mirror; got != "https://new.example.com/gentoo" {
			t.Fatalf("mirror = %q", got)
		}
		if !m.Dirty() {
			t.Fatal("editing mirror must mark config dirty")
		}
		if !strings.Contains(m.View(), "checking") {
			t.Fatalf("editing mirror must re-enter the checking state:\n%s", m.View())
		}
	})
}
