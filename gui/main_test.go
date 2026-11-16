package gui

import (
	"os"
	"testing"

	"github.com/egoist/mygo/i18n"

	_ "ternssh-desktop/locales"
)

// testLocale is the language the tests run the app in: they find its
// controls by their Chinese labels.
var testLocale = "zh-Hans"

func TestMain(m *testing.M) {
	i18n.SetLocaleFunc(func() string { return testLocale })
	os.Exit(m.Run())
}
