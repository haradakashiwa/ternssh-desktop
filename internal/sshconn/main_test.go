package sshconn

import (
	"os"
	"testing"

	"github.com/egoist/mygo/i18n"

	_ "ternssh-desktop/locales"
)

// The tests read the messages of the package in Chinese.
func TestMain(m *testing.M) {
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	os.Exit(m.Run())
}
