package gui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"ternssh-desktop/internal/store"
	"ternssh-desktop/internal/ternssh"
	"ternssh-desktop/locales"
)

// request returns the context of a request to a site.
func request() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 20*time.Second)
}

// dialog is a panel over the dimmed window.
func dialog(c *ui.Context, open *bool, width float32, fn func()) {
	ui.DialogBase(c, open, func(backdrop, box ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.32))
		// A dialog taller than the window scrolls what it scrolls.
		box.Width(width).MaxHeightPercent(92).Padding(22).Gap(14).Radius(18).
			Background(panel(c)).Border(1, hairline(c)).
			Shadow(0, 12, 36, 0, ui.RGBA(0, 0, 0, 0.25))
		fn()
	})
}

func dialogTitle(c *ui.Context, title, subtitle string) {
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, title).FontSize(17).Bold()
		if subtitle != "" {
			ui.Text(c, subtitle).FontSize(12).TextColor(c.Theme().TextMuted)
		}
	})
}

func dialogButtons(c *ui.Context, busy bool, primary string, cancel func(), submit func()) {
	ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
		if busy {
			ui.Spinner(c).Size(14, 14)
		}
		if ui.Button(c, locales.Cancel()).Clicked() {
			cancel()
		}
		b := ui.PrimaryButton(c, primary).Disabled(busy)
		if b.Clicked() || (!busy && c.Shortcut(0, ui.KeyEnter)) {
			submit()
		}
	})
}

func errorText(c *ui.Context, err string) {
	if err != "" {
		ui.Text(c, err).FontSize(12).TextColor(c.Theme().Danger)
	}
}

// --- Sites

type siteForm struct {
	editing      string // the ID of the site edited, empty for a new one
	name, url    string
	auth         int // 0 none, 1 basic, 2 Access
	username     string
	password     string
	clientID     string
	clientSecret string
	busy         bool
	err          string
}

var authKinds = []store.AuthKind{store.AuthNone, store.AuthBasic, store.AuthAccess}

func (m *Model) openSiteForm(site *store.Site) {
	m.siteForm = siteForm{auth: 1}
	if site != nil {
		f := &m.siteForm
		f.editing, f.name, f.url = site.ID, site.Name, site.URL
		f.username, f.clientID = site.Username, site.ClientID
		for i, k := range authKinds {
			if k == site.Auth {
				f.auth = i
			}
		}
	}
	m.siteFormOpen = true
}

func (m *Model) siteDialog(c *ui.Context) {
	f := &m.siteForm
	dialog(c, &m.siteFormOpen, 440, func() {
		if f.editing == "" {
			dialogTitle(c, locales.SiteAddTitle(), locales.SiteAddSubtitle())
		} else {
			dialogTitle(c, locales.SiteEditTitle(), "")
		}
		ui.Form(c, func() {
			ui.Field(c, locales.SiteFieldAddress(), func() {
				ui.TextInput(c, &f.url).Placeholder("ssh.example.com").AutoFocus()
			})
			ui.Field(c, locales.DialogFieldName(), func() {
				ui.TextInput(c, &f.name).Placeholder(locales.SiteNamePlaceholder())
			})
			ui.Field(c, locales.SiteFieldAuth(), func() {
				ui.Segmented(c, &f.auth, locales.SiteAuthNone(), locales.SiteAuthBasic(), locales.SiteAuthAccess()).Label(locales.SiteFieldAuth())
			})
			keep := ""
			if f.editing != "" {
				keep = locales.DialogKeepBlank()
			}
			switch f.auth {
			case 1:
				ui.Field(c, locales.DialogFieldUsername(), func() { ui.TextInput(c, &f.username) })
				ui.Field(c, locales.DialogFieldPassword(), func() { ui.TextInput(c, &f.password).Password().Placeholder(keep) })
			case 2:
				ui.Field(c, "Client ID", func() { ui.TextInput(c, &f.clientID).Placeholder("xxxx.access") })
				ui.Field(c, "Client Secret", func() { ui.TextInput(c, &f.clientSecret).Password().Placeholder(keep) })
			}
		})
		if f.auth == 2 {
			ui.Text(c, locales.SiteAccessHint()).
				FontSize(11).TextColor(c.Theme().TextMuted)
		}
		errorText(c, f.err)
		dialogButtons(c, f.busy, addOrSave(f.editing == ""),
			func() { m.siteFormOpen = false }, m.saveSite)
	})
}

func (m *Model) saveSite() {
	f := &m.siteForm
	u, err := ternssh.NormalizeURL(f.url)
	if err != nil {
		f.err = err.Error()
		return
	}
	site := store.Site{
		ID: f.editing, Name: strings.TrimSpace(f.name), URL: u.String(), Auth: authKinds[f.auth],
	}
	if site.ID == "" {
		site.ID = store.NewID()
	}
	var secret string
	switch site.Auth {
	case store.AuthBasic:
		site.Username, secret = strings.TrimSpace(f.username), f.password
		if site.Username == "" {
			f.err = locales.DialogErrUsername()
			return
		}
	case store.AuthAccess:
		site.ClientID, secret = strings.TrimSpace(f.clientID), strings.TrimSpace(f.clientSecret)
		if site.ClientID == "" {
			f.err = locales.SiteErrClientID()
			return
		}
	}
	if secret == "" && f.editing != "" {
		secret = m.store.SiteSecret(f.editing)
	}
	if secret == "" && site.Auth != store.AuthNone {
		f.err = locales.SiteErrSecret()
		return
	}

	client, err := m.newClient(site, secret)
	if err != nil {
		f.err = err.Error()
		return
	}
	f.busy, f.err = true, ""
	go func() {
		ctx, cancel := request()
		defer cancel()
		_, err := client.Me(ctx)
		m.update(func() {
			f.busy = false
			if err != nil {
				f.err = err.Error()
				return
			}
			if err := m.store.PutSite(site, secret); err != nil {
				f.err = locales.SiteErrSave(err.Error())
				return
			}
			m.siteFormOpen = false
			for i, s := range m.sites {
				if s.site.ID == site.ID {
					sessions, current := s.sessions, s.current
					ns := m.newSiteState(site)
					ns.sessions, ns.current = sessions, current
					m.sites[i] = ns
					m.selectSite(i)
					m.refresh(ns)
					return
				}
			}
			m.sites = append(m.sites, m.newSiteState(site))
			m.selectSite(len(m.sites) - 1)
		})
	}()
}

// --- Servers

type serverForm struct {
	site     *siteState
	editing  string // the ID of the server edited, of the site or this machine
	origAuth string // the auth type of the site's server edited
	kind     int    // kindServer or kindTerminal, at home
	dir      string // where a local terminal starts
	name     string
	host     string
	port     string
	username string
	authType int // 0 password, 1 private key
	password string
	key      string
	pass     string // the private key's passphrase
	group    string
	busy     bool
	err      string
}

// What the home tab's dialog adds.
const (
	kindServer = iota
	kindTerminal
)

func (m *Model) openServerForm(s *siteState) {
	m.serverForm = serverForm{site: s, port: "22", username: "root"}
	m.serverFormOpen = true
}

// noGroup is the picker's choice of no group, which the form keeps as "".
func noGroup() string { return locales.ServerNoGroup() }

// addOrSave is the label of a form's primary button.
func addOrSave(adding bool) string {
	if adding {
		return locales.Add()
	}
	return locales.Save()
}

// groupChoices lists the groups of a tab, as paths, for the group picker,
// with their IDs.
func groupChoices(s *siteState) ([]string, map[string]string) {
	names, ids := []string{noGroup()}, map[string]string{}
	if !s.home() {
		groups(s.tree, "", &names, ids)
		return names, ids
	}
	var walk func(parent, prefix string)
	walk = func(parent, prefix string) {
		for _, g := range s.groups {
			if g.ParentID == parent {
				path := prefix + g.Name
				names = append(names, path)
				ids[path] = g.ID
				walk(g.ID, path+" / ")
			}
		}
	}
	walk("", "")
	return names, ids
}

// groupPath is the path of a group in the picker, empty for none.
func groupPath(s *siteState, id string) string {
	_, ids := groupChoices(s)
	for path, gid := range ids {
		if gid == id && id != "" {
			return path
		}
	}
	return ""
}

// groupID is the ID of the group the form chose, empty for none.
func (f *serverForm) groupID() string {
	_, ids := groupChoices(f.site)
	return ids[f.group]
}

// groupField picks the group of what the form adds, when there are any.
func groupField(c *ui.Context, f *serverForm) {
	names, _ := groupChoices(f.site)
	if len(names) > 1 {
		// The form keeps "" for no group, so that the choice outlives a
		// change of language; the picker shows its label.
		group := f.group
		if group == "" {
			group = noGroup()
		}
		ui.Field(c, locales.DialogFieldGroup(), func() {
			if ui.Select(c, &group, names).Label(locales.DialogFieldGroup()).Changed() {
				if group == noGroup() {
					group = ""
				}
				f.group = group
			}
		})
	}
}

// groups lists the groups of a tree, as paths, for the group picker.
func groups(nodes []ternssh.Node, prefix string, out *[]string, ids map[string]string) {
	for _, n := range nodes {
		if n.Type != "group" {
			continue
		}
		path := prefix + n.Name
		*out = append(*out, path)
		ids[path] = n.ID
		groups(n.Children, path+" / ", out, ids)
	}
}

func (m *Model) serverDialog(c *ui.Context) {
	f := &m.serverForm
	if f.site == nil {
		return
	}
	t := c.Theme()
	home := f.site.home()
	dialog(c, &m.serverFormOpen, 500, func() {
		if home && f.editing == "" {
			dialogTitle(c, locales.ServerAddToLocalTitle(), "")
			ui.Segmented(c, &f.kind, locales.ServerKindServer(), locales.TerminalFormLocalTerminal()).Label(locales.ServerKindLabel())
		}
		if home && f.kind == kindTerminal {
			m.terminalForm(c, f)
			return
		}
		title := locales.ServerEditTitle()
		if f.editing == "" {
			title = locales.ServerAddTitle()
		}
		switch {
		case !home:
			dialogTitle(c, title, locales.DialogSavedToSite(f.site.displayName()))
		case f.editing != "":
			dialogTitle(c, title, locales.DialogSavedLocally())
		}
		ui.Form(c, func() {
			ui.Field(c, locales.DialogFieldName(), func() { ui.TextInput(c, &f.name).Placeholder(locales.ServerNamePlaceholder()).AutoFocus() })
			ui.Field(c, locales.ServerFieldHost(), func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.TextInput(c, &f.host).Placeholder(locales.ServerHostPlaceholder()).Grow(1)
					ui.TextInput(c, &f.port).Width(64).Label(locales.ServerFieldPort())
				})
			})
			ui.Field(c, locales.DialogFieldUsername(), func() { ui.TextInput(c, &f.username) })
			ui.Field(c, locales.ServerFieldAuth(), func() {
				ui.Segmented(c, &f.authType, locales.DialogFieldPassword(), locales.ServerFieldKey()).Label(locales.ServerFieldAuth())
			})
			keep := ""
			if f.editing != "" {
				keep = locales.DialogKeepBlank()
			}
			if f.authType == 0 {
				ui.Field(c, locales.DialogFieldPassword(), func() { ui.TextInput(c, &f.password).Password().Placeholder(keep) })
			} else {
				ui.Field(c, locales.ServerFieldKey(), func() {
					ui.Column(c).Gap(6).Children(func() {
						ui.TextArea(c, &f.key).Lines(4, 6).Font("monospace").FontSize(11).
							Placeholder(map[bool]string{true: keep, false: "-----BEGIN OPENSSH PRIVATE KEY-----"}[keep != ""])
						if m.backend.PickFile != nil && ui.Button(c, locales.ServerImportKey()).Clicked() {
							m.importKey(f)
						}
					})
				})
				ui.Field(c, locales.ServerFieldPassphrase(), func() { ui.TextInput(c, &f.pass).Password().Placeholder(locales.ServerPassphrasePlaceholder()) })
			}
			groupField(c, f)
		})
		if home {
			ui.Text(c, locales.ServerLocalHint()).FontSize(11).TextColor(t.TextMuted)
		} else {
			ui.Text(c, locales.ServerSiteHint()).FontSize(11).TextColor(t.TextMuted)
		}
		errorText(c, f.err)
		dialogButtons(c, f.busy, addOrSave(f.editing == ""),
			func() { m.serverFormOpen = false }, m.saveServer)
	})
}

func (m *Model) importKey(f *serverForm) {
	pick := m.backend.PickFile
	go func() {
		path, err := pick()
		if err != nil || path == "" {
			return
		}
		b, err := os.ReadFile(path)
		m.update(func() {
			if err != nil {
				f.err = err.Error()
				return
			}
			if len(b) > 64<<10 {
				f.err = locales.ServerErrKeyTooLarge()
				return
			}
			f.key = string(b)
		})
	}()
}

// terminalForm is the dialog's form of a local terminal.
func (m *Model) terminalForm(c *ui.Context, f *serverForm) {
	if f.editing != "" {
		dialogTitle(c, locales.TerminalFormEditTitle(), "")
	}
	ui.Form(c, func() {
		ui.Field(c, locales.DialogFieldName(), func() { ui.TextInput(c, &f.name).Placeholder(locales.TerminalFormLocalTerminal()).AutoFocus() })
		ui.Field(c, locales.TerminalFormStartDir(), func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &f.dir).Placeholder(locales.TerminalFormStartDirPlaceholder()).Grow(1)
				if m.backend.PickDir != nil && ui.Button(c, locales.TerminalFormChooseDir()).Clicked() {
					m.pickDir(f)
				}
			})
		})
		groupField(c, f)
	})
	ui.Text(c, locales.TerminalFormHint()).FontSize(11).TextColor(c.Theme().TextMuted)
	errorText(c, f.err)
	dialogButtons(c, false, addOrSave(f.editing == ""),
		func() { m.serverFormOpen = false }, m.saveTerminal)
}

// pickDir asks for the directory a terminal starts in, from the one the
// form has.
func (m *Model) pickDir(f *serverForm) {
	pick := m.backend.PickDir
	start := expandHome(strings.TrimSpace(f.dir))
	if start == "" {
		start, _ = os.UserHomeDir()
	}
	go func() {
		dir, err := pick(start)
		m.update(func() {
			switch {
			case err != nil:
				f.err = err.Error()
			case dir != "":
				f.dir, f.err = shortenHome(dir), ""
			}
		})
	}()
}

func (m *Model) saveTerminal() {
	f := &m.serverForm
	s := f.site
	t := store.Terminal{ID: f.editing, Name: strings.TrimSpace(f.name), Dir: strings.TrimSpace(f.dir), GroupID: f.groupID()}
	if t.Name == "" {
		t.Name = locales.TerminalFormLocalTerminal()
	}
	if t.Dir != "" {
		if fi, err := os.Stat(expandHome(t.Dir)); err != nil || !fi.IsDir() {
			f.err = locales.TerminalFormErrNoDir(t.Dir)
			return
		}
	}
	if t.ID == "" {
		t.ID = store.NewID()
	}
	if err := m.store.PutTerminal(t); err != nil {
		f.err = locales.DialogErrSave(err.Error())
		return
	}
	s.terminals = m.store.Terminals()
	s.selected = "t:" + t.ID
	m.serverFormOpen = false
}

func (m *Model) saveServer() {
	f := &m.serverForm
	s := f.site
	name, host, user := strings.TrimSpace(f.name), strings.TrimSpace(f.host), strings.TrimSpace(f.username)
	port, perr := strconv.Atoi(strings.TrimSpace(f.port))
	authType := "password"
	credential := f.password
	if f.authType == 1 {
		authType, credential = "private_key", strings.TrimSpace(f.key)
	}
	switch {
	case name == "":
		f.err = locales.DialogErrName()
	case host == "" || strings.ContainsAny(host, " /@"):
		f.err = locales.ServerErrHost()
	case perr != nil || port < 1 || port > 65535:
		f.err = locales.ServerErrPort()
	case user == "":
		f.err = locales.DialogErrUsername()
	case credential == "" && f.editing == "":
		f.err = credentialMissing(f.authType == 1)
	default:
		f.err = ""
	}
	if f.err != "" {
		return
	}

	if s.home() {
		srv := store.Server{ID: f.editing, Name: name, Host: host, Port: port, Username: user, AuthType: authType, GroupID: f.groupID()}
		if srv.ID == "" {
			srv.ID = store.NewID()
		} else {
			for _, old := range s.local {
				if old.ID == srv.ID && old.Host == host && old.Port == port {
					srv.HostKey = old.HostKey
				}
			}
		}
		var cred *store.Credential
		if credential != "" {
			cred = &store.Credential{}
			if authType == "private_key" {
				cred.PrivateKey, cred.Passphrase = credential, f.pass
			} else {
				cred.Password = credential
			}
		} else if old, err := m.store.ServerCredential(srv.ID); err == nil {
			// The kind of credential changed without a new one.
			if (authType == "private_key") != (old.PrivateKey != "") {
				f.err = credentialMissing(f.authType == 1)
				return
			}
		}
		if err := m.store.PutServer(srv, cred); err != nil {
			f.err = locales.DialogErrSave(err.Error())
			return
		}
		s.local = m.store.Servers()
		s.selected = "l:" + srv.ID
		m.serverFormOpen = false
		return
	}

	var groupID *string
	if id := f.groupID(); id != "" {
		groupID = &id
	}
	client, editing := s.client, f.editing
	var save func(context.Context) (string, error)
	if editing != "" {
		if credential == "" && authType != f.origAuth {
			f.err = credentialMissing(f.authType == 1)
			return
		}
		u := ternssh.ServerUpdate{Name: name, Host: host, Port: port, Username: user, AuthType: authType, GroupID: groupID}
		if credential != "" {
			u.Credential = &credential
		}
		if authType == "private_key" && (credential != "" || f.pass != "") {
			pass := f.pass
			u.Passphrase = &pass
		}
		save = func(ctx context.Context) (string, error) { return editing, client.UpdateServer(ctx, editing, u) }
	} else {
		if authType == "private_key" && f.pass != "" {
			// The site keeps a key with its passphrase as JSON.
			b, _ := json.Marshal(map[string]string{"privateKey": credential, "passphrase": f.pass})
			credential = string(b)
		}
		req := ternssh.NewServer{Name: name, Host: host, Port: port, Username: user, AuthType: authType, Credential: credential, GroupID: groupID}
		save = func(ctx context.Context) (string, error) {
			node, err := client.CreateServer(ctx, req)
			if err != nil {
				return "", err
			}
			return node.ID, nil
		}
	}
	f.busy = true
	go func() {
		ctx, cancel := request()
		defer cancel()
		id, err := save(ctx)
		m.update(func() {
			f.busy = false
			if err != nil {
				var apiErr *ternssh.Error
				if errors.As(err, &apiErr) {
					f.err = locales.ServerErrSiteRefused(apiErr.Error())
				} else {
					f.err = err.Error()
				}
				return
			}
			m.serverFormOpen = false
			s.selected = "r:" + id
			m.refresh(s)
		})
	}()
}

// credentialMissing is the error of a form without its credential.
func credentialMissing(key bool) string {
	if key {
		return locales.ServerErrKey()
	}
	return locales.ServerErrPassword()
}

// --- Confirmation

type confirmation struct {
	title, message, action string
	do                     func()
}

func (m *Model) ask(q confirmation) {
	m.confirm, m.confirmOpen = q, true
}

func (m *Model) confirmDialog(c *ui.Context) {
	q := m.confirm
	dialog(c, &m.confirmOpen, 380, func() {
		dialogTitle(c, q.title, "")
		ui.Text(c, q.message).FontSize(13).TextColor(c.Theme().TextMuted)
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c, locales.Cancel()).Clicked() {
				m.confirmOpen = false
			}
			b := ui.PrimaryButton(c, q.action).Background(c.Theme().Danger)
			if b.Clicked() {
				m.confirmOpen = false
				if q.do != nil {
					q.do()
				}
			}
		})
	})
}
