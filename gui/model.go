// Package gui is the app's native UI: tabs at the top of the window for
// where servers are kept, first this machine and then each ternssh site
// they are synced with, the tab's servers in a sidebar, and terminals on
// them, which this machine connects to itself.
package gui

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/terminal"

	"ternssh-desktop/internal/sshconn"
	"ternssh-desktop/internal/store"
	"ternssh-desktop/internal/ternssh"
	"ternssh-desktop/locales"
)

// Mode is where a server is kept. Either way this machine makes the SSH
// connection.
type Mode int

const (
	// Synced servers are kept on the site, which gives their credentials
	// when connecting, so its web app and other machines have them too.
	Synced Mode = iota
	// Local servers and their credentials are kept only on this machine,
	// in the 本机 tab.
	Local
	// Shell is a local terminal: a shell on this machine.
	Shell
)

// Conn is a connection a terminal shows.
type Conn interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
	Status() sshconn.Status
}

// Backend is what the model needs of the app, which tests replace.
type Backend struct {
	// Update runs fn on the main thread and builds the view again.
	Update func(fn func())
	// NewTerminal makes the terminal of a connection.
	NewTerminal func(conn Conn) (*terminal.Terminal, error)
	// NewShell makes a terminal of a shell of this machine, started in
	// dir, which tells onExit when it ends.
	NewShell func(dir string, onExit func(code int)) (*terminal.Terminal, error)
	// Dial connects to a server.
	Dial func(opts sshconn.LocalOptions, ev sshconn.Events) Conn
	// PickFile asks for a file, as a private key to import; nil for none.
	PickFile func() (string, error)
	// PickDir asks for a directory, starting in start; "" when canceled.
	PickDir func(start string) (string, error)
	// OpenPath opens a file of this machine with its application, and
	// ShowInFolder shows it in the system's file manager.
	OpenPath     func(path string) error
	ShowInFolder func(path string)
	// OpenEditor opens a window for an editor of a file.
	OpenEditor func(e *Editor) EditorHost
	// PickFiles asks for files to upload; nil for none.
	PickFiles func() ([]string, error)
	// SaveFile asks where to save a file named name; "" when canceled.
	SaveFile func(name string) (string, error)
	// SetHotkey makes acc, an accelerator such as "Alt+Space", the
	// system-wide shortcut that shows and hides the window, in place of
	// the one before; "" removes it.
	SetHotkey func(acc string) error
	// SetLanguage makes the app show the language of tag, as "en", or
	// the system's for "".
	SetLanguage func(tag string)
	// AccessLogin opens a window where the user signs in to the
	// Cloudflare Access of the site at siteURL, and returns the token of
	// the sign-in once they did, or an error when they closed the window.
	// It is called from another goroutine than the main thread, and nil
	// leaves requests that need a sign-in to fail.
	AccessLogin func(ctx context.Context, siteURL string) (token string, err error)
}

// Model is the state of the window.
type Model struct {
	store   *store.Store
	backend Backend

	sites  []*siteState
	active int

	siteForm       siteForm
	siteFormOpen   bool
	serverForm     serverForm
	serverFormOpen bool

	groupForm     groupForm
	groupFormOpen bool

	prompt     prompt
	promptOpen bool

	filesSplit float32 // the share of a session's height its terminal takes over its files, in percent

	confirm     confirmation
	confirmOpen bool

	commandForm     commandForm
	commandFormOpen bool

	settings     settingsForm
	settingsOpen bool

	editors map[string]*Editor // the editors' windows open, by session and file

	middlePressed string // the session whose tab the middle button presses

	toast     string
	toastTime time.Time
}

// siteState is a tab: a site, or this machine (home) when its site has
// no ID.
type siteState struct {
	site   store.Site
	client *ternssh.Client // nil at home

	me      *ternssh.Me
	tree    []ternssh.Node
	loading bool
	err     string
	loaded  time.Time

	local     []store.Server   // at home
	terminals []store.Terminal // at home
	groups    []store.Group    // at home

	commands        []ternssh.QuickCommand // this machine's, or the site's
	commandsLoaded  bool
	commandsLoading bool
	commandsErr     string

	sessions []*session
	current  int    // the session shown
	selected string // the server chosen in the sidebar

	groupsOpen map[string]*bool
}

// home reports whether the tab is this machine's rather than a site's.
func (s *siteState) home() bool { return s.site.ID == "" }

// session is a terminal on a server or of a local shell.
type session struct {
	id       string
	title    string
	subtitle string
	mode     Mode
	node     ternssh.Node // the server, when Synced
	localID  string       // the server when Local, the terminal when Shell
	conn     Conn
	term     *terminal.Terminal
	status   sshconn.Status
	err      string
	dir      string
	startDir string // where a local terminal started

	showBottom, showStatus bool        // whether its panels show, at first for servers
	bottomTab              int         // the tab of the pane below, tabFiles or tabCommands
	refocus                bool        // give the terminal back the keyboard, after a command was sent
	files                  *filesState // the files panel, once shown
	stats                  statsSource // the server's status, sampled once the panel shows
	iface                  string      // the network interface its status shows
}

// New returns the model of the window: the home tab, then the sites of st.
func New(st *store.Store, b Backend) *Model {
	m := &Model{store: st, backend: b, filesSplit: 62}
	home := m.newSiteState(store.Site{})
	home.local, home.terminals, home.groups = st.Servers(), st.Terminals(), st.Groups()
	m.sites = []*siteState{home}
	active := st.ActiveSite()
	for _, site := range st.Sites() {
		m.sites = append(m.sites, m.newSiteState(site))
		if site.ID == active {
			m.active = len(m.sites) - 1
		}
	}
	return m
}

func (m *Model) newSiteState(site store.Site) *siteState {
	s := &siteState{site: site, groupsOpen: map[string]*bool{}}
	if site.ID != "" {
		s.client, _ = m.newClient(site, m.store.SiteSecret(site.ID))
	}
	return s
}

// newClient returns a client of site, which authenticates with secret
// and signs in to the site's Cloudflare Access when it asks.
func (m *Model) newClient(site store.Site, secret string) (*ternssh.Client, error) {
	auth := ternssh.Auth{AccessToken: m.store.SiteAccessToken(site.ID)}
	switch site.Auth {
	case store.AuthBasic:
		auth.Username, auth.Password = site.Username, secret
	case store.AuthAccess:
		auth.AccessClientID, auth.AccessClientSecret = site.ClientID, secret
	}
	client, err := ternssh.New(site.URL, auth)
	if err != nil || m.backend.AccessLogin == nil {
		return client, err
	}
	url := client.BaseURL()
	client.SetAccessLogin(func(ctx context.Context) (string, error) {
		token, err := m.backend.AccessLogin(ctx, url)
		if err == nil {
			m.store.SetSiteAccessToken(site.ID, token)
		}
		return token, err
	})
	return client, nil
}

// Start loads the servers of the tab shown.
func (m *Model) Start() {
	m.startHotkey()
	if s := m.current(); s != nil {
		m.refresh(s)
	}
}

func (m *Model) current() *siteState {
	if m.active < 0 || m.active >= len(m.sites) {
		return nil
	}
	return m.sites[m.active]
}

func (m *Model) update(fn func()) {
	if m.backend.Update != nil {
		m.backend.Update(fn)
	} else {
		fn()
	}
}

func (m *Model) notify(msg string) {
	m.toast, m.toastTime = msg, time.Now()
}

// selectSite shows the tab of the site at i.
func (m *Model) selectSite(i int) {
	if i < 0 || i >= len(m.sites) {
		return
	}
	m.active = i
	s := m.sites[i]
	m.store.SetActiveSite(s.site.ID)
	if s.loaded.IsZero() && !s.loading {
		m.refresh(s)
	}
}

// refresh loads the tab's servers again.
func (m *Model) refresh(s *siteState) {
	if s.home() {
		s.local, s.terminals, s.groups = m.store.Servers(), m.store.Terminals(), m.store.Groups()
		return
	}
	if s.client == nil {
		s.err = locales.ModelSiteURLInvalid()
		return
	}
	s.loading = true
	client := s.client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		me, err := client.Me(ctx)
		var tree []ternssh.Node
		if err == nil {
			tree, err = client.Tree(ctx)
		}
		m.update(func() {
			s.loading = false
			s.loaded = time.Now()
			if err != nil {
				s.err = err.Error()
				return
			}
			s.err, s.me, s.tree = "", me, tree
		})
	}()
}

// displayName is the name of a site's tab: the site name it set for
// itself, or the one given here.
func (s *siteState) displayName() string {
	if s.home() {
		return locales.ThisMachine()
	}
	if s.site.Name != "" {
		return s.site.Name
	}
	if s.me != nil && s.me.User.SiteName != nil && *s.me.User.SiteName != "" {
		return *s.me.User.SiteName
	}
	return hostOf(s.site.URL)
}

func hostOf(raw string) string {
	if u, err := ternssh.NormalizeURL(raw); err == nil {
		return u.Host
	}
	return raw
}

// health reports whether the site answered.
func (s *siteState) health() health {
	switch {
	case s.loading && s.loaded.IsZero():
		return healthUnknown
	case s.err != "":
		return healthDown
	case s.me != nil:
		return healthUp
	}
	return healthUnknown
}

type health int

const (
	healthUnknown health = iota
	healthUp
	healthDown
)

// countServers counts the servers of a tree.
func countServers(nodes []ternssh.Node) int {
	n := 0
	for _, node := range nodes {
		if node.Type == "server" {
			n++
		}
		n += countServers(node.Children)
	}
	return n
}

// openSynced opens a terminal on a server the site keeps: its address
// and credential are asked of the site as it connects.
func (m *Model) openSynced(s *siteState, node ternssh.Node) {
	client, siteID := s.client, s.site.ID
	opts := sshconn.LocalOptions{
		HostKey: m.store.SiteHostKey(siteID, node.ID),
		OnHostKey: func(fp string) {
			m.store.SetSiteHostKey(siteID, node.ID, fp)
		},
		Resolve: func(ctx context.Context, o *sshconn.LocalOptions) error {
			cred, err := client.ServerCredential(ctx, node.ID)
			if err != nil {
				return err
			}
			o.Host, o.Port, o.Username = cred.Host, cred.Port, cred.Username
			if cred.AuthType == "private_key" {
				o.PrivateKey, o.Passphrase = cred.PrivateKey, cred.Passphrase
			} else {
				o.Password = cred.Password
			}
			return nil
		},
	}
	m.open(s, &session{
		title:    node.Name,
		subtitle: node.Username + "@" + node.Host + portSuffix(node.Port),
		mode:     Synced,
		node:     node,
	}, func(ev sshconn.Events) Conn {
		return m.backend.Dial(opts, ev)
	})
}

// openLocal opens a terminal on a server this machine connects to.
func (m *Model) openLocal(s *siteState, srv store.Server) {
	cred, err := m.store.ServerCredential(srv.ID)
	if err != nil {
		m.notify(locales.ModelCredentialFailed(srv.Name, err.Error()))
		return
	}
	opts := sshconn.LocalOptions{
		Host: srv.Host, Port: srv.Port, Username: srv.Username,
		Password: cred.Password, PrivateKey: cred.PrivateKey, Passphrase: cred.Passphrase,
		HostKey: srv.HostKey,
		OnHostKey: func(fp string) {
			m.store.SetHostKey(srv.ID, fp)
			m.update(func() { s.local = m.store.Servers() })
		},
	}
	if srv.AuthType != "private_key" {
		opts.PrivateKey, opts.Passphrase = "", ""
	}
	m.open(s, &session{
		title:    srv.Name,
		subtitle: srv.Username + "@" + srv.Host + portSuffix(srv.Port),
		mode:     Local,
		localID:  srv.ID,
	}, func(ev sshconn.Events) Conn {
		return m.backend.Dial(opts, ev)
	})
}

func portSuffix(port int) string {
	if port == 22 || port == 0 {
		return ""
	}
	return ":" + strconv.Itoa(port)
}

func (m *Model) open(s *siteState, sess *session, dial func(sshconn.Events) Conn) {
	sess.id = store.NewID()
	sess.status = sshconn.StatusConnecting
	sess.showBottom, sess.showStatus = true, true
	sess.conn = dial(sshconn.Events{
		OnStatus: func(st sshconn.Status, err error) {
			m.update(func() {
				sess.status = st
				if err != nil {
					sess.err = err.Error()
				}
			})
		},
		OnDir: func(path string) { m.update(func() { sess.dir = path }) },
	})
	if m.backend.NewTerminal != nil {
		term, err := m.backend.NewTerminal(sess.conn)
		if err != nil {
			sess.conn.Close()
			m.notify(locales.ModelTerminalCreateFailed(err.Error()))
			return
		}
		sess.term = term
	}
	s.sessions = append(s.sessions, sess)
	s.current = len(s.sessions) - 1
}

// openShell opens a local terminal.
func (m *Model) openShell(s *siteState, t store.Terminal) {
	sess := &session{
		id:       store.NewID(),
		title:    t.Name,
		subtitle: displayDir(t.Dir),
		mode:     Shell,
		localID:  t.ID,
		status:   sshconn.StatusReady,
		startDir: expandHome(t.Dir),
		// Its files and the quick commands show below, as a server's.
		showBottom: true,
	}
	if m.backend.NewShell != nil {
		term, err := m.backend.NewShell(expandHome(t.Dir), func(int) {
			m.update(func() { sess.status = sshconn.StatusClosed })
		})
		if err != nil {
			m.notify(locales.ModelTerminalOpenFailed(err.Error()))
			return
		}
		sess.term = term
	}
	s.sessions = append(s.sessions, sess)
	s.current = len(s.sessions) - 1
}

// newSession opens another terminal in the tab shown: on what its current
// terminal is on, else on what the sidebar chose, else, at home, on the
// first local terminal, which it makes when there is none.
func (m *Model) newSession() {
	s := m.current()
	if s.current >= 0 && s.current < len(s.sessions) && m.openOn(s, s.sessions[s.current].on()) {
		return
	}
	if s.selected != "" && m.openOn(s, s.selected) {
		return
	}
	if !s.home() {
		m.notify(locales.ModelSelectServerFirst())
		return
	}
	terms := m.store.Terminals()
	if len(terms) == 0 {
		t := store.Terminal{ID: store.NewID(), Name: locales.ModelLocalTerminal()}
		if err := m.store.PutTerminal(t); err != nil {
			m.notify(locales.ModelTerminalSaveFailed(err.Error()))
			return
		}
		terms = []store.Terminal{t}
		s.terminals = m.store.Terminals()
	}
	s.selected = "t:" + terms[0].ID
	m.openShell(s, terms[0])
}

// on returns the key of the sidebar's row of what the session is on.
func (sess *session) on() string {
	switch sess.mode {
	case Synced:
		return "r:" + sess.node.ID
	case Shell:
		return "t:" + sess.localID
	}
	return "l:" + sess.localID
}

// openOn opens a terminal on the row of the sidebar with key, and reports
// whether there is one.
func (m *Model) openOn(s *siteState, key string) bool {
	kind, id, _ := strings.Cut(key, ":")
	switch kind {
	case "r":
		if node, ok := findServer(s.tree, func(n ternssh.Node) bool { return n.ID == id }); ok {
			m.openSynced(s, node)
			return true
		}
	case "l":
		for _, srv := range m.store.Servers() {
			if srv.ID == id {
				m.openLocal(s, srv)
				return true
			}
		}
	case "t":
		for _, t := range m.store.Terminals() {
			if t.ID == id {
				m.openShell(s, t)
				return true
			}
		}
	}
	return false
}

// expandHome turns a leading ~ into the home directory.
func expandHome(dir string) string {
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, dir[1:])
		}
	}
	return dir
}

// shortenHome writes a path in the home directory from "~", as
// expandHome reads it.
func shortenHome(dir string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return dir
	}
	if dir == home {
		return "~"
	}
	if rel, ok := strings.CutPrefix(dir, home+string(filepath.Separator)); ok {
		return "~/" + filepath.ToSlash(rel)
	}
	return dir
}

func displayDir(dir string) string {
	if dir == "" {
		return "~"
	}
	return dir
}

// closeSession closes the terminal at i of the site.
func (m *Model) closeSession(s *siteState, i int) {
	if i < 0 || i >= len(s.sessions) {
		return
	}
	sess := s.sessions[i]
	closePanels(sess)
	if sess.term != nil {
		sess.term.Close()
	} else if sess.conn != nil {
		sess.conn.Close()
	}
	s.sessions = append(s.sessions[:i], s.sessions[i+1:]...)
	if s.current >= len(s.sessions) {
		s.current = len(s.sessions) - 1
	} else if s.current > i {
		s.current--
	}
}

// reconnect replaces a session that ended with a new one to its server.
func (m *Model) reconnect(s *siteState, i int) {
	sess := s.sessions[i]
	if sess.mode == Synced {
		m.closeSession(s, i)
		m.openSynced(s, sess.node)
		m.moveLast(s, i)
		return
	}
	if sess.mode == Shell {
		for _, t := range m.store.Terminals() {
			if t.ID == sess.localID {
				m.closeSession(s, i)
				m.openShell(s, t)
				m.moveLast(s, i)
				return
			}
		}
		m.notify(locales.ModelTerminalDeleted(sess.title))
		return
	}
	for _, srv := range m.store.Servers() {
		if srv.ID == sess.localID {
			m.closeSession(s, i)
			m.openLocal(s, srv)
			m.moveLast(s, i)
			return
		}
	}
	m.notify(locales.ModelServerDeleted(sess.title))
}

// moveLast moves the last session to i, where the one it replaced was.
func (m *Model) moveLast(s *siteState, i int) {
	n := len(s.sessions)
	if n == 0 || i >= n-1 {
		return
	}
	last := s.sessions[n-1]
	copy(s.sessions[i+1:], s.sessions[i:n-1])
	s.sessions[i] = last
	s.current = i
}

func findServer(nodes []ternssh.Node, match func(ternssh.Node) bool) (ternssh.Node, bool) {
	for _, n := range nodes {
		if n.Type == "server" && match(n) {
			return n, true
		}
		if found, ok := findServer(n.Children, match); ok {
			return found, true
		}
	}
	return ternssh.Node{}, false
}

// removeSite closes the tab of a site and forgets it.
func (m *Model) removeSite(i int) {
	if i <= 0 || i >= len(m.sites) {
		return
	}
	s := m.sites[i]
	for len(s.sessions) > 0 {
		m.closeSession(s, 0)
	}
	m.store.DeleteSite(s.site.ID)
	m.sites = append(m.sites[:i], m.sites[i+1:]...)
	if m.active >= len(m.sites) {
		m.active = len(m.sites) - 1
	}
	m.store.SetActiveSite(m.current().site.ID)
}

// Close ends every connection, as the window closes.
func (m *Model) Close() {
	for _, s := range m.sites {
		for len(s.sessions) > 0 {
			m.closeSession(s, 0)
		}
	}
}

// filter reports whether a server matches what the sidebar searches for.
func filter(q string, fields ...string) bool {
	q = strings.TrimSpace(strings.ToLower(q))
	if q == "" {
		return true
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}
