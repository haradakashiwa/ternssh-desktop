package gui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"ternssh-desktop/internal/sshconn"
	"ternssh-desktop/internal/store"
	"ternssh-desktop/internal/ternssh"
	"ternssh-desktop/locales"
)

// View builds the window.
func (m *Model) View(c *ui.Context) {
	c.SetTheme(branded(c.Theme()))
	t := c.Theme()
	// The window's material shows wherever the view draws no background.
	if c.Vibrancy() {
		c.Root().Background(ui.Transparent)
	} else {
		c.Root().Background(t.Background)
	}

	m.markSeen()

	// The keys of a shortcut being recorded are its own.
	if m.settings.recording == "" {
		m.handleShortcuts(c)
	}

	ui.Column(c).Fill().Children(func() {
		m.titleBar(c)
		s := m.current()
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			m.sidebar(c, s)
			m.workspace(c, s)
		})
	})

	m.siteDialog(c)
	m.serverDialog(c)
	m.groupDialog(c)
	m.promptDialog(c)
	m.commandDialog(c)
	m.commandAskDialog(c)
	m.confirmDialog(c)
	m.settingsDialog(c)
	m.toastView(c)
}

// titleBar holds the tabs of the sites, after the window controls.
func (m *Model) titleBar(c *ui.Context) {
	tb := c.TitleBar()
	// The tabs wrap onto more lines when they do not fit; the first line
	// stays beside the window controls and the bar grows to hold the rest.
	pad := float32(titleBarHeight-siteTabHeight) / 2
	ui.Row(c).MinHeight(titleBarHeight).Shrink(0).AlignItems(ui.Start).Gap(6).
		Padding(pad, tb.Right+12, pad, tb.Left+12).DragWindow().Children(func() {
		ui.Row(c).Wrap().Gap(4).AlignItems(ui.Center).MinWidth(0).Shrink(1).Children(func() {
			for i, s := range m.sites {
				m.siteTab(c, i, s)
			}
			if iconButton(c, plusIcon, locales.ViewAddSite()).Clicked() {
				m.openSiteForm(nil)
			}
		})
		ui.Spacer(c)
		ui.Row(c).Height(siteTabHeight).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
			if s := m.current(); !s.home() {
				b := iconButton(c, refreshIcon, locales.ViewRefreshFromSite())
				if s.loading {
					b.Disabled(true)
				}
				if b.Clicked() {
					m.refresh(s)
				}
			}
			if iconButton(c, settingsIcon, locales.ViewSettings()).Clicked() {
				m.OpenSettings()
			}
		})
	})
}

func (m *Model) siteTab(c *ui.Context, i int, s *siteState) {
	t := c.Theme()
	active := i == m.active
	tab := ui.ButtonBase(c).Key(s.site.ID).Height(siteTabHeight).Padding(0, 10, 0, 12).Radius(10).Gap(8).
		AlignItems(ui.Center).MinWidth(120).MaxWidth(220).Label(s.displayName())
	switch {
	case active:
		tab.Background(frost(c, 0.9)).Border(1, hairline(c)).Shadow(0, 1, 4, 0, ui.RGBA(0, 0, 0, 0.10))
	case tab.Hovered():
		tab.Background(hover(c))
	}
	if tab.Clicked() {
		m.selectSite(i)
	}
	if s.home() {
		tab.Children(func() {
			ui.Icon(c, laptopIcon).Size(14, 14).TextColor(t.TextMuted)
			m.tabLabel(c, s, active)
		})
		return
	}
	tab.ContextMenu(func(menu *ui.Menu) {
		if menu.Item(locales.ViewEditSite()).Chosen() {
			site := s.site
			m.openSiteForm(&site)
		}
		if menu.Item(locales.Refresh()).Chosen() {
			m.refresh(s)
		}
		menu.Separator()
		if menu.Item(locales.ViewMoveLeft()).Disabled(i <= 1).Chosen() {
			m.moveSite(i, i-1)
		}
		if menu.Item(locales.ViewMoveRight()).Disabled(i == len(m.sites)-1).Chosen() {
			m.moveSite(i, i+1)
		}
		menu.Separator()
		if menu.Item(locales.ViewRemoveSite()).Danger().Chosen() {
			m.ask(confirmation{
				title:   locales.ViewRemoveSiteTitle(s.displayName()),
				message: locales.ViewRemoveSiteMessage(),
				action:  locales.ViewRemove(),
				do:      func() { m.removeSite(m.indexOf(s)) },
			})
		}
	})
	tab.Children(func() {
		dot(c, healthColor(s.health()))
		m.tabLabel(c, s, active)
	})
}

func (m *Model) tabLabel(c *ui.Context, s *siteState, active bool) {
	t := c.Theme()
	text := ui.Text(c, s.displayName()).FontSize(13).SingleLine().Grow(1).MinWidth(0)
	if active {
		text.FontWeight(600)
	} else {
		text.TextColor(t.TextMuted)
	}
	if n := len(s.sessions); n > 0 {
		// Sessions whose agent asks to be looked at color the count.
		badge := ui.Box(c).Padding(0, 6).Radius(8).Background(hover(c))
		color := t.TextMuted
		if s.attention() > 0 {
			badge.Background(attentionColor)
			color = ui.RGB(255, 255, 255)
		}
		badge.Children(func() {
			ui.Textf(c, "%d", n).FontSize(11).TextColor(color)
		})
	}
}

func (m *Model) indexOf(s *siteState) int {
	for i, x := range m.sites {
		if x == s {
			return i
		}
	}
	return -1
}

// moveSite moves a site's tab; the home tab stays first.
func (m *Model) moveSite(from, to int) {
	if from < 1 || to < 1 || to >= len(m.sites) {
		return
	}
	cur := m.current()
	s := m.sites[from]
	m.sites[from], m.sites[to] = m.sites[to], s
	m.store.MoveSite(s.site.ID, to-1)
	m.active = m.indexOf(cur)
}

// sidebar lists the servers of the tab.
func (m *Model) sidebar(c *ui.Context, s *siteState) {
	t := c.Theme()
	side := ui.Column(c).Width(sidebarWidth).Shrink(0).Padding(4, 10, 10, 10).Gap(10)
	if !c.Vibrancy() {
		side.Background(t.Surface)
	}
	side.Children(func() {
		if s.err != "" {
			ui.Text(c, s.err).FontSize(11).TextColor(t.Danger).MaxLines(4).Padding(4, 8, 0, 8)
		}

		// Nothing to search in a tab without rows.
		if s.searching() || len(s.tree)+len(s.local)+len(s.terminals)+len(s.groups) > 0 {
			ui.SearchField(c, &s.query).Placeholder(locales.ViewSearch()).Label(locales.ViewSearch())
		}

		// Dropped outside the groups, a row goes to the top.
		list := ui.Scroll(c).Grow(1).MinHeight(0).Radius(8)
		if d, ok := ui.DragOver[dragged](list); ok && m.canPlace(s, d, "", len(m.siblings(s, "", d))) {
			list.Background(hover(c))
		}
		if d, ok := ui.Drop[dragged](list); ok {
			m.moveLastIn(s, d, "")
		}
		list.Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				if s.home() {
					m.localServers(c, s)
				} else {
					m.siteServers(c, s)
				}
			})
		})

		ui.Row(c).Gap(8).Children(func() {
			addLabel := locales.ViewAddServer()
			if s.home() {
				addLabel = locales.ViewAddServerOrTerminal()
			}
			b := sidebarButton(c).Grow(1).Gap(8).Label(addLabel)
			if b.Clicked() {
				m.openServerForm(s)
			}
			b.Children(func() {
				ui.Icon(c, plusIcon).Size(14, 14)
				ui.Text(c, addLabel).FontSize(13).FontWeight(500)
			})
			m.foldButton(c, s)
			g := sidebarButton(c).Width(38).Label(locales.ViewNewGroup()).Tooltip(locales.ViewNewGroup())
			if g.Clicked() {
				m.openGroupForm(s, "", "", "")
			}
			g.Children(func() { ui.Icon(c, folderPlusIcon).Size(16, 16) })
		})
	})
}

// sidebarButton is a button at the foot of the sidebar.
func sidebarButton(c *ui.Context) ui.Element {
	b := ui.ButtonBase(c).Height(34).Radius(10).Center().Border(1, hairline(c))
	if b.Hovered() {
		b.Background(frost(c, 1))
	} else {
		b.Background(frost(c, 0.5))
	}
	return b
}

func (m *Model) siteServers(c *ui.Context, s *siteState) {
	if len(s.tree) == 0 {
		if s.loading {
			emptyHint(c, locales.ViewLoading())
		} else {
			emptyHint(c, locales.ViewSiteEmpty())
		}
		return
	}
	if !slices.ContainsFunc(s.tree, func(n ternssh.Node) bool { return nodeMatches(s.query, n) }) {
		emptyHint(c, locales.ViewSearchEmpty(strings.TrimSpace(s.query)))
		return
	}
	m.nodes(c, s, s.tree, "", 0, false)
}

// nodeMatches reports whether a node of a site's tree matches the search
// q, or for a group whether something in it does.
func nodeMatches(q string, n ternssh.Node) bool {
	if n.Type == "group" {
		return filter(q, n.Name) || slices.ContainsFunc(n.Children, func(child ternssh.Node) bool { return nodeMatches(q, child) })
	}
	return filter(q, n.Name, n.Host, n.Username)
}

// nodes lists the nodes of a site's tree in the group parent: all of
// them, or those that match the search.
func (m *Model) nodes(c *ui.Context, s *siteState, nodes []ternssh.Node, parent string, depth int, all bool) {
	for _, node := range nodes {
		node := node
		if !all && !nodeMatches(s.query, node) {
			continue
		}
		if node.Type == "group" {
			// A group whose name matches shows everything in it.
			in := all || filter(s.query, node.Name)
			m.groupBlock(c, s, node.ID, node.Name, parent, countServers(node.Children), depth, func() { m.nodes(c, s, node.Children, node.ID, depth+1, in) })
			continue
		}
		m.serverRow(c, s, serverRowData{
			key: "r:" + node.ID, name: node.Name, addr: node.Username + "@" + node.Host + portSuffix(node.Port),
			depth: depth, drag: dragged{s, store.KindServer, node.ID}, parent: parent,
		}, func() { m.openSynced(s, node) }, func(menu *ui.Menu) {
			if menu.Item(locales.Edit()).Chosen() {
				m.editSynced(s, node)
			}
			if menu.Item(locales.ViewResetHostKey()).Disabled(m.store.SiteHostKey(s.site.ID, node.ID) == "").Chosen() {
				m.store.SetSiteHostKey(s.site.ID, node.ID, "")
				m.notify(locales.ViewHostKeyReset(node.Name))
			}
			menu.Separator()
			if menu.Item(locales.ViewDeleteFromSite()).Danger().Chosen() {
				m.ask(confirmation{
					title:   locales.ViewDeleteFromSiteTitle(node.Name),
					message: locales.ViewDeleteFromSiteMessage(s.displayName()),
					action:  locales.Delete(),
					do:      func() { m.deleteSynced(s, node) },
				})
			}
		})
	}
}

type serverRowData struct {
	key, name, addr string
	depth           int
	icon            *ui.SVG // serverIcon when nil
	drag            dragged // the row, as dragged
	parent          string  // the group it is in
}

func (m *Model) serverRow(c *ui.Context, s *siteState, d serverRowData, open func(), menu func(*ui.Menu)) {
	t := c.Theme()
	// With groups, a row's icon lines up with their folders, past the
	// chevron; without, it starts at the edge.
	indent := float32(d.depth) * 14
	if len(s.groupIDs()) > 0 {
		indent += 16
	}
	row := ui.ButtonBase(c).Key(d.key).Padding(6, 8, 6, 8+indent).Gap(8).Radius(8).AlignItems(ui.Center).Label(d.name)
	// While searching, rows hidden would make where one is dropped unclear.
	if !s.searching() {
		row.Drag(d.drag)
	}
	// No highlight for the row chosen: it would read as the server the
	// terminal shown is on.
	if row.Hovered() {
		row.Background(hover(c))
	}
	if row.Clicked() {
		s.selected = d.key
	}
	m.dropOn(c, s, row, d.drag, d.parent)
	if row.DoubleClicked() {
		s.selected = d.key
		open()
	}
	row.ContextMenu(func(mm *ui.Menu) {
		if mm.Item(locales.ViewOpenTerminal()).Chosen() {
			open()
		}
		mm.Separator()
		menu(mm)
	})
	row.Children(func() {
		svg := d.icon
		if svg == nil {
			svg = serverIcon
		}
		ui.Icon(c, svg).Size(16, 16).TextColor(t.TextMuted)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, d.name).FontSize(13).SingleLine()
			ui.Text(c, d.addr).FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
		if row.Hovered() {
			b := ui.ButtonBase(c).Size(24, 22).Radius(6).Center().Label(locales.ViewConnectTo(d.name)).Tooltip(locales.ViewOpenTerminal())
			if b.Hovered() {
				b.Background(t.Accent).TextColor(t.AccentText)
			} else {
				b.Background(hover(c))
			}
			if b.Clicked() {
				open()
			}
			b.Children(func() { ui.Icon(c, terminalIcon).Size(13, 13) })
		}
	})
}

func emptyHint(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(12).TextColor(c.Theme().TextMuted).Padding(4, 12)
}

func listLabel(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(11).Bold().TextColor(c.Theme().TextMuted).Padding(6, 8, 2, 8)
}

// localServers lists the groups, local terminals and servers of the home
// tab.
func (m *Model) localServers(c *ui.Context, s *siteState) {
	if len(s.local) == 0 && len(s.terminals) == 0 && len(s.groups) == 0 {
		emptyHint(c, locales.ViewHomeEmpty())
		return
	}
	if !s.homeMatches("") {
		emptyHint(c, locales.ViewSearchEmpty(strings.TrimSpace(s.query)))
		return
	}
	m.homeTree(c, s, "", 0, false)
}

// homeMatches reports whether something in a group of the home tab, or
// at the top when empty, matches the search.
func (s *siteState) homeMatches(parent string) bool {
	for _, g := range s.groups {
		if g.ParentID == parent && (filter(s.query, g.Name) || s.homeMatches(g.ID)) {
			return true
		}
	}
	for _, t := range s.terminals {
		if t.GroupID == parent && filter(s.query, t.Name, t.Dir) {
			return true
		}
	}
	for _, srv := range s.local {
		if srv.GroupID == parent && filter(s.query, srv.Name, srv.Host, srv.Username) {
			return true
		}
	}
	return false
}

// homeTree lists what is in a group of the home tab, in its order: all
// of it, or what matches the search.
func (m *Model) homeTree(c *ui.Context, s *siteState, parent string, depth int, all bool) {
	for _, it := range store.Children(s.groups, s.terminals, s.local, parent) {
		switch it.Kind {
		case store.KindGroup:
			if i := slices.IndexFunc(s.groups, func(g store.Group) bool { return g.ID == it.ID }); i >= 0 {
				g := s.groups[i]
				// A group whose name matches shows everything in it.
				in := all || filter(s.query, g.Name)
				if !in && !s.homeMatches(g.ID) {
					continue
				}
				m.groupBlock(c, s, g.ID, g.Name, parent, s.homeCount(g.ID), depth, func() { m.homeTree(c, s, g.ID, depth+1, in) })
			}
		case store.KindTerminal:
			if i := slices.IndexFunc(s.terminals, func(t store.Terminal) bool { return t.ID == it.ID }); i >= 0 {
				if t := s.terminals[i]; all || filter(s.query, t.Name, t.Dir) {
					m.terminalRow(c, s, t, parent, depth)
				}
			}
		case store.KindServer:
			if i := slices.IndexFunc(s.local, func(srv store.Server) bool { return srv.ID == it.ID }); i >= 0 {
				if srv := s.local[i]; all || filter(s.query, srv.Name, srv.Host, srv.Username) {
					m.localRow(c, s, srv, parent, depth)
				}
			}
		}
	}
}

func (m *Model) terminalRow(c *ui.Context, s *siteState, term store.Terminal, parent string, depth int) {
	m.serverRow(c, s, serverRowData{
		key: "t:" + term.ID, name: term.Name, addr: displayDir(term.Dir), icon: terminalIcon, depth: depth,
		drag: dragged{s, store.KindTerminal, term.ID}, parent: parent,
	}, func() { m.openShell(s, term) }, func(menu *ui.Menu) {
		if menu.Item(locales.Edit()).Chosen() {
			m.editTerminal(s, term)
		}
		menu.Separator()
		if menu.Item(locales.Delete()).Danger().Chosen() {
			m.ask(confirmation{
				title:   locales.ViewDeleteTerminalTitle(term.Name),
				message: locales.ViewDeleteTerminalMessage(),
				action:  locales.Delete(),
				do: func() {
					m.store.DeleteTerminal(term.ID)
					s.terminals = m.store.Terminals()
				},
			})
		}
	})
}

func (m *Model) localRow(c *ui.Context, s *siteState, srv store.Server, parent string, depth int) {
	m.serverRow(c, s, serverRowData{
		key: "l:" + srv.ID, name: srv.Name, addr: srv.Username + "@" + srv.Host + portSuffix(srv.Port), depth: depth,
		drag: dragged{s, store.KindServer, srv.ID}, parent: parent,
	}, func() { m.openLocal(s, srv) }, func(menu *ui.Menu) {
		if menu.Item(locales.Edit()).Chosen() {
			m.editLocal(s, srv)
		}
		if menu.Item(locales.ViewResetHostKey()).Disabled(srv.HostKey == "").Chosen() {
			m.store.SetHostKey(srv.ID, "")
			s.local = m.store.Servers()
			m.notify(locales.ViewHostKeyReset(srv.Name))
		}
		menu.Separator()
		if menu.Item(locales.Delete()).Danger().Chosen() {
			m.ask(confirmation{
				title:   locales.ViewDeleteServerTitle(srv.Name),
				message: locales.ViewDeleteServerMessage(),
				action:  locales.Delete(),
				do: func() {
					m.store.DeleteServer(srv.ID)
					s.local = m.store.Servers()
				},
			})
		}
	})
}

// workspace shows the terminals of the site.
func (m *Model) workspace(c *ui.Context, s *siteState) {
	t := c.Theme()
	pane := ui.Column(c).Grow(1).MinWidth(0).Margin(0, 10, 10, 0).Radius(14).Clip().
		Background(frost(c, 0.75)).Border(1, hairline(c)).Shadow(0, 2, 12, 0, ui.RGBA(0, 0, 0, 0.06))
	pane.Children(func() {
		if len(s.sessions) == 0 {
			ui.Column(c).Grow(1).Center().Gap(10).Children(func() {
				ui.Icon(c, terminalIcon).Size(40, 40).TextColor(t.TextMuted.Alpha(0.6))
				ui.Text(c, locales.ViewNoTerminals()).FontSize(16).Bold()
				ui.Text(c, locales.ViewNoTerminalsHint()).TextColor(t.TextMuted)
				switch key := m.shortcutLabel("newTerminal"); {
				case key == "":
				case s.home():
					ui.Text(c, locales.ViewLocalTerminalShortcut(key)).FontSize(12).TextColor(t.TextMuted)
				default:
					ui.Text(c, locales.ViewTerminalShortcut(key)).FontSize(12).TextColor(t.TextMuted)
				}
				if s.home() && len(m.sites) == 1 {
					ui.Text(c, locales.ViewSyncHint()).FontSize(12).TextColor(t.TextMuted)
				}
			})
			return
		}
		m.sessionTabs(c, s)
		if len(s.sessions) == 0 {
			// The last one was closed in its tab; the next frame shows the
			// empty workspace.
			c.Invalidate()
			return
		}
		ui.Box(c).Height(1).Background(hairline(c)).Shrink(0)
		sess := s.sessions[s.current]
		m.sessionView(c, sess)
		m.statusBar(c, s, sess)
	})
}

func (m *Model) sessionTabs(c *ui.Context, s *siteState) {
	ui.Row(c).Shrink(0).AlignItems(ui.Start).Padding(0, 8, 0, 0).Gap(2).Children(func() {
		m.tabStrip(c, s)
		if s.current >= 0 && s.current < len(s.sessions) {
			// Beside the first line of tabs.
			ui.Row(c).Height(40).Shrink(0).Gap(2).AlignItems(ui.Center).Children(func() {
				m.panelButtons(c, s.sessions[s.current])
			})
		}
	})
}

// tabStrip is the tabs of the terminals, which wrap onto more lines when
// they are many.
func (m *Model) tabStrip(c *ui.Context, s *siteState) {
	t := c.Theme()
	ui.Row(c).Grow(1).MinWidth(0).Wrap().Padding(6).Gap(4).AlignItems(ui.Center).Children(func() {
		closed := -1
		for i, sess := range s.sessions {
			tab := ui.ButtonBase(c).Key(sess.id).Height(28).Padding(0, 4, 0, 10).Gap(7).Radius(8).AlignItems(ui.Center).MaxWidth(240).Label(sess.title)
			if tip := agentTip(sess.agent); tip != "" {
				tab.Tooltip(tip)
			}
			if i == s.current {
				tab.Background(selected(c)).Shadow(0, 1, 4, 0, ui.RGBA(0, 0, 0, 0.10))
			} else if tab.Hovered() {
				tab.Background(hover(c))
			}
			if tab.Clicked() {
				s.current = i
			}
			// A middle click closes it: pressed on it, released on it.
			if b, ok := tab.PressedButton(); ok && b == 2 {
				m.middlePressed = sess.id
			} else if m.middlePressed == sess.id {
				m.middlePressed = ""
				if tab.Hovered() {
					closed = i
				}
			}
			i := i
			tab.ContextMenu(func(menu *ui.Menu) {
				if menu.Item(locales.ViewReconnect()).Chosen() {
					m.reconnect(s, i)
				}
				if menu.Item(locales.Close()).Chosen() {
					closed = i
				}
			})
			tab.Children(func() {
				agentMark(c, sess)
				text := ui.Text(c, sess.title).FontSize(12).SingleLine().MinWidth(0).Shrink(1)
				if i != s.current {
					text.TextColor(t.TextMuted)
				}
				x := ui.ButtonBase(c).Size(18, 18).Radius(5).Center().Label(locales.ViewCloseTab(sess.title))
				if x.Hovered() {
					x.Background(hover(c))
				}
				if x.Clicked() {
					closed = i
				}
				x.Children(func() { ui.Icon(c, closeIcon).Size(10, 10).TextColor(t.TextMuted) })
			})
		}
		if closed >= 0 {
			m.closeSession(s, closed)
		}
		tip := locales.ViewNewSession()
		if key := m.shortcutLabel("newTerminal"); key != "" {
			tip = locales.ViewNewSessionTooltip(key)
		}
		if iconButton(c, plusIcon, locales.ViewNewSession()).Tooltip(tip).Clicked() {
			m.newSession()
		}
	})
}

func sessionColor(st sshconn.Status) ui.Color {
	switch st {
	case sshconn.StatusReady:
		return ui.Hex("#30d158")
	case sshconn.StatusConnecting:
		return ui.Hex("#ffd60a")
	case sshconn.StatusError:
		return ui.Hex("#ff453a")
	}
	return ui.Hex("#8e8e93")
}

func (m *Model) statusBar(c *ui.Context, s *siteState, sess *session) {
	t := c.Theme()
	ui.Row(c).Height(28).Shrink(0).Padding(0, 12).Gap(8).AlignItems(ui.Center).Border(0, hairline(c)).Children(func() {
		dot(c, sessionColor(sess.status))
		status := sess.status.String()
		if sess.mode == Shell {
			status = locales.ViewShellExited()
			if sess.status == sshconn.StatusReady {
				status = locales.ViewShellRunning()
			}
		}
		ui.Text(c, status).FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, "·").FontSize(11).TextColor(t.TextMuted)
		via := locales.ViewViaDirect()
		switch sess.mode {
		case Synced:
			via = locales.ViewViaSite(hostOf(s.site.URL))
		case Shell:
			via = locales.ViewViaLocal()
			if sess.term != nil {
				if d := sess.term.Dir(); d != "" {
					sess.dir = d
				}
			}
		}
		ui.Text(c, sess.subtitle+"  "+via).FontSize(11).TextColor(t.TextMuted).SingleLine().Grow(1).MinWidth(0)
		if sess.dir != "" {
			ui.Text(c, sess.dir).FontSize(11).TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0)
		}
		if sess.status == sshconn.StatusError || sess.status == sshconn.StatusClosed {
			if ui.Button(c, reconnectLabel(sess)).FontSize(11).Padding(2, 10).Clicked() {
				m.reconnect(s, s.current)
			}
		}
	})
}

// reconnectLabel is the label of the button that opens a session again.
func reconnectLabel(sess *session) string {
	if sess.mode == Shell {
		return locales.ViewReopen()
	}
	return locales.ViewReconnect()
}

// toastView shows the last notice for a few seconds.
func (m *Model) toastView(c *ui.Context) {
	if m.toast == "" {
		return
	}
	left := 4*time.Second - time.Since(m.toastTime)
	if left <= 0 {
		m.toast = ""
		return
	}
	c.After(left)
	ui.Overlay(c, func() {
		ui.Column(c).Fill().Justify(ui.End).AlignItems(ui.Center).Padding(24).PassThrough().Children(func() {
			ui.Row(c).Padding(10, 16).Radius(12).MaxWidth(520).Background(panel(c)).
				Border(1, hairline(c)).Shadow(0, 4, 18, 0, ui.RGBA(0, 0, 0, 0.18)).Children(func() {
				ui.Text(c, m.toast).FontSize(13)
			})
		})
	})
}

func (m *Model) deleteSynced(s *siteState, node ternssh.Node) {
	client := s.client
	go func() {
		ctx, cancel := request()
		defer cancel()
		err := client.DeleteServer(ctx, node.ID)
		m.update(func() {
			if err != nil {
				m.notify(locales.ViewDeleteFailed(err.Error()))
				return
			}
			m.notify(locales.ViewDeletedFromSite(node.Name))
			m.refresh(s)
		})
	}()
}

func (m *Model) editSynced(s *siteState, node ternssh.Node) {
	m.openServerForm(s)
	f := &m.serverForm
	f.editing, f.origAuth = node.ID, node.AuthType
	f.name, f.host, f.port, f.username = node.Name, node.Host, fmt.Sprint(node.Port), node.Username
	if node.AuthType == "private_key" {
		f.authType = 1
	}
	if node.GroupID != nil {
		f.group = groupPath(s, *node.GroupID)
	}
}

func (m *Model) editTerminal(s *siteState, term store.Terminal) {
	m.openServerForm(s)
	f := &m.serverForm
	f.editing, f.kind = term.ID, kindTerminal
	f.name, f.dir, f.group = term.Name, term.Dir, groupPath(s, term.GroupID)
}

func (m *Model) editLocal(s *siteState, srv store.Server) {
	m.openServerForm(s)
	f := &m.serverForm
	f.editing, f.group = srv.ID, groupPath(s, srv.GroupID)
	f.name, f.host, f.port, f.username = srv.Name, srv.Host, fmt.Sprint(srv.Port), srv.Username
	if srv.AuthType == "private_key" {
		f.authType = 1
	}
}
