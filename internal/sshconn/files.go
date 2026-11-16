package sshconn

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Files is a server's files over SFTP.
type Files struct {
	c *sftp.Client
}

// OpenFiles starts SFTP over an SSH connection.
func OpenFiles(client *ssh.Client) (*Files, error) {
	c, err := sftp.NewClient(client)
	if err != nil {
		return nil, fmt.Errorf("无法打开 SFTP: %w", err)
	}
	return &Files{c: c}, nil
}

// NewFiles wraps an SFTP client, as one of a server in tests.
func NewFiles(c *sftp.Client) *Files { return &Files{c: c} }

// Close ends SFTP.
func (f *Files) Close() error { return f.c.Close() }

// Home returns the directory SFTP starts in, the user's home.
func (f *Files) Home() (string, error) { return f.c.Getwd() }

// Entry is a file or directory.
type Entry struct {
	Name string
	Dir  bool
	Link bool
	os.FileInfo
}

// List returns what is in the directory dir: directories first, then
// files, each by name.
func (f *Files) List(dir string) ([]Entry, error) {
	infos, err := f.c.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(infos))
	for _, fi := range infos {
		e := Entry{Name: fi.Name(), Dir: fi.IsDir(), FileInfo: fi}
		if fi.Mode()&os.ModeSymlink != 0 {
			e.Link = true
			// A link to a directory opens as one.
			if target, err := f.c.Stat(path.Join(dir, fi.Name())); err == nil && target.IsDir() {
				e.Dir = true
			}
		}
		entries = append(entries, e)
	}
	sortEntries(entries)
	return entries, nil
}

// Mkdir makes a directory.
func (f *Files) Mkdir(p string) error { return f.c.Mkdir(p) }

// Rename renames a file or directory.
func (f *Files) Rename(from, to string) error { return f.c.Rename(from, to) }

// Remove removes a file, or a directory with what is in it.
func (f *Files) Remove(p string) error {
	fi, err := f.c.Lstat(p)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return f.c.RemoveAll(p)
	}
	return f.c.Remove(p)
}

// Progress is told how many bytes of how many a transfer moved.
type Progress func(done, total int64)

// Upload copies the local file into the directory dir of the server, and
// returns the path it took there.
func (f *Files) Upload(local, dir string, progress Progress) (string, error) {
	src, err := os.Open(local)
	if err != nil {
		return "", err
	}
	defer src.Close()
	fi, err := src.Stat()
	if err != nil {
		return "", err
	}
	if fi.IsDir() {
		return "", fmt.Errorf("%s 是目录，只能上传文件", filepath.Base(local))
	}
	to := path.Join(dir, filepath.Base(local))
	dst, err := f.c.Create(to)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(dst, &counting{r: src, total: fi.Size(), progress: progress})
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	return to, err
}

// Download copies a file of the server to the local path.
func (f *Files) Download(remote, local string, progress Progress) error {
	src, err := f.c.Open(remote)
	if err != nil {
		return err
	}
	defer src.Close()
	fi, err := src.Stat()
	if err != nil {
		return err
	}
	dst, err := os.Create(local)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, &counting{r: src, total: fi.Size(), progress: progress})
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(local)
	}
	return err
}

// counting reads r, telling progress how far it got.
type counting struct {
	r        io.Reader
	done     int64
	total    int64
	progress Progress
}

func (c *counting) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	if c.progress != nil && n > 0 {
		c.progress(c.done, c.total)
	}
	return n, err
}

// Stat returns what a file is.
func (f *Files) Stat(p string) (os.FileInfo, error) { return f.c.Stat(p) }

// ReadFile returns what a file holds, up to max bytes: ErrTooLarge past
// them.
func (f *Files) ReadFile(p string, max int64) ([]byte, error) {
	r, err := f.c.Open(p)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return readMax(r, max)
}

// WriteFile replaces what a file holds, keeping its mode, or makes it.
func (f *Files) WriteFile(p string, data []byte) error {
	w, err := f.c.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	return err
}
