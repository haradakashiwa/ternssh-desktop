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
	"github.com/egoist/mygo/plugins/updater"
	updaterui "github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"

	"ternssh-desktop/gui"
	"ternssh-desktop/internal/sshconn"
	"ternssh-desktop/internal/store"
	"ternssh-desktop/locales"
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

	// The language chosen in the settings, or the system's.
	mygo.App.SetLocale(st.Settings().Language)

	// New versions come from the releases on GitHub (updates in
	// mygo.json): checked in the background once a day, and installed
	// from the update window, which relaunches the app.
	mygo.Use(updaterui.Plugin)

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
			win.Update(model.FocusTerminal)
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
			NewTerminal: func(conn gui.Conn, ev gui.TermEvents) (*terminal.Terminal, error) {
				return terminal.New(terminal.Options{
					Conn:        conn,
					Transparent: true,
					Padding:     terminal.Padding{X: 10, Y: 8},
					OnKey:       model.TerminalKey,
					OnTitle:     ev.OnTitle,
					OnNotify:    ev.OnNotify,
					OnBell:      ev.OnBell,
				})
			},
			NewShell: func(dir string, ev gui.TermEvents) (*terminal.Terminal, error) {
				return terminal.New(terminal.Options{
					Dir:              dir,
					ShellIntegration: true,
					Transparent:      true,
					Padding:          terminal.Padding{X: 10, Y: 8},
					OnKey:            model.TerminalKey,
					OnTitle:          ev.OnTitle,
					OnNotify:         ev.OnNotify,
					OnBell:           ev.OnBell,
					OnExit:           ev.OnExit,
				})
			},
			Notify: func(title, body string, onClick func()) {
				n := mygo.NewNotification(mygo.NotificationOptions{Title: title, Body: body})
				n.OnClick(func() {
					show()
					win.Update(onClick)
				})
				go n.Show() // the first asks the user to allow them, and waits
			},
			Active:   mygo.App.IsActive,
			SetBadge: mygo.App.SetBadgeCount,
			Dial: func(opts sshconn.LocalOptions, ev sshconn.Events) gui.Conn {
				return sshconn.DialLocal(opts, ev)
			},
			PickDir: func(start string) (string, error) {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: locales.AppPickStartDir(), DefaultPath: start, Directory: true, CreateDirectories: true, ShowHiddenFiles: true})
				if len(paths) == 0 {
					return "", err
				}
				return paths[0], err
			},
			OpenPath:     mygo.Shell.OpenPath,
			ShowInFolder: mygo.Shell.ShowItemInFolder,
			OpenEditor: func(e *gui.Editor) gui.EditorHost {
				w := mygo.NewWindow(mygo.WindowOptions{
					Title:     e.Title,
					Width:     920,
					Height:    680,
					MinWidth:  480,
					MinHeight: 320,
					Content:   ui.View(e.View),
				})
				w.OnClose(func(ev *mygo.CloseEvent) {
					if !e.CanClose() {
						ev.PreventDefault()
					}
				})
				w.OnClosed(e.Closed)
				return gui.EditorHost{
					Update: func(fn func()) { w.Update(fn) },
					Close:  w.Close,
					Focus:  func() { w.Show(); w.Focus() },
				}
			},
			PickFiles: func() ([]string, error) {
				return mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: locales.AppPickUpload(), Multiple: true, ShowHiddenFiles: true})
			},
			SaveFile: func(name string) (string, error) {
				return mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: win, Title: locales.AppDownloadTo(), DefaultPath: name, CreateDirectories: true})
			},
			PickFile: func() (string, error) {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: locales.AppPickKey(), ShowHiddenFiles: true})
				if len(paths) == 0 {
					return "", err
				}
				return paths[0], err
			},
			SetLanguage: mygo.App.SetLocale,
			AccessLogin: accessLogin,
			Version:     mygo.App.Version(),
			Updates: &gui.Updates{
				Check:        updater.CheckForUpdates,
				Automatic:    updater.AutomaticChecks,
				SetAutomatic: updater.SetAutomaticChecks,
				Available:    updater.AvailableVersion,
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
				// Not while an editor has changes to save, which it asks
				// about.
				if !model.CanQuit() {
					e.PreventDefault()
					quitting = false
				}
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

		// appMenu is macOS's menu bar, made again in the language the app
		// shows: MyGo's own but for Command+W, which closes the terminal
		// shown in the window rather than the window, as its shortcut in
		// the settings says. Editors' windows close as before.
		appMenu := func() *mygo.Menu {
			return mygo.NewMenu([]*mygo.MenuItem{
				{Role: mygo.RoleAppMenu},
				{Role: mygo.RoleFileMenu, Submenu: []*mygo.MenuItem{
					{Label: locales.Close(), Accelerator: "Cmd+W", Click: func(_ *mygo.MenuItem, w *mygo.Window) {
						switch w {
						case nil:
						case win:
							win.Update(func() {
								if !model.MenuKey(ui.Super, ui.KeyW) {
									win.Close()
								}
							})
						default:
							w.Close()
						}
					}},
				}},
				{Role: mygo.RoleEditMenu},
				{Role: mygo.RoleViewMenu},
				{Role: mygo.RoleWindowMenu},
			})
		}
		if runtime.GOOS == "darwin" {
			mygo.App.SetMenu(appMenu())
		}

		// trayMenu is made again in the language the app shows.
		trayMenu := func() *mygo.Menu {
			return mygo.NewMenu([]*mygo.MenuItem{
				{Label: locales.TrayShow(), Click: func(*mygo.MenuItem, *mygo.Window) { show() }},
				{Label: locales.TraySettings(), Click: func(*mygo.MenuItem, *mygo.Window) {
					show()
					win.Update(model.OpenSettings)
				}},
				updater.MenuItem(),
				mygo.Separator(),
				{Label: locales.TrayQuit(), Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
			})
		}
		tray, err := mygo.NewTray(mygo.TrayOptions{
			ID:             "ternssh",
			Icon:           trayIcon,
			IconIsTemplate: true,
			ToolTip:        "ternssh",
			Menu:           trayMenu(),
		})
		if err != nil {
			log.Print("tray: ", err)
		}
		// The settings show the version a check found.
		updater.OnChange(func() { win.Update(func() {}) })
		mygo.App.OnLocaleChanged(func(string) {
			if tray != nil {
				tray.SetMenu(trayMenu())
			}
			if runtime.GOOS == "darwin" {
				mygo.App.SetMenu(appMenu())
			}
		})
		model.Start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
