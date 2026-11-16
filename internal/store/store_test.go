package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOldHistoriesRemoved(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "history", "server-s1.vt")
	os.MkdirAll(filepath.Dir(old), 0o700)
	os.WriteFile(old, []byte("$ cat secrets"), 0o600)
	if _, err := Open(filepath.Join(dir, "config.json"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(old)); !os.IsNotExist(err) {
		t.Errorf("the old histories are still there: %v", err)
	}
}

func TestGroups(t *testing.T) {
	st, _ := Open(filepath.Join(t.TempDir(), "config.json"), nil)
	st.PutGroup(Group{ID: "a", Name: "生产"})
	st.PutGroup(Group{ID: "b", Name: "数据库", ParentID: "a"})
	st.PutGroup(Group{ID: "c", Name: "其他"})
	st.PutServer(Server{ID: "s1", Name: "web"}, nil)
	st.PutServer(Server{ID: "s2", Name: "db"}, nil)
	st.PutTerminal(Terminal{ID: "t1", Name: "shell"})

	if err := st.Move(KindServer, "s1", "b", -1); err != nil {
		t.Fatal(err)
	}
	st.Move(KindTerminal, "t1", "a", -1)
	if srv := st.Servers(); srv[0].GroupID != "b" {
		t.Errorf("servers %+v: s1 should be in b", srv)
	}
	if err := st.Move(KindGroup, "a", "b", -1); err == nil {
		t.Error("moved a group into its own subgroup")
	}
	if err := st.Move(KindGroup, "a", "a", -1); err == nil {
		t.Error("moved a group into itself")
	}
	if err := st.Move(KindServer, "s2", "nope", -1); err == nil {
		t.Error("moved into a group that is not there")
	}
	st.PutGroup(Group{ID: "c", Name: "改名"})
	if g := st.Groups(); g[2].Name != "改名" || len(g) != 3 {
		t.Errorf("groups %+v", g)
	}

	// Its subgroups go with a group; what was in them goes to the top.
	st.DeleteGroup("a")
	if g := st.Groups(); len(g) != 1 || g[0].ID != "c" {
		t.Errorf("groups %+v", g)
	}
	if st.Servers()[1].GroupID != "" || st.Terminals()[0].GroupID != "" {
		t.Errorf("servers %+v, terminals %+v", st.Servers(), st.Terminals())
	}
}

func TestOrder(t *testing.T) {
	st, _ := Open(filepath.Join(t.TempDir(), "config.json"), nil)
	order := func(parent string) string {
		var ids []string
		for _, it := range Children(st.Groups(), st.Terminals(), st.Servers(), parent) {
			ids = append(ids, it.ID)
		}
		return strings.Join(ids, " ")
	}
	st.PutServer(Server{ID: "s1"}, nil)
	st.PutTerminal(Terminal{ID: "t1"})
	st.PutGroup(Group{ID: "g"})
	st.PutServer(Server{ID: "s2"}, nil)
	if got := order(""); got != "s1 t1 g s2" {
		t.Fatalf("order %q: what is added goes last", got)
	}
	st.Move(KindServer, "s2", "", 0)
	st.Move(KindGroup, "g", "", 2)
	if got := order(""); got != "s2 s1 g t1" {
		t.Errorf("order %q", got)
	}
	// Into a group, at a place; edited, in place.
	st.Move(KindServer, "s1", "g", 0)
	st.Move(KindTerminal, "t1", "g", 0)
	st.PutServer(Server{ID: "s1", Name: "edited", GroupID: "g"}, nil)
	if got := order("g"); got != "t1 s1" {
		t.Errorf("order in g %q", got)
	}
	// Edited into another group, last there.
	st.PutServer(Server{ID: "s1", GroupID: ""}, nil)
	if got := order(""); got != "s2 g s1" {
		t.Errorf("order %q", got)
	}
}
