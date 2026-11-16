package gui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestLanguage chooses English in the settings, which the app then shows.
func TestLanguage(t *testing.T) {
	m, st := testModel(t)
	var chosen []string
	m.backend.SetLanguage = func(tag string) {
		chosen = append(chosen, tag)
		testLocale = tag
		if tag == "" {
			testLocale = "zh-Hans" // the tests' system language
		}
	}
	t.Cleanup(func() { testLocale = "zh-Hans" })
	tt := ui.NewTester(m.View, 1100, 700)

	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("English"); err != nil {
		t.Fatal(err)
	}
	if st.Settings().Language != "en" || len(chosen) != 1 || chosen[0] != "en" {
		t.Fatalf("language %q, chosen %q", st.Settings().Language, chosen)
	}
	tt.Frame()
	for _, s := range []string{"Settings", "Language", "Done", "Local"} {
		if !tt.HasText(s) {
			t.Fatalf("%q not shown in English: %q", s, tt.Texts())
		}
	}
	snapshot(t, tt, "settings-en")
	if err := tt.Click("Done"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapshot(t, tt, "main-en")

	// Opened again, the dialog shows the language chosen; the system's
	// is chosen back.
	m.OpenSettings()
	tt.Frame()
	if m.settings.language != 1 {
		t.Fatalf("language shown %d", m.settings.language)
	}
	if err := tt.Click("System"); err != nil {
		t.Fatal(err)
	}
	if st.Settings().Language != "" || chosen[len(chosen)-1] != "" {
		t.Fatalf("language %q, chosen %q", st.Settings().Language, chosen)
	}
	tt.Frame()
	if !tt.HasText("语言") {
		t.Fatalf("not shown in Chinese again: %q", tt.Texts())
	}
}
