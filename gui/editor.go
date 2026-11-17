package gui

import (
	"bytes"
	"errors"
	"os"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"ternssh-desktop/internal/sshconn"
	"ternssh-desktop/locales"
)

// editorMaxSize is the largest file an editor opens.
const editorMaxSize = 5 << 20

// editorFiles is where an editor reads and writes its file: a server's
// files over SFTP, or this machine's.
type editorFiles interface {
	Stat(p string) (os.FileInfo, error)
	ReadFile(p string, max int64) ([]byte, error)
	WriteFile(p string, data []byte) error
}

// EditorHost is the window the app opens for an editor.
type EditorHost struct {
	// Update runs fn on the main thread and builds the window again.
	Update func(fn func())
	// Close closes the window, without asking.
	Close func()
	// Focus brings the window to the front.
	Focus func()
}

// Editor edits a text file, of a server or of this machine, in a window
// of its own.
type Editor struct {
	// Title is the window's: the file's name, and where it is.
	Title string

	path  string
	where string // the server, or "" for this machine (本机)
	fs    editorFiles
	host  EditorHost

	buf     *ui.TextBuffer
	saved   uint64 // the version of buf last read or saved
	crlf    bool   // the file ends its lines with CRLF, which it keeps
	modTime time.Time
	size    int64

	loading, saving bool
	err             string // why the file cannot be edited
	saveErr         string
	conflict        bool // the file changed since: ask to overwrite it
	closeAsk        bool // ask to save before the window closes
	closeAfterSave  bool

	hl   highlighter
	lang string // the language chosen, as the highlighter's

	onClosed func()
}

func newEditor(fs editorFiles, p, name, where string) *Editor {
	return &Editor{Title: name + " — " + whereLabel(where), path: p, where: where, fs: fs, buf: ui.NewTextBuffer(""), loading: true}
}

func (e *Editor) update(fn func()) {
	if e.host.Update != nil {
		e.host.Update(fn)
	}
}

// load reads the file, if it is text.
func (e *Editor) load() {
	go func() {
		var data []byte
		fi, err := e.fs.Stat(e.path)
		switch {
		case err != nil:
		case fi.IsDir():
			err = errors.New(locales.EditorIsFolder())
		case fi.Size() > editorMaxSize:
			err = sshconn.ErrTooLarge
		default:
			data, err = e.fs.ReadFile(e.path, editorMaxSize)
		}
		if errors.Is(err, sshconn.ErrTooLarge) {
			err = errors.New(locales.EditorTooLarge(formatBytes(editorMaxSize)))
		}
		if err == nil && (bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data)) {
			err = errors.New(locales.EditorNotText())
		}
		e.update(func() {
			e.loading = false
			if err != nil {
				e.err = err.Error()
				return
			}
			e.crlf = bytes.Contains(data, []byte("\r\n"))
			text := strings.ReplaceAll(string(data), "\r\n", "\n")
			e.buf.Set(text)
			e.lang = detectLanguage(baseName(e.path), text)
			e.hl.setLanguage(e.lang)
			e.saved, e.modTime, e.size = e.buf.Version(), fi.ModTime(), fi.Size()
		})
	}()
}

// Dirty reports whether the text has changes not saved.
func (e *Editor) Dirty() bool {
	return !e.loading && e.err == "" && e.buf.Version() != e.saved
}

// save writes the text to the file; unless force, not over a file that
// changed since it was read.
func (e *Editor) save(force bool) {
	if e.saving || e.loading || e.err != "" {
		return
	}
	e.saving, e.saveErr, e.conflict = true, "", false
	version, text := e.buf.Version(), e.buf.String()
	if e.crlf {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	modTime, size := e.modTime, e.size
	go func() {
		if !force {
			if fi, err := e.fs.Stat(e.path); err == nil && (!fi.ModTime().Equal(modTime) || fi.Size() != size) {
				e.update(func() { e.saving, e.conflict = false, true })
				return
			}
		}
		err := e.fs.WriteFile(e.path, []byte(text))
		var fi os.FileInfo
		if err == nil {
			fi, err = e.fs.Stat(e.path)
		}
		e.update(func() {
			e.saving = false
			if err != nil {
				e.saveErr = locales.EditorSaveFailed(err.Error())
				e.closeAfterSave = false
				return
			}
			e.saved, e.modTime, e.size = version, fi.ModTime(), fi.Size()
			if e.closeAfterSave && !e.Dirty() && e.host.Close != nil {
				e.host.Close()
			}
		})
	}()
}

// CanClose reports whether the window may close: not with changes not
// saved, which it asks about instead.
func (e *Editor) CanClose() bool {
	if !e.Dirty() {
		return true
	}
	e.closeAsk = true
	if e.host.Focus != nil {
		e.host.Focus()
	}
	return false
}

// Closed tells the editor its window closed.
func (e *Editor) Closed() {
	if e.onClosed != nil {
		e.onClosed()
	}
}

// View builds the editor's window.
func (e *Editor) View(c *ui.Context) {
	c.SetTheme(branded(c.Theme()))
	t := c.Theme()
	if c.Shortcut(ui.Cmd, ui.KeyS) && !c.ShortcutRepeated() {
		e.save(false)
	}
	dirty := e.Dirty()
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Padding(8, 12).Gap(10).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Icon(c, fileIcon).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
			ui.Text(c, whereLabel(e.where)+"  "+e.path).Font("monospace").FontSize(12).TextColor(t.TextMuted).
				SingleLine().Ellipsis("…").Grow(1).Shrink(1).MinWidth(0)
			switch {
			case e.saving:
				ui.Spinner(c).Size(12, 12)
				ui.Text(c, locales.EditorSaving()).FontSize(12).TextColor(t.TextMuted)
			case dirty:
				ui.Text(c, locales.EditorUnsaved()).FontSize(12).TextColor(accentInk(c))
			}
			b := ui.PrimaryButton(c, locales.Save()).Disabled(!dirty || e.saving).Tooltip(locales.EditorSaveTooltip(saveShortcut()))
			if b.Clicked() {
				e.save(false)
			}
		})
		ui.Box(c).Height(1).Background(hairline(c)).Shrink(0)
		if e.saveErr != "" {
			ui.Text(c, e.saveErr).FontSize(12).TextColor(t.Danger).Padding(6, 12).Shrink(0)
		}
		if e.conflict {
			ui.Row(c).Padding(6, 12).Gap(8).AlignItems(ui.Center).Background(t.Warning.Alpha(0.14)).Shrink(0).Children(func() {
				ui.Text(c, locales.EditorConflict()).FontSize(12).Grow(1)
				if ui.Button(c, locales.Cancel()).Clicked() {
					e.conflict = false
				}
				if ui.PrimaryButton(c, locales.EditorOverwrite()).Clicked() {
					e.save(true)
				}
			})
		}
		switch {
		case e.loading:
			ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c) })
		case e.err != "":
			panelHint(c, e.err)
		default:
			ui.TextAreaBufferBase(c, e.buf).Grow(1).MinHeight(0).Font("monospace").FontSize(13).
				Padding(10, 14).AutoFocus().Label(locales.EditorContent()).TextRanges(e.highlight(t.Dark)...)
			ui.Box(c).Height(1).Background(hairline(c)).Shrink(0)
			ending := "LF"
			if e.crlf {
				ending = "CRLF"
			}
			ui.Row(c).Padding(2, 12).Gap(12).AlignItems(ui.Center).Shrink(0).Children(func() {
				ui.Text(c, locales.EditorStatus(e.buf.LineCount(), ending)).FontSize(11).TextColor(t.TextMuted)
				ui.Spacer(c)
				if e.buf.ByteLen() > highlightMax && e.lang != plainText {
					ui.Text(c, locales.EditorNoHighlight()).FontSize(11).TextColor(t.TextMuted)
				}
				// Plain text is chosen by its localized label.
				options, chosen := e.hl.languageOptions(), e.lang
				if chosen == plainText {
					chosen = options[0]
				}
				ui.Select(c, &chosen, options).Label(locales.EditorLanguage()).FontSize(11).MaxWidth(180).MinWidth(0).
					Padding(0).Gap(4).Border(0, ui.Transparent).Background(ui.Transparent).Shadow(0, 0, 0, 0, ui.Transparent)
				if chosen == options[0] {
					chosen = plainText
				}
				e.lang = chosen
				if e.lang != e.hl.lang {
					e.hl.setLanguage(e.lang)
				}
			})
		}
	})
	dialog(c, &e.closeAsk, 380, func() {
		dialogTitle(c, locales.EditorCloseTitle(baseName(e.path)), "")
		ui.Text(c, locales.EditorCloseMessage()).FontSize(13).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, locales.EditorDontSave()).Clicked() {
				e.closeAsk = false
				e.saved = e.buf.Version()
				if e.host.Close != nil {
					e.host.Close()
				}
			}
			ui.Spacer(c)
			if ui.Button(c, locales.Cancel()).Clicked() {
				e.closeAsk = false
			}
			if ui.PrimaryButton(c, locales.Save()).Label(locales.EditorSaveAndClose()).Clicked() {
				e.closeAsk, e.closeAfterSave = false, true
				e.save(false)
			}
		})
	})
}

// whereLabel is what an editor says its file is on: the server, or this
// machine.
func whereLabel(where string) string {
	if where == "" {
		return locales.ThisMachine()
	}
	return where
}

func saveShortcut() string {
	if runtime.GOOS == "darwin" {
		return "⌘S"
	}
	return "Ctrl+S"
}

// baseName is the last element of a path, of a server's or this
// machine's.
func baseName(p string) string {
	p = strings.TrimRight(p, `/\`)
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// --- Opening editors

// openEditor opens a file of the files panel in an editor's window, or
// brings forward the one it has.
func (m *Model) openEditor(sess *session, f *filesState, p string) {
	fs, ok := f.fs.(editorFiles)
	if !ok || m.backend.OpenEditor == nil {
		if f.local {
			m.openLocalFile(p)
		} else {
			m.download(f, p, "")
		}
		return
	}
	key := sess.id + "\x00" + p
	if f.local {
		key = "local\x00" + p
	}
	if e := m.editors[key]; e != nil {
		if e.host.Focus != nil {
			e.host.Focus()
		}
		return
	}
	where := ""
	if !f.local {
		where = sess.subtitle
	}
	e := newEditor(fs, p, f.fs.Base(p), where)
	e.onClosed = func() { delete(m.editors, key) }
	if m.editors == nil {
		m.editors = map[string]*Editor{}
	}
	m.editors[key] = e
	e.host = m.backend.OpenEditor(e)
	e.load()
}

// CanQuit reports whether the app may quit: not while an editor has
// changes not saved, which it asks about instead.
func (m *Model) CanQuit() bool {
	ok := true
	for _, e := range m.editors {
		if !e.CanClose() {
			ok = false
		}
	}
	return ok
}
