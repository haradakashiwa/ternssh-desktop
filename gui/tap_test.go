package gui

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"

	"ternssh-desktop/internal/sshconn"
	"ternssh-desktop/internal/web"
)

// chunkConn reads the chunks sent on out, and ends as out closes.
type chunkConn struct {
	fakeConn
	out chan []byte
}

func (c *chunkConn) Read(p []byte) (int, error) {
	b, ok := <-c.out
	if !ok {
		return 0, io.EOF
	}
	return copy(p, b), nil
}

// newTestTerminal makes a terminal reading conn, or skips the test where
// libghostty-vt cannot load.
func newTestTerminal(t *testing.T, conn io.ReadWriteCloser) *terminal.Terminal {
	t.Helper()
	term, err := terminal.New(terminal.Options{Conn: conn})
	if err != nil {
		t.Skip("no terminal: ", err)
	}
	t.Cleanup(func() { term.Close() })
	return term
}

// waitText waits until the terminal shows want.
func waitText(t *testing.T, term *terminal.Terminal, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(term.Text(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("the terminal never showed %q; it shows:\n%s", want, term.Text())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// replay shows the frames of a stream in another terminal, as a browser
// does, until it shows want.
func replay(t *testing.T, s web.Stream, want string) *terminal.Terminal {
	t.Helper()
	other := newTestTerminal(t, &scriptConn{fakeConn: fakeConn{status: sshconn.StatusReady}})
	deadline := time.After(5 * time.Second)
	for {
		select {
		case f := <-s.Frames():
			if f.Reset {
				other.Feed([]byte("\x1bc"))
				other.Resize(f.Cols, f.Rows)
			}
			other.Feed(f.Data)
			if strings.Contains(other.Text(), want) {
				return other
			}
		case <-deadline:
			t.Fatalf("the browser never showed %q; it shows:\n%s", want, other.Text())
		}
	}
}

func TestTapStreamsWhatTheTerminalShows(t *testing.T) {
	conn := &chunkConn{fakeConn: fakeConn{status: sshconn.StatusReady}, out: make(chan []byte)}
	tp := newTap(conn)
	term := newTestTerminal(t, tp)
	tp.setTerminal(term)

	conn.out <- []byte("before the browser\r\n")
	waitText(t, term, "before the browser")

	// Output keeps coming as the browser attaches: it misses no line, and
	// shows none twice.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 300 {
			line := fmt.Sprintf("line %03d\r\n", i)
			// Split, so that reads end within lines.
			conn.out <- []byte(line[:4])
			conn.out <- []byte(line[4:])
		}
		conn.out <- []byte("the end")
	}()
	time.Sleep(2 * time.Millisecond)
	s := tp.attach()
	defer s.Close()
	<-done
	waitText(t, term, "the end")
	other := replay(t, s, "the end")
	if got, want := other.Text(), term.Text(); got != want {
		t.Fatalf("the browser shows\n%s\nthe terminal shows\n%s", got, want)
	}
}

func TestTapStartsOverWhenCleared(t *testing.T) {
	conn := &chunkConn{fakeConn: fakeConn{status: sshconn.StatusReady}, out: make(chan []byte)}
	tp := newTap(conn)
	term := newTestTerminal(t, tp)
	tp.setTerminal(term)
	conn.out <- []byte("old output\r\n$ ")
	waitText(t, term, "$")
	s := tp.attach()
	defer s.Close()
	replay(t, s, "old output")

	// The user clears in the window, which the program does not write.
	tp.resyncSoon()
	term.ClearScreen()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case f := <-s.Frames():
			if f.Reset && !strings.Contains(string(f.Data), "old output") {
				return
			}
		case <-deadline:
			t.Fatal("the browser was not started over after the clear")
		}
	}
}
