// Package sshconn connects terminals to SSH servers from this machine,
// with credentials kept here or fetched from a ternssh site. A connection
// is the Conn of a terminal of the terminal plugin: an io.ReadWriteCloser of the shell's
// output and input, told the terminal's size with Resize.
package sshconn

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Status is how far a connection got.
type Status int32

const (
	StatusConnecting Status = iota
	StatusReady
	StatusError
	StatusClosed
)

func (s Status) String() string {
	switch s {
	case StatusConnecting:
		return "连接中"
	case StatusReady:
		return "已连接"
	case StatusError:
		return "连接失败"
	default:
		return "已断开"
	}
}

// Events are told what happens to a connection, on goroutines of its own.
type Events struct {
	OnStatus func(s Status, err error)
	OnDir    func(path string)
}

// stream is the output side every connection shares: the shell's output,
// and notices of the connection's own, which the terminal reads.
type stream struct {
	events Events
	ctx    context.Context
	cancel context.CancelFunc

	st  atomic.Int32
	err atomic.Value // error

	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	closed bool
}

func newStream(events Events) *stream {
	s := &stream{events: events}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Status returns how far the connection got.
func (s *stream) Status() Status { return Status(s.st.Load()) }

// Err returns why the connection failed, if it did.
func (s *stream) Err() error {
	err, _ := s.err.Load().(error)
	return err
}

func (s *stream) status(st Status, _ string) {
	s.st.Store(int32(st))
	if s.events.OnStatus != nil {
		s.events.OnStatus(st, s.Err())
	}
}

// Read returns the shell's output, blocking until there is some, and
// io.EOF once the connection ended and all of it was read.
func (s *stream) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.buf) == 0 && !s.closed {
		s.cond.Wait()
	}
	if len(s.buf) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}

func (s *stream) output(p []byte) {
	s.mu.Lock()
	if !s.closed {
		s.buf = append(s.buf, p...)
		s.cond.Broadcast()
	}
	s.mu.Unlock()
}

// notice writes a line of the connection's own to the terminal, dimmed.
func (s *stream) notice(format string, args ...any) {
	s.output([]byte("\x1b[2m" + fmt.Sprintf(format, args...) + "\x1b[0m\r\n"))
}

func (s *stream) ready() {
	s.status(StatusReady, "")
}

func (s *stream) dir(path string) {
	if s.events.OnDir != nil && path != "" {
		s.events.OnDir(path)
	}
}

// fail ends the connection with err, which the terminal shows.
func (s *stream) fail(err error) {
	if s.Status() == StatusError || s.Status() == StatusClosed {
		return
	}
	s.err.Store(err)
	s.output([]byte("\r\n\x1b[31m" + err.Error() + "\x1b[0m\r\n"))
	s.status(StatusError, "")
	s.finish()
}

// end ends the connection as the shell exited.
func (s *stream) end() {
	if s.Status() == StatusError || s.Status() == StatusClosed {
		return
	}
	s.output([]byte("\r\n\x1b[2m连接已关闭\x1b[0m\r\n"))
	s.status(StatusClosed, "")
	s.finish()
}

// finish lets the terminal read what is left, and then io.EOF. The
// terminal stays open on what it shows, so the user reads why it ended.
func (s *stream) finish() {
	s.cancel()
	s.mu.Lock()
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
}

// Close ends the connection.
func (s *stream) Close() error {
	if st := s.Status(); st != StatusError {
		s.st.Store(int32(StatusClosed))
	}
	s.finish()
	return nil
}
