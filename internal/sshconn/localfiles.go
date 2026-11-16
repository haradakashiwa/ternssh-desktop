package sshconn

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// LocalFiles is this machine's files, as Files is a server's.
type LocalFiles struct {
	Start string // where they are first shown: the home directory when empty
}

// Close does nothing: there is no connection.
func (LocalFiles) Close() error { return nil }

// Home returns Start, or the home directory.
func (l LocalFiles) Home() (string, error) {
	if l.Start != "" {
		return filepath.Abs(l.Start)
	}
	return os.UserHomeDir()
}

// Join, Dir and Base take local paths apart, with the separator of the
// system.
func (LocalFiles) Join(dir, name string) string { return filepath.Join(dir, name) }
func (LocalFiles) Dir(p string) string          { return filepath.Dir(p) }
func (LocalFiles) Base(p string) string         { return filepath.Base(p) }

// List returns what is in the directory dir, as Files.List.
func (LocalFiles) List(dir string) ([]Entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(des))
	for _, de := range des {
		fi, err := de.Info()
		if err != nil {
			continue // gone since
		}
		e := Entry{Name: de.Name(), Dir: fi.IsDir(), FileInfo: fi}
		if fi.Mode()&os.ModeSymlink != 0 {
			e.Link = true
			if target, err := os.Stat(filepath.Join(dir, de.Name())); err == nil && target.IsDir() {
				e.Dir = true
			}
		}
		entries = append(entries, e)
	}
	sortEntries(entries)
	return entries, nil
}

func (LocalFiles) Mkdir(p string) error         { return os.Mkdir(p, 0o755) }
func (LocalFiles) Rename(from, to string) error { return os.Rename(from, to) }

// Remove removes a file, or a directory with what is in it.
func (LocalFiles) Remove(p string) error { return os.RemoveAll(p) }

// Upload copies a file into the directory dir.
func (LocalFiles) Upload(local, dir string, progress Progress) (string, error) {
	to := filepath.Join(dir, filepath.Base(local))
	if same(local, to) {
		return "", fmt.Errorf("%s 已经在这个目录里", filepath.Base(local))
	}
	return to, copyFile(local, to, progress)
}

// Download copies a file to the path local.
func (LocalFiles) Download(from, local string, progress Progress) error {
	if same(from, local) {
		return errors.New("不能复制到它自己")
	}
	return copyFile(from, local, progress)
}

func same(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	return err == nil && os.SameFile(fa, fb)
}

func copyFile(from, to string, progress Progress) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	fi, err := src.Stat()
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("%s 是目录，只能复制文件", filepath.Base(from))
	}
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, &counting{r: src, total: fi.Size(), progress: progress})
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(to)
	}
	return err
}

// sortEntries puts directories first, then files, each by name.
func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
}

// Join, Dir and Base take a server's paths apart: they are POSIX ones.
func (*Files) Join(dir, name string) string { return path.Join(dir, name) }
func (*Files) Dir(p string) string          { return path.Dir(p) }
func (*Files) Base(p string) string         { return path.Base(p) }
