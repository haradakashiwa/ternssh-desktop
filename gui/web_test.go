package gui

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/bcrypt"

	"ternssh-desktop/internal/store"
	"ternssh-desktop/internal/web"
)

// fakeWeb records what the model publishes.
type fakeWeb struct {
	states [][]byte
	opts   *web.Options
	urls   []string
}

func (w *fakeWeb) Apply(o *web.Options) error { w.opts = o; return nil }
func (w *fakeWeb) Err() error                 { return nil }
func (w *fakeWeb) URLs() []string             { return w.urls }
func (w *fakeWeb) Watching() bool             { return true }
func (w *fakeWeb) Publish(b []byte)           { w.states = append(w.states, b) }

func (w *fakeWeb) last(t *testing.T) webState {
	t.Helper()
	var st webState
	if err := json.Unmarshal(w.states[len(w.states)-1], &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestWebState(t *testing.T) {
	m, st := testModel(t)
	withSites(t, m, st)
	w := &fakeWeb{}
	m.SetWeb(w)
	m.publish()
	got := w.last(t)
	if len(got.Tabs) != 3 || got.Tabs[0].ID != "home" || got.Tabs[1].ID != "a" {
		t.Fatalf("tabs: %+v", got.Tabs)
	}
	if it := got.Tabs[0].Items; len(it) != 1 || it[0].Key != "l:l1" || it[0].Detail != "pi@192.168.1.20" {
		t.Fatalf("home: %+v", it)
	}
	if it := got.Tabs[1].Items; len(it) != 2 || it[0].Kind != "group" || len(it[0].Children) != 2 || it[1].Detail != "ubuntu@db.internal:2222" {
		t.Fatalf("site: %+v", it)
	}
	if got.Tabs[2].Err == "" {
		t.Fatal("the site's error is not shown")
	}
}

func TestWebOpenLeavesTheWindowsTerminal(t *testing.T) {
	m, st := testModel(t)
	withSites(t, m, st)
	w := &fakeWeb{}
	m.SetWeb(w)
	s := m.sites[1]
	m.openOn(s, "r:s1")
	m.act(webOp{Op: "open", Tab: "a", Key: "r:s3"})
	if len(s.sessions) != 2 || s.current != 0 {
		t.Fatalf("%d sessions, the window shows %d", len(s.sessions), s.current)
	}
	m.publish()
	sessions := w.last(t).Tabs[1].Sessions
	if len(sessions) != 2 || !sessions[0].Shown || sessions[1].Shown || sessions[1].Key != "r:s3" {
		t.Fatalf("sessions: %+v", sessions)
	}
	m.act(webOp{Op: "close", Session: sessions[1].ID})
	if len(s.sessions) != 1 || s.sessions[0].node.ID != "s1" {
		t.Fatalf("closing: %d sessions", len(s.sessions))
	}
}

func TestWebSettings(t *testing.T) {
	m, st := testModel(t)
	w := &fakeWeb{}
	m.SetWeb(w)
	m.applyWeb()
	if w.opts != nil {
		t.Fatal("serving while off")
	}
	set := st.Settings()
	set.Web = store.Web{Enabled: true}
	st.SetSettings(set)
	m.applyWeb()
	if w.opts == nil || w.opts.Port != store.DefaultWebPort || w.opts.Access != web.Local || w.opts.Username != "" || w.opts.PasswordHash != "" {
		t.Fatalf("options: %+v", w.opts)
	}
	// The local network signs in with the password too.
	set.Web.SetAccess(store.WebLAN)
	set.Web.Username = "me"
	st.SetSettings(set)
	st.SetWebPassword("correct horse")
	m.applyWeb()
	if w.opts.Access != web.LAN || w.opts.Username != "me" || w.opts.PasswordHash == "" {
		t.Fatalf("LAN options: %+v", w.opts)
	}
}

func TestWebPublicAccess(t *testing.T) {
	m, st := testModel(t)
	w := &fakeWeb{}
	m.SetWeb(w)
	set := st.Settings()
	set.Web = store.Web{Enabled: true, Port: 20000}
	set.Web.SetAccess(store.WebPublic)
	set.Web.Username = "me"
	st.SetSettings(set)
	if err := st.SetWebPassword("correct horse"); err != nil {
		t.Fatal(err)
	}
	m.applyWeb()
	o := w.opts
	if o.Access != web.Public || o.Port != 20000 || o.Username != "me" ||
		bcrypt.CompareHashAndPassword([]byte(o.PasswordHash), []byte("correct horse")) != nil {
		t.Fatalf("options: %+v", o)
	}
	if st.Settings().Web.Access() != store.WebPublic || st.Settings().Web.LAN {
		t.Fatal("access")
	}
}

func TestWebSettingsDialog(t *testing.T) {
	m, st := testModel(t)
	w := &fakeWeb{urls: []string{"http://127.0.0.1:17722/", "http://192.168.1.5:17722/"}}
	m.SetWeb(w)
	set := st.Settings()
	set.Web = store.Web{Enabled: true}
	set.Web.SetAccess(store.WebPublic)
	st.SetSettings(set)
	tt := ui.NewTester(m.View, 1100, 900)
	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"访问范围", "端口", "账号", "公网", "http://127.0.0.1:17722/"} {
		if !tt.HasText(text) {
			t.Fatalf("no %q: %v", text, tt.Texts())
		}
	}
	// A short password is refused, a good one saved and the server told.
	m.settings.webUser, m.settings.webPass = "me", "short"
	tt.Frame()
	if err := tt.Click("保存账号"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(st.WebPasswordHash()) != 0 || !tt.HasText("密码至少需要 8 位。") {
		t.Fatalf("a short password: %v", tt.Texts())
	}
	m.settings.webPass = "correct horse"
	tt.Frame()
	tt.Click("保存账号")
	tt.Frame()
	if st.Settings().Web.Username != "me" || len(st.WebPasswordHash()) == 0 || w.opts == nil || w.opts.Username != "me" {
		t.Fatalf("not saved: %+v", w.opts)
	}
	snapshot(t, tt, "settings-web")
	small := ui.NewTester(m.View, 900, 540)
	small.Frame()
	snapshot(t, small, "settings-web-small")
}

func TestWebLANAsksForAnAccount(t *testing.T) {
	m, st := testModel(t)
	m.SetWeb(&fakeWeb{})
	set := st.Settings()
	set.Web = store.Web{Enabled: true}
	set.Web.SetAccess(store.WebLAN)
	st.SetSettings(set)
	tt := ui.NewTester(m.View, 1100, 900)
	tt.Click("设置")
	if !tt.HasText("账号") || !tt.HasText("局域网") {
		t.Fatalf("no account for the LAN: %v", tt.Texts())
	}
	set.Web.SetAccess(store.WebLocal)
	st.SetSettings(set)
	tt.Frame()
	if tt.HasText("账号") {
		t.Fatal("an account for this machine only")
	}
}
