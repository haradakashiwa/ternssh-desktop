package gui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// newSessionMods and newSessionLabel are the shortcut of a new terminal
// by default: its modifiers with T, and as the settings show it.
var newSessionMods, newSessionLabel = func() (ui.Modifiers, string) {
	acc := shortcutActions[0].byDefault()
	mods, _, _ := parseHotkey(acc)
	return mods, hotkeyLabel(acc)
}()

func TestParseHotkey(t *testing.T) {
	for _, a := range shortcutActions {
		for _, acc := range []string{a.mac, a.other} {
			mods, key, ok := parseHotkey(acc)
			if !ok || hotkeyString(mods, keyName(key)) != acc {
				t.Errorf("%s: %q read as %v %v %v", a.id, acc, mods, key, ok)
			}
		}
	}
	for _, acc := range []string{"Alt+F12", "Ctrl+Alt+Super+Up", "Shift+Super+["} {
		if mods, key, ok := parseHotkey(acc); !ok || hotkeyString(mods, keyName(key)) != acc {
			t.Errorf("%q read as %v %v %v", acc, mods, key, ok)
		}
	}
	for _, acc := range []string{"", "Hyper+K", "Ctrl+Nope"} {
		if _, _, ok := parseHotkey(acc); ok {
			t.Errorf("%q read", acc)
		}
	}
}

func TestShortcuts(t *testing.T) {
	m, st := testModel(t)
	withSites(t, m, st)
	tt := ui.NewTester(m.View, 1100, 700)
	press := func(id string) {
		t.Helper()
		mods, key, ok := parseHotkey(m.shortcut(id))
		if !ok {
			t.Fatalf("no shortcut for %s", id)
		}
		tt.Key(mods, key)
	}

	// The terminals of the tab, going round.
	s := m.sites[1]
	m.connectGroup(s, "g1")
	tt.Frame()
	if s.current != 1 {
		t.Fatalf("terminal %d shown", s.current)
	}
	press("nextTab")
	if s.current != 0 || !s.sessions[0].refocus {
		t.Fatalf("next: terminal %d shown", s.current)
	}
	press("prevTab")
	if s.current != 1 {
		t.Fatalf("previous: terminal %d shown", s.current)
	}

	// The tabs of the window, going round.
	press("nextSite")
	if m.active != 2 {
		t.Fatalf("next site: tab %d", m.active)
	}
	press("nextSite")
	if m.active != 0 {
		t.Fatalf("next site: tab %d", m.active)
	}
	press("prevSite")
	if m.active != 2 {
		t.Fatalf("previous site: tab %d", m.active)
	}

	// Changed in the settings, the shortcut does the action, and the
	// one before no longer does.
	m.selectSite(1)
	old := m.shortcut("nextSite")
	m.OpenSettings()
	m.settings.tab = settingsShortcuts
	tt.Frame()
	if err := tt.Click("下一个站点快捷键"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Alt|ui.Shift, ui.KeyN)
	if got := m.shortcut("nextSite"); got != "Alt+Shift+N" || m.settings.err != "" {
		t.Fatalf("shortcut %q, error %q", got, m.settings.err)
	}
	// Taken by another action, it is refused.
	tt.Click("上一个站点快捷键")
	tt.Key(ui.Alt|ui.Shift, ui.KeyN)
	if m.settings.err == "" || m.shortcut("prevSite") == "Alt+Shift+N" {
		t.Fatalf("taken shortcut set: error %q", m.settings.err)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Click("完成")
	press("nextSite")
	if m.active != 2 {
		t.Fatalf("new shortcut: tab %d", m.active)
	}
	mods, key, _ := parseHotkey(old)
	tt.Key(mods, key)
	if m.active != 2 {
		t.Fatalf("old shortcut still works: tab %d", m.active)
	}

	// A terminal, which would send the keys to the shell, leaves them to
	// the window, but for those of no shortcut.
	if !m.TerminalKey(ui.Alt|ui.Shift, ui.KeyN) || m.active != 0 {
		t.Fatalf("terminal key: tab %d", m.active)
	}
	if m.TerminalKey(ui.Ctrl, ui.KeyC) {
		t.Fatal("Control+C taken from the shell")
	}

	// Cleared, there is none; the defaults come back.
	m.setShortcut("newTerminal", "")
	if m.shortcutLabel("newTerminal") != "" {
		t.Fatal("cleared shortcut shown")
	}
	tt.Frame()
	if err := st.ResetShortcuts(); err != nil || m.shortcut("nextSite") != old || m.shortcutLabel("newTerminal") != newSessionLabel {
		t.Fatalf("defaults not back: %v", err)
	}
}

func TestCloseTabShortcut(t *testing.T) {
	m, st := testModel(t)
	withSites(t, m, st)
	tt := ui.NewTester(m.View, 1100, 700)
	s := m.sites[1]
	m.connectGroup(s, "g1")
	tt.Frame()
	n := len(s.sessions)
	if n < 2 {
		t.Fatalf("%d terminals", n)
	}

	// The menu bar's Command+W closes the terminal shown, and shows the
	// one before it.
	mods, key, _ := parseHotkey(m.shortcut("closeTab"))
	if !m.MenuKey(mods, key) || len(s.sessions) != n-1 || s.current != n-2 || !s.sessions[s.current].refocus {
		t.Fatalf("closed: %d terminals, %d shown", len(s.sessions), s.current)
	}

	// While a shortcut is recorded, the keys are its own.
	m.startRecording("newTerminal")
	if !m.MenuKey(ui.Super|ui.Shift, ui.KeyW) || m.shortcut("newTerminal") != "Shift+Super+W" || len(s.sessions) != n-1 {
		t.Fatalf("recorded %q, %d terminals", m.shortcut("newTerminal"), len(s.sessions))
	}

	// Changed to another, the keys are not the window's: the window
	// closes as before.
	m.setShortcut("closeTab", "Alt+Super+W")
	if m.MenuKey(ui.Super, ui.KeyW) {
		t.Fatal("Command+W taken with no action")
	}
}
