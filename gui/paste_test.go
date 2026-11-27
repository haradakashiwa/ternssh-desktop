package gui

import (
	"os"
	"path/filepath"
	"testing"

	clip "github.com/egoist/mygo/transfer"

	"ternssh-desktop/internal/web"
)

func TestPasteData(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "image.png")
	file := filepath.Join(dir, "my notes.txt")
	os.WriteFile(img, []byte("PNG"), 0o600)
	os.WriteFile(file, []byte("hi"), 0o600)
	d, ok := pasteData([]web.Pasted{{Path: img, Type: "image/png"}, {Path: file}})
	if !ok {
		t.Fatal("nothing to paste")
	}
	if b, err := d.Read(clip.PNG); err != nil || string(b) != "PNG" {
		t.Fatalf("image: %q %v", b, err)
	}
	if paths, err := d.Files(); err != nil || len(paths) != 1 || paths[0] != file {
		t.Fatalf("files: %q %v", paths, err)
	}
	if b, _ := d.Read(clip.Text); string(b) != quotePath(file)+" " {
		t.Fatalf("text: %q", b)
	}
	if _, ok := pasteData([]web.Pasted{{Path: filepath.Join(dir, "gone.png"), Type: "image/png"}}); ok {
		t.Fatal("pasted an image that is gone")
	}
}

func TestQuotePath(t *testing.T) {
	for in, want := range map[string]string{"/tmp/a.png": "/tmp/a.png", "/tmp/my notes.txt": "'/tmp/my notes.txt'", "/tmp/it's": `'/tmp/it'\''s'`} {
		if got := quotePath(in); got != want {
			t.Errorf("quotePath(%q) = %q, want %q", in, got, want)
		}
	}
}
