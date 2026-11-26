package gui

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"ternssh-desktop/internal/sshconn"
	"ternssh-desktop/internal/store"
	"ternssh-desktop/internal/ternssh"
	"ternssh-desktop/internal/web"
	"ternssh-desktop/locales"
)

// WebServer serves the window to browsers (web.Server).
type WebServer interface {
	Apply(opts *web.Options) error
	Err() error
	URLs() []string
	Watching() bool
	Publish(state []byte)
}

// taps are the sessions' taps by ID, for the web's goroutines.
type taps struct {
	sync.Mutex
	m map[string]*tap
}

func (t *taps) put(id string, tp *tap) {
	t.Lock()
	defer t.Unlock()
	if t.m == nil {
		t.m = map[string]*tap{}
	}
	t.m[id] = tp
}

func (t *taps) get(id string) *tap {
	t.Lock()
	defer t.Unlock()
	return t.m[id]
}

func (t *taps) remove(id string) {
	t.Lock()
	defer t.Unlock()
	delete(t.m, id)
}

// SetWeb gives the model the server that shows it on the web, which
// Start starts when the settings say.
func (m *Model) SetWeb(w WebServer) { m.web = w }

// applyWeb starts, restarts or stops the web server as the settings say.
func (m *Model) applyWeb() error {
	if m.web == nil {
		return nil
	}
	st := m.store.Settings().Web
	if !st.Enabled {
		return m.web.Apply(nil)
	}
	port := st.Port
	if port == 0 {
		port = store.DefaultWebPort
	}
	opts := &web.Options{Port: port, Access: st.Access()}
	if opts.Access != web.Local {
		opts.Username, opts.PasswordHash = st.Username, string(m.store.WebPasswordHash())
	}
	return m.web.Apply(opts)
}

// publish sends the browsers what the window shows, when one does.
func (m *Model) publish() {
	if m.web == nil || !m.web.Watching() {
		return
	}
	b, err := json.Marshal(m.webState())
	if err == nil {
		m.web.Publish(b)
	}
}

// What the web shows of the window.
type (
	webState struct {
		Language string   `json:"language"` // "" for the browser's
		Tabs     []webTab `json:"tabs"`
	}
	webTab struct {
		ID       string       `json:"id"` // "home" for this machine's
		Name     string       `json:"name"`
		Home     bool         `json:"home"`
		Loading  bool         `json:"loading"`
		Loaded   bool         `json:"loaded"`
		Err      string       `json:"err,omitempty"`
		Items    []webItem    `json:"items"`
		Commands []webCommand `json:"commands"`
		// CommandsLoaded tells the commands were asked of the site; the
		// web asks for them otherwise.
		CommandsLoaded bool         `json:"commandsLoaded"`
		Sessions       []webSession `json:"sessions"`
	}
	webItem struct {
		Kind     string    `json:"kind"` // group, server or terminal
		Key      string    `json:"key"`  // the sidebar's: r:, l:, t:, or the group's ID
		Name     string    `json:"name"`
		Detail   string    `json:"detail,omitempty"`
		Children []webItem `json:"children,omitempty"`
	}
	webCommand struct {
		ID      string `json:"id"`
		Label   string `json:"label"`
		Command string `json:"command"`
		Builtin bool   `json:"builtin,omitempty"`
	}
	webSession struct {
		ID         string `json:"id"`
		Key        string `json:"key"` // of the sidebar's row it is on
		Title      string `json:"title"`
		Subtitle   string `json:"subtitle"`
		Shell      bool   `json:"shell"`
		Status     string `json:"status"` // connecting, ready, error, closed
		StatusText string `json:"statusText"`
		Err        string `json:"err,omitempty"`
		Agent      string `json:"agent,omitempty"` // what its coding agent does, as its tab's tooltip says
		Shown      bool   `json:"shown"`           // the window shows it
	}
)

func tabID(s *siteState) string {
	if s.home() {
		return "home"
	}
	return s.site.ID
}

func (m *Model) webState() webState {
	st := webState{Language: m.store.Settings().Language}
	for i, s := range m.sites {
		tab := webTab{
			ID: tabID(s), Name: s.displayName(), Home: s.home(),
			Loading: s.loading, Loaded: s.home() || !s.loaded.IsZero(), Err: s.err,
			CommandsLoaded: s.commandsLoaded,
		}
		if s.home() {
			tab.Items = homeItems(s, "")
		} else {
			tab.Items = siteItems(s.tree)
		}
		for _, q := range s.commands {
			tab.Commands = append(tab.Commands, webCommand{ID: q.ID, Label: q.Label, Command: q.Command})
		}
		for _, q := range builtinCommands() {
			tab.Commands = append(tab.Commands, webCommand{ID: q.ID, Label: q.Label, Command: q.Command, Builtin: true})
		}
		for j, sess := range s.sessions {
			ws := webSession{
				ID: sess.id, Key: sess.on(), Title: sess.title, Subtitle: sess.subtitle,
				Shell: sess.mode == Shell, Status: statusName(sess.status), StatusText: sess.status.String(),
				Err: sess.err, Shown: i == m.active && j == s.current,
			}
			if sess.agent.state != agentNone {
				ws.Agent = agentTip(sess.agent)
			}
			tab.Sessions = append(tab.Sessions, ws)
		}
		st.Tabs = append(st.Tabs, tab)
	}
	return st
}

func statusName(st sshconn.Status) string {
	switch st {
	case sshconn.StatusConnecting:
		return "connecting"
	case sshconn.StatusReady:
		return "ready"
	case sshconn.StatusError:
		return "error"
	}
	return "closed"
}

// homeItems are what is in the group parent of this machine's tab, as
// its sidebar lists them.
func homeItems(s *siteState, parent string) []webItem {
	var items []webItem
	for _, it := range store.Children(s.groups, s.terminals, s.local, parent) {
		switch it.Kind {
		case store.KindGroup:
			for _, g := range s.groups {
				if g.ID == it.ID {
					items = append(items, webItem{Kind: "group", Key: g.ID, Name: g.Name, Children: homeItems(s, g.ID)})
				}
			}
		case store.KindTerminal:
			for _, t := range s.terminals {
				if t.ID == it.ID {
					items = append(items, webItem{Kind: "terminal", Key: "t:" + t.ID, Name: t.Name, Detail: displayDir(t.Dir)})
				}
			}
		case store.KindServer:
			for _, srv := range s.local {
				if srv.ID == it.ID {
					items = append(items, webItem{Kind: "server", Key: "l:" + srv.ID, Name: srv.Name,
						Detail: srv.Username + "@" + srv.Host + portSuffix(srv.Port)})
				}
			}
		}
	}
	return items
}

// siteItems are a site's servers in their groups.
func siteItems(nodes []ternssh.Node) []webItem {
	var items []webItem
	for _, n := range nodes {
		if n.Type == "server" {
			items = append(items, webItem{Kind: "server", Key: "r:" + n.ID, Name: n.Name,
				Detail: n.Username + "@" + n.Host + portSuffix(n.Port)})
			continue
		}
		items = append(items, webItem{Kind: "group", Key: n.ID, Name: n.Name, Children: siteItems(n.Children)})
	}
	return items
}

// webOp is what a browser asks of the window.
type webOp struct {
	Op      string `json:"op"`
	Tab     string `json:"tab"`
	Key     string `json:"key"`
	Session string `json:"session"`
	Command string `json:"command"`
}

// Act does what a browser asked, on the window's thread (web.App).
func (m *Model) Act(raw json.RawMessage) {
	var op webOp
	if json.Unmarshal(raw, &op) != nil {
		return
	}
	m.update(func() { m.act(op) })
}

func (m *Model) act(op webOp) {
	switch op.Op {
	case "hello":
		// publish, which follows, sends the state to the browser that came.
	case "refresh":
		if s := m.tab(op.Tab); s != nil {
			m.refresh(s)
			if s.commandsLoaded {
				s.commandsLoaded = false
				m.loadCommands(s)
			}
		}
	case "load":
		// A tab the browser shows that the window has not loaded.
		if s := m.tab(op.Tab); s != nil {
			if !s.home() && s.loaded.IsZero() && !s.loading {
				m.refresh(s)
			}
			if !s.commandsLoaded {
				m.loadCommands(s)
			}
		}
	case "open":
		s := m.tab(op.Tab)
		if s == nil {
			return
		}
		// The window goes on showing its terminal, unless it has none.
		had, cur := len(s.sessions), s.current
		if !m.openOn(s, op.Key) {
			m.notify(locales.WebOpenFailed())
			return
		}
		if had > 0 && len(s.sessions) > had {
			s.current = cur
		}
	case "close":
		if s, i := m.findSession(op.Session); s != nil {
			m.closeSession(s, i)
		}
	case "reconnect":
		if s, i := m.findSession(op.Session); s != nil {
			cur := s.current
			m.reconnect(s, i)
			s.current = cur
		}
	case "run":
		if s, i := m.findSession(op.Session); s != nil {
			sess := s.sessions[i]
			if sess.status == sshconn.StatusReady {
				input(sess, strings.ReplaceAll(strings.ReplaceAll(op.Command, "\r\n", "\n"), "\n", "\r")+"\r", false)
			}
		}
	}
}

// tab returns the tab of an ID webState gave.
func (m *Model) tab(id string) *siteState {
	for _, s := range m.sites {
		if tabID(s) == id {
			return s
		}
	}
	return nil
}

func (m *Model) findSession(id string) (*siteState, int) {
	for _, s := range m.sites {
		for i, sess := range s.sessions {
			if sess.id == id {
				return s, i
			}
		}
	}
	return nil, -1
}

// Attach streams the terminal of a session to a browser (web.App).
func (m *Model) Attach(session string) (web.Stream, error) {
	tp := m.taps.get(session)
	if tp == nil {
		return nil, errors.New(locales.WebNoTerminal())
	}
	return tp.attach(), nil
}

// Input types what a browser typed into the terminal of a session
// (web.App).
func (m *Model) Input(session string, data []byte) {
	tp := m.taps.get(session)
	if tp == nil {
		return
	}
	tp.mu.Lock()
	term := tp.term
	tp.mu.Unlock()
	if term != nil {
		term.Send(data)
	}
}
