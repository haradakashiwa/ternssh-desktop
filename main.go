// Command ternssh-desktop is the desktop client of ternssh: a window of
// native UI drawn by MyGo, with a tab of servers kept on this machine and
// one for each ternssh site they are synced with, which open in terminals
// this machine connects with SSH.
//
//	go tool mygo dev     # development, with live reload
//	go tool mygo build   # the app bundle and disk image in build/
package main

import (
	_ "embed"
	"log"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"ternssh-desktop/gui"
	"ternssh-desktop/internal/sshconn"
	"ternssh-desktop/internal/store"
)

// trayIcon is the swallow in black, which the menu bar and the taskbar
// tint to theirs.
//
//go:embed resources/tray.png
var trayIcon []byte

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
		var model *gui.Model
		// show brings the window back from the tray, and hide puts it
		// there: on macOS, the app leaves the Dock while its window is
		// hidden, and lives in the menu bar alone.
		show := func() {
			if runtime.GOOS == "darwin" {
				mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)
			}
			if win.IsMinimized() {
				win.Restore()
			}
			win.Show()
			win.Focus()
			mygo.App.Focus()
		}
		hide := func() {
			win.Hide()
			if runtime.GOOS == "darwin" {
				mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
			}
		}
		toggle := func() {
			if win.IsVisible() && !win.IsMinimized() && mygo.App.IsActive() {
				hide()
			} else {
				show()
			}
		}
		var hotkey struct {
			sync.Mutex
			acc string
		}
		model = gui.New(st, gui.Backend{
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
			PickDir: func(start string) (string, error) {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: "选择起始目录", DefaultPath: start, Directory: true, CreateDirectories: true, ShowHiddenFiles: true})
				if len(paths) == 0 {
					return "", err
				}
				return paths[0], err
			},
			OpenPath:     mygo.Shell.OpenPath,
			ShowInFolder: mygo.Shell.ShowItemInFolder,
			PickFiles: func() ([]string, error) {
				return mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: "选择要上传的文件", Multiple: true, ShowHiddenFiles: true})
			},
			SaveFile: func(name string) (string, error) {
				return mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: win, Title: "下载到", DefaultPath: name, CreateDirectories: true})
			},
			PickFile: func() (string, error) {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: "选择私钥文件", ShowHiddenFiles: true})
				if len(paths) == 0 {
					return "", err
				}
				return paths[0], err
			},
			SetHotkey: func(acc string) error {
				hotkey.Lock()
				defer hotkey.Unlock()
				if hotkey.acc != "" {
					mygo.GlobalShortcut.Unregister(hotkey.acc)
					hotkey.acc = ""
				}
				if acc == "" {
					return nil
				}
				if err := mygo.GlobalShortcut.Register(acc, toggle); err != nil {
					return err
				}
				hotkey.acc = acc
				return nil
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
		// Closing the window keeps the app in the tray, unless the settings
		// say to quit, or the app is quitting.
		quitting := false
		mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { quitting = true })
		win.OnClose(func(e *mygo.CloseEvent) {
			if quitting || st.Settings().QuitOnClose {
				return
			}
			e.PreventDefault()
			hide()
		})
		win.OnClosed(func() {
			model.Close()
			mygo.App.Quit()
		})
		mygo.App.OnActivate(func(visible bool) {
			if !visible {
				show() // the Dock icon clicked
			}
		})
		mygo.App.OnSecondInstance(func([]string, string) { show() })

		_, err := mygo.NewTray(mygo.TrayOptions{
			ID:             "ternssh",
			Icon:           trayIcon,
			IconIsTemplate: true,
			ToolTip:        "ternssh",
			Menu: mygo.NewMenu([]*mygo.MenuItem{
				{Label: "显示 ternssh", Click: func(*mygo.MenuItem, *mygo.Window) { show() }},
				{Label: "设置…", Click: func(*mygo.MenuItem, *mygo.Window) {
					show()
					win.Update(model.OpenSettings)
				}},
				mygo.Separator(),
				{Label: "退出 ternssh", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
			}),
		})
		if err != nil {
			log.Print("tray: ", err)
		}
		model.Start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
