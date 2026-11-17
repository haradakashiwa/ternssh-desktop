package gui

import (
	"errors"
	"runtime"
	"testing"

	"github.com/egoist/mygo/ui"

	"ternssh-desktop/internal/store"
)

func TestSettings(t *testing.T) {
	m, st := testModel(t)
	var registered []string
	m.backend.SetHotkey = func(acc string) error {
		if acc == "Ctrl+Alt+Super+K" {
			return errors.New("taken")
		}
		registered = append(registered, acc)
		return nil
	}
	tt := ui.NewTester(m.View, 1100, 700)

	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	if !m.settingsOpen || !tt.HasText("关闭窗口时") {
		t.Fatal("the settings are not shown")
	}
	snapshot(t, tt, "settings")
	// Where quick commands go is saved as it is chosen.
	m.settings.target = 2
	tt.Frame()
	if got := st.Settings().CommandTarget; got != store.CommandTargetAsk {
		t.Fatalf("command target %q", got)
	}
	m.settings.target = 0
	tt.Frame()
	if err := tt.Click("快捷键"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("未设置") || !tt.HasText(hotkeyLabel(shortcutActions[0].byDefault())) {
		t.Fatalf("the shortcuts are not shown: %q", tt.Texts())
	}
	snapshot(t, tt, "settings-shortcuts")

	// A key alone is no hotkey; one with a modifier is.
	if err := tt.Click("快速唤出快捷键"); err != nil {
		t.Fatal(err)
	}
	if m.settings.recording != hotkeyID || !tt.HasText("请按下快捷键…") {
		t.Fatal("not recording")
	}
	tt.Key(0, ui.KeyK)
	if m.settings.recording == "" || m.settings.err == "" {
		t.Fatalf("a key alone taken, error %q", m.settings.err)
	}
	tt.Key(ui.Alt, ui.KeySpace)
	if got := st.Settings().Hotkey; got != "Alt+Space" || m.settings.recording != "" || m.settings.err != "" {
		t.Fatalf("hotkey %q, recording %v, error %q", got, m.settings.recording, m.settings.err)
	}
	tt.Frame()
	if !tt.HasText(hotkeyLabel("Alt+Space")) {
		t.Fatalf("hotkey not shown: %q", tt.Texts())
	}

	// One the system keeps fails, and the hotkey before stays.
	tt.Click("快速唤出快捷键")
	tt.Key(ui.Ctrl|ui.Alt|ui.Super, ui.KeyK)
	if st.Settings().Hotkey != "Alt+Space" || m.settings.err == "" {
		t.Fatalf("hotkey %q, error %q", st.Settings().Hotkey, m.settings.err)
	}
	// Escape cancels, and puts the hotkey back.
	tt.Click("快速唤出快捷键")
	tt.Key(0, ui.KeyEscape)
	if m.settings.recording != "" || st.Settings().Hotkey != "Alt+Space" || registered[len(registered)-1] != "Alt+Space" {
		t.Fatalf("recording %v, registered %q", m.settings.recording, registered)
	}
	// While recording, the window's shortcuts are the hotkey's, but for
	// one that is another's.
	tt.Click("快速唤出快捷键")
	tt.Key(newSessionMods, ui.KeyT)
	if st.Settings().Hotkey != "Alt+Space" || m.settings.err != "Ctrl+Shift+T 已用于「新建终端」" && m.settings.err != "⌘T 已用于「新建终端」" {
		t.Fatalf("hotkey %q, error %q", st.Settings().Hotkey, m.settings.err)
	}
	tt.Key(ui.Ctrl|ui.Alt, ui.KeyJ)
	if got := st.Settings().Hotkey; got != "Ctrl+Alt+J" {
		t.Fatalf("hotkey %q", got)
	}

	if err := tt.Click("清除快速唤出快捷键"); err != nil {
		t.Fatal(err)
	}
	if st.Settings().Hotkey != "" || registered[len(registered)-1] != "" {
		t.Fatalf("hotkey %q, registered %q", st.Settings().Hotkey, registered)
	}

	// Closing the window quits, as chosen.
	if err := tt.Click("通用"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click(minimizeLabel()); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("退出应用"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !st.Settings().QuitOnClose {
		t.Fatal("quit on close not saved")
	}
	tt.Click("完成")
	if m.settingsOpen {
		t.Fatal("still open")
	}
	tt.Key(ui.Cmd, ui.KeyComma)
	if !m.settingsOpen || m.settings.closeAction != 1 {
		t.Fatal("the shortcut does not open the settings as saved")
	}
}

func TestHotkeyNames(t *testing.T) {
	if ui.KeyZ-ui.KeyA != 25 || ui.Key9-ui.Key0 != 9 || ui.KeyF12-ui.KeyF1 != 11 {
		t.Fatal("the keys are not in order")
	}
	for k, want := range map[ui.Key]string{ui.KeyA: "A", ui.KeyZ: "Z", ui.Key0: "0", ui.KeyF1: "F1", ui.KeyF12: "F12", ui.KeySpace: "Space", ui.KeyComma: ",", ui.KeyEscape: ""} {
		if got := keyName(k); got != want {
			t.Errorf("keyName(%v) = %q, want %q", k, got, want)
		}
	}
	if got := hotkeyString(ui.Super|ui.Shift|ui.Ctrl, "K"); got != "Ctrl+Shift+Super+K" {
		t.Errorf("hotkeyString = %q", got)
	}
	want := map[string]string{"Ctrl+Shift+Super+K": "Ctrl+Shift+Win+K", "Alt+Up": "Alt+Up", "Ctrl++": "Ctrl++"}
	if runtime.GOOS == "darwin" {
		want = map[string]string{"Ctrl+Shift+Super+K": "⌃⇧⌘K", "Alt+Up": "⌥↑", "Ctrl++": "⌃+"}
	}
	for acc, w := range want {
		if got := hotkeyLabel(acc); got != w {
			t.Errorf("hotkeyLabel(%q) = %q, want %q", acc, got, w)
		}
	}
}
