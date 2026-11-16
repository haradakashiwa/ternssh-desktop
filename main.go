// Command ternssh-desktop is the desktop client of ternssh: a window of
// native UI drawn by MyGo, with a tab of servers kept on this machine and
// one for each ternssh site they are synced with, which open in terminals
// this machine connects with SSH.
//
//	go tool mygo dev     # development, with live reload
//	go tool mygo build   # the app bundle and disk image in build/
package main

import (
	"log"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"ternssh-desktop/gui"
	"ternssh-desktop/internal/sshconn"
	"ternssh-desktop/internal/store"
)

func main() {
	mygo.App.SetName("ternssh")
	if !mygo.App.RequestSingleInstanceLock() {
		return // the running instance shows its window
	}

	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		log.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "config.json"), mygo.Secrets)
	if err != nil {
		log.Fatal(err)
	}

	mygo.App.WhenReady(func() {
		var win *mygo.Window
		model := gui.New(st, gui.Backend{
			Update: func(fn func()) { win.Update(fn) },
			NewTerminal: func(conn gui.Conn) (*terminal.Terminal, error) {
				return terminal.New(terminal.Options{
					Conn:        conn,
					Transparent: true,
					Padding:     terminal.Padding{X: 10, Y: 8},
				})
			},
			NewShell: func(dir string, onExit func(int)) (*terminal.Terminal, error) {
				return terminal.New(terminal.Options{
					Dir:              dir,
					ShellIntegration: true,
					Transparent:      true,
					Padding:          terminal.Padding{X: 10, Y: 8},
					OnExit:           onExit,
				})
			},
			Dial: func(opts sshconn.LocalOptions, ev sshconn.Events) gui.Conn {
				return sshconn.DialLocal(opts, ev)
			},
			PickFile: func() (string, error) {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: "选择私钥文件", ShowHiddenFiles: true})
				if len(paths) == 0 {
					return "", err
				}
				return paths[0], err
			},
		})
		win = mygo.NewWindow(mygo.WindowOptions{
			Title:          "ternssh",
			Width:          1200,
			Height:         760,
			MinWidth:       820,
			MinHeight:      520,
			TitleBarStyle:  mygo.TitleBarHiddenInset,
			TitleBarHeight: 52,
			Vibrancy:       mygo.VibrancyUnderWindow,
			Content:        ui.View(model.View),
		})
		win.OnClosed(func() {
			model.Close()
			mygo.App.Quit()
		})
		mygo.App.OnSecondInstance(func([]string, string) { win.Focus() })
		model.Start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
