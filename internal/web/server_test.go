package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/net/websocket"
)

// fakeApp records what browsers ask, and streams one terminal.
type fakeApp struct {
	mu     sync.Mutex
	acts   []string
	inputs []string
	frames chan Frame
}

func (a *fakeApp) Act(op json.RawMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.acts = append(a.acts, string(op))
}

func (a *fakeApp) Attach(id string) (Stream, error) { return fakeStream{a.frames}, nil }

func (a *fakeApp) Input(id string, data []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.inputs = append(a.inputs, id+":"+string(data))
}

type fakeStream struct{ ch chan Frame }

func (s fakeStream) Frames() <-chan Frame { return s.ch }
func (s fakeStream) Close()               {}

// password is the test server's, hashed at the lowest cost.
var passwordHash = func() string {
	h, _ := bcrypt.GenerateFromPassword([]byte("correct horse"), bcrypt.MinCost)
	return string(h)
}()

func start(t *testing.T, access string) (*Server, *fakeApp, string) {
	t.Helper()
	app := &fakeApp{frames: make(chan Frame, 8)}
	s := New(app)
	if err := s.Apply(&Options{Port: 0, Access: access, Username: "me", PasswordHash: passwordHash}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Apply(nil) })
	return s, app, "http://" + s.ln.Addr().String()
}

func get(t *testing.T, url, user, pass string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res
}

func TestBasicAuth(t *testing.T) {
	_, _, base := start(t, LAN)
	if res := get(t, base+"/", "", ""); res.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Basic ") {
		t.Fatalf("without a password: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	if res := get(t, base+"/", "me", "wrong"); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a wrong password: %d", res.StatusCode)
	}
	for _, path := range []string{"/", "/assets/app.js", "/assets/xterm.js", "/assets/icon.png"} {
		if res := get(t, base+path, "me", "correct horse"); res.StatusCode != http.StatusOK {
			t.Errorf("%s signed in: %d", path, res.StatusCode)
		}
	}
	// Even the page's scripts ask.
	if res := get(t, base+"/assets/app.js", "", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a script without a password: %d", res.StatusCode)
	}
}

func TestLocalNeedsNoPasswordButItsOwnHost(t *testing.T) {
	_, _, base := start(t, Local)
	if res := get(t, base+"/", "", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("this machine: %d", res.StatusCode)
	}
	// A page of another site whose name points at this machine.
	req, _ := http.NewRequest("GET", base+"/", nil)
	req.Host = "evil.example:17722"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("another host: %d", res.StatusCode)
	}
}

func dial(t *testing.T, base, origin, user, pass string) (*websocket.Conn, error) {
	cfg, _ := websocket.NewConfig(strings.Replace(base, "http", "ws", 1)+"/ws", origin)
	cfg.Header = http.Header{}
	if user != "" {
		r := &http.Request{Header: cfg.Header}
		r.SetBasicAuth(user, pass)
	}
	return websocket.DialConfig(cfg)
}

func TestWebSocket(t *testing.T) {
	s, app, base := start(t, LAN)
	if ws, err := dial(t, base, "http://evil.example", "me", "correct horse"); err == nil {
		ws.Close()
		t.Fatal("another site's page opened the WebSocket")
	}
	if ws, err := dial(t, base, base, "me", "wrong"); err == nil {
		ws.Close()
		t.Fatal("the WebSocket opened with a wrong password")
	}
	ws, err := dial(t, base, base, "me", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetDeadline(time.Now().Add(5 * time.Second))

	// The window publishes its state as the browser comes.
	deadline := time.Now().Add(2 * time.Second)
	for !s.Watching() {
		if time.Now().After(deadline) {
			t.Fatal("not watching")
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.Publish([]byte(`{"tabs":[]}`))
	var m message
	if err := websocket.JSON.Receive(ws, &m); err != nil || m.Type != "state" || string(m.State) != `{"tabs":[]}` {
		t.Fatalf("state: %+v %v", m, err)
	}

	websocket.JSON.Send(ws, message{Type: "watch", ID: "s1"})
	app.frames <- Frame{Reset: true, Cols: 100, Rows: 30, Data: []byte("\x1b[1mhi\xff")}
	if err := websocket.JSON.Receive(ws, &m); err != nil || m.Type != "term" || !m.Reset || m.Cols != 100 || string(m.Data) != "\x1b[1mhi\xff" {
		t.Fatalf("frame: %+v %v", m, err)
	}

	websocket.JSON.Send(ws, message{Type: "input", ID: "s1", Text: "ls\r"})
	websocket.JSON.Send(ws, message{Type: "action", Action: json.RawMessage(`{"op":"open","tab":"home","key":"t:1"}`)})
	deadline = time.Now().Add(2 * time.Second)
	for {
		app.mu.Lock()
		inputs, acts := strings.Join(app.inputs, ","), strings.Join(app.acts, ",")
		app.mu.Unlock()
		if inputs == "s1:ls\r" && strings.Contains(acts, `"op":"open"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("inputs %q, acts %q", inputs, acts)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWhere(t *testing.T) {
	for addr, want := range map[string]string{
		"127.0.0.1:1": Local, "[::1]:1": Local, "[::ffff:127.0.0.1]:1": Local,
		"192.168.1.5:1": LAN, "10.0.0.2:1": LAN, "172.16.3.4:1": LAN, "100.101.1.2:1": LAN, "[fd00::1]:1": LAN, "[fe80::1]:1": LAN,
		"8.8.8.8:1": Public, "[2001:4860::8888]:1": Public, "[::ffff:8.8.8.8]:1": Public, "garbage": Public,
	} {
		if got := where(&http.Request{RemoteAddr: addr}); got != want {
			t.Errorf("%s: %s, want %s", addr, got, want)
		}
	}
}

// request runs one request of the auth handler from addr.
func request(s *Server, o Options, target, addr, user, pass string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", target, nil)
	r.RemoteAddr = addr
	if user != "" {
		r.SetBasicAuth(user, pass)
	}
	w := httptest.NewRecorder()
	s.auth(o, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("app")) })).ServeHTTP(w, r)
	return w
}

func TestAccess(t *testing.T) {
	s := New(&fakeApp{})
	const lan, pub = "192.168.1.9:5000", "8.8.8.8:5000"
	local := Options{Access: Local}
	if w := request(s, local, "http://127.0.0.1/", lan, "", ""); w.Code != http.StatusForbidden {
		t.Errorf("this machine only, from the LAN: %d", w.Code)
	}
	lanOnly := Options{Access: LAN, Username: "me", PasswordHash: passwordHash}
	if w := request(s, lanOnly, "/", lan, "me", "correct horse"); w.Body.String() != "app" {
		t.Errorf("LAN, from the LAN: %d", w.Code)
	}
	if w := request(s, lanOnly, "/", pub, "me", "correct horse"); w.Code != http.StatusForbidden {
		t.Errorf("LAN, from the internet: %d", w.Code)
	}
	// Behind a reverse proxy, requests come from this machine: they sign
	// in too.
	if w := request(s, lanOnly, "/", "127.0.0.1:5000", "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("LAN, from this machine without a password: %d", w.Code)
	}
	public := Options{Access: Public, Username: "me", PasswordHash: passwordHash}
	if w := request(s, public, "/", pub, "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("public without a password: %d", w.Code)
	}
	if w := request(s, public, "/", pub, "me", "correct horse"); w.Body.String() != "app" {
		t.Errorf("public signed in: %d", w.Code)
	}
}

func TestLockout(t *testing.T) {
	s := New(&fakeApp{})
	o := Options{Access: Public, Username: "me", PasswordHash: passwordHash}
	const bad, other = "9.9.9.9:1", "8.8.4.4:1"
	for i := range maxFailures {
		if w := request(s, o, "/", bad, "me", "guess"); w.Code != http.StatusUnauthorized {
			t.Fatalf("guess %d: %d", i, w.Code)
		}
	}
	// It waits, even with the right password; other addresses do not.
	w := request(s, o, "/", bad, "me", "correct horse")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("after %d guesses: %d", maxFailures, w.Code)
	}
	if w := request(s, o, "/", other, "me", "correct horse"); w.Body.String() != "app" {
		t.Fatalf("another address: %d", w.Code)
	}
	// Each round waits twice as long, up to maxLockout.
	f := &failures{}
	var waits []time.Duration
	for range 10 {
		for range maxFailures - 1 {
			f.failed("h")
		}
		waits = append(waits, f.failed("h"))
	}
	if waits[0] != lockout || waits[1] != 2*lockout || waits[2] != 4*lockout || waits[9] != maxLockout {
		t.Fatalf("waits: %v", waits)
	}
	f.passed("h")
	if f.blocked("h") > 0 {
		t.Fatal("still waiting after signing in")
	}
}

func TestPublicNeedsAPassword(t *testing.T) {
	s := New(&fakeApp{})
	for _, access := range []string{LAN, Public} {
		if err := s.Apply(&Options{Port: 0, Access: access}); !errors.Is(err, ErrNoPassword) || s.Listening() {
			t.Fatalf("%s started without a password: %v", access, err)
		}
	}
}

// TestSignInOnce: the page's sign-in lets the WebSocket in by its cookie,
// for browsers that open it without the username and password, and the
// WebSocket's 401 does not ask for them again.
func TestSignInOnce(t *testing.T) {
	s, _, base := start(t, LAN)
	res := get(t, base+"/", "me", "correct horse")
	var cookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("no session cookie: %v", res.Cookies())
	}
	withCookie := func(path string) *http.Response {
		req, _ := http.NewRequest("GET", base+path, nil)
		req.AddCookie(cookie)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	if res := withCookie("/assets/app.js"); res.StatusCode != http.StatusOK {
		t.Fatalf("with the cookie: %d", res.StatusCode)
	}
	cfg, _ := websocket.NewConfig(strings.Replace(base, "http", "ws", 1)+"/ws", base)
	cfg.Header = http.Header{"Cookie": {cookie.String()}}
	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("the WebSocket with the cookie: %v", err)
	}
	ws.Close()
	if res := get(t, base+"/ws", "", ""); res.StatusCode != http.StatusUnauthorized || res.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("the WebSocket without signing in: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	// A forged cookie does not, nor one from before the settings changed.
	cookie.Value = "forged"
	if res := withCookie("/"); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a forged cookie: %d", res.StatusCode)
	}
	cookie.Value = s.sessions.add()
	s.sessions.clear()
	if res := withCookie("/"); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a forgotten session: %d", res.StatusCode)
	}
}
