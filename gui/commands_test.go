package gui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"ternssh-desktop/internal/sshconn"
	"ternssh-desktop/internal/store"
	"ternssh-desktop/internal/ternssh"
)

// typed records what the quick commands send to terminals.
func typed(t *testing.T) *[]string {
	var sent []string
	old := input
	input = func(_ *session, text string, paste bool) bool {
		if paste {
			text = "paste:" + text
		}
		sent = append(sent, text)
		return true
	}
	t.Cleanup(func() { input = old })
	return &sent
}

func TestLocalCommands(t *testing.T) {
	m, st := testModel(t)
	withSites(t, m, st)
	sent := typed(t)
	m.selectSite(0)
	m.newSession() // a local terminal
	home := m.sites[0]
	sess := home.sessions[0]
	tt := ui.NewTester(m.View, 1100, 700)

	// Its pane below shows at first, its files chosen; the commands are a
	// tab away, without the built-in ones, which are for servers.
	if !sess.showBottom || sess.bottomTab != tabFiles {
		t.Fatalf("pane shown %v, tab %d", sess.showBottom, sess.bottomTab)
	}
	if err := tt.Click("快捷指令"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("保存在本机") || !tt.HasText("还没有快捷指令，点右上角的「+」添加") || tt.HasText("内置") {
		t.Fatalf("texts %q", tt.Texts())
	}
	tt.Click("添加快捷指令")
	if !m.commandFormOpen {
		t.Fatal("the form is not open")
	}
	m.saveCommand()
	if m.commandForm.err != "请填写名称" {
		t.Fatalf("error %q", m.commandForm.err)
	}
	m.commandForm.label, m.commandForm.command = "构建", "cd ~/src\r\nmake"
	m.saveCommand()
	if m.commandFormOpen || len(st.QuickCommands()) != 1 || len(home.commands) != 1 {
		t.Fatalf("open %v, saved %v", m.commandFormOpen, st.QuickCommands())
	}
	tt.Frame()
	tt.Click("构建")
	if len(*sent) != 1 || (*sent)[0] != "cd ~/src\rmake\r" || !sess.refocus {
		t.Fatalf("sent %q", *sent)
	}
	// Not when the terminal is not up.
	sess.status = sshconn.StatusClosed
	tt.Click("构建")
	if len(*sent) != 1 || m.toast == "" {
		t.Errorf("sent %q, notice %q", *sent, m.toast)
	}
	sess.status = sshconn.StatusReady
	m.runCommand(sess, "ls -l", true)
	if (*sent)[1] != "paste:ls -l" {
		t.Errorf("sent %q", *sent)
	}

	m.deleteCommand(home, home.commands[0])
	m.confirm.do()
	if len(st.QuickCommands()) != 0 || len(home.commands) != 0 {
		t.Errorf("not deleted: %v", st.QuickCommands())
	}
}

func TestSiteCommands(t *testing.T) {
	var mu sync.Mutex
	widgets := []map[string]any{{"id": "w1", "type": "quick_commands", "grid_x": 0, "grid_y": 0, "grid_w": 3, "grid_h": 6,
		"config_json": `{"customCommands":[{"id":"q1","label":"磁盘","command":"df -h"}],"targetMode":"current"}`}}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/api/v1/dashboards" {
			w.WriteHeader(404)
			io.WriteString(w, `{"error":"not found"}`)
			return
		}
		if r.Method == http.MethodPut {
			var body struct{ Widgets []map[string]any }
			json.NewDecoder(r.Body).Decode(&body)
			widgets = body.Widgets
		}
		json.NewEncoder(w).Encode(map[string]any{"widgets": widgets})
	}))
	defer site.Close()

	m, st := testModel(t)
	withSites(t, m, st)
	sent := typed(t)
	apply := updatesHere(t, m)
	s := m.sites[1]
	s.client, _ = ternssh.New(site.URL, m.authOf(s.site))
	m.openSynced(s, s.tree[1])
	sess := s.sessions[0]
	sess.status = sshconn.StatusReady
	tt := ui.NewTester(m.View, 1100, 700)

	// Files first, the commands a tab away, read from the site.
	if err := tt.Click("快捷指令"); err != nil {
		t.Fatal(err)
	}
	apply(func() bool { return s.commandsLoaded })
	tt.Frame()
	if !tt.HasText("来自站点「生产环境」") || !tt.HasText("磁盘") || !tt.HasText("内置") {
		t.Fatalf("texts %q", tt.Texts())
	}
	snapshot(t, tt, "commands")
	tt.Click("磁盘")
	tt.Click("监听端口")
	if len(*sent) != 2 || (*sent)[0] != "df -h\r" {
		t.Fatalf("sent %q", *sent)
	}

	// Added in the app, on the site.
	m.openCommandForm(s, nil)
	m.commandForm.label, m.commandForm.command = "日志", "journalctl -f"
	m.saveCommand()
	apply(func() bool { return !m.commandForm.busy })
	if m.commandFormOpen || len(s.commands) != 2 || s.commands[1].Label != "日志" {
		t.Fatalf("commands %v, error %q", s.commands, m.commandForm.err)
	}
	mu.Lock()
	cfg := widgets[0]["config_json"].(string)
	mu.Unlock()
	var got struct{ CustomCommands []ternssh.QuickCommand }
	json.Unmarshal([]byte(cfg), &got)
	if len(got.CustomCommands) != 2 {
		t.Errorf("site config %s", cfg)
	}

	m.deleteCommand(s, s.commands[0])
	m.confirm.do()
	apply(func() bool { return len(s.commands) == 1 })
	if s.commands[0].Label != "日志" {
		t.Errorf("commands %v", s.commands)
	}
}

func TestLocalFiles(t *testing.T) {
	m, st := testModel(t)
	apply := updatesHere(t, m)
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o644)
	var opened string
	m.backend.OpenPath = func(p string) error { opened = p; return nil }
	st.PutTerminal(store.Terminal{ID: "t1", Name: "项目", Dir: dir})
	home := m.sites[0]
	home.terminals = st.Terminals()
	m.openShell(home, home.terminals[0])
	sess := home.sessions[0]
	tt := ui.NewTester(m.View, 1100, 700)

	// The files of where the terminal started, at first.
	apply(func() bool { return !sess.files.loading })
	tt.Frame()
	f := sess.files
	if !f.local || f.dir != dir || len(f.entries) != 2 || f.entries[0].Name != "src" {
		t.Fatalf("files %+v", f)
	}
	if !tt.HasText("notes.txt") || !tt.HasText("src") {
		t.Fatalf("texts %q", tt.Texts())
	}
	snapshot(t, tt, "local-files")

	// A file opens with its application, a directory in the panel.
	f.selected = 1
	m.openLocalFile(filepath.Join(f.dir, f.entries[1].Name))
	if opened != filepath.Join(dir, "notes.txt") {
		t.Errorf("opened %q", opened)
	}
	m.listDir(f, filepath.Join(dir, "src"))
	apply(func() bool { return !f.loading })
	if f.dir != filepath.Join(dir, "src") || len(f.entries) != 0 {
		t.Fatalf("dir %q, entries %v", f.dir, f.entries)
	}
	// Files dropped are copied here.
	m.upload(f, []string{filepath.Join(dir, "notes.txt")})
	apply(func() bool { return len(f.transfers) == 1 && f.transfers[0].finished && !f.loading })
	if b, err := os.ReadFile(filepath.Join(dir, "src", "notes.txt")); err != nil || string(b) != "hello" || f.transfers[0].err != "" {
		t.Fatalf("copied %q, %v, %q", b, err, f.transfers[0].err)
	}
	if len(f.entries) != 1 {
		t.Errorf("entries %v", f.entries)
	}
	// Up, to where it started.
	tt.Frame()
	if err := tt.Click("上级目录"); err != nil {
		t.Fatal(err)
	}
	apply(func() bool { return !f.loading })
	if f.dir != dir {
		t.Errorf("dir %q", f.dir)
	}
}
