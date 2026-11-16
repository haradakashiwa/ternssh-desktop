package gui

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"ternssh-desktop/internal/store"
	"ternssh-desktop/internal/ternssh"
)

// dragged is a row of the sidebar being dragged: a group, a server or a
// local terminal (store.KindGroup, KindServer, KindTerminal) of a tab.
type dragged struct {
	site *siteState
	kind string
	id   string
}

// groupOpen returns whether a group of the tab shows what is in it.
func (s *siteState) groupOpen(id string) *bool {
	open, ok := s.groupsOpen[id]
	if !ok {
		v := true
		open = &v
		s.groupsOpen[id] = open
	}
	return open
}

// groupIDs lists the IDs of every group of the tab.
func (s *siteState) groupIDs() []string {
	var ids []string
	if s.home() {
		for _, g := range s.groups {
			ids = append(ids, g.ID)
		}
		return ids
	}
	var walk func([]ternssh.Node)
	walk = func(nodes []ternssh.Node) {
		for _, n := range nodes {
			if n.Type == "group" {
				ids = append(ids, n.ID)
				walk(n.Children)
			}
		}
	}
	walk(s.tree)
	return ids
}

// allOpen reports whether every group of the tab shows what is in it.
func (s *siteState) allOpen() bool {
	for _, id := range s.groupIDs() {
		if !*s.groupOpen(id) {
			return false
		}
	}
	return true
}

// setAllOpen opens or closes every group of the tab.
func (s *siteState) setAllOpen(open bool) {
	for _, id := range s.groupIDs() {
		*s.groupOpen(id) = open
	}
}

// foldButton opens every group of the tab while one is closed, and
// closes them all otherwise; there is none without groups.
func (m *Model) foldButton(c *ui.Context, s *siteState) {
	if len(s.groupIDs()) == 0 {
		return
	}
	svg, label := collapseIcon, "全部收起"
	if !s.allOpen() {
		svg, label = expandIcon, "全部展开"
	}
	b := sidebarButton(c).Width(38).Label(label).Tooltip(label)
	if b.Clicked() {
		s.setAllOpen(!s.allOpen())
	}
	b.Children(func() { ui.Icon(c, svg).Size(16, 16) })
}

// groupBlock is a group's row and, while open, what is in it.
func (m *Model) groupBlock(c *ui.Context, s *siteState, id, name, parent string, count, depth int, children func()) {
	t := c.Theme()
	open := s.groupOpen(id)
	ui.Column(c).Key("gb:" + id).Children(func() {
		row := ui.ButtonBase(c).Key("g:"+id).Height(28).Padding(0, 8, 0, 8+float32(depth)*14).Gap(6).Radius(7).
			AlignItems(ui.Center).Label(name).Drag(dragged{s, store.KindGroup, id})
		if row.Hovered() {
			row.Background(hover(c))
		}
		if row.Clicked() {
			*open = !*open
		}
		m.dropOn(c, s, row, dragged{s, store.KindGroup, id}, parent)
		row.ContextMenu(func(menu *ui.Menu) {
			if menu.Item("全部展开").Chosen() {
				s.setAllOpen(true)
			}
			if menu.Item("全部收起").Chosen() {
				s.setAllOpen(false)
			}
			menu.Separator()
			if menu.Item("新建子分组…").Chosen() {
				m.openGroupForm(s, "", id, "")
			}
			if menu.Item("重命名…").Chosen() {
				m.openGroupForm(s, id, "", name)
			}
			menu.Separator()
			if menu.Item("删除分组").Danger().Chosen() {
				m.ask(confirmation{
					title:   "删除分组「" + name + "」？",
					message: "它的子分组会一起删除，其中的服务器和终端移到顶层，不会被删除。",
					action:  "删除",
					do:      func() { m.deleteGroup(s, id, name) },
				})
			}
		})
		row.Children(func() {
			rot := float32(0)
			if *open {
				rot = 90
			}
			ui.Icon(c, chevronIcon).Size(10, 10).TextColor(t.TextMuted).Rotate(rot)
			ui.Icon(c, folderIcon).Size(14, 14).TextColor(t.TextMuted)
			ui.Text(c, name).FontSize(13).SingleLine().Grow(1).MinWidth(0)
			ui.Textf(c, "%d", count).FontSize(11).TextColor(t.TextMuted)
		})
		if *open {
			children()
		}
	})
}

// Where a row dropped on another goes.
type zone int

const (
	zoneBefore zone = iota
	zoneAfter
	zoneInto // a group
)

// zoneOf is where the pointer over a row puts what is dropped: before it
// over its top half, after it over its bottom half; for a group, before it
// over its top part and into it elsewhere.
func zoneOf(row ui.Element, group bool) zone {
	_, y, in := row.PointerPosition()
	h := row.Bounds().H
	switch {
	case group && (!in || h == 0 || y >= h*0.3):
		return zoneInto
	case !in || h == 0 || y >= h/2:
		return zoneAfter
	}
	return zoneBefore
}

// dropOn makes a row of the sidebar, it, in the group parent, take rows
// dragged onto it, showing where they would go.
func (m *Model) dropOn(c *ui.Context, s *siteState, row ui.Element, it dragged, parent string) {
	d, over := ui.DragOver[dragged](row)
	dropped, ok := ui.Drop[dragged](row)
	if ok {
		d = dropped
	}
	if !over && !ok {
		return
	}
	z := zoneOf(row, it.kind == store.KindGroup)
	to, index := parent, -1
	if z == zoneInto {
		to = it.id
		index = len(m.siblings(s, it.id, d))
	} else if i := slices.Index(m.siblings(s, parent, d), it); i >= 0 {
		index = i
		if z == zoneAfter {
			index++
		}
	}
	if !m.canPlace(s, d, to, index) {
		return
	}
	if ok {
		m.moveTo(s, d, to, index)
		return
	}
	accent := c.Theme().Accent
	if z == zoneInto {
		row.Background(accent.Alpha(0.16))
		return
	}
	row.DrawOver(func(p *ui.Painter, r ui.Rect) {
		y := r.Y - 1
		if z == zoneAfter {
			y = r.Y + r.H - 1
		}
		p.Fill(ui.Rect{X: r.X + 4, Y: y, W: r.W - 8, H: 2}, accent, 1)
	})
}

// siblings lists the rows in the group parent of the tab, the top when
// empty, in order, without but.
func (m *Model) siblings(s *siteState, parent string, but dragged) []dragged {
	var out []dragged
	add := func(kind, id string) {
		if kind != but.kind || id != but.id {
			out = append(out, dragged{s, kind, id})
		}
	}
	if s.home() {
		for _, it := range store.Children(s.groups, s.terminals, s.local, parent) {
			add(it.Kind, it.ID)
		}
		return out
	}
	nodes := s.tree
	if parent != "" {
		g, _, _ := findNode(s.tree, "", "group", parent)
		nodes = g.Children
	}
	for _, n := range nodes {
		kind := store.KindServer
		if n.Type == "group" {
			kind = store.KindGroup
		}
		add(kind, n.ID)
	}
	return out
}

// canPlace reports whether d can go into the group to (the top when
// empty) at index: somewhere else than it is, and a group not into
// itself.
func (m *Model) canPlace(s *siteState, d dragged, to string, index int) bool {
	if d.site != s || index < 0 {
		return false
	}
	if d.kind == store.KindGroup && to != "" && m.within(s, to, d.id) {
		return false
	}
	all := m.siblings(s, to, dragged{})
	return slices.Index(all, d) != index
}

// within reports whether the group id is the group ancestor or in it.
func (m *Model) within(s *siteState, id, ancestor string) bool {
	if s.home() {
		for p := id; p != ""; {
			if p == ancestor {
				return true
			}
			next := ""
			for _, g := range s.groups {
				if g.ID == p {
					next = g.ParentID
				}
			}
			p = next
		}
		return false
	}
	node, _, ok := findNode(s.tree, "", "group", ancestor)
	if !ok {
		return false
	}
	_, _, in := findNode([]ternssh.Node{node}, "", "group", id)
	return in
}

// moveTo moves a row into the group to, or to the top, at index among
// what else is there.
func (m *Model) moveTo(s *siteState, d dragged, to string, index int) {
	if !m.canPlace(s, d, to, index) {
		return
	}
	if to != "" {
		*s.groupOpen(to) = true
	}
	if s.home() {
		if err := m.store.Move(d.kind, d.id, to, index); err != nil {
			m.notify("无法移动: " + err.Error())
		}
		m.refresh(s)
		return
	}
	var parent *string
	if to != "" {
		parent = &to
	}
	client := s.client
	go func() {
		ctx, cancel := request()
		defer cancel()
		tree, err := client.Move(ctx, typeOf(d.kind), d.id, parent, index)
		m.update(func() {
			if err != nil {
				m.notify("移动失败: " + err.Error())
				return
			}
			s.tree = tree
		})
	}()
}

// moveLastIn moves a row last into the group to, or to the top.
func (m *Model) moveLastIn(s *siteState, d dragged, to string) {
	m.moveTo(s, d, to, len(m.siblings(s, to, d)))
}

// typeOf is the site's type of a kind of row.
func typeOf(kind string) string {
	if kind == store.KindGroup {
		return "group"
	}
	return "server"
}

// findNode finds the node of a type and ID in a tree, and the group it is
// in, under parent.
func findNode(nodes []ternssh.Node, parent, typ, id string) (ternssh.Node, string, bool) {
	for _, n := range nodes {
		if n.Type == typ && n.ID == id {
			return n, parent, true
		}
		if n.Type == "group" {
			if found, p, ok := findNode(n.Children, n.ID, typ, id); ok {
				return found, p, true
			}
		}
	}
	return ternssh.Node{}, "", false
}

// homeCount counts the servers and terminals in a group of the home tab
// and its subgroups.
func (s *siteState) homeCount(id string) int {
	n := 0
	for _, srv := range s.local {
		if srv.GroupID == id {
			n++
		}
	}
	for _, t := range s.terminals {
		if t.GroupID == id {
			n++
		}
	}
	for _, g := range s.groups {
		if g.ParentID == id {
			n += s.homeCount(g.ID)
		}
	}
	return n
}

// --- The group dialog

type groupForm struct {
	site    *siteState
	editing string // the ID of the group renamed
	parent  string // the group a new one goes in
	name    string
	busy    bool
	err     string
}

func (m *Model) openGroupForm(s *siteState, editing, parent, name string) {
	m.groupForm = groupForm{site: s, editing: editing, parent: parent, name: name}
	m.groupFormOpen = true
}

func (m *Model) groupDialog(c *ui.Context) {
	f := &m.groupForm
	if f.site == nil {
		return
	}
	dialog(c, &m.groupFormOpen, 380, func() {
		where := "保存在本机"
		if !f.site.home() {
			where = "保存到站点「" + f.site.displayName() + "」"
		}
		dialogTitle(c, map[bool]string{true: "新建分组", false: "重命名分组"}[f.editing == ""], where)
		ui.Form(c, func() {
			ui.Field(c, "名称", func() { ui.TextInput(c, &f.name).Placeholder("例如：生产环境").AutoFocus() })
		})
		errorText(c, f.err)
		dialogButtons(c, f.busy, map[bool]string{true: "新建", false: "保存"}[f.editing == ""],
			func() { m.groupFormOpen = false }, m.saveGroup)
	})
}

func (m *Model) saveGroup() {
	f := &m.groupForm
	s := f.site
	name := strings.TrimSpace(f.name)
	if name == "" {
		f.err = "请输入名称"
		return
	}
	if s.home() {
		g := store.Group{ID: f.editing, Name: name, ParentID: f.parent}
		if g.ID == "" {
			g.ID = store.NewID()
		}
		if err := m.store.PutGroup(g); err != nil {
			f.err = "无法保存: " + err.Error()
			return
		}
		if f.parent != "" {
			*s.groupOpen(f.parent) = true
		}
		m.groupFormOpen = false
		m.refresh(s)
		return
	}
	client, editing := s.client, f.editing
	var parent *string
	if f.parent != "" {
		parent = &f.parent
		*s.groupOpen(f.parent) = true
	}
	f.busy, f.err = true, ""
	go func() {
		ctx, cancel := request()
		defer cancel()
		var err error
		if editing != "" {
			err = client.RenameGroup(ctx, editing, name)
		} else {
			_, err = client.CreateGroup(ctx, name, parent)
		}
		m.update(func() {
			f.busy = false
			if err != nil {
				f.err = err.Error()
				return
			}
			m.groupFormOpen = false
			m.refresh(s)
		})
	}()
}

func (m *Model) deleteGroup(s *siteState, id, name string) {
	if s.home() {
		if err := m.store.DeleteGroup(id); err != nil {
			m.notify("删除失败: " + err.Error())
		}
		m.refresh(s)
		return
	}
	client := s.client
	go func() {
		ctx, cancel := request()
		defer cancel()
		err := client.DeleteGroup(ctx, id)
		m.update(func() {
			if err != nil {
				m.notify("删除失败: " + err.Error())
				return
			}
			m.notify("已删除分组 " + name)
			m.refresh(s)
		})
	}()
}
