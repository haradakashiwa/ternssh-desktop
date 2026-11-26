// Package web serves the app in a browser: the tabs, servers, groups,
// quick commands and terminals of the window, kept in step with it as
// either side changes them. Its terminals are the window's own sessions,
// not copies: what is typed in either shows in both.
//
// It lets in this machine only, the local network too, or any address, as
// the settings say. Let in beyond this machine, every browser signs in
// with the username and password the settings set (HTTP basic
// authentication), and an address that keeps getting them wrong waits
// longer and longer.
package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/net/websocket"
)

//go:embed assets
var assets embed.FS

// Frame is what a terminal shows next on the web: output to show, or,
// with Reset, the whole terminal again at its size, as when the window
// resized it.
type Frame struct {
	Reset      bool
	Cols, Rows int
	Data       []byte
}

// Stream is a terminal's frames for one browser, from what it shows as
// the browser attaches.
type Stream interface {
	Frames() <-chan Frame
	Close()
}

// App is the window the web shows.
type App interface {
	// Act does what the browser asked, on the window's thread: op with
	// its arguments, as {"op":"open","tab":"…","key":"r:…"}.
	Act(op json.RawMessage)
	// Attach streams the terminal of a session.
	Attach(session string) (Stream, error)
	// Input types data into the terminal of a session.
	Input(session string, data []byte)
}

// Who the server lets in (Options.Access).
const (
	Local  = "local"  // this machine
	LAN    = "lan"    // and private addresses: the local network, VPNs
	Public = "public" // and any address, which signs in with a password
)

// Options are where the server listens and who it lets in.
type Options struct {
	Port   int
	Access string // Local, LAN or Public
	// Username and PasswordHash, a bcrypt hash, are what browsers sign in
	// with, which LAN and Public need.
	Username, PasswordHash string
}

// Server serves the web app while the settings turn it on.
type Server struct {
	app App

	mu      sync.Mutex
	opts    Options
	ln      net.Listener
	srv     *http.Server
	err     error
	state   []byte // the window's state, as last published
	clients map[*client]struct{}
	fails   failures
	// signedIn are the credentials checked right, by their hash, so that
	// each request does not run bcrypt again; Apply forgets them.
	signedIn sync.Map
}

// New returns a server of app, which Apply starts.
func New(app App) *Server {
	return &Server{app: app, clients: map[*client]struct{}{}}
}

// Apply listens as opts say, or stops for nil, when it does not already.
// The error is also kept for Err.
func (s *Server) Apply(opts *Options) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if opts != nil && s.ln != nil && *opts == s.opts {
		return nil
	}
	if s.srv != nil {
		s.srv.Close()
		s.srv, s.ln = nil, nil
		for c := range s.clients {
			c.ws.Close()
		}
	}
	s.err = nil
	s.signedIn.Clear()
	if opts == nil {
		return nil
	}
	s.opts = *opts
	if opts.Access != Local && (opts.Username == "" || opts.PasswordHash == "") {
		s.err = ErrNoPassword
		return s.err
	}
	host := "127.0.0.1"
	if opts.Access != Local {
		host = ""
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(opts.Port)))
	if err != nil {
		s.err = err
		return err
	}
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "assets")
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(static)))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		b, _ := assets.ReadFile("assets/index.html")
		w.Write(b)
	})
	mux.Handle("GET /ws", websocket.Server{Handshake: sameOrigin, Handler: s.serveWS})
	s.ln = ln
	s.srv = &http.Server{Handler: s.auth(*opts, mux), ReadHeaderTimeout: 10 * time.Second}
	go func(srv *http.Server) {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Print("web: ", err)
		}
	}(s.srv)
	return nil
}

// Err is why the server could not listen, nil when it does or is off.
func (s *Server) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Listening reports whether the server is on.
func (s *Server) Listening() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ln != nil
}

// URLs are the addresses that open the web app: this machine's, then the
// local network's when it is let in.
func (s *Server) URLs() []string {
	s.mu.Lock()
	opts, on := s.opts, s.ln != nil
	s.mu.Unlock()
	if !on {
		return nil
	}
	link := func(host string) string {
		return "http://" + net.JoinHostPort(host, strconv.Itoa(opts.Port)) + "/"
	}
	urls := []string{link("127.0.0.1")}
	if opts.Access != Local {
		for _, ip := range lanAddrs() {
			urls = append(urls, link(ip))
		}
	}
	return urls
}

// lanAddrs are this machine's IPv4 addresses on its networks.
func lanAddrs() []string {
	addrs, _ := net.InterfaceAddrs()
	var out []string
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() && n.IP.To4() != nil {
			out = append(out, n.IP.String())
		}
	}
	sort.Strings(out)
	return out
}

// Watching reports whether a browser shows the app, for the window to
// publish its state.
func (s *Server) Watching() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients) > 0
}

// Publish sends the window's state to the browsers, when it changed.
func (s *Server) Publish(state []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if string(state) == string(s.state) {
		return
	}
	s.state = state
	for c := range s.clients {
		c.send(message{Type: "state", State: state})
	}
}

// ErrNoPassword is why access beyond this machine does not start: it
// needs a username and a password.
var ErrNoPassword = errors.New("access beyond this machine needs a username and a password")

// where says whose address a request comes from: Local, LAN (private
// addresses: the local network, VPNs) or Public.
func where(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return Public
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return Public
	}
	ip = ip.Unmap()
	switch {
	case ip.IsLoopback():
		return Local
	case ip.IsPrivate() || ip.IsLinkLocalUnicast() || cgnat.Contains(ip):
		return LAN
	}
	return Public
}

// cgnat is shared address space, which VPNs such as Tailscale use.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// auth lets in who the options let in: this machine as it is, asked by a
// page of its own (the Host a browser sends, which a site that has its
// name point at this machine cannot make), and everyone beyond it, only
// with the username and password.
func (s *Server) auth(o Options, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		from := where(r)
		if from == Public && o.Access != Public || from == LAN && o.Access == Local {
			http.Error(w, "ternssh: this address is not let in", http.StatusForbidden)
			return
		}
		if o.Access == Local {
			if !localHost(r.Host) {
				http.Error(w, "ternssh: open the web app at 127.0.0.1 or localhost", http.StatusMisdirectedRequest)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if s.signIn(w, r, o) {
			next.ServeHTTP(w, r)
		}
	})
}

// localHost reports whether a request's Host names this machine.
func localHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// signIn checks the request's username and password, and otherwise asks
// for them, or says to wait after too many wrong ones.
func (s *Server) signIn(w http.ResponseWriter, r *http.Request, o Options) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if wait := s.fails.blocked(host); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("ternssh: too many wrong passwords from this address. Try again in " + wait.Round(time.Second).String() + ".\n" +
			"ternssh：此地址密码错误次数过多，请在 " + wait.Round(time.Second).String() + " 后重试。\n"))
		return false
	}
	user, pass, ok := r.BasicAuth()
	if ok {
		key := sha256.Sum256([]byte(user + "\x00" + pass))
		if _, known := s.signedIn.Load(key); known {
			return true
		}
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte(o.Username)) == 1
		passOK := bcrypt.CompareHashAndPassword([]byte(o.PasswordHash), []byte(pass)) == nil
		if userOK && passOK {
			s.fails.passed(host)
			s.signedIn.Store(key, struct{}{})
			return true
		}
		if wait := s.fails.failed(host); wait > 0 {
			log.Printf("web: %s locked out for %s after wrong passwords", host, wait)
		}
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="ternssh", charset="UTF-8"`)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte("ternssh: sign in with the username and password set in the app's settings.\n" +
		"ternssh：请使用应用设置中设定的用户名和密码登录。\n"))
	return false
}

// failures counts the wrong passwords of each address: after maxFailures
// in a row, it waits lockout, then twice as long after each next round, up
// to maxLockout, until it signs in.
type failures struct {
	mu sync.Mutex
	m  map[string]*failure
}

type failure struct {
	n      int           // wrong in a row, since the last wait
	rounds int           // the waits it had
	until  time.Time     // when it may try again
	seen   time.Time     // the last wrong one
	wait   time.Duration // its last wait
}

const (
	maxFailures = 5
	lockout     = time.Minute
	maxLockout  = time.Hour
	// forgetAfter forgets an address with no wrong password for a day.
	forgetAfter = 24 * time.Hour
)

func (f *failures) blocked(host string) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e := f.m[host]; e != nil {
		return time.Until(e.until)
	}
	return 0
}

// failed counts a wrong password, and returns how long the address now
// waits, 0 for not.
func (f *failures) failed(host string) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	if f.m == nil {
		f.m = map[string]*failure{}
	}
	for h, e := range f.m {
		if now.Sub(e.seen) > forgetAfter && now.After(e.until) {
			delete(f.m, h)
		}
	}
	e := f.m[host]
	if e == nil {
		e = &failure{}
		f.m[host] = e
	}
	e.n++
	e.seen = now
	if e.n < maxFailures {
		return 0
	}
	e.wait = min(lockout<<e.rounds, maxLockout)
	e.n, e.until = 0, now.Add(e.wait)
	if e.wait < maxLockout {
		e.rounds++
	}
	return e.wait
}

func (f *failures) passed(host string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, host)
}

// sameOrigin refuses WebSockets other sites' pages open.
func sameOrigin(cfg *websocket.Config, r *http.Request) error {
	o, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || !strings.EqualFold(o.Host, r.Host) {
		return errors.New("cross-origin")
	}
	cfg.Origin = o
	return nil
}

// message is a WebSocket message, either way.
type message struct {
	Type string `json:"type"`
	// state
	State json.RawMessage `json:"state,omitempty"`
	// term, watch, input
	ID    string `json:"id,omitempty"`
	Reset bool   `json:"reset,omitempty"`
	Cols  int    `json:"cols,omitempty"`
	Rows  int    `json:"rows,omitempty"`
	Data  []byte `json:"data,omitempty"` // base64
	Text  string `json:"text,omitempty"` // typed
	// action
	Action json.RawMessage `json:"action,omitempty"`
}

// client is a browser's WebSocket.
type client struct {
	ws   *websocket.Conn
	out  chan message
	done chan struct{}
	once sync.Once
}

// send queues m, dropping the browser if it falls far behind.
func (c *client) send(m message) {
	select {
	case c.out <- m:
	case <-c.done:
	default:
		c.close()
	}
}

func (c *client) close() {
	c.once.Do(func() {
		close(c.done)
		c.ws.Close()
	})
}

func (s *Server) serveWS(ws *websocket.Conn) {
	ws.PayloadType = websocket.TextFrame
	c := &client{ws: ws, out: make(chan message, 1024), done: make(chan struct{})}
	defer c.close()
	go func() {
		for {
			select {
			case m := <-c.out:
				ws.SetWriteDeadline(time.Now().Add(30 * time.Second))
				if err := websocket.JSON.Send(ws, m); err != nil {
					c.close()
					return
				}
			case <-c.done:
				return
			}
		}
	}()

	s.mu.Lock()
	s.clients[c] = struct{}{}
	state := s.state
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, c)
		s.mu.Unlock()
	}()
	if state != nil {
		c.send(message{Type: "state", State: state})
	}
	// The window publishes its state for the browser that came.
	s.app.Act(json.RawMessage(`{"op":"hello"}`))

	var watched Stream
	defer func() {
		if watched != nil {
			watched.Close()
		}
	}()
	for {
		var m message
		if err := websocket.JSON.Receive(ws, &m); err != nil {
			return
		}
		switch m.Type {
		case "watch":
			if watched != nil {
				watched.Close()
				watched = nil
			}
			if m.ID == "" {
				continue
			}
			st, err := s.app.Attach(m.ID)
			if err != nil {
				c.send(message{Type: "term", ID: m.ID, Reset: true, Cols: 80, Rows: 24,
					Data: []byte("\x1b[2m" + err.Error() + "\x1b[0m")})
				continue
			}
			watched = st
			go func(id string) {
				for f := range st.Frames() {
					c.send(message{Type: "term", ID: id, Reset: f.Reset, Cols: f.Cols, Rows: f.Rows, Data: f.Data})
				}
			}(m.ID)
		case "input":
			data := m.Data
			if m.Text != "" {
				data = []byte(m.Text)
			}
			if m.ID != "" && len(data) > 0 {
				s.app.Input(m.ID, data)
			}
		case "action":
			if len(m.Action) > 0 {
				s.app.Act(m.Action)
			}
		}
	}
}
