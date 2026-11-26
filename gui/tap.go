package gui

import (
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/terminal"

	"ternssh-desktop/internal/web"
)

// tap passes on what a session's terminal reads from its Conn to the
// browsers that show it, which start from a snapshot of the terminal.
//
// The terminal reads its Conn in a loop, showing each read before the
// next: a chunk read is shown once the next Read starts. So a chunk goes
// to the browsers then, and a browser attaching while one is read but not
// yet shown waits for it, so that its snapshot and the chunks that follow
// neither miss nor repeat any.
type tap struct {
	Conn

	mu       sync.Mutex
	term     *terminal.Terminal // once made
	inflight []byte             // read and maybe not yet shown
	shown    *sync.Cond         // signaled as inflight is shown
	subs     map[*tapStream]struct{}
}

func newTap(c Conn) *tap {
	t := &tap{Conn: c, subs: map[*tapStream]struct{}{}}
	t.shown = sync.NewCond(&t.mu)
	return t
}

// Read reads the Conn, passing on what the read before showed.
func (t *tap) Read(p []byte) (int, error) {
	t.mu.Lock()
	t.flush()
	t.mu.Unlock()
	n, err := t.Conn.Read(p)
	t.mu.Lock()
	if n > 0 {
		t.inflight = append(t.inflight[:0], p[:n]...)
	}
	if err != nil {
		// No Read follows to tell the last one shown: the terminal shows
		// it, then that it ended, and the browsers start over from that.
		t.flush()
		t.markStale()
	}
	t.mu.Unlock()
	return n, err
}

// flush sends what was in flight, now shown, with t.mu held.
func (t *tap) flush() {
	if t.inflight == nil {
		return
	}
	if len(t.subs) > 0 {
		cols, rows := t.size()
		for s := range t.subs {
			if s.cols != cols || s.rows != rows {
				// The window resized the terminal: the browser starts over
				// at the new size, once resizing settles.
				s.stale = true
			}
			if s.stale {
				continue
			}
			s.push(web.Frame{Data: append([]byte(nil), t.inflight...)})
		}
	}
	t.inflight = nil
	t.shown.Broadcast()
}

func (t *tap) size() (int, int) {
	if t.term == nil {
		return 80, 24
	}
	return t.term.Size()
}

// setTerminal gives the tap the terminal that reads it.
func (t *tap) setTerminal(term *terminal.Terminal) {
	t.mu.Lock()
	t.term = term
	t.mu.Unlock()
}

// attach starts a browser's stream with what the terminal shows now.
func (t *tap) attach() *tapStream {
	s := &tapStream{t: t, ch: make(chan web.Frame, 512), done: make(chan struct{})}
	t.mu.Lock()
	t.waitShown()
	t.subs[s] = struct{}{}
	s.reset()
	t.mu.Unlock()
	go s.watch()
	return s
}

// waitShown waits, with t.mu held, until no chunk is read but not shown,
// for at most a moment: a Conn that ended has no next Read.
func (t *tap) waitShown() {
	if t.inflight == nil {
		return
	}
	timer := time.AfterFunc(200*time.Millisecond, func() {
		t.mu.Lock()
		t.shown.Broadcast()
		t.mu.Unlock()
	})
	defer timer.Stop()
	deadline := time.Now().Add(200 * time.Millisecond)
	for t.inflight != nil && time.Now().Before(deadline) {
		t.shown.Wait()
	}
}

// markStale has every browser start over at its next tick, with t.mu
// held: for what the terminal shows that it did not read.
func (t *tap) markStale() {
	for s := range t.subs {
		s.stale = true
	}
}

// resyncSoon has every browser start over once what the terminal is about
// to show by itself, as a clear the user asked for, is shown.
func (t *tap) resyncSoon() {
	time.AfterFunc(50*time.Millisecond, func() {
		t.mu.Lock()
		t.markStale()
		t.mu.Unlock()
	})
}

// tapStream is a browser's stream of a terminal.
type tapStream struct {
	t          *tap
	ch         chan web.Frame
	done       chan struct{}
	once       sync.Once
	cols, rows int  // the size the browser shows
	stale      bool // the browser starts over at the next tick
}

func (s *tapStream) Frames() <-chan web.Frame { return s.ch }

func (s *tapStream) Close() {
	s.once.Do(func() {
		s.t.mu.Lock()
		delete(s.t.subs, s)
		s.t.mu.Unlock()
		close(s.done)
	})
}

// push sends a frame, or has the browser start over when it falls behind;
// with t.mu held.
func (s *tapStream) push(f web.Frame) {
	select {
	case s.ch <- f:
	default:
		s.stale = true
	}
}

// reset sends the terminal whole, with t.mu held.
func (s *tapStream) reset() {
	s.cols, s.rows = s.t.size()
	var snap []byte
	if s.t.term != nil {
		snap = s.t.term.Snapshot()
	}
	// The channel may be full: the browser drops what it shows for this.
	for len(s.ch) > 0 {
		select {
		case <-s.ch:
		default:
		}
	}
	s.stale = false
	s.ch <- web.Frame{Reset: true, Cols: s.cols, Rows: s.rows, Data: snap}
}

// watch starts the browser over when it is stale or the terminal resized,
// a few times a second, until the stream closes.
func (s *tapStream) watch() {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	defer close(s.ch)
	for {
		select {
		case <-s.done:
			return
		case <-tick.C:
		}
		t := s.t
		t.mu.Lock()
		if cols, rows := t.size(); s.stale || cols != s.cols || rows != s.rows {
			t.waitShown()
			s.reset()
		}
		t.mu.Unlock()
	}
}
