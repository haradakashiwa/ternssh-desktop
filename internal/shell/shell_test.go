//go:build darwin || linux

package shell

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func TestShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	dir := t.TempDir()
	s, err := Start(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// The size the terminal tells starts it.
	if err := s.ResizePixels(100, 30, 8, 16); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write([]byte("stty size; pwd; exit 3\n")); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	buf := make([]byte, 4096)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		n, err := s.Read(buf)
		out.Write(buf[:n])
		if err != nil {
			break
		}
	}
	real, _ := os.Readlink(dir)
	if !strings.Contains(out.String(), "30 100") || !(strings.Contains(out.String(), dir) || real != "" && strings.Contains(out.String(), real)) {
		t.Fatalf("output: %q", out.String())
	}
	if s.Status().String() == "" {
		t.Fatal("no status")
	}
}

func TestShellClosedBeforeItStarts(t *testing.T) {
	s, err := Start("")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := s.Read(make([]byte, 8)); err == nil {
		t.Fatal("read a shell closed before it started")
	}
}
