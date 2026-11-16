package gui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"

	"ternssh-desktop/internal/sshconn"
	"ternssh-desktop/locales"
)

// An SSH session's terminal shows its server's files over SFTP below it
// and its status beside it, both over the session's own connection; each
// can be hidden.

const statusWidth = 320

// statsSource is where the status panel reads a server's status: an
// sshconn.Monitor, which tests replace.
type statsSource interface {
	History() []sshconn.Stats
	Err() error
	Stop()
}

// fileSystem is what the files panel browses: a server's files over
// SFTP (sshconn.Files), or this machine's (sshconn.LocalFiles).
type fileSystem interface {
	Close() error
	Home() (string, error)
	Join(dir, name string) string
	Dir(p string) string
	Base(p string) string
	List(dir string) ([]sshconn.Entry, error)
	Mkdir(p string) error
	Rename(from, to string) error
	Remove(p string) error
	Upload(local, dir string, progress sshconn.Progress) (string, error)
	Download(remote, local string, progress sshconn.Progress) error
}

// filesState is what the files panel of a session shows.
type filesState struct {
	fs        fileSystem
	local     bool // this machine's files, of a local terminal
	opening   bool
	dir       string
	pathInput string
	entries   []sshconn.Entry
	loading   bool
	err       string
	selected  int
	list      ui.ListState
	transfers []*transfer
}

// transfer is a file going up to the server or down from it.
type transfer struct {
	name        string
	up          bool
	done, total atomic.Int64
	last        atomic.Int64 // when it last asked for a frame, in ns
	finished    bool
	err         string
}

// sshClient returns the SSH connection of a session once it is ready.
func sshClient(sess *session) *ssh.Client {
	if c, ok := sess.conn.(interface{ Client() *ssh.Client }); ok {
		return c.Client()
	}
	return nil
}

// hasPanels reports whether a session is on a server, which the panels
// are for.
func (sess *session) hasPanels() bool { return sess.mode != Shell }

// closePanels ends what the panels of a session started.
func closePanels(sess *session) {
	if sess.stats != nil {
		sess.stats.Stop()
	}
	if sess.files != nil && sess.files.fs != nil {
		sess.files.fs.Close()
	}
}

// panelButtons show and hide the panels, at the end of the tab bar.
func (m *Model) panelButtons(c *ui.Context, sess *session) {
	t := c.Theme()
	type button struct {
		shown *bool
		svg   *ui.SVG
		label string
	}
	buttons := []button{{&sess.showBottom, panelBottomIcon, locales.FilesPanelToggle()}}
	if sess.hasPanels() {
		buttons = []button{{&sess.showBottom, panelBottomIcon, locales.FilesPanelToggle()}, {&sess.showStatus, activityIcon, locales.StatsTitle()}}
	}
	for _, b := range buttons {
		btn := ui.ButtonBase(c).Size(28, 26).Radius(7).Center().Label(b.label).Tooltip(b.label)
		color := t.TextMuted
		switch {
		case *b.shown:
			btn.Background(t.Accent.Alpha(0.16))
			color = accentInk(c)
		case btn.Hovered():
			btn.Background(hover(c))
		}
		if btn.Clicked() {
			*b.shown = !*b.shown
		}
		btn.Children(func() { ui.Icon(c, b.svg).Size(15, 15).TextColor(color) })
	}
}

// sessionView is the terminal of a session, with its files and quick
// commands below and its status beside it.
func (m *Model) sessionView(c *ui.Context, sess *session) {
	terminalPane := func() {
		ui.Column(c).Key(sess.id).Fill().MinHeight(0).Children(func() {
			if sess.term != nil {
				v := terminal.View(c, sess.term).Fill().AutoFocus()
				if sess.refocus {
					v.Focus()
					sess.refocus = false
				}
			}
		})
	}
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			if sess.showBottom {
				ui.SplitVerticalPercent(c, &m.filesSplit, terminalPane, func() {
					ui.Column(c).Key(sess.id + "/bottom").Fill().MinHeight(0).Children(func() { m.bottomPanel(c, sess) })
				}).Grow(1).MinHeight(0)
			} else {
				ui.Column(c).Grow(1).MinHeight(0).Children(terminalPane)
			}
		})
		if sess.hasPanels() && sess.showStatus {
			ui.Box(c).Width(1).Background(hairline(c)).Shrink(0)
			ui.Column(c).Key(sess.id + "/status").Width(statusWidth).Shrink(0).MinHeight(0).Children(func() { m.statusPanel(c, sess) })
		}
	})
}

func panelHint(c *ui.Context, text string) {
	ui.Column(c).Grow(1).Center().Padding(24).Children(func() {
		ui.Text(c, text).FontSize(12).TextColor(c.Theme().TextMuted).TextAlign(ui.Center)
	})
}

// --- Files

func (m *Model) filesPanel(c *ui.Context, sess *session) {
	t := c.Theme()
	if sess.files == nil {
		sess.files = &filesState{selected: -1}
	}
	f := sess.files
	if f.fs == nil && sess.mode == Shell {
		// This machine's files, from where the terminal started.
		fs := sshconn.LocalFiles{Start: sess.startDir}
		f.fs, f.local = fs, true
		home, err := fs.Home()
		if err != nil {
			f.err = err.Error()
		}
		m.listDir(f, home)
	}
	if f.fs == nil {
		if f.err != "" {
			panelHint(c, f.err)
			return
		}
		client := sshClient(sess)
		if client == nil {
			panelHint(c, locales.FilesHintConnect())
			return
		}
		if !f.opening {
			f.opening = true
			go func() {
				fs, err := sshconn.OpenFiles(client)
				var home string
				if err == nil {
					home, err = fs.Home()
				}
				m.update(func() {
					f.opening = false
					if err != nil {
						f.err = err.Error()
						return
					}
					f.fs = fs
					m.listDir(f, home)
				})
			}()
		}
		ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c) })
		return
	}

	// The path, which takes another to go to.
	ui.Row(c).Padding(8, 8, 6, 8).Gap(4).AlignItems(ui.Center).Children(func() {
		up := iconButton(c, upIcon, locales.FilesParentFolder()).Disabled(f.fs.Dir(f.dir) == f.dir)
		if up.Clicked() {
			m.listDir(f, f.fs.Dir(f.dir))
		}
		in := ui.TextInput(c, &f.pathInput).Grow(1).MinWidth(0).FontSize(12).Label(locales.FilesPath())
		if in.Submitted() {
			m.listDir(f, strings.TrimSpace(f.pathInput))
		}
		if iconButton(c, refreshIcon, locales.Refresh()).Clicked() {
			m.listDir(f, f.dir)
		}
		if iconButton(c, folderPlusIcon, locales.FilesNewFolder()).Clicked() {
			m.openPrompt(locales.FilesNewFolder(), locales.FilesName(), "", locales.FilesCreate(), func(name string) {
				m.fileOp(f, func() error { return f.fs.Mkdir(f.fs.Join(f.dir, name)) })
			})
		}
		if m.backend.PickFiles != nil && iconButton(c, uploadIcon, map[bool]string{false: locales.FilesUpload(), true: locales.FilesCopyHere()}[f.local]).Clicked() {
			pick := m.backend.PickFiles
			go func() {
				paths, err := pick()
				if err == nil && len(paths) > 0 {
					m.update(func() { m.upload(f, paths) })
				}
			}()
		}
	})
	if f.err != "" {
		ui.Text(c, f.err).FontSize(11).TextColor(t.Danger).Padding(0, 10, 6, 10).MaxLines(3)
	}

	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			f.list.Selected = &f.selected
			f.list.CellMenu = func(menu *ui.Menu, row, _ int) {
				if row < 0 || row >= len(f.entries) {
					return
				}
				e := f.entries[row]
				p := f.fs.Join(f.dir, e.Name)
				switch {
				case e.Dir:
					if menu.Item(locales.FilesOpen()).Chosen() {
						m.listDir(f, p)
					}
				case f.local:
					if menu.Item(locales.FilesEdit()).Chosen() {
						m.openEditor(sess, f, p)
					}
					if m.backend.OpenPath != nil && menu.Item(locales.FilesOpenWithDefault()).Chosen() {
						m.openLocalFile(p)
					}
				default:
					if menu.Item(locales.FilesEdit()).Chosen() {
						m.openEditor(sess, f, p)
					}
					if menu.Item(locales.FilesDownload()).Chosen() {
						m.download(f, p, "")
					}
					if m.backend.SaveFile != nil && menu.Item(locales.FilesDownloadTo()).Chosen() {
						save := m.backend.SaveFile
						go func() {
							dest, err := save(e.Name)
							if err == nil && dest != "" {
								m.update(func() { m.download(f, p, dest) })
							}
						}()
					}
				}
				if f.local && m.backend.ShowInFolder != nil && menu.Item(showInFolderLabel()).Chosen() {
					m.backend.ShowInFolder(p)
				}
				if menu.Item(locales.FilesCopyPath()).Chosen() {
					c.WriteClipboard(p)
				}
				menu.Separator()
				if menu.Item(locales.FilesRenameMenu()).Chosen() {
					m.openPrompt(locales.Rename(), locales.FilesName(), e.Name, locales.Rename(), func(name string) {
						m.fileOp(f, func() error { return f.fs.Rename(p, f.fs.Join(f.dir, name)) })
					})
				}
				if menu.Item(locales.Delete()).Danger().Chosen() {
					var message string
					switch {
					case f.local && e.Dir:
						message = locales.FilesDeleteLocalFolder()
					case f.local:
						message = locales.FilesDeleteLocalFile()
					case e.Dir:
						message = locales.FilesDeleteRemoteFolder()
					default:
						message = locales.FilesDeleteRemoteFile()
					}
					m.ask(confirmation{
						title:   locales.FilesDeleteTitle(e.Name),
						message: message,
						action:  locales.Delete(),
						do:      func() { m.fileOp(f, func() error { return f.fs.Remove(p) }) },
					})
				}
			}
			cols := []ui.TableColumn{{Title: locales.FilesName()}, {Title: locales.FilesColumnSize(), Width: 70, Align: ui.End}, {Title: locales.FilesColumnModified(), Width: 96}}
			table := ui.Table(c, &f.list, cols, len(f.entries), func(row, col int) {
				e := f.entries[row]
				switch col {
				case 0:
					ui.Row(c).Gap(6).AlignItems(ui.Center).MinWidth(0).Children(func() {
						svg, color := fileIcon, t.TextMuted
						if e.Dir {
							svg, color = folderIcon, accentInk(c)
						}
						ui.Icon(c, svg).Size(14, 14).TextColor(color).Shrink(0)
						ui.Text(c, e.Name).FontSize(12).SingleLine().MinWidth(0)
					})
				case 1:
					if !e.Dir {
						ui.Text(c, formatBytes(uint64(e.Size()))).FontSize(11).TextColor(t.TextMuted).SingleLine()
					}
				case 2:
					ui.Text(c, formatTime(e.ModTime())).FontSize(11).TextColor(t.TextMuted).SingleLine()
				}
			}).Grow(1).MinHeight(0).Label(locales.FilesTable())
			if table.Submitted() && f.selected >= 0 && f.selected < len(f.entries) {
				e := f.entries[f.selected]
				p := f.fs.Join(f.dir, e.Name)
				switch {
				case e.Dir:
					m.listDir(f, p)
				default:
					m.openEditor(sess, f, p)
				}
			}
			if files := table.DroppedFiles(); len(files) > 0 {
				m.upload(f, files)
			}
			if f.loading {
				ui.Row(c).Padding(4, 10).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(12, 12)
					ui.Text(c, locales.FilesLoading()).FontSize(11).TextColor(t.TextMuted)
				})
			} else if len(f.entries) == 0 && f.err == "" {
				ui.Text(c, map[bool]string{false: locales.FilesEmptyUpload(), true: locales.FilesEmptyCopy()}[f.local]).FontSize(11).TextColor(t.TextMuted).Padding(4, 10)
			}
		})
		m.transfers(c, f)
	})
}

// listDir shows the directory dir.
func (m *Model) listDir(f *filesState, dir string) {
	if f.fs == nil || dir == "" {
		return
	}
	f.loading, f.pathInput = true, dir
	fs := f.fs
	go func() {
		entries, err := fs.List(dir)
		m.update(func() {
			f.loading = false
			if err != nil {
				f.err = locales.FilesCantOpen(dir, err.Error())
				f.pathInput = f.dir
				return
			}
			f.err, f.dir, f.pathInput, f.entries, f.selected = "", dir, dir, entries, -1
		})
	}()
}

// fileOp runs an operation on the server's files, then shows the
// directory again.
func (m *Model) fileOp(f *filesState, op func() error) {
	go func() {
		err := op()
		m.update(func() {
			if err != nil {
				f.err = err.Error()
			}
			m.listDir(f, f.dir)
		})
	}()
}

// --- A prompt for a name

type prompt struct {
	title, label, value, action string
	err                         string
	do                          func(name string)
}

func (m *Model) openPrompt(title, label, value, action string, do func(name string)) {
	m.prompt, m.promptOpen = prompt{title: title, label: label, value: value, action: action, do: do}, true
}

func (m *Model) promptDialog(c *ui.Context) {
	q := &m.prompt
	dialog(c, &m.promptOpen, 380, func() {
		dialogTitle(c, q.title, "")
		ui.Form(c, func() {
			ui.Field(c, q.label, func() { ui.TextInput(c, &q.value).AutoFocus() })
		})
		errorText(c, q.err)
		dialogButtons(c, false, q.action, func() { m.promptOpen = false }, func() {
			name := strings.TrimSpace(q.value)
			switch {
			case name == "" || name == "." || name == "..":
				q.err = locales.FilesNameRequired()
			case strings.Contains(name, "/"):
				q.err = locales.FilesNameSlash()
			default:
				m.promptOpen = false
				q.do(name)
			}
		})
	})
}

// track asks for a frame as a transfer moves, at most ten times a second.
func (m *Model) track(tr *transfer) sshconn.Progress {
	return func(done, total int64) {
		tr.done.Store(done)
		tr.total.Store(total)
		now := time.Now().UnixNano()
		if now-tr.last.Load() > int64(100*time.Millisecond) {
			tr.last.Store(now)
			m.update(func() {})
		}
	}
}

// upload sends local files into the directory shown.
func (m *Model) upload(f *filesState, paths []string) {
	dir, fs := f.dir, f.fs
	for _, p := range paths {
		tr := &transfer{name: filepath.Base(p), up: true}
		f.transfers = append(f.transfers, tr)
		progress := m.track(tr)
		go func() {
			_, err := fs.Upload(p, dir, progress)
			m.update(func() {
				tr.finished = true
				if err != nil {
					tr.err = err.Error()
				}
				if f.dir == dir {
					m.listDir(f, dir)
				}
			})
		}()
	}
}

// download fetches a file of the server to dest, or to the Downloads
// folder when dest is empty.
func (m *Model) download(f *filesState, remote, dest string) {
	if dest == "" {
		dest = downloadPath(f.fs.Base(remote))
	}
	tr := &transfer{name: filepath.Base(dest)}
	f.transfers = append(f.transfers, tr)
	fs, progress := f.fs, m.track(tr)
	go func() {
		err := fs.Download(remote, dest, progress)
		m.update(func() {
			tr.finished = true
			if err != nil {
				tr.err = err.Error()
				return
			}
			m.notify(locales.FilesDownloaded(displayPath(dest)))
		})
	}()
}

// openLocalFile opens a file of this machine with its application.
func (m *Model) openLocalFile(p string) {
	if m.backend.OpenPath == nil {
		return
	}
	if err := m.backend.OpenPath(p); err != nil {
		m.notify(locales.FilesCantOpen(displayPath(p), err.Error()))
	}
}

func showInFolderLabel() string {
	switch runtime.GOOS {
	case "darwin":
		return locales.FilesShowInFinder()
	case "windows":
		return locales.FilesShowInExplorer()
	}
	return locales.FilesShowInFileManager()
}

// downloadPath is where a file named name downloads: in the Downloads
// folder, under a name no file there has.
func downloadPath(name string) string {
	dir := "."
	if home, err := os.UserHomeDir(); err == nil {
		dir = filepath.Join(home, "Downloads")
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	p := filepath.Join(dir, name)
	for i := 2; ; i++ {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
	}
}

// displayPath shortens a path under the home directory with ~.
func displayPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

// transfers lists the files going up and down, the last few.
func (m *Model) transfers(c *ui.Context, f *filesState) {
	if len(f.transfers) == 0 {
		return
	}
	t := c.Theme()
	ui.Box(c).Width(1).Background(hairline(c)).Shrink(0)
	ui.Column(c).Width(230).Padding(8, 10).Gap(6).Shrink(0).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.FilesTransfers()).FontSize(11).Bold().TextColor(t.TextMuted).Grow(1)
			if ui.ButtonBase(c).Label(locales.FilesClearFinished()).Children(func() {
				ui.Text(c, locales.FilesClearFinished()).FontSize(11).TextColor(accentInk(c))
			}).Clicked() {
				var left []*transfer
				for _, tr := range f.transfers {
					if !tr.finished {
						left = append(left, tr)
					}
				}
				f.transfers = left
			}
		})
		shown := f.transfers[max(0, len(f.transfers)-6):]
		for _, tr := range shown {
			done, total := tr.done.Load(), tr.total.Load()
			ui.Column(c).Gap(3).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					svg := downloadIcon
					if tr.up {
						svg = uploadIcon
					}
					ui.Icon(c, svg).Size(12, 12).TextColor(t.TextMuted)
					ui.Text(c, tr.name).FontSize(11).SingleLine().Grow(1).MinWidth(0)
					var status string
					switch {
					case tr.err != "":
						status = locales.FilesTransferFailed()
					case tr.finished:
						status = locales.Done()
					case total > 0:
						status = fmt.Sprintf("%d%%", done*100/total)
					default:
						status = locales.FilesTransferWaiting()
					}
					st := ui.Text(c, status).FontSize(11).TextColor(t.TextMuted)
					if tr.err != "" {
						st.TextColor(t.Danger).Tooltip(tr.err)
					}
				})
				if !tr.finished {
					frac := 0.0
					if total > 0 {
						frac = float64(done) / float64(total)
					}
					ui.Progress(c, frac).Height(3)
				}
			})
		}
	})
}

// --- Status

func (m *Model) statusPanel(c *ui.Context, sess *session) {
	t := c.Theme()
	if sess.stats == nil {
		client := sshClient(sess)
		if client == nil {
			panelHint(c, locales.StatsHintConnect())
			return
		}
		sess.stats = sshconn.StartMonitor(client, 2*time.Second, func() { m.update(func() {}) })
	}
	h := sess.stats.History()
	if len(h) == 0 {
		if err := sess.stats.Err(); err != nil {
			panelHint(c, err.Error())
			return
		}
		ui.Column(c).Grow(1).Center().Gap(8).Children(func() {
			ui.Spinner(c)
			ui.Text(c, locales.StatsLoading()).FontSize(11).TextColor(t.TextMuted)
		})
		return
	}
	st := h[len(h)-1]
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(14).Gap(16).Children(func() {
			ui.Column(c).Gap(3).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					ui.Text(c, st.Host).FontSize(14).Bold().SingleLine().Grow(1).MinWidth(0)
					ui.Text(c, locales.StatsUptime(formatUptime(st.Uptime))).FontSize(11).TextColor(t.TextMuted)
				})
				ui.Text(c, locales.StatsCoresLoad(st.Cores, fmt.Sprintf("%.2f  %.2f  %.2f", st.Load[0], st.Load[1], st.Load[2]))).
					FontSize(11).TextColor(t.TextMuted)
				if err := sess.stats.Err(); err != nil {
					ui.Text(c, locales.StatsReadFailed(err.Error())).FontSize(11).TextColor(t.Danger).MaxLines(2)
				}
			})
			usage(c, "CPU", fmt.Sprintf("%.0f%%", st.CPU*100), st.CPU)
			ui.Column(c).Gap(4).Children(func() {
				usage(c, locales.StatsMemory(), formatBytes(st.MemUsed)+" / "+formatBytes(st.MemTotal), share(st.MemUsed, st.MemTotal))
				if st.SwapTotal > 0 {
					ui.Text(c, locales.StatsSwap(formatBytes(st.SwapUsed), formatBytes(st.SwapTotal))).FontSize(11).TextColor(t.TextMuted)
				}
			})
			if len(st.Disks) > 0 {
				ui.Column(c).Gap(10).Children(func() {
					ui.Text(c, locales.StatsStorage()).FontSize(12).Bold()
					for _, d := range st.Disks {
						usage(c, d.Mount, formatBytes(d.Used)+" / "+formatBytes(d.Size), share(d.Used, d.Size))
					}
				})
			}
			m.network(c, sess, h)
			processes(c, st.Procs)
		})
	})
}

func share(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total)
}

// usage is a measure of how much of something is used: its name, the
// figures and a bar, in one hue whatever the level.
func usage(c *ui.Context, name, figures string, frac float64) {
	t := c.Theme()
	ui.Column(c).Gap(5).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, name).FontSize(12).Bold().SingleLine().Grow(1).MinWidth(0)
			ui.Text(c, figures).FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
		track, fill := hover(c), accentInk(c)
		ui.Box(c).Height(6).Label(name).Draw(func(p *ui.Painter, r ui.Rect) {
			p.Fill(r, track, 3)
			if w := r.W * float32(min(max(frac, 0), 1)); w > 0 {
				p.Fill(ui.Rect{X: r.X, Y: r.Y, W: max(w, 6), H: r.H}, fill, 3)
			}
		})
	})
}

// The network's two series, in the first two hues of the categorical
// palette (validated together for light and dark).
func seriesRX(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#3987e5")
	}
	return ui.Hex("#2a78d6")
}

func seriesTX(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#d95926")
	}
	return ui.Hex("#eb6834")
}

// network shows the rates of the server's network, now and over the time
// sampled, as a chart whose hover reads the rates at a sample.
func (m *Model) network(c *ui.Context, sess *session, h []sshconn.Stats) {
	t := c.Theme()
	rx, tx := seriesRX(c), seriesTX(c)
	st := h[len(h)-1]
	// The interface shown: all of them, or one the select chose.
	names := []string{locales.StatsAllInterfaces()}
	for _, i := range st.Ifaces {
		names = append(names, i.Name)
	}
	if !slices.Contains(names[1:], sess.iface) {
		sess.iface = allIfaces
	}
	iface := sess.iface
	type point struct{ rx, tx float64 }
	pts := make([]point, len(h))
	peak := 1024.0
	for i, s := range h {
		r, t := s.Net(iface)
		pts[i] = point{r, t}
		peak = max(peak, r, t)
	}
	now := pts[len(pts)-1]
	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.StatsNetwork()).FontSize(12).Bold()
			ui.Spacer(c)
			// Plain text with its arrow, at the right of the heading.
			// The select says the localized label of allIfaces.
			chosen := sess.iface
			if chosen == allIfaces {
				chosen = names[0]
			}
			ui.Select(c, &chosen, names).Label(locales.StatsInterface()).FontSize(11).MaxWidth(140).MinWidth(0).
				Padding(0).Gap(4).Border(0, ui.Transparent).Background(ui.Transparent).Shadow(0, 0, 0, 0, ui.Transparent)
			if chosen == names[0] {
				chosen = allIfaces
			}
			sess.iface = chosen
		})
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			legend(c, rx, locales.StatsIn(formatRate(now.rx)))
			legend(c, tx, locales.StatsOut(formatRate(now.tx)))
		})
		top := niceCeil(peak)
		grid, muted := hairline(c), t.TextMuted
		chart := ui.Box(c).Height(120).Label(locales.StatsChart())
		hovered := -1
		if x, _, in := chart.PointerPosition(); in {
			if w := chart.Bounds().W; w > 0 {
				slot := int(x/w*float32(sshconn.HistoryLen-1) + 0.5)
				if i := slot - (sshconn.HistoryLen - len(h)); i >= 0 && i < len(h) {
					hovered = i
				}
			}
		}
		chart.Draw(func(p *ui.Painter, r ui.Rect) {
			// Recessive gridlines at 0, half and the top, the top labeled.
			for _, f := range []float32{0, 0.5, 1} {
				y := r.Y + r.H - f*r.H
				p.Line(r.X, y, r.X+r.W, y, 1, grid)
			}
			p.Text(r.X+2, r.Y+2, formatRate(top), 10, muted)
			xOf := func(i int) float32 {
				slot := sshconn.HistoryLen - len(h) + i
				return r.X + float32(slot)/float32(sshconn.HistoryLen-1)*r.W
			}
			yOf := func(v float64) float32 { return r.Y + r.H - float32(v/top)*r.H }
			for _, series := range []struct {
				color ui.Color
				value func(point) float64
			}{{rx, func(p point) float64 { return p.rx }}, {tx, func(p point) float64 { return p.tx }}} {
				var line, area ui.Path
				for i, pt := range pts {
					x, y := xOf(i), yOf(series.value(pt))
					if i == 0 {
						line.MoveTo(x, y)
						area.MoveTo(x, r.Y+r.H).LineTo(x, y)
					} else {
						line.LineTo(x, y)
						area.LineTo(x, y)
					}
				}
				area.LineTo(xOf(len(h)-1), r.Y+r.H).Close()
				p.FillPath(&area, series.color.Alpha(0.12))
				p.StrokePath(&line, 2, series.color)
			}
			if hovered >= 0 {
				x := xOf(hovered)
				p.Line(x, r.Y, x, r.Y+r.H, 1, muted.Alpha(0.6))
				for _, d := range []struct {
					v     float64
					color ui.Color
				}{{pts[hovered].rx, rx}, {pts[hovered].tx, tx}} {
					y := yOf(d.v)
					p.Fill(ui.Rect{X: x - 5, Y: y - 5, W: 10, H: 10}, t.Background, 5)
					p.Fill(ui.Rect{X: x - 4, Y: y - 4, W: 8, H: 8}, d.color, 4)
				}
			}
		})
		ui.Row(c).Children(func() {
			if hovered >= 0 {
				s := h[hovered]
				ago := st.At.Sub(s.At).Round(time.Second)
				when := locales.StatsNow()
				if ago > 0 {
					when = locales.StatsSecondsAgo(int(ago.Seconds()))
				}
				ui.Text(c, locales.StatsReading(when, formatRate(pts[hovered].rx), formatRate(pts[hovered].tx))).FontSize(11).TextColor(t.TextMuted)
				return
			}
			span := time.Duration(sshconn.HistoryLen) * 2 * time.Second
			ui.Text(c, locales.StatsLastMinutes(int(span.Minutes()))).FontSize(11).TextColor(t.TextMuted).Grow(1)
			ui.Text(c, locales.StatsPeak(formatRate(peak))).FontSize(11).TextColor(t.TextMuted)
		})
	})
}

// allIfaces is the choice of every network interface but lo: the empty
// name, which sshconn's Net reads as all of them. The select shows it as
// statsAllInterfaces.
const allIfaces = ""

// processes lists the processes using the most CPU, then memory.
func processes(c *ui.Context, procs []sshconn.Proc) {
	if len(procs) == 0 {
		return
	}
	t := c.Theme()
	cell := func(text string, width float32, bold bool) {
		e := ui.Text(c, text).FontSize(11).SingleLine().TextAlign(ui.End).Width(width).Shrink(0)
		if bold {
			e.Bold().TextColor(t.TextMuted)
		}
	}
	ui.Column(c).Gap(6).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.StatsProcesses()).FontSize(12).Bold().Grow(1)
			cell("CPU", 52, true)
			cell(locales.StatsMemory(), 64, true)
		})
		for _, p := range procs {
			ui.Row(c).Key(p.PID).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Gap(5).Grow(1).MinWidth(0).AlignItems(ui.Baseline).Children(func() {
					ui.Text(c, p.Name).FontSize(12).SingleLine().MinWidth(0).Shrink(1).Tooltip(p.Name)
					ui.Textf(c, "%d", p.PID).FontSize(10).TextColor(t.TextMuted).Shrink(0)
				})
				cell(fmt.Sprintf("%.1f%%", p.CPU), 52, false)
				cell(formatBytes(p.Mem), 64, false)
			})
		}
	})
}

// legend is a series' mark and its label, in text color.
func legend(c *ui.Context, color ui.Color, label string) {
	ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(8, 8).Radius(4).Background(color)
		ui.Text(c, label).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
	})
}

// niceCeil rounds a rate up to 1, 2 or 5 times a power of 1024's unit.
func niceCeil(v float64) float64 {
	unit := 1.0
	for v >= 1024*unit && unit < 1<<40 {
		unit *= 1024
	}
	n := v / unit
	for _, step := range []float64{1, 2, 5, 10, 20, 50, 100, 200, 500, 1024} {
		if n <= step {
			return step * unit
		}
	}
	return v
}

func formatBytes(n uint64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	v, i := float64(n), 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	switch {
	case i == 0:
		return fmt.Sprintf("%d B", n)
	case v >= 100:
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

func formatRate(v float64) string { return formatBytes(uint64(v)) + "/s" }

func formatUptime(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	switch {
	case days > 0:
		return locales.StatsUptimeDays(days, hours)
	case hours > 0:
		return locales.StatsUptimeHours(hours, int(d.Minutes())%60)
	}
	return locales.StatsUptimeMinutes(int(d.Minutes()))
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	if t.Year() == time.Now().Year() {
		return t.Format("01-02 15:04")
	}
	return t.Format("2006-01-02")
}
