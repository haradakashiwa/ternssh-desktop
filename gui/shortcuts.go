package gui

import (
	"runtime"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"ternssh-desktop/locales"
)

// shortcutAction is something the window does on a shortcut the user
// can change in the settings.
type shortcutAction struct {
	id    string        // its key in the store
	label func() string // its name in the settings
	mac   string        // its shortcut by default on macOS
	other string        // and elsewhere
	do    func(m *Model)
}

// shortcutActions are the actions of the window with a shortcut, in the
// order the settings list them. Control with a letter belongs to the
// shell, so the shortcuts are Command's on macOS, and Control with Shift
// elsewhere, as Windows Terminal's.
var shortcutActions = []shortcutAction{
	{id: "newTerminal", label: locales.ShortcutNewTerminal, mac: "Super+T", other: "Ctrl+Shift+T", do: (*Model).newSession},
	{id: "nextTab", label: locales.ShortcutNextTab, mac: "Shift+Super+]", other: "Ctrl+Tab", do: func(m *Model) { m.cycleSession(1) }},
	{id: "prevTab", label: locales.ShortcutPrevTab, mac: "Shift+Super+[", other: "Ctrl+Shift+Tab", do: func(m *Model) { m.cycleSession(-1) }},
	{id: "nextSite", label: locales.ShortcutNextSite, mac: "Alt+Super+Right", other: "Ctrl+Shift+PageDown", do: func(m *Model) { m.cycleSite(1) }},
	{id: "prevSite", label: locales.ShortcutPrevSite, mac: "Alt+Super+Left", other: "Ctrl+Shift+PageUp", do: func(m *Model) { m.cycleSite(-1) }},
}

// settingsShortcut opens the settings, as is the system's custom: it is
// not one to change.
var settingsShortcut = hotkeyString(ui.Cmd, ",")

func (a shortcutAction) byDefault() string {
	if runtime.GOOS == "darwin" {
		return a.mac
	}
	return a.other
}

func actionOf(id string) (shortcutAction, bool) {
	for _, a := range shortcutActions {
		if a.id == id {
			return a, true
		}
	}
	return shortcutAction{}, false
}

// shortcut returns the shortcut of the action id: the one the user set,
// else its own; "" for none.
func (m *Model) shortcut(id string) string {
	if acc, ok := m.store.Shortcut(id); ok {
		return acc
	}
	a, _ := actionOf(id)
	return a.byDefault()
}

// shortcutLabel is the shortcut of the action id as the system's menus
// show it, "" for none.
func (m *Model) shortcutLabel(id string) string {
	if acc := m.shortcut(id); acc != "" {
		return hotkeyLabel(acc)
	}
	return ""
}

// handleShortcuts does the actions whose shortcuts are pressed.
func (m *Model) handleShortcuts(c *ui.Context) {
	for _, a := range shortcutActions {
		if mods, key, ok := parseHotkey(m.shortcut(a.id)); ok && c.Shortcut(mods, key) && !c.ShortcutRepeated() {
			a.do(m)
		}
	}
	if c.Shortcut(ui.Cmd, ui.KeyComma) && !c.ShortcutRepeated() {
		m.OpenSettings()
	}
}

// TerminalKey does the action whose shortcut a terminal sees pressed,
// and reports whether there is one: a terminal sends the keys it does
// not leave to the window to the shell, as Control's elsewhere than on
// macOS.
func (m *Model) TerminalKey(mods ui.Modifiers, key ui.Key) bool {
	acc := hotkeyString(mods, keyName(key))
	if m.settings.recording != "" {
		return false
	}
	for _, a := range shortcutActions {
		if m.shortcut(a.id) == acc {
			a.do(m)
			return true
		}
	}
	if acc == settingsShortcut {
		m.OpenSettings()
		return true
	}
	return false
}

// cycleSession shows the terminal d tabs after the one shown in the tab
// of the window, going round.
func (m *Model) cycleSession(d int) {
	s := m.current()
	if s == nil || len(s.sessions) < 2 {
		return
	}
	n := len(s.sessions)
	s.current = ((s.current+d)%n + n) % n
	s.sessions[s.current].refocus = true
}

// cycleSite shows the tab of the window d after the one shown, going
// round, with the keyboard in its terminal.
func (m *Model) cycleSite(d int) {
	n := len(m.sites)
	if n < 2 {
		return
	}
	m.selectSite(((m.active+d)%n + n) % n)
	m.FocusTerminal()
}

// FocusTerminal gives the keyboard to the terminal shown, if any, as the
// window shows on the hotkey.
func (m *Model) FocusTerminal() {
	if s := m.current(); s != nil && s.current >= 0 && s.current < len(s.sessions) {
		s.sessions[s.current].refocus = true
	}
}

// parseHotkey reads an accelerator hotkeyString wrote: "Ctrl+Shift+T".
func parseHotkey(acc string) (ui.Modifiers, ui.Key, bool) {
	if acc == "" {
		return 0, 0, false
	}
	parts := strings.Split(acc, "+")
	name := parts[len(parts)-1]
	if name == "" && len(parts) > 1 { // "Ctrl++"
		name, parts = "+", parts[:len(parts)-2]
	} else {
		parts = parts[:len(parts)-1]
	}
	var mods ui.Modifiers
	for _, p := range parts {
		switch p {
		case "Ctrl":
			mods |= ui.Ctrl
		case "Alt":
			mods |= ui.Alt
		case "Shift":
			mods |= ui.Shift
		case "Super":
			mods |= ui.Super
		default:
			return 0, 0, false
		}
	}
	key, ok := keyNamed(name)
	return mods, key, ok
}

// keyNamed is the key keyName names.
func keyNamed(name string) (ui.Key, bool) {
	switch {
	case len(name) == 1 && name[0] >= 'A' && name[0] <= 'Z':
		return ui.KeyA + ui.Key(name[0]-'A'), true
	case len(name) == 1 && name[0] >= '0' && name[0] <= '9':
		return ui.Key0 + ui.Key(name[0]-'0'), true
	case len(name) > 1 && name[0] == 'F':
		if n, err := strconv.Atoi(name[1:]); err == nil && n >= 1 && n <= 12 {
			return ui.KeyF1 + ui.Key(n-1), true
		}
	}
	for k, n := range namedKeys {
		if n == name {
			return k, true
		}
	}
	return 0, false
}
