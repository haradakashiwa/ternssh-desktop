package gui

import (
	"slices"
	"testing"

	"ternssh-desktop/internal/store"
)

func TestConnectGroup(t *testing.T) {
	m, st := testModel(t)
	withSites(t, m, st)

	// A site's group: its servers, not those outside it.
	s := m.sites[1]
	m.connectGroup(s, "g1")
	if len(s.sessions) != 2 || s.sessions[0].node.ID != "s1" || s.sessions[1].node.ID != "s2" {
		t.Fatalf("site sessions %+v", s.sessions)
	}
	if m.toast != "正在连接 2 台服务器" {
		t.Errorf("toast %q", m.toast)
	}

	// At home: the servers of the subgroups too, in the sidebar's order,
	// and no local terminal.
	home := m.sites[0]
	st.PutGroup(store.Group{ID: "a", Name: "生产"})
	st.PutGroup(store.Group{ID: "b", Name: "数据库", ParentID: "a", Sort: 1})
	cred := &store.Credential{Password: "x"}
	st.PutServer(store.Server{ID: "x1", Name: "web", Host: "10.0.0.1", Port: 22, Username: "root", AuthType: "password", GroupID: "a"}, cred)
	st.PutServer(store.Server{ID: "x2", Name: "db", Host: "10.0.0.2", Port: 22, Username: "root", AuthType: "password", GroupID: "b"}, cred)
	st.PutTerminal(store.Terminal{ID: "t1", Name: "shell", GroupID: "a"})
	m.refresh(home)
	if n := home.serverCount("a"); n != 2 {
		t.Errorf("%d servers in the group, want 2", n)
	}
	m.connectGroup(home, "a")
	var got []string
	for _, sess := range home.sessions {
		got = append(got, sess.localID)
	}
	if len(got) != 2 || !slices.Contains(got, "x1") || !slices.Contains(got, "x2") {
		t.Errorf("home sessions %v", got)
	}
	for _, sess := range home.sessions {
		if sess.mode != Local {
			t.Errorf("session %s is %v, want a server's", sess.title, sess.mode)
		}
	}
	if n := home.serverCount("b"); n != 1 {
		t.Errorf("%d servers in the subgroup, want 1", n)
	}
}
