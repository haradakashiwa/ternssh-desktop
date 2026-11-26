// Package shell runs a shell of this machine in a pseudo-terminal, as a
// terminal's Conn: what the terminal shows of it then passes through this
// app, which shows it on the web too, as it does a server's.
//
// It starts the shell as the terminal plugin would for a terminal of its
// own: the user's login shell, with zsh's prompts marked (OSC 133) so the
// terminal redraws them as it resizes, and only once the terminal told
// the size of its screen, for the shell's first prompt to fit it.
package shell

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"ternssh-desktop/internal/pty"
	"ternssh-desktop/internal/sshconn"
)

// startDelay is how long a shell whose terminal tells no size waits
// before starting at 80×24, as a terminal no view shows.
const startDelay = 500 * time.Millisecond

// Shell is a shell running in a pseudo-terminal.
type Shell struct {
	mu      sync.Mutex
	started chan struct{} // closed once it started, or could not
	launch  *pty.Options  // until it starts
	proc    *pty.PTY
	err     error
	closed  bool
	zdotdir string // the zsh integration's, removed as it ends
	timer   *time.Timer

	cols, rows, cellW, cellH int
}

// Start makes a shell that starts in dir, the home directory when empty,
// once Resize is told the size, or after half a second.
func Start(dir string) (*Shell, error) {
	path, args, err := command()
	if err != nil {
		return nil, err
	}
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	s := &Shell{started: make(chan struct{}), cols: 80, rows: 24, cellW: 8, cellH: 16}
	o := &pty.Options{Path: path, Args: args, Dir: dir, Env: environment()}
	if filepath.Base(path) == "zsh" {
		if o.Env, s.zdotdir, err = zshEnvironment(o.Env); err != nil {
			return nil, err
		}
	}
	s.launch = o
	s.timer = time.AfterFunc(startDelay, s.start)
	return s, nil
}

// Zsh reports whether the shell is a zsh whose prompts are marked, for
// the terminal's shell integration.
func (s *Shell) Zsh() bool { return s.zdotdir != "" }

// start starts the shell at the size it was told, once.
func (s *Shell) start() {
	s.mu.Lock()
	o := s.launch
	s.launch = nil
	if o == nil {
		s.mu.Unlock()
		return
	}
	s.timer.Stop()
	if s.closed {
		s.err = errors.New("closed")
		s.mu.Unlock()
		close(s.started)
		return
	}
	o.Cols, o.Rows, o.Width, o.Height = s.cols, s.rows, s.cols*s.cellW, s.rows*s.cellH
	s.proc, s.err = pty.Start(*o)
	s.mu.Unlock()
	close(s.started)
	if s.proc != nil {
		go func() {
			s.proc.Wait()
			// What the shell wrote before it exited is still to be read;
			// then hang up whatever else has the terminal, as terminal apps
			// do.
			time.Sleep(100 * time.Millisecond)
			s.proc.Close()
		}()
	}
}

// Read reads what the shell writes.
func (s *Shell) Read(p []byte) (int, error) {
	<-s.started
	if s.err != nil {
		return 0, s.err
	}
	return s.proc.Read(p)
}

// Write types into the shell.
func (s *Shell) Write(p []byte) (int, error) {
	<-s.started
	if s.err != nil {
		return 0, s.err
	}
	return s.proc.Write(p)
}

// Resize tells the shell the size of the screen in cells.
func (s *Shell) Resize(cols, rows int) error {
	s.mu.Lock()
	return s.resize(cols, rows, s.cellW, s.cellH)
}

// ResizePixels tells the shell the size of the screen with the size of a
// cell in pixels, and starts it the first time.
func (s *Shell) ResizePixels(cols, rows, cellW, cellH int) error {
	s.mu.Lock()
	return s.resize(cols, rows, cellW, cellH)
}

// resize resizes with s.mu held, which it unlocks.
func (s *Shell) resize(cols, rows, cellW, cellH int) error {
	if cols <= 0 || rows <= 0 {
		s.mu.Unlock()
		return nil
	}
	s.cols, s.rows, s.cellW, s.cellH = cols, rows, cellW, cellH
	proc, pending := s.proc, s.launch != nil
	s.mu.Unlock()
	if pending {
		s.start()
		return nil
	}
	if proc == nil {
		return nil
	}
	return proc.Resize(cols, rows, cols*cellW, rows*cellH)
}

// ClearScreen tells Windows' console that the terminal cleared its
// screen, which it otherwise draws again; nothing elsewhere.
func (s *Shell) ClearScreen() {
	s.mu.Lock()
	proc := s.proc
	s.mu.Unlock()
	if proc != nil {
		proc.ClearScreen()
	}
}

// Status is ready until the shell ends: a local terminal does not
// connect.
func (s *Shell) Status() sshconn.Status {
	select {
	case <-s.started:
		if s.err != nil {
			return sshconn.StatusClosed
		}
		select {
		case <-s.proc.Exited():
			return sshconn.StatusClosed
		default:
		}
	default:
	}
	return sshconn.StatusReady
}

// Close ends the shell.
func (s *Shell) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	pending := s.launch != nil
	proc := s.proc
	s.mu.Unlock()
	if pending {
		s.start() // which does not start it, now that it is closed
	}
	if s.zdotdir != "" {
		os.RemoveAll(s.zdotdir)
	}
	if proc != nil {
		return proc.Close()
	}
	return nil
}

// command returns the user's shell as a login shell ("-zsh"), with
// args[0] its name; PowerShell on Windows.
func command() (path string, args []string, err error) {
	if runtime.GOOS == "windows" {
		for _, name := range []string{"pwsh.exe", "powershell.exe", os.Getenv("COMSPEC"), "cmd.exe"} {
			if p, err := exec.LookPath(name); name != "" && err == nil {
				return p, []string{name}, nil
			}
		}
		return "", nil, errors.New("no shell found")
	}
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
		if runtime.GOOS == "darwin" {
			sh = "/bin/zsh"
		}
	}
	return sh, []string{"-" + filepath.Base(sh)}, nil
}

// environment is this process's with a terminal's variables.
func environment() []string {
	env := os.Environ()
	set := func(kv string) {
		k, _, _ := strings.Cut(kv, "=")
		for i, e := range env {
			if strings.HasPrefix(e, k+"=") {
				env[i] = kv
				return
			}
		}
		env = append(env, kv)
	}
	set("TERM=xterm-256color")
	set("COLORTERM=truecolor")
	if runtime.GOOS != "windows" && os.Getenv("LANG") == "" && os.Getenv("LC_ALL") == "" && os.Getenv("LC_CTYPE") == "" {
		set("LANG=en_US.UTF-8")
	}
	return env
}
