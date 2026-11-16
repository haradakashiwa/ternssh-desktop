package gui

import (
	"runtime"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// settingsForm is the state of the settings dialog.
type settingsForm struct {
	recording   bool // waiting for the keys of the hotkey
	err         string
	closeAction int // 0 keeps the app in the tray, 1 quits
}

// OpenSettings shows the settings dialog.
func (m *Model) OpenSettings() {
	m.settings = settingsForm{}
	if m.store.Settings().QuitOnClose {
		m.settings.closeAction = 1
	}
	m.settingsOpen = true
}

// trayName is what the system calls the place of the app's icon.
func trayName() string {
	if runtime.GOOS == "darwin" {
		return "菜单栏"
	}
	return "托盘"
}

func (m *Model) settingsDialog(c *ui.Context) {
	f := &m.settings
	if !m.settingsOpen {
		if f.recording { // closed while recording
			m.stopRecording()
		}
		return
	}
	dialog(c, &m.settingsOpen, 460, func() {
		dialogTitle(c, "设置", "")
		ui.Field(c, "快速唤出", func() {
			ui.Column(c).Gap(6).Children(func() {
				m.hotkeyField(c)
				ui.Text(c, "在任何应用中按下这个快捷键，显示或隐藏 ternssh 窗口。").
					FontSize(11).TextColor(c.Theme().TextMuted)
				errorText(c, f.err)
			})
		})
		ui.Field(c, "关闭窗口时", func() {
			ui.Segmented(c, &f.closeAction, "最小化到"+trayName(), "退出应用").Label("关闭窗口时")
		})
		if st := m.store.Settings(); st.QuitOnClose != (f.closeAction == 1) {
			st.QuitOnClose = f.closeAction == 1
			if err := m.store.SetSettings(st); err != nil {
				f.err = "保存失败：" + err.Error()
			}
		}
		ui.Row(c).Justify(ui.End).Children(func() {
			if ui.PrimaryButton(c, "完成").Clicked() {
				m.settingsOpen = false
				m.stopRecording()
			}
		})
	})
}

// hotkeyField shows the hotkey, and records another once clicked.
func (m *Model) hotkeyField(c *ui.Context) {
	t := c.Theme()
	f := &m.settings
	hotkey := m.store.Settings().Hotkey
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		box := ui.ButtonBase(c).Grow(1).Height(30).Padding(0, 10).Radius(t.Radius).
			AlignItems(ui.Center).Label("快速唤出快捷键").Background(t.Surface).Border(1, t.Border)
		if f.recording {
			box.Border(1, t.Accent).Focus().HandleInput(m.recordKey)
		} else if box.Clicked() {
			m.startRecording()
		}
		box.Children(func() {
			switch {
			case f.recording:
				ui.Text(c, "请按下快捷键…").TextColor(accentInk(c))
			case hotkey == "":
				ui.Text(c, "未设置").TextColor(t.TextMuted)
			default:
				ui.Text(c, hotkeyLabel(hotkey)).Bold()
			}
		})
		switch {
		case f.recording:
			if ui.Button(c, "取消").Clicked() {
				m.stopRecording()
			}
		case hotkey != "":
			if ui.Button(c, "清除").Clicked() {
				m.setHotkey("")
			}
		}
	})
}

// startRecording waits for the keys of a hotkey, without the one set,
// which would otherwise hide the window as it is pressed again.
func (m *Model) startRecording() {
	m.settings.recording, m.settings.err = true, ""
	m.registerHotkey("")
}

// stopRecording stops waiting and puts the hotkey set back.
func (m *Model) stopRecording() {
	if !m.settings.recording {
		return
	}
	m.settings.recording = false
	if err := m.registerHotkey(m.store.Settings().Hotkey); err != nil {
		m.settings.err = err.Error()
	}
}

// recordKey takes the keys while the hotkey is recorded: Escape cancels,
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
	switch {
	case ev.Mods == 0 && ev.Key == ui.KeyEscape:
		m.stopRecording()
		return true
	case ev.Mods == 0 && (ev.Key == ui.KeyBackspace || ev.Key == ui.KeyDelete):
		m.settings.recording = false
		m.setHotkey("")
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
		m.settings.err = "快捷键需要包含 " + modifierHint() + " 之一"
		return true
	}
	m.settings.recording = false
	m.setHotkey(hotkeyString(ev.Mods, name))
	return true
}

// setHotkey registers acc, empty for none, and saves it once registered.
func (m *Model) setHotkey(acc string) {
	f := &m.settings
	st := m.store.Settings()
	if err := m.registerHotkey(acc); err != nil {
		f.err = "无法使用 " + hotkeyLabel(acc) + "，它可能已被系统或其他应用占用（" + err.Error() + "）"
		m.registerHotkey(st.Hotkey)
		return
	}
	f.err = ""
	st.Hotkey = acc
	if err := m.store.SetSettings(st); err != nil {
		f.err = "保存失败：" + err.Error()
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
			m.notify("快速唤出快捷键 " + hotkeyLabel(acc) + " 无法注册：" + err.Error())
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
		return "⌘、⌥、⌃"
	}
	return "Ctrl、Alt、Win"
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
