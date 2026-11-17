package gui

import (
	"runtime"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"ternssh-desktop/internal/store"
	"ternssh-desktop/locales"
)

// settingsForm is the state of the settings dialog.
type settingsForm struct {
	tab int // settingsGeneral or settingsShortcuts
	// recording is what the keys pressed become: hotkeyID for the
	// hotkey, or the ID of an action of the window; "" while none is.
	recording   string
	err         string
	closeAction int // 0 keeps the app in the tray, 1 quits
	language    int // of languages
	target      int // of commandTargets
}

// The tabs of the settings.
const (
	settingsGeneral = iota
	settingsShortcuts
)

// hotkeyID is the hotkey's in settingsForm.recording.
const hotkeyID = "hotkey"

// languages are the languages the app is written in, by tag: "" follows
// the system's.
var languages = []string{"", "en", "zh-Hans"}

// commandTargets are where a quick command clicked may be sent.
var commandTargets = []string{store.CommandTargetCurrent, store.CommandTargetAll, store.CommandTargetAsk}

func commandTargetLabels() []string {
	return []string{locales.SettingsCommandTargetCurrent(), locales.SettingsCommandTargetAll(), locales.SettingsCommandTargetAsk()}
}

// languageLabels name the languages, each in itself but the system's.
func languageLabels() []string {
	return []string{locales.SettingsLanguageSystem(), "English", "简体中文"}
}

// OpenSettings shows the settings dialog.
func (m *Model) OpenSettings() {
	m.settings = settingsForm{}
	st := m.store.Settings()
	if st.QuitOnClose {
		m.settings.closeAction = 1
	}
	for i, tag := range languages {
		if tag == st.Language {
			m.settings.language = i
		}
	}
	for i, target := range commandTargets {
		if target == st.CommandTarget {
			m.settings.target = i
		}
	}
	m.settingsOpen = true
}

// minimizeLabel is the choice of keeping the app in the place the system
// keeps the app's icon.
func minimizeLabel() string {
	if runtime.GOOS == "darwin" {
		return locales.SettingsMinimizeToMenuBar()
	}
	return locales.SettingsMinimizeToTray()
}

func (m *Model) settingsDialog(c *ui.Context) {
	f := &m.settings
	if !m.settingsOpen {
		if f.recording != "" { // closed while recording
			m.stopRecording()
		}
		return
	}
	dialog(c, &m.settingsOpen, 500, func() {
		dialogTitle(c, locales.SettingsTitle(), "")
		if ui.Tabs(c, &f.tab, locales.SettingsGeneral(), locales.SettingsShortcuts()).Changed() {
			m.stopRecording()
			f.err = ""
		}
		switch f.tab {
		case settingsGeneral:
			m.generalSettings(c)
		case settingsShortcuts:
			m.shortcutSettings(c)
		}
		ui.Row(c).Justify(ui.End).Children(func() {
			if ui.PrimaryButton(c, locales.Done()).Clicked() {
				m.settingsOpen = false
				m.stopRecording()
			}
		})
	})
}

// choose is a drop-down of labels, whose index *i is the one chosen.
func choose(c *ui.Context, i *int, label string, labels []string) {
	chosen := labels[*i]
	ui.Select(c, &chosen, labels).Label(label)
	for j, l := range labels {
		if l == chosen {
			*i = j
		}
	}
}

func (m *Model) generalSettings(c *ui.Context) {
	f := &m.settings
	ui.Form(c, func() {
		ui.Field(c, locales.SettingsOnClose(), func() {
			choose(c, &f.closeAction, locales.SettingsOnClose(), []string{minimizeLabel(), locales.SettingsQuitApp()})
		})
		ui.Field(c, locales.SettingsLanguage(), func() {
			choose(c, &f.language, locales.SettingsLanguage(), languageLabels())
		})
		ui.Field(c, locales.SettingsCommandTarget(), func() {
			ui.Column(c).Gap(6).Children(func() {
				choose(c, &f.target, locales.SettingsCommandTarget(), commandTargetLabels())
				ui.Text(c, locales.SettingsCommandTargetHint()).FontSize(11).TextColor(c.Theme().TextMuted)
			})
		})
	})
	errorText(c, f.err)
	if st := m.store.Settings(); st.QuitOnClose != (f.closeAction == 1) || st.Language != languages[f.language] ||
		st.CommandTarget != commandTargets[f.target] {
		changed := st.Language != languages[f.language]
		st.QuitOnClose, st.Language = f.closeAction == 1, languages[f.language]
		st.CommandTarget = commandTargets[f.target]
		if err := m.store.SetSettings(st); err != nil {
			f.err = locales.SettingsErrSave(err.Error())
		}
		if changed && m.backend.SetLanguage != nil {
			m.backend.SetLanguage(st.Language)
		}
	}
}

func (m *Model) shortcutSettings(c *ui.Context) {
	t := c.Theme()
	f := &m.settings
	ui.Form(c, func() {
		ui.Field(c, locales.SettingsHotkey(), func() {
			ui.Column(c).Gap(6).Children(func() {
				m.hotkeyField(c, hotkeyID, locales.HotkeyFieldLabel(), m.store.Settings().Hotkey)
				ui.Text(c, locales.SettingsHotkeyHint()).
					FontSize(11).TextColor(t.TextMuted)
			})
		})
		for _, a := range shortcutActions {
			ui.Field(c, a.label(), func() {
				m.hotkeyField(c, a.id, locales.ShortcutFieldLabel(a.label()), m.shortcut(a.id))
			})
		}
	})
	errorText(c, f.err)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, locales.ShortcutSettingsHint(hotkeyLabel(settingsShortcut))).
			FontSize(11).TextColor(t.TextMuted).Grow(1).MinWidth(0)
		if ui.Button(c, locales.ShortcutReset()).Clicked() {
			m.stopRecording()
			f.err = ""
			if err := m.store.ResetShortcuts(); err != nil {
				f.err = locales.SettingsErrSave(err.Error())
			}
		}
	})
}

// hotkeyField shows the shortcut acc of id, the hotkey or an action of
// the window, and records another once clicked.
func (m *Model) hotkeyField(c *ui.Context, id, label, acc string) {
	t := c.Theme()
	f := &m.settings
	recording := f.recording == id
	ui.Row(c).Key("hk:" + id).Gap(8).AlignItems(ui.Center).Children(func() {
		box := ui.ButtonBase(c).Grow(1).Height(30).Padding(0, 10).Radius(t.Radius).
			AlignItems(ui.Center).Label(label).Background(t.Surface).Border(1, t.Border)
		if recording {
			box.Border(1, t.Accent).Focus().HandleInput(m.recordKey)
		} else if box.Clicked() {
			m.startRecording(id)
		}
		box.Children(func() {
			switch {
			case recording:
				ui.Text(c, locales.HotkeyRecording()).TextColor(accentInk(c))
			case acc == "":
				ui.Text(c, locales.HotkeyNotSet()).TextColor(t.TextMuted)
			default:
				ui.Text(c, hotkeyLabel(acc)).Bold()
			}
		})
		switch {
		case recording:
			if ui.Button(c, locales.Cancel()).Clicked() {
				m.stopRecording()
			}
		case acc != "":
			if ui.Button(c, locales.HotkeyClear()).Label(locales.HotkeyClearLabel(label)).Clicked() {
				m.setShortcut(id, "")
			}
		}
	})
}

// startRecording waits for the keys of the shortcut of id, without the
// hotkey set, which would otherwise hide the window as it is pressed.
func (m *Model) startRecording(id string) {
	m.stopRecording()
	m.settings.recording, m.settings.err = id, ""
	m.registerHotkey("")
}

// stopRecording stops waiting and puts the hotkey set back.
func (m *Model) stopRecording() {
	if m.settings.recording == "" {
		return
	}
	m.settings.recording = ""
	if err := m.registerHotkey(m.store.Settings().Hotkey); err != nil {
		m.settings.err = err.Error()
	}
}

// recordKey takes the keys while a shortcut is recorded: Escape cancels,
// Backspace clears it, and a key with modifiers becomes it.
func (m *Model) recordKey(ev ui.InputEvent) bool {
	switch ev.Kind {
	case ui.InputKeyDown:
	case ui.InputKeyUp, ui.InputText, ui.InputCompose, ui.InputCommand:
		return true
	default:
		return false
	}
	if ev.Repeat {
		return true
	}
	id := m.settings.recording
	switch {
	case ev.Mods == 0 && ev.Key == ui.KeyEscape:
		m.stopRecording()
		return true
	case ev.Mods == 0 && (ev.Key == ui.KeyBackspace || ev.Key == ui.KeyDelete):
		m.stopRecording()
		m.setShortcut(id, "")
		return true
	}
	name := keyName(ev.Key)
	if name == "" {
		name = keyName(ev.PhysicalKey)
	}
	if name == "" {
		return true // a modifier on its own, or a key with no name
	}
	if ev.Mods&^ui.Shift == 0 && !strings.HasPrefix(name, "F") {
		m.settings.err = locales.HotkeyErrModifier(modifierHint())
		return true
	}
	acc := hotkeyString(ev.Mods, name)
	if other := m.shortcutUsing(acc, id); other != "" {
		m.settings.err = locales.ShortcutErrTaken(hotkeyLabel(acc), other)
		return true
	}
	m.stopRecording()
	m.setShortcut(id, acc)
	return true
}

// shortcutUsing names what has the shortcut acc but id, "" for none.
func (m *Model) shortcutUsing(acc, id string) string {
	if id != hotkeyID && m.store.Settings().Hotkey == acc {
		return locales.SettingsHotkey()
	}
	for _, a := range shortcutActions {
		if a.id != id && m.shortcut(a.id) == acc {
			return a.label()
		}
	}
	if acc == settingsShortcut {
		return locales.SettingsTitle()
	}
	return ""
}

// setShortcut saves acc, "" for none, as the shortcut of id: the hotkey,
// once registered, or an action of the window.
func (m *Model) setShortcut(id, acc string) {
	f := &m.settings
	f.err = ""
	if id != hotkeyID {
		if err := m.store.SetShortcut(id, acc); err != nil {
			f.err = locales.SettingsErrSave(err.Error())
		}
		return
	}
	st := m.store.Settings()
	if err := m.registerHotkey(acc); err != nil {
		f.err = locales.HotkeyErrInUse(hotkeyLabel(acc), err.Error())
		m.registerHotkey(st.Hotkey)
		return
	}
	st.Hotkey = acc
	if err := m.store.SetSettings(st); err != nil {
		f.err = locales.SettingsErrSave(err.Error())
	}
}

func (m *Model) registerHotkey(acc string) error {
	if m.backend.SetHotkey == nil {
		return nil
	}
	return m.backend.SetHotkey(acc)
}

// startHotkey registers the hotkey saved, as the app starts.
func (m *Model) startHotkey() {
	if acc := m.store.Settings().Hotkey; acc != "" {
		if err := m.registerHotkey(acc); err != nil {
			m.notify(locales.HotkeyErrRegister(hotkeyLabel(acc), err.Error()))
		}
	}
}

// hotkeyString writes a key with modifiers as an accelerator:
// "Ctrl+Alt+Shift+Super+K".
func hotkeyString(mods ui.Modifiers, key string) string {
	var parts []string
	for _, m := range []struct {
		mod  ui.Modifiers
		name string
	}{{ui.Ctrl, "Ctrl"}, {ui.Alt, "Alt"}, {ui.Shift, "Shift"}, {ui.Super, "Super"}} {
		if mods&m.mod != 0 {
			parts = append(parts, m.name)
		}
	}
	return strings.Join(append(parts, key), "+")
}

// hotkeyLabel writes an accelerator as the system's menus do: "⌥Space"
// on macOS, "Ctrl+Alt+K" elsewhere.
func hotkeyLabel(acc string) string {
	parts := strings.Split(acc, "+")
	key := parts[len(parts)-1]
	if key == "" && len(parts) > 1 { // "Ctrl++"
		key, parts = "+", parts[:len(parts)-2]
	} else {
		parts = parts[:len(parts)-1]
	}
	if runtime.GOOS != "darwin" {
		for i, p := range parts {
			if p == "Super" {
				parts[i] = "Win"
			}
		}
		return strings.Join(append(parts, key), "+")
	}
	var b strings.Builder
	for _, sym := range []struct{ name, sym string }{{"Ctrl", "⌃"}, {"Alt", "⌥"}, {"Shift", "⇧"}, {"Super", "⌘"}} {
		for _, p := range parts {
			if p == sym.name {
				b.WriteString(sym.sym)
			}
		}
	}
	switch key {
	case "Up":
		key = "↑"
	case "Down":
		key = "↓"
	case "Left":
		key = "←"
	case "Right":
		key = "→"
	case "Enter":
		key = "↩"
	}
	b.WriteString(key)
	return b.String()
}

func modifierHint() string {
	if runtime.GOOS == "darwin" {
		return locales.HotkeyModifiersMac()
	}
	return locales.HotkeyModifiersOther()
}

// keyName is a key's name in an accelerator, "" for one it has none for.
func keyName(k ui.Key) string {
	switch {
	case k >= ui.KeyA && k <= ui.KeyZ:
		return string(rune('A' + k - ui.KeyA))
	case k >= ui.Key0 && k <= ui.Key9:
		return string(rune('0' + k - ui.Key0))
	case k >= ui.KeyF1 && k <= ui.KeyF12:
		return "F" + strconv.Itoa(int(k-ui.KeyF1)+1)
	}
	return namedKeys[k]
}

var namedKeys = map[ui.Key]string{
	ui.KeySpace: "Space", ui.KeyEnter: "Enter", ui.KeyTab: "Tab",
	ui.KeyInsert: "Insert", ui.KeyHome: "Home", ui.KeyEnd: "End",
	ui.KeyPageUp: "PageUp", ui.KeyPageDown: "PageDown",
	ui.KeyUp: "Up", ui.KeyDown: "Down", ui.KeyLeft: "Left", ui.KeyRight: "Right",
	ui.KeyMinus: "-", ui.KeyEqual: "=", ui.KeyComma: ",", ui.KeyPeriod: ".",
	ui.KeySlash: "/", ui.KeySemicolon: ";", ui.KeyQuote: "'",
	ui.KeyBracketLeft: "[", ui.KeyBracketRight: "]", ui.KeyBackslash: `\`, ui.KeyBackquote: "`",
}
