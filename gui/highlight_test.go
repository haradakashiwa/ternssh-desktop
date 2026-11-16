package gui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"ternssh-desktop/internal/sshconn"
)

func TestDetectLanguage(t *testing.T) {
	for name, want := range map[string]string{
		"main.go":       "Go",
		"deploy.sh":     "Bash",
		"nginx.conf":    "Nginx configuration file",
		"Dockerfile":    "Docker",
		"config.yaml":   "YAML",
		"notes.unknown": plainText,
	} {
		if got := detectLanguage(name, ""); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	// A script without an extension, by its #! line.
	if got := detectLanguage("backup", "#!/bin/bash\necho hi\n"); got != "Bash" {
		t.Errorf("by #!: %q", got)
	}
}

func TestHighlight(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "deploy.sh")
	src := "#!/bin/bash\n# 部署\nset -e\nfor host in web-01 web-02; do\n  ssh \"$host\" 'systemctl restart nginx'\ndone\nexit 0\n"
	os.WriteFile(p, []byte(src), 0o755)
	w := &editorWindows{t: t, updates: make(chan func(), 16)}
	e := newEditor(sshconn.LocalFiles{}, p, "deploy.sh", "本机")
	e.host = w.host(e)
	e.load()
	w.apply(func() bool { return !e.loading })
	if e.lang != "Bash" {
		t.Fatalf("language %q", e.lang)
	}
	ranges := e.highlight(false)
	if len(ranges) == 0 {
		t.Fatal("nothing highlighted")
	}
	// The comment, "# 部署", counted in runes, is one range.
	found := false
	for _, r := range ranges {
		if r.Start == 12 && r.End == 16 {
			found = true
		}
		if r.End > len([]rune(src)) {
			t.Errorf("range %v past the text", r)
		}
	}
	if !found {
		t.Errorf("the comment is not a range: %v", ranges)
	}
	// Dark mode colors differently; plain text, none.
	dark := e.highlight(true)
	if len(dark) == 0 || dark[0].Color == ranges[0].Color {
		t.Errorf("dark ranges %v", dark[:1])
	}
	tt := ui.NewTester(e.View, 900, 600)
	snapshot(t, tt, "editor-highlight")
	tt.SetDark(true)
	snapshot(t, tt, "editor-highlight-dark")
	tt.SetDark(false)
	e.lang = plainText
	tt.Frame()
	if e.hl.lexer != nil || len(e.highlight(false)) != 0 {
		t.Error("plain text highlighted")
	}
}
