package gui

import (
	"fmt"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"ternssh-desktop/internal/sshconn"
	"ternssh-desktop/internal/store"
	"ternssh-desktop/internal/ternssh"
)

// View builds the window.
func (m *Model) View(c *ui.Context) {
	t := c.Theme()
	// The window's material shows wherever the view draws no background.
	if c.Vibrancy() {
		c.Root().Background(ui.Transparent)
	} else {
		c.Root().Background(t.Background)
	}

	if c.Shortcut(newSessionMods, ui.KeyT) && !c.ShortcutRepeated() {
		m.newSession()
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
	m.confirmDialog(c)
	m.toastView(c)
}

// titleBar holds the tabs of the sites, after the window controls.
func (m *Model) titleBar(c *ui.Context) {
	tb := c.TitleBar()
	ui.Row(c).Height(titleBarHeight).Shrink(0).AlignItems(ui.Center).Gap(6).
		Padding(0, tb.Right+12, 0, tb.Left+12).DragWindow().Children(func() {
		ui.Row(c).Gap(4).AlignItems(ui.Center).MinWidth(0).Shrink(1).ClipX().Children(func() {
			for i, s := range m.sites {
				m.siteTab(c, i, s)
			}
		})
		if iconButton(c, plusIcon, "添加站点").Clicked() {
			m.openSiteForm(nil)
		}
		ui.Spacer(c)
		if s := m.current(); !s.home() {
			b := iconButton(c, refreshIcon, "从站点刷新")
			if s.loading {
				b.Disabled(true)
			}
			if b.Clicked() {
				m.refresh(s)
			}
		}
	})
}

func (m *Model) siteTab(c *ui.Context, i int, s *siteState) {
	t := c.Theme()
	active := i == m.active
	tab := ui.ButtonBase(c).Key(s.site.ID).Height(32).Padding(0, 10, 0, 12).Radius(10).Gap(8).
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
		if menu.Item("编辑站点…").Chosen() {
			site := s.site
			m.openSiteForm(&site)
		}
		if menu.Item("刷新").Chosen() {
			m.refresh(s)
		}
		menu.Separator()
		if menu.Item("向左移动").Disabled(i <= 1).Chosen() {
			m.moveSite(i, i-1)
		}
		if menu.Item("向右移动").Disabled(i == len(m.sites)-1).Chosen() {
			m.moveSite(i, i+1)
		}
		menu.Separator()
		if menu.Item("移除站点").Danger().Chosen() {
			m.ask(confirmation{
				title:   "移除站点「" + s.displayName() + "」？",
				message: "会关闭它的终端，并删除本机保存的站点凭据。站点上的数据不受影响。",
				action:  "移除",
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
		ui.Box(c).Padding(0, 6).Radius(8).Background(hover(c)).Children(func() {
			ui.Textf(c, "%d", n).FontSize(11).TextColor(t.TextMuted)
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
			b := sidebarButton(c).Grow(1).Gap(8).Label(map[bool]string{true: "添加服务器或终端", false: "添加服务器"}[s.home()])
			if b.Clicked() {
				m.openServerForm(s)
			}
			b.Children(func() {
				ui.Icon(c, plusIcon).Size(14, 14)
				ui.Text(c, map[bool]string{true: "添加服务器或终端", false: "添加服务器"}[s.home()]).FontSize(13).FontWeight(500)
			})
			m.foldButton(c, s)
			g := sidebarButton(c).Width(38).Label("新建分组").Tooltip("新建分组")
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
		emptyHint(c, map[bool]string{true: "加载中…", false: "站点上还没有服务器，点击下方「添加服务器」"}[s.loading])
		return
	}
	m.nodes(c, s, s.tree, "", 0)
}

// nodes lists the nodes of a site's tree in the group parent.
func (m *Model) nodes(c *ui.Context, s *siteState, nodes []ternssh.Node, parent string, depth int) {
	for _, node := range nodes {
		node := node
		if node.Type == "group" {
			m.groupBlock(c, s, node.ID, node.Name, parent, countServers(node.Children), depth, func() { m.nodes(c, s, node.Children, node.ID, depth+1) })
			continue
		}
		m.serverRow(c, s, serverRowData{
			key: "r:" + node.ID, name: node.Name, addr: node.Username + "@" + node.Host + portSuffix(node.Port),
			depth: depth, drag: dragged{s, store.KindServer, node.ID}, parent: parent,
		}, func() { m.openSynced(s, node) }, func(menu *ui.Menu) {
			if menu.Item("编辑…").Chosen() {
				m.editSynced(s, node)
			}
			if menu.Item("重置主机密钥").Disabled(m.store.SiteHostKey(s.site.ID, node.ID) == "").Chosen() {
				m.store.SetSiteHostKey(s.site.ID, node.ID, "")
				m.notify("已重置 " + node.Name + " 的主机密钥，下次连接时重新记录")
			}
			menu.Separator()
			if menu.Item("从站点删除").Danger().Chosen() {
				m.ask(confirmation{
					title:   "从站点删除「" + node.Name + "」？",
					message: "服务器和它的凭据会从站点 " + s.displayName() + " 上删除，网页端也将看不到它。",
					action:  "删除",
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
	row := ui.ButtonBase(c).Key(d.key).Padding(6, 8, 6, 8+float32(d.depth)*14+16).Gap(8).Radius(8).AlignItems(ui.Center).Label(d.name).
		Drag(d.drag)
	switch {
	case s.selected == d.key:
		row.Background(selected(c))
	case row.Hovered():
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
		if mm.Item("打开终端").Chosen() {
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
		if row.Hovered() || s.selected == d.key {
			b := ui.ButtonBase(c).Size(24, 22).Radius(6).Center().Label("连接 " + d.name).Tooltip("打开终端")
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
		emptyHint(c, "还没有服务器或本地终端，点击下方「添加服务器或终端」")
		return
	}
	m.homeTree(c, s, "", 0)
}

// homeTree lists what is in a group of the home tab, in its order.
func (m *Model) homeTree(c *ui.Context, s *siteState, parent string, depth int) {
	for _, it := range store.Children(s.groups, s.terminals, s.local, parent) {
		switch it.Kind {
		case store.KindGroup:
			if i := slices.IndexFunc(s.groups, func(g store.Group) bool { return g.ID == it.ID }); i >= 0 {
				g := s.groups[i]
				m.groupBlock(c, s, g.ID, g.Name, parent, s.homeCount(g.ID), depth, func() { m.homeTree(c, s, g.ID, depth+1) })
			}
		case store.KindTerminal:
			if i := slices.IndexFunc(s.terminals, func(t store.Terminal) bool { return t.ID == it.ID }); i >= 0 {
				m.terminalRow(c, s, s.terminals[i], parent, depth)
			}
		case store.KindServer:
			if i := slices.IndexFunc(s.local, func(srv store.Server) bool { return srv.ID == it.ID }); i >= 0 {
				m.localRow(c, s, s.local[i], parent, depth)
			}
		}
	}
}

func (m *Model) terminalRow(c *ui.Context, s *siteState, term store.Terminal, parent string, depth int) {
	m.serverRow(c, s, serverRowData{
		key: "t:" + term.ID, name: term.Name, addr: displayDir(term.Dir), icon: terminalIcon, depth: depth,
		drag: dragged{s, store.KindTerminal, term.ID}, parent: parent,
	}, func() { m.openShell(s, term) }, func(menu *ui.Menu) {
		if menu.Item("编辑…").Chosen() {
			m.editTerminal(s, term)
		}
		menu.Separator()
		if menu.Item("删除").Danger().Chosen() {
			m.ask(confirmation{
				title:   "删除终端「" + term.Name + "」？",
				message: "只删除这一项，不影响本机上的文件。",
				action:  "删除",
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
		if menu.Item("编辑…").Chosen() {
			m.editLocal(s, srv)
		}
		if menu.Item("重置主机密钥").Disabled(srv.HostKey == "").Chosen() {
			m.store.SetHostKey(srv.ID, "")
			s.local = m.store.Servers()
			m.notify("已重置 " + srv.Name + " 的主机密钥，下次连接时重新记录")
		}
		menu.Separator()
		if menu.Item("删除").Danger().Chosen() {
			m.ask(confirmation{
				title:   "删除服务器「" + srv.Name + "」？",
				message: "会删除本机保存的连接信息和凭据。",
				action:  "删除",
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
				ui.Text(c, "没有打开的终端").FontSize(16).Bold()
				ui.Text(c, "双击左侧的服务器，或点它右侧的终端按钮打开终端").TextColor(t.TextMuted)
				if s.home() {
					ui.Text(c, "按 "+newSessionLabel+" 打开本地终端").FontSize(12).TextColor(t.TextMuted)
				} else {
					ui.Text(c, "选中服务器后按 "+newSessionLabel+" 打开终端").FontSize(12).TextColor(t.TextMuted)
				}
				if s.home() && len(m.sites) == 1 {
					ui.Text(c, "要在多台设备间同步服务器，可以点击顶部的 + 添加 ternssh 站点").FontSize(12).TextColor(t.TextMuted)
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
	ui.Row(c).Shrink(0).AlignItems(ui.Center).Padding(0, 8, 0, 0).Gap(2).Children(func() {
		m.tabStrip(c, s)
		if s.current >= 0 && s.current < len(s.sessions) {
			m.panelButtons(c, s.sessions[s.current])
		}
	})
}

// tabStrip is the tabs of the terminals, which scroll when they are many.
func (m *Model) tabStrip(c *ui.Context, s *siteState) {
	t := c.Theme()
	ui.ScrollHorizontal(c).Grow(1).MinWidth(0).Scrollbars(false).Children(func() {
		ui.Row(c).Height(40).Padding(0, 6).Gap(4).AlignItems(ui.Center).Children(func() {
			closed := -1
			for i, sess := range s.sessions {
				tab := ui.ButtonBase(c).Key(sess.id).Height(28).Padding(0, 4, 0, 10).Gap(7).Radius(8).AlignItems(ui.Center).MaxWidth(240).Label(sess.title)
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
					if menu.Item("重新连接").Chosen() {
						m.reconnect(s, i)
					}
					if menu.Item("关闭").Chosen() {
						closed = i
					}
				})
				tab.Children(func() {
					dot(c, sessionColor(sess.status))
					text := ui.Text(c, sess.title).FontSize(12).SingleLine().MinWidth(0).Shrink(1)
					if i != s.current {
						text.TextColor(t.TextMuted)
					}
					x := ui.ButtonBase(c).Size(18, 18).Radius(5).Center().Label("关闭 " + sess.title)
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
			if iconButton(c, plusIcon, "新建会话").Tooltip("新建会话 (" + newSessionLabel + ")").Clicked() {
				m.newSession()
			}
		})
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
			status = map[bool]string{true: "运行中", false: "已退出"}[sess.status == sshconn.StatusReady]
		}
		ui.Text(c, status).FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, "·").FontSize(11).TextColor(t.TextMuted)
		via := "本机直连"
		switch sess.mode {
		case Synced:
			via += " · 凭据来自 " + hostOf(s.site.URL)
		case Shell:
			via = "本地终端"
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
			if ui.Button(c, map[bool]string{true: "重新打开", false: "重新连接"}[sess.mode == Shell]).FontSize(11).Padding(2, 10).Clicked() {
				m.reconnect(s, s.current)
			}
		}
	})
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
				m.notify("删除失败: " + err.Error())
				return
			}
			m.notify("已从站点删除 " + node.Name)
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
