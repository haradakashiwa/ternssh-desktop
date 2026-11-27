package web

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

// A browser pastes files and images into a terminal in two steps: it
// sends each to POST /upload, which keeps it on this machine and names it,
// showing how far it got; then it asks over the WebSocket to paste those
// it sent, which the terminal pastes as the window pastes the clipboard.

// MaxUpload is the largest file a browser pastes.
const MaxUpload = 100 << 20

// Image reports whether the paste is an image, which is pasted as the
// image itself rather than as a file.
func (p Pasted) Image() bool { return strings.HasPrefix(p.Type, "image/") }

const (
	// unpastedFor is how long an upload waits to be pasted.
	unpastedFor = time.Hour
	// keepPasted is how long a pasted file is kept, for the program that
	// was given its path to read it.
	keepPasted = 7 * 24 * time.Hour
)

// uploads are the files browsers sent and have not pasted yet, by name.
type uploads struct {
	mu   sync.Mutex
	root string // where they are kept; the cache folder's when empty
	m    map[string]upload
}

type upload struct {
	Pasted
	at time.Time
}

func (u *uploads) dir() (string, error) {
	if u.root != "" {
		return u.root, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "ternssh", "pasted"), nil
}

// add keeps what r holds as a file named name of MIME type typ, in a
// folder of its own, and returns the upload's name.
func (u *uploads) add(name, typ string, r io.Reader) (string, error) {
	root, err := u.dir()
	if err != nil {
		return "", err
	}
	u.prune(root)
	b := make([]byte, 18)
	rand.Read(b)
	id := base64.RawURLEncoding.EncodeToString(b)
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.m == nil {
		u.m = map[string]upload{}
	}
	u.m[id] = upload{Pasted{Path: p, Type: typ}, time.Now()}
	return id, nil
}

// prune forgets uploads never pasted, and removes pasted files kept long
// enough.
func (u *uploads) prune(root string) {
	u.mu.Lock()
	for id, up := range u.m {
		if time.Since(up.at) > unpastedFor {
			delete(u.m, id)
			os.RemoveAll(filepath.Dir(up.Path))
		}
	}
	u.mu.Unlock()
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > keepPasted {
			os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

// take returns the uploads of the names given, which are pasted once.
func (u *uploads) take(ids []string) []Pasted {
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []Pasted
	for _, id := range ids {
		if up, ok := u.m[id]; ok {
			delete(u.m, id)
			out = append(out, up.Pasted)
		}
	}
	return out
}

// done removes the images pasted, which the terminal read; files stay
// for the program given their paths.
func (u *uploads) done(items []Pasted) {
	for _, p := range items {
		if p.Image() {
			os.RemoveAll(filepath.Dir(p.Path))
		}
	}
}

// serveUpload keeps a file a browser sends to paste, the body of the
// request, named by its query (name, type), and answers its upload name.
func (s *Server) serveUpload(w http.ResponseWriter, r *http.Request) {
	// Only the app's own page: another site's could send a file on this
	// machine's behalf, which does not need a password.
	if o, err := url.Parse(r.Header.Get("Origin")); err != nil || !strings.EqualFold(o.Host, r.Host) {
		http.Error(w, "ternssh: cross-origin", http.StatusForbidden)
		return
	}
	if r.ContentLength > MaxUpload {
		http.Error(w, "too large", http.StatusRequestEntityTooLarge)
		return
	}
	q := r.URL.Query()
	id, err := s.uploads.add(cleanName(q.Get("name")), cleanType(q.Get("type")), http.MaxBytesReader(w, r.Body, MaxUpload))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": id})
}

// cleanName is a file name a browser gave, safe to make a file of: its
// last element, with no control characters, at most 120 bytes.
func cleanName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	for len(name) > 120 {
		ext := filepath.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		stem := []rune(strings.TrimSuffix(name, ext))
		name = string(stem[:len(stem)-1]) + ext
	}
	if strings.Trim(name, ".") == "" {
		return "pasted"
	}
	return name
}

// cleanType is a MIME type a browser gave, "" when it is not one.
func cleanType(t string) string {
	mt, _, err := mime.ParseMediaType(t)
	if err != nil || !strings.Contains(mt, "/") {
		return ""
	}
	return mt
}
