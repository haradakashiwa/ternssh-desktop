package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"ternssh-desktop/internal/sshconn"
	"ternssh-desktop/internal/store"
)

// editorWindows opens editors as the app's windows, recording them, with
// their updates run by apply.
type editorWindows struct {
	t       *testing.T
	updates chan func()
	open    []*Editor
	closed  []*Editor
	focused int
}

func (w *editorWindows) host(e *Editor) EditorHost {
	w.open = append(w.open, e)
	return EditorHost{
		Update: func(fn func()) { w.updates <- fn },
		Close: func() {
			if e.CanClose() {
				w.closed = append(w.closed, e)
				e.Closed()
			}
		},
		Focus: func() { w.focused++ },
	}
}

func (w *editorWindows) apply(done func() bool) {
	w.t.Helper()
	deadline := time.After(5 * time.Second)
	for !done() {
		select {
		case fn := <-w.updates:
			fn()
		case <-deadline:
			w.t.Fatal("timed out")
		}
	}
}

func TestEditor(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "app.conf")
	os.WriteFile(p, []byte("port = 80\r\nhost = a\r\n"), 0o600)
	w := &editorWindows{t: t, updates: make(chan func(), 16)}
	e := newEditor(sshconn.LocalFiles{}, p, "app.conf", "本机")
	e.host = w.host(e)
	e.load()
	w.apply(func() bool { return !e.loading })
	if e.err != "" || e.buf.String() != "port = 80\nhost = a\n" || !e.crlf || e.Dirty() {
		t.Fatalf("loaded %q, crlf %v, error %q", e.buf.String(), e.crlf, e.err)
	}
	tt := ui.NewTester(e.View, 900, 600)
	if !tt.HasText("3 行 · UTF-8 · CRLF") {
		t.Errorf("texts %q", tt.Texts())
	}

	// Edited, it is dirty until saved, with its CRLF and its mode.
	e.buf.Replace(7, 9, "8080")
	if !e.Dirty() {
		t.Fatal("not dirty")
	}
	tt.Frame()
	if !tt.HasText("未保存") {
		t.Errorf("texts %q", tt.Texts())
	}
	snapshot(t, tt, "editor")
	tt.Key(ui.Cmd, ui.KeyS)
	w.apply(func() bool { return !e.saving })
	b, _ := os.ReadFile(p)
	if string(b) != "port = 8080\r\nhost = a\r\n" || e.Dirty() || e.saveErr != "" {
		t.Fatalf("saved %q, dirty %v, error %q", b, e.Dirty(), e.saveErr)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}

	// Changed by another program since: it asks before overwriting.
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(p, []byte("changed elsewhere\n"), 0o600)
	e.buf.Replace(0, 0, "# ")
	e.save(false)
	w.apply(func() bool { return !e.saving })
	if !e.conflict {
		t.Fatal("no conflict")
	}
	if b, _ := os.ReadFile(p); string(b) != "changed elsewhere\n" {
		t.Fatalf("overwritten: %q", b)
	}
	e.save(true)
	w.apply(func() bool { return !e.saving })
	if b, _ := os.ReadFile(p); !strings.HasPrefix(string(b), "# port") || e.conflict {
		t.Fatalf("not overwritten: %q", b)
	}

	// Closing with changes asks, and saves when told to.
	e.buf.Replace(0, 2, "")
	if e.CanClose() || !e.closeAsk {
		t.Fatal("closes with changes")
	}
	tt.Frame()
	if err := tt.Click("保存并关闭"); err != nil {
		t.Fatal(err)
	}
	w.apply(func() bool { return len(w.closed) == 1 })
	if b, _ := os.ReadFile(p); !strings.HasPrefix(string(b), "port") {
		t.Errorf("not saved before closing: %q", b)
	}
}

func TestEditorRefuses(t *testing.T) {
	dir := t.TempDir()
	w := &editorWindows{t: t, updates: make(chan func(), 16)}
	for name, data := range map[string][]byte{
		"a.bin":   {0x7f, 'E', 'L', 'F', 0, 1},
		"b.gbk":   {0xc4, 0xe3, 0xba, 0xc3},
		"big.log": make([]byte, editorMaxSize+1),
	} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, data, 0o644)
		e := newEditor(sshconn.LocalFiles{}, p, name, "本机")
		e.host = w.host(e)
		e.load()
		w.apply(func() bool { return !e.loading })
		if e.err == "" || e.Dirty() {
			t.Errorf("%s: opened", name)
		}
		e.save(false)
		if e.saving {
			t.Errorf("%s: saves", name)
		}
	}
}

func TestOpenEditor(t *testing.T) {
	m, st := testModel(t)
	apply := updatesHere(t, m)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o644)
	w := &editorWindows{t: t, updates: make(chan func(), 16)}
	m.backend.OpenEditor = w.host
	st.PutTerminal(store.Terminal{ID: "t1", Name: "项目", Dir: dir})
	home := m.sites[0]
	home.terminals = st.Terminals()
	m.openShell(home, home.terminals[0])
	sess := home.sessions[0]
	tt := ui.NewTester(m.View, 1100, 700)
	apply(func() bool { return sess.files != nil && !sess.files.loading })
	tt.Frame()

	// A double click opens a file; again, it brings its window forward.
	f := sess.files
	r, ok := tt.Find("notes.txt")
	if !ok {
		t.Fatalf("texts %q", tt.Texts())
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	if len(w.open) != 1 {
		t.Fatalf("%d windows after a double click", len(w.open))
	}
	m.openEditor(sess, f, filepath.Join(dir, "notes.txt"))
	if len(w.open) != 1 || w.focused != 1 || len(m.editors) != 1 {
		t.Fatalf("%d windows, focused %d times", len(w.open), w.focused)
	}
	e := w.open[0]
	w.apply(func() bool { return !e.loading })
	if e.Title != "notes.txt — 本机" || e.buf.String() != "hello" {
		t.Fatalf("title %q, text %q", e.Title, e.buf.String())
	}
	// Quitting waits for changes to be saved.
	e.buf.Replace(5, 5, "!")
	if m.CanQuit() || !e.closeAsk {
		t.Fatal("quits with changes")
	}
	e.closeAsk = false
	e.saved = e.buf.Version()
	if !m.CanQuit() {
		t.Fatal("does not quit")
	}
	e.host.Close()
	if len(m.editors) != 0 {
		t.Error("the editor is still listed")
	}
}
