package store

import (
	"errors"
	"slices"
	"sort"
)

// What Move moves.
const (
	KindGroup    = "group"
	KindServer   = "server"
	KindTerminal = "terminal"
)

// Groups returns the groups of the 本机 tab.
func (s *Store) Groups() []Group {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Group(nil), s.d.Groups...)
}

// PutGroup adds a group, or renames the one with its ID.
func (s *Store) PutGroup(g Group) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.d.Groups {
		if s.d.Groups[i].ID == g.ID {
			s.d.Groups[i].Name = g.Name
			return s.save()
		}
	}
	g.Sort = s.nextSort(g.ParentID)
	s.d.Groups = append(s.d.Groups, g)
	return s.save()
}

// DeleteGroup removes a group with the groups in it, as a ternssh site
// does; the servers and terminals in them go to the top.
func (s *Store) DeleteGroup(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	gone := map[string]bool{id: true}
	for changed := true; changed; {
		changed = false
		for _, g := range s.d.Groups {
			if gone[g.ParentID] && !gone[g.ID] {
				gone[g.ID], changed = true, true
			}
		}
	}
	groups := s.d.Groups[:0]
	for _, g := range s.d.Groups {
		if !gone[g.ID] {
			groups = append(groups, g)
		}
	}
	s.d.Groups = groups
	for i := range s.d.Servers {
		if gone[s.d.Servers[i].GroupID] {
			s.d.Servers[i].GroupID = ""
		}
	}
	for i := range s.d.Terminals {
		if gone[s.d.Terminals[i].GroupID] {
			s.d.Terminals[i].GroupID = ""
		}
	}
	return s.save()
}

// Item is a group, server or terminal of the 本机 tab.
type Item struct {
	Kind string // KindGroup, KindServer or KindTerminal
	ID   string
	Sort int
}

// Children lists what is in the group parent, the top when empty, in
// order: by Sort, and of equal Sort (as before there was any) groups,
// terminals, then servers, each as listed.
func Children(groups []Group, terminals []Terminal, servers []Server, parent string) []Item {
	var items []Item
	for _, g := range groups {
		if g.ParentID == parent {
			items = append(items, Item{KindGroup, g.ID, g.Sort})
		}
	}
	for _, t := range terminals {
		if t.GroupID == parent {
			items = append(items, Item{KindTerminal, t.ID, t.Sort})
		}
	}
	for _, srv := range servers {
		if srv.GroupID == parent {
			items = append(items, Item{KindServer, srv.ID, srv.Sort})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Sort < items[j].Sort })
	return items
}

func (s *Store) children(parent string) []Item {
	return Children(s.d.Groups, s.d.Terminals, s.d.Servers, parent)
}

// nextSort is the Sort of what goes last in the group parent.
func (s *Store) nextSort(parent string) int {
	n := 0
	for _, it := range s.children(parent) {
		n = max(n, it.Sort+1)
	}
	return n
}

// errCycle refuses moving a group into itself or a group in it.
var errCycle = errors.New("不能把分组移到它自己或它的子分组里")

// Move moves a group, server or terminal into the group groupID, the top
// when empty, to index among what else is there: last when index is out
// of range.
func (s *Store) Move(kind, id, groupID string, index int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if groupID != "" && s.group(groupID) < 0 {
		return errors.New("分组不存在")
	}
	switch kind {
	case KindGroup:
		i := s.group(id)
		if i < 0 {
			return errors.New("分组不存在")
		}
		for p := groupID; p != ""; {
			if p == id {
				return errCycle
			}
			j := s.group(p)
			if j < 0 {
				break
			}
			p = s.d.Groups[j].ParentID
		}
		s.d.Groups[i].ParentID = groupID
	case KindServer:
		i := slices.IndexFunc(s.d.Servers, func(srv Server) bool { return srv.ID == id })
		if i < 0 {
			return errors.New("服务器不存在")
		}
		s.d.Servers[i].GroupID = groupID
	case KindTerminal:
		i := slices.IndexFunc(s.d.Terminals, func(t Terminal) bool { return t.ID == id })
		if i < 0 {
			return errors.New("终端不存在")
		}
		s.d.Terminals[i].GroupID = groupID
	default:
		return errors.New("unknown kind " + kind)
	}
	items := slices.DeleteFunc(s.children(groupID), func(it Item) bool { return it.Kind == kind && it.ID == id })
	if index < 0 || index > len(items) {
		index = len(items)
	}
	items = slices.Insert(items, index, Item{Kind: kind, ID: id})
	for n, it := range items {
		s.setSort(it, n)
	}
	return s.save()
}

func (s *Store) setSort(it Item, n int) {
	switch it.Kind {
	case KindGroup:
		s.d.Groups[s.group(it.ID)].Sort = n
	case KindServer:
		for i := range s.d.Servers {
			if s.d.Servers[i].ID == it.ID {
				s.d.Servers[i].Sort = n
			}
		}
	case KindTerminal:
		for i := range s.d.Terminals {
			if s.d.Terminals[i].ID == it.ID {
				s.d.Terminals[i].Sort = n
			}
		}
	}
}

func (s *Store) group(id string) int {
	for i, g := range s.d.Groups {
		if g.ID == id {
			return i
		}
	}
	return -1
}
