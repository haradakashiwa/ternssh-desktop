package store

import (
	"os"
	"path/filepath"
)

// removeHistories deletes the terminal histories that earlier versions
// kept next to the config file: what terminals showed, SSH sessions'
// output among it, which the app no longer keeps.
func removeHistories(configPath string) {
	if configPath != "" {
		os.RemoveAll(filepath.Join(filepath.Dir(configPath), "history"))
	}
}
