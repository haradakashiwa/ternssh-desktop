package gui

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	clip "github.com/egoist/mygo/transfer"

	"ternssh-desktop/internal/web"
)

// Paste pastes into the terminal of a session what a browser pasted or
// dropped on it (web.App), as the window pastes the clipboard: an image
// as the image, offered to a program that reads the clipboard itself (as
// coding agents do, over SSH too), and a file as a file, which a program
// that does not gets as its path.
func (m *Model) Paste(session string, items []web.Pasted) {
	tp := m.taps.get(session)
	if tp == nil {
		return
	}
	d, ok := pasteData(items)
	if !ok {
		return
	}
	tp.mu.Lock()
	term := tp.term
	tp.mu.Unlock()
	if term != nil {
		term.PasteData(d)
	}
}

// pasteData is what the browser's files paste as; it reads the images.
func pasteData(items []web.Pasted) (clip.Data, bool) {
	var out []clip.Item
	var paths []string
	for _, p := range items {
		if p.Image() {
			b, err := os.ReadFile(p.Path)
			if err != nil {
				continue
			}
			out = append(out, clip.NewItem(clip.Bytes(clip.Format(p.Type), b)))
			continue
		}
		u := (&url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(p.Path), "/")}).String()
		out = append(out, clip.NewItem(clip.Bytes(clip.FileList, []byte(u)), clip.Bytes(clip.URIList, []byte(u))))
		paths = append(paths, quotePath(p.Path))
	}
	if len(out) == 0 {
		return clip.Data{}, false
	}
	if len(paths) > 0 {
		// The paths, as dropping files on a terminal types them.
		out = append(out, clip.NewItem(clip.Bytes(clip.Text, []byte(strings.Join(paths, " ")+" "))))
	}
	return clip.New(out...), true
}

// quotePath quotes a path of this machine for its shell, when it needs
// it.
func quotePath(p string) string {
	if runtime.GOOS == "windows" {
		if strings.ContainsAny(p, " &()[]{}^=;!'+,`~%") {
			return `"` + p + `"`
		}
		return p
	}
	if p != "" && strings.Trim(p, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-+@%:,") == "" {
		return p
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}
