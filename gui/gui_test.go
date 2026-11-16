package gui

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/pkg/sftp"

	"ternssh-desktop/internal/sshconn"
	"ternssh-desktop/internal/store"
	"ternssh-desktop/internal/ternssh"
)

type memSecrets map[string][]byte

func (s memSecrets) Set(k string, v []byte) error { s[k] = v; return nil }
func (s memSecrets) Get(k string) ([]byte, error) {
	if v, ok := s[k]; ok {
		return v, nil
	}
	return nil, errors.New("not found")
}
func (s memSecrets) Delete(k string) error { delete(s, k); return nil }

// fakeConn is a connection that never connects.
type fakeConn struct{ status sshconn.Status }

func (f *fakeConn) Read([]byte) (int, error)    { select {} }
func (f *fakeConn) Write(p []byte) (int, error) { return len(p), nil }
func (f *fakeConn) Close() error                { f.status = sshconn.StatusClosed; return nil }
func (f *fakeConn) Resize(int, int) error       { return nil }
func (f *fakeConn) Status() sshconn.Status      { return f.status }

func testModel(t *testing.T) (*Model, *store.Store) {
	t.Helper()
	pending := make(chan func(), 64)
	st, err := store.Open(filepath.Join(t.TempDir(), "config.json"), memSecrets{})
	if err != nil {
		t.Fatal(err)
	}
	m := New(st, Backend{
		// Updates from goroutines wait, as for a main thread, unless a
		// test applies them.
		Update: func(fn func()) {
			select {
			case pending <- fn:
			default:
			}
		},
		Dial: func(sshconn.LocalOptions, sshconn.Events) Conn { return &fakeConn{} },
	})
	return m, st
}

// withSites gives the model a server at home and two sites, the first
// loaded with servers and shown.
func withSites(t *testing.T, m *Model, st *store.Store) {
	t.Helper()
	for _, site := range []store.Site{
		{ID: "a", Name: "生产环境", URL: "https://ssh.example.com", Auth: store.AuthBasic, Username: "admin"},
		{ID: "b", Name: "家里 NAS", URL: "https://nas.example.net", Auth: store.AuthAccess, ClientID: "x.access"},
	} {
		if err := st.PutSite(site, "secret"); err != nil {
			t.Fatal(err)
		}
		m.sites = append(m.sites, m.newSiteState(site))
	}
	st.PutServer(store.Server{ID: "l1", Name: "办公室树莓派", Host: "192.168.1.20", Port: 22, Username: "pi", AuthType: "password"}, &store.Credential{Password: "x"})
	m.sites[0].local = st.Servers()
	g := "g1"
	s := m.sites[1]
	s.me = &ternssh.Me{AuthMode: "basic"}
	s.tree = []ternssh.Node{
		{Type: "group", ID: g, Name: "Web 集群", Children: []ternssh.Node{
			{Type: "server", ID: "s1", Name: "web-01", Host: "10.0.0.11", Port: 22, Username: "root", AuthType: "password", GroupID: &g},
			{Type: "server", ID: "s2", Name: "web-02", Host: "10.0.0.12", Port: 22, Username: "root", AuthType: "password", GroupID: &g},
		}},
		{Type: "server", ID: "s3", Name: "数据库主库", Host: "db.internal", Port: 2222, Username: "ubuntu", AuthType: "private_key"},
	}
	m.sites[2].err = "认证失败，请检查站点的用户名和密码"
	m.active = 1
}

func TestHome(t *testing.T) {
	m, _ := testModel(t)
	tt := ui.NewTester(m.View, 1100, 700)
	for _, want := range []string{"本机", "还没有服务器或本地终端，点击下方「添加服务器或终端」", "要在多台设备间同步服务器，可以点击顶部的 + 添加 ternssh 站点"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	snapshot(t, tt, "home")
	if err := tt.Click("添加站点"); err != nil {
		t.Fatal(err)
	}
	if !m.siteFormOpen || !tt.HasText("连接一个已部署的 ternssh 站点，它会成为顶部的一个标签。") {
		t.Errorf("the site dialog is not open: %q", tt.Texts())
	}
	snapshot(t, tt, "add-site")
}

func TestTabsSwitchSites(t *testing.T) {
	m, st := testModel(t)
	withSites(t, m, st)
	tt := ui.NewTester(m.View, 1100, 700)
	for _, want := range []string{"本机", "生产环境", "家里 NAS", "web-01", "数据库主库"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("办公室树莓派") {
		t.Error("a site's tab lists the servers of this machine")
	}
	snapshot(t, tt, "sites")

	if err := tt.Click("家里 NAS"); err != nil {
		t.Fatal(err)
	}
	if m.active != 2 || st.ActiveSite() != "b" {
		t.Errorf("active %d, stored %q", m.active, st.ActiveSite())
	}
	if !tt.HasText("认证失败，请检查站点的用户名和密码") {
		t.Errorf("the second site's error is not shown: %q", tt.Texts())
	}
	if err := tt.Click("本机"); err != nil {
		t.Fatal(err)
	}
	if m.active != 0 || st.ActiveSite() != "" || !tt.HasText("办公室树莓派") {
		t.Errorf("active %d, stored %q, texts %q", m.active, st.ActiveSite(), tt.Texts())
	}

	// The home tab stays first.
	m.moveSite(2, 1)
	m.moveSite(1, 0)
	if !m.sites[0].home() || m.sites[1].site.ID != "b" {
		t.Errorf("tabs %q %q %q", m.sites[0].site.ID, m.sites[1].site.ID, m.sites[2].site.ID)
	}
	if got := st.Sites(); got[0].ID != "b" {
		t.Errorf("stored order %q", got[0].ID)
	}
}

func TestOpenSessions(t *testing.T) {
	m, st := testModel(t)
	withSites(t, m, st)
	home, site := m.sites[0], m.sites[1]
	m.openSynced(site, site.tree[0].Children[0])
	m.openLocal(home, home.local[0])
	m.active = 0
	tt := ui.NewTester(m.View, 1100, 700)
	if len(site.sessions) != 1 || site.sessions[0].mode != Synced || len(home.sessions) != 1 || home.sessions[0].mode != Local {
		t.Fatalf("sessions %+v, %+v", site.sessions, home.sessions)
	}
	if !tt.HasText("pi@192.168.1.20  本机直连") {
		t.Errorf("the status bar does not say how it connects: %q", tt.Texts())
	}
	snapshot(t, tt, "sessions")
	tt.SetDark(true)
	snapshot(t, tt, "sessions-dark")
	tt.SetDark(false)
	if err := tt.Click("关闭 办公室树莓派"); err != nil {
		t.Fatal(err)
	}
	if len(home.sessions) != 0 || len(site.sessions) != 1 {
		t.Errorf("after closing: %d sessions at home, %d on the site", len(home.sessions), len(site.sessions))
	}
}

func TestMiddleClickClosesTab(t *testing.T) {
	m, st := testModel(t)
	withSites(t, m, st)
	site := m.sites[1]
	m.openSynced(site, site.tree[0].Children[0])
	m.openSynced(site, site.tree[1])
	tt := ui.NewTester(m.View, 1100, 700)
	// The tab of 数据库主库, left of its close button.
	x, ok := tt.Find("关闭 数据库主库")
	if !ok {
		t.Fatalf("no tab: %q", tt.Texts())
	}
	tabX, tabY := x.X-30, x.Y+x.H/2
	middle := func(releaseX, releaseY float32) {
		tt.PressButton(2, tabX, tabY)
		tt.Move(releaseX, releaseY)
		tt.ReleaseButton(2, releaseX, releaseY)
		tt.Frame()
	}

	// Pressed on the tab, let go elsewhere: kept.
	middle(700, 400)
	if len(site.sessions) != 2 {
		t.Fatalf("released away from the tab, it closed: %d left", len(site.sessions))
	}
	middle(tabX, tabY)
	if len(site.sessions) != 1 || site.sessions[0].title != "web-01" {
		t.Errorf("%d sessions, the first %q", len(site.sessions), site.sessions[0].title)
	}
	// A primary click on a tab only shows it.
	x, _ = tt.Find("关闭 web-01")
	tt.ClickAt(x.X-30, x.Y+x.H/2)
	if len(site.sessions) != 1 || site.current != 0 {
		t.Errorf("a click closed the tab: %d left", len(site.sessions))
	}
}

func TestFoldAllGroups(t *testing.T) {
	m, st := testModel(t)
	withSites(t, m, st)
	site := m.sites[1]
	tt := ui.NewTester(m.View, 1100, 700)
	if !tt.HasText("web-01") {
		t.Fatalf("groups start open: %q", tt.Texts())
	}
	if err := tt.Click("全部收起"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("web-01") || site.allOpen() {
		t.Errorf("still open: %q", tt.Texts())
	}
	// One closed, the button opens them all.
	if err := tt.Click("全部展开"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("web-01") || !site.allOpen() {
		t.Errorf("not opened: %q", tt.Texts())
	}
	// No groups, no button.
	m.selectSite(0)
	tt.Frame()
	if _, ok := tt.Find("全部收起"); ok {
		t.Error("a tab without groups shows the button")
	}
}

// fakeStats is a server's status sampled already.
type fakeStats struct{ h []sshconn.Stats }

func (f *fakeStats) History() []sshconn.Stats { return f.h }
func (f *fakeStats) Err() error               { return nil }
func (f *fakeStats) Stop()                    {}

func sampleStats(n int) []sshconn.Stats {
	var h []sshconn.Stats
	start := time.Unix(1_700_000_000, 0)
	for i := range n {
		wave := float64((i*37)%23) / 23
		h = append(h, sshconn.Stats{
			At: start.Add(time.Duration(i) * 2 * time.Second), Host: "web-01", Cores: 4,
			CPU: 0.25, Load: [3]float64{0.52, 0.40, 0.31}, Uptime: 75 * time.Hour,
			MemUsed: 3 << 30, MemTotal: 8 << 30, SwapUsed: 256 << 20, SwapTotal: 1 << 30,
			Disks: []sshconn.Disk{{Mount: "/", Used: 18 << 30, Size: 40 << 30}, {Mount: "/data", Used: 420 << 30, Size: 1000 << 30}},
			RX:    200_000 + 1_800_000*wave + 3000, TX: 80_000 + 400_000*(1-wave) + 1000,
			Ifaces: []sshconn.Iface{
				{Name: "docker0", RX: 3000, TX: 1000},
				{Name: "eth0", RX: 200_000 + 1_800_000*wave, TX: 80_000 + 400_000*(1-wave)},
			},
			Procs: []sshconn.Proc{
				{PID: 812, Name: "nginx", CPU: 42.5, Mem: 120 << 20},
				{PID: 1033, Name: "postgres: checkpointer process with a long name", CPU: 12, Mem: 900 << 20},
				{PID: 77, Name: "node", CPU: 3.2, Mem: 300 << 20},
				{PID: 1, Name: "systemd", CPU: 0, Mem: 12 << 20},
			},
		})
	}
	return h
}

// updatesHere makes the model's updates wait for apply, as for a main
// thread; apply runs them until done reports true.
func updatesHere(t *testing.T, m *Model) (apply func(done func() bool)) {
	updates := make(chan func(), 64)
	m.backend.Update = func(fn func()) { updates <- fn }
	return func(done func() bool) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for !done() {
			select {
			case fn := <-updates:
				fn()
			case <-deadline:
				t.Fatal("timed out")
			}
		}
	}
}

func TestPanelToggles(t *testing.T) {
	m, st := testModel(t)
	withSites(t, m, st)
	site := m.sites[1]
	m.openSynced(site, site.tree[1])
	sess := site.sessions[0]
	tt := ui.NewTester(m.View, 1100, 700)
	// Both show at first, until the connection is up.
	if !sess.showBottom || !sess.showStatus || !tt.HasText("连接成功后显示服务器状态") || !tt.HasText("连接成功后可以浏览服务器上的文件") {
		t.Fatalf("files %v, status %v, texts %q", sess.showBottom, sess.showStatus, tt.Texts())
	}
	// The files below the terminal, the status beside it.
	files, _ := tt.Find("连接成功后可以浏览服务器上的文件")
	status, _ := tt.Find("连接成功后显示服务器状态")
	if files.Y < 300 || status.X < 800 {
		t.Errorf("files at %v, status at %v", files, status)
	}
	tt.Click("文件与快捷指令")
	if sess.showBottom || tt.HasText("连接成功后可以浏览服务器上的文件") {
		t.Errorf("files still shown")
	}
	tt.Click("服务器状态")
	if sess.showStatus || tt.HasText("连接成功后显示服务器状态") {
		t.Errorf("status still shown")
	}
	tt.Click("文件与快捷指令")
	if !sess.showBottom {
		t.Errorf("files not shown again")
	}
	// A local terminal has no status, and its pane below the quick
	// commands alone.
	m.selectSite(0)
	m.newSession()
	tt.Frame()
	if _, ok := tt.Find("服务器状态"); ok || tt.HasText("连接成功后可以浏览服务器上的文件") {
		t.Error("a local terminal shows the panels")
	}
	if _, ok := tt.Find("快捷指令"); !ok {
		t.Error("a local terminal has no quick commands")
	}
}

func TestStatusPanel(t *testing.T) {
	m, st := testModel(t)
	withSites(t, m, st)
	site := m.sites[1]
	m.openSynced(site, site.tree[1])
	sess := site.sessions[0]
	sess.stats = &fakeStats{sampleStats(60)}
	tt := ui.NewTester(m.View, 1200, 760)
	for _, want := range []string{"web-01", "运行 3 天 3 小时", "4 核 · 负载 0.52  0.40  0.31", "CPU", "25%",
		"3.0 GB / 8.0 GB", "交换 256 MB / 1.0 GB", "存储", "18.0 GB / 40.0 GB", "/data", "网络", "最近 3 分钟"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	for _, want := range []string{"进程", "nginx", "812", "42.5%", "120 MB", "900 MB", "全部网卡"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	// One interface: its rates, not every one's.
	sess.iface = "docker0"
	tt.Frame()
	if !tt.HasText("下行 2.9 KB/s") || !tt.HasText("上行 1000 B/s") {
		t.Errorf("docker0's rates are not shown: %q", tt.Texts())
	}
	// Gone, all of them again.
	sess.iface = "eth9"
	tt.Frame()
	if sess.iface != allIfaces {
		t.Errorf("iface %q", sess.iface)
	}
	snapshot(t, tt, "status")
	tt.SetDark(true)
	snapshot(t, tt, "status-dark")
	tt.SetDark(false)

	// Hovering the chart reads a sample.
	r, ok := tt.Find("网络流量图")
	if !ok {
		t.Fatal("no chart")
	}
	tt.Move(r.X+r.W-0.5, r.Y+r.H/2)
	tt.Frame()
	if !tt.HasText("现在 · 下行 " + formatRate(sess.stats.History()[59].RX) + " · 上行 " + formatRate(sess.stats.History()[59].TX)) {
		t.Errorf("no reading at the right edge: %q", tt.Texts())
	}
	tt.Move(r.X+r.W*0.6, r.Y+r.H/2)
	tt.Frame()
	if !strings.Contains(strings.Join(tt.Texts(), " "), "秒前 · 下行") {
		t.Errorf("no reading earlier: %q", tt.Texts())
	}
	snapshot(t, tt, "status-hover")
}

func TestFilesPanel(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "logs"), 0o755)
	os.WriteFile(filepath.Join(dir, "logs", "app.log"), []byte("started"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o644)
	a, b := net.Pipe()
	srv, _ := sftp.NewServer(a, sftp.WithServerWorkingDirectory(dir))
	go srv.Serve()
	client, err := sftp.NewClientPipe(b, b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })

	m, st := testModel(t)
	withSites(t, m, st)
	apply := updatesHere(t, m)
	site := m.sites[1]
	m.openSynced(site, site.tree[1])
	sess := site.sessions[0]
	f := &filesState{fs: sshconn.NewFiles(client), selected: -1}
	sess.files = f
	m.listDir(f, dir)
	apply(func() bool { return !f.loading })
	tt := ui.NewTester(m.View, 1200, 760)
	for _, want := range []string{"logs", "notes.txt", "5 B"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	snapshot(t, tt, "files")

	// Into a directory, and up again.
	f.selected = 0
	m.listDir(f, path.Join(dir, f.entries[0].Name))
	apply(func() bool { return !f.loading })
	tt.Frame()
	if f.dir != path.Join(dir, "logs") || !tt.HasText("app.log") {
		t.Fatalf("dir %q, texts %q", f.dir, tt.Texts())
	}
	tt.Click("上级目录")
	apply(func() bool { return !f.loading })
	if f.dir != dir {
		t.Fatalf("dir %q after going up", f.dir)
	}

	// A new folder, by the prompt.
	tt.Frame()
	if err := tt.Click("新建文件夹"); err != nil {
		t.Fatal(err)
	}
	m.prompt.value = "a/b"
	tt.Frame()
	tt.Click("新建")
	if m.prompt.err == "" || !m.promptOpen {
		t.Errorf("a name with / was taken")
	}
	m.prompt.value = "backup"
	tt.Frame()
	tt.Click("新建")
	apply(func() bool { return !f.loading && len(f.entries) == 3 })
	if fi, err := os.Stat(filepath.Join(dir, "backup")); err != nil || !fi.IsDir() {
		t.Errorf("no folder: %v", err)
	}

	// Up and down.
	local := filepath.Join(t.TempDir(), "deploy.sh")
	os.WriteFile(local, []byte("#!/bin/sh\necho hi\n"), 0o755)
	m.upload(f, []string{local})
	apply(func() bool { return len(f.transfers) == 1 && f.transfers[0].finished && !f.loading })
	if b, err := os.ReadFile(filepath.Join(dir, "deploy.sh")); err != nil || string(b) != "#!/bin/sh\necho hi\n" {
		t.Errorf("uploaded %q, %v", b, err)
	}
	if f.transfers[0].err != "" || !slices.ContainsFunc(f.entries, func(e sshconn.Entry) bool { return e.Name == "deploy.sh" }) {
		t.Errorf("transfer %+v, entries %v", f.transfers[0], f.entries)
	}
	dest := filepath.Join(t.TempDir(), "notes.txt")
	m.download(f, path.Join(dir, "notes.txt"), dest)
	apply(func() bool { return f.transfers[1].finished })
	if b, _ := os.ReadFile(dest); string(b) != "hello" {
		t.Errorf("downloaded %q", b)
	}
	tt.Frame()
	if !tt.HasText("传输") || !tt.HasText("deploy.sh") {
		t.Errorf("no transfers: %q", tt.Texts())
	}
	snapshot(t, tt, "files-transfers")

	// Removed, with what is in it.
	m.fileOp(f, func() error { return f.fs.Remove(path.Join(dir, "logs")) })
	apply(func() bool { return !f.loading && len(f.entries) == 3 })
	if _, err := os.Stat(filepath.Join(dir, "logs")); !os.IsNotExist(err) {
		t.Errorf("logs is still there: %v", err)
	}
}

func TestFormat(t *testing.T) {
	for _, c := range []struct {
		n    uint64
		want string
	}{{0, "0 B"}, {1023, "1023 B"}, {1536, "1.5 KB"}, {3 << 30, "3.0 GB"}, {420 << 30, "420 GB"}} {
		if got := formatBytes(c.n); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
	for _, c := range []struct{ v, want float64 }{{900, 1024}, {1500, 2048}, {3 << 20, 5 << 20}, {600 << 20, 1 << 30}} {
		if got := niceCeil(c.v); got != c.want {
			t.Errorf("niceCeil(%v) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestSyncedServer(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "admin" || p != "secret" || r.URL.Path != "/api/v1/servers/s3/credential" {
			w.WriteHeader(404)
			io.WriteString(w, `{"error":"server not found"}`)
			return
		}
		io.WriteString(w, `{"host":"db.internal","port":2222,"username":"ubuntu","auth_type":"password","password":"pw"}`)
	}))
	defer site.Close()
	m, st := testModel(t)
	withSites(t, m, st)
	s := m.sites[1]
	s.site.URL = site.URL
	s.client, _ = ternssh.New(site.URL, m.authOf(s.site))
	var opts sshconn.LocalOptions
	m.backend.Dial = func(o sshconn.LocalOptions, _ sshconn.Events) Conn { opts = o; return &fakeConn{} }

	m.openSynced(s, s.tree[1])
	if opts.Resolve == nil {
		t.Fatal("a server of the site does not ask it for the credential")
	}
	if err := opts.Resolve(context.Background(), &opts); err != nil {
		t.Fatal(err)
	}
	if opts.Host != "db.internal" || opts.Port != 2222 || opts.Username != "ubuntu" || opts.Password != "pw" || opts.PrivateKey != "" {
		t.Errorf("options %+v", opts)
	}
	opts.OnHostKey("SHA256:abc")
	if got := st.SiteHostKey("a", "s3"); got != "SHA256:abc" {
		t.Errorf("host key %q", got)
	}
	m.reconnect(s, 0)
	if opts.HostKey != "SHA256:abc" {
		t.Errorf("the host key is not checked when connecting again: %q", opts.HostKey)
	}
	tt := ui.NewTester(m.View, 1100, 700)
	if !tt.HasText("ubuntu@db.internal:2222  本机直连 · 凭据来自 " + hostOf(site.URL)) {
		t.Errorf("the status bar does not say how it connects: %q", tt.Texts())
	}

	m.openSynced(s, ternssh.Node{ID: "gone", Name: "gone"})
	if err := opts.Resolve(context.Background(), &opts); err == nil || !strings.Contains(err.Error(), "server not found") {
		t.Errorf("error %v", err)
	}
}

func TestAddLocalServer(t *testing.T) {
	m, st := testModel(t)
	withSites(t, m, st)
	m.active = 0
	tt := ui.NewTester(m.View, 1100, 760)
	if err := tt.Click("添加服务器或终端"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("添加到本机") {
		t.Errorf("the dialog does not say where the server is kept: %q", tt.Texts())
	}
	snapshot(t, tt, "add-server")
	f := &m.serverForm
	f.name, f.host, f.password = "跳板机", "10.1.1.1", "pw"
	tt.Frame()
	if err := tt.Click("添加"); err != nil {
		t.Fatal(err)
	}
	if m.serverFormOpen || f.err != "" {
		t.Fatalf("the dialog stays open: %q", f.err)
	}
	servers := st.Servers()
	if len(servers) != 2 || servers[1].Name != "跳板机" || servers[1].Username != "root" {
		t.Fatalf("servers %+v", servers)
	}
	cred, err := st.ServerCredential(servers[1].ID)
	if err != nil || cred.Password != "pw" {
		t.Errorf("credential %+v, %v", cred, err)
	}
}

func TestAddLocalTerminal(t *testing.T) {
	m, st := testModel(t)
	apply := updatesHere(t, m)
	userHome, _ := os.UserHomeDir()
	picked := filepath.Join(userHome, "src")
	var asked string
	m.backend.PickDir = func(start string) (string, error) {
		asked = start
		return picked, nil
	}
	m.Start()
	tt := ui.NewTester(m.View, 1100, 700)
	if err := tt.Click("添加服务器或终端"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("本地终端"); err != nil {
		t.Fatal(err)
	}
	snapshot(t, tt, "add-terminal")
	f := &m.serverForm
	// The directory picked, written from the home directory; the picker
	// starts in the one written.
	tt.Click("选择…")
	apply(func() bool { return f.dir != "" })
	if f.dir != "~/src" || asked != userHome {
		t.Fatalf("dir %q, picker started in %q", f.dir, asked)
	}
	picked = "/opt"
	tt.Click("选择…")
	apply(func() bool { return f.dir != "~/src" })
	if f.dir != "/opt" || asked != filepath.Join(userHome, "src") {
		t.Fatalf("dir %q, picker started in %q", f.dir, asked)
	}
	f.dir = filepath.Join(t.TempDir(), "nope")
	tt.Frame()
	if err := tt.Click("添加"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.err, "目录不存在") {
		t.Fatalf("error %q", f.err)
	}
	f.dir = "~"
	tt.Frame()
	tt.Click("添加")
	terms := st.Terminals()
	if m.serverFormOpen || len(terms) != 1 || terms[0].Name != "本地终端" || terms[0].Dir != "~" {
		t.Fatalf("open %v, terminals %+v, error %q", m.serverFormOpen, terms, f.err)
	}
	if !tt.HasText("本地终端") || !tt.HasText("~") {
		t.Errorf("the terminal is not listed: %q", tt.Texts())
	}

	home := m.sites[0]
	m.openShell(home, terms[0])
	tt.Frame()
	if len(home.sessions) != 1 || home.sessions[0].mode != Shell || !tt.HasText("运行中") || !tt.HasText("~  本地终端") {
		t.Errorf("sessions %+v, texts %q", home.sessions, tt.Texts())
	}
	snapshot(t, tt, "terminal-list")
}

func TestNewSessionShortcut(t *testing.T) {
	m, st := testModel(t)
	withSites(t, m, st)
	home, site := m.sites[0], m.sites[1]
	tt := ui.NewTester(m.View, 1100, 700)

	// On a site, the server chosen in the sidebar; none chosen, none.
	tt.Key(newSessionMods, ui.KeyT)
	if len(site.sessions) != 0 || m.toast == "" {
		t.Fatalf("%d sessions, notice %q", len(site.sessions), m.toast)
	}
	site.selected = "r:s3"
	tt.Key(newSessionMods, ui.KeyT)
	if len(site.sessions) != 1 || site.sessions[0].node.ID != "s3" {
		t.Fatalf("sessions %+v", site.sessions)
	}
	// With a terminal open, another on its server, though the sidebar
	// chose another.
	site.selected = "r:s1"
	tt.Key(newSessionMods, ui.KeyT)
	if len(site.sessions) != 2 || site.sessions[1].node.ID != "s3" || site.current != 1 {
		t.Fatalf("sessions %+v, current %d", site.sessions, site.current)
	}
	// Held, it opens one.
	tt.KeyRepeat(newSessionMods, ui.KeyT)
	if len(site.sessions) != 2 {
		t.Errorf("the key's repeat opened %d", len(site.sessions)-2)
	}

	// At home with nothing chosen, a local terminal, made the first time.
	m.selectSite(0)
	tt.Frame()
	if !tt.HasText("按 " + newSessionLabel + " 打开本地终端") {
		t.Errorf("no hint of the shortcut: %q", tt.Texts())
	}
	tt.Key(newSessionMods, ui.KeyT)
	terms := st.Terminals()
	if len(terms) != 1 || len(home.sessions) != 1 || home.sessions[0].mode != Shell || home.selected != "t:"+terms[0].ID {
		t.Fatalf("terminals %+v, sessions %+v", terms, home.sessions)
	}
	if err := tt.Click("新建会话"); err != nil {
		t.Fatal(err)
	}
	if len(st.Terminals()) != 1 || len(home.sessions) != 2 || home.sessions[1].localID != terms[0].ID {
		t.Errorf("terminals %+v, sessions %+v", st.Terminals(), home.sessions)
	}

	// Not behind a dialog.
	m.openSiteForm(nil)
	tt.Frame()
	tt.Key(newSessionMods, ui.KeyT)
	if len(home.sessions) != 2 {
		t.Errorf("a dialog let the shortcut through: %d sessions", len(home.sessions))
	}
}

// drag drags the element showing from onto the one showing to.
func drag(t *testing.T, tt *ui.Tester, from, to string) {
	t.Helper()
	a, ok := tt.Find(from)
	if !ok {
		t.Fatalf("no %q to drag", from)
	}
	b, ok := tt.Find(to)
	if !ok {
		t.Fatalf("no %q to drop on", to)
	}
	dragTo(tt, a, b.X+b.W/2, b.Y+b.H/2)
}

func dragTo(tt *ui.Tester, a ui.Rect, x, y float32) {
	ax, ay := a.X+a.W/2, a.Y+a.H/2
	tt.Press(ax, ay)
	for i := float32(1); i <= 8; i++ {
		tt.Move(ax+(x-ax)*i/8, ay+(y-ay)*i/8)
	}
	tt.Release(x, y)
	tt.Frame()
}

func TestHomeGroups(t *testing.T) {
	m, st := testModel(t)
	withSites(t, m, st)
	m.selectSite(0)
	home := m.sites[0]
	st.PutTerminal(store.Terminal{ID: "t1", Name: "构建机"})
	m.refresh(home)
	tt := ui.NewTester(m.View, 1100, 700)

	if err := tt.Click("新建分组"); err != nil {
		t.Fatal(err)
	}
	m.groupForm.name = "实验室"
	tt.Frame()
	if err := tt.Click("新建"); err != nil {
		t.Fatal(err)
	}
	groups := st.Groups()
	if m.groupFormOpen || len(groups) != 1 || groups[0].Name != "实验室" {
		t.Fatalf("open %v, groups %+v, error %q", m.groupFormOpen, groups, m.groupForm.err)
	}
	g := groups[0].ID

	drag(t, tt, "办公室树莓派", "实验室")
	drag(t, tt, "构建机", "实验室")
	if st.Servers()[0].GroupID != g || st.Terminals()[0].GroupID != g {
		t.Fatalf("servers %+v, terminals %+v", st.Servers(), st.Terminals())
	}
	if r, _ := tt.Find("实验室"); !strings.Contains(strings.Join(tt.Texts(), " "), "2") || r.W == 0 {
		t.Errorf("the group does not count what is in it: %q", tt.Texts())
	}

	// A subgroup; its parent cannot go into it.
	m.openGroupForm(home, "", g, "")
	m.groupForm.name = "树莓派"
	m.saveGroup()
	sub := st.Groups()[1]
	if sub.ParentID != g {
		t.Fatalf("subgroup %+v", sub)
	}
	tt.Frame()
	snapshot(t, tt, "groups")
	drag(t, tt, "实验室", "树莓派")
	if st.Groups()[0].ParentID != "" {
		t.Error("a group went into its own subgroup")
	}
	drag(t, tt, "办公室树莓派", "树莓派")
	if st.Servers()[0].GroupID != sub.ID {
		t.Errorf("server in %q, not the subgroup", st.Servers()[0].GroupID)
	}

	// Editing keeps the group.
	m.editLocal(home, st.Servers()[0])
	m.serverForm.name = "树莓派 4"
	m.saveServer()
	if srv := st.Servers()[0]; srv.Name != "树莓派 4" || srv.GroupID != sub.ID {
		t.Errorf("edited %+v", srv)
	}

	// Dropped below the list, a row goes to the top.
	tt.Frame()
	a, _ := tt.Find("构建机")
	dragTo(tt, a, 120, 600)
	if st.Terminals()[0].GroupID != "" {
		t.Errorf("the terminal stayed in %q", st.Terminals()[0].GroupID)
	}

	// Deleting a group deletes its subgroups, not what is in them.
	m.deleteGroup(home, g, "实验室")
	if len(st.Groups()) != 0 || st.Servers()[0].GroupID != "" || len(home.groups) != 0 {
		t.Errorf("groups %+v, servers %+v", st.Groups(), st.Servers())
	}
}

func TestReorderHome(t *testing.T) {
	m, st := testModel(t)
	withSites(t, m, st)
	m.selectSite(0)
	home := m.sites[0]
	st.PutTerminal(store.Terminal{ID: "t1", Name: "构建机"})
	m.refresh(home)
	order := func() string {
		var ids []string
		for _, it := range store.Children(st.Groups(), st.Terminals(), st.Servers(), "") {
			ids = append(ids, it.ID)
		}
		return strings.Join(ids, " ")
	}
	tt := ui.NewTester(m.View, 1100, 700)
	if got := order(); got != "l1 t1" {
		t.Fatalf("order %q", got)
	}
	// Over the top half of a row, before it.
	a, _ := tt.Find("构建机")
	b, _ := tt.Find("办公室树莓派")
	dragTo(tt, a, b.X+b.W/2, b.Y+1)
	if got := order(); got != "t1 l1" {
		t.Fatalf("order %q after dropping before", got)
	}
	// Over the bottom half, after it.
	a, _ = tt.Find("构建机")
	b, _ = tt.Find("pi@192.168.1.20")
	dragTo(tt, a, b.X+b.W/2, b.Y+b.H-1)
	if got := order(); got != "l1 t1" {
		t.Errorf("order %q after dropping after", got)
	}
	// What is shown follows.
	texts := strings.Join(tt.Texts(), "|")
	if strings.Index(texts, "办公室树莓派") > strings.Index(texts, "构建机") {
		t.Errorf("the sidebar shows another order: %q", texts)
	}
}

func TestSiteGroups(t *testing.T) {
	var requests []string
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/api/v1/servers/move":
			requests = append(requests, string(b))
			if strings.Contains(string(b), `"s2"`) {
				io.WriteString(w, `{"tree":[{"type":"group","id":"g1","name":"Web 集群","children":[`+
					`{"type":"server","id":"s2","name":"web-02","host":"10.0.0.12","port":22,"username":"root"},`+
					`{"type":"server","id":"s1","name":"web-01","host":"10.0.0.11","port":22,"username":"root"}]},`+
					`{"type":"server","id":"s3","name":"数据库主库","host":"db.internal","port":2222,"username":"ubuntu"}]}`)
				return
			}
			io.WriteString(w, `{"tree":[{"type":"group","id":"g1","name":"Web 集群","children":[{"type":"server","id":"s3","name":"数据库主库","host":"db.internal","port":2222,"username":"ubuntu"}]}]}`)
		case "/api/v1/servers/groups":
			requests = append(requests, string(b))
			io.WriteString(w, `{"group":{"id":"g2","name":"新组"}}`)
		case "/api/v1/me":
			io.WriteString(w, `{"user":{"id":"u"},"authMode":"basic"}`)
		case "/api/v1/servers/tree":
			io.WriteString(w, `{"tree":[]}`)
		}
	}))
	defer site.Close()
	m, st := testModel(t)
	withSites(t, m, st)
	s := m.sites[1]
	s.client, _ = ternssh.New(site.URL, m.authOf(s.site))
	updates := make(chan func(), 8)
	m.backend.Update = func(fn func()) { updates <- fn }
	apply := func() {
		t.Helper()
		select {
		case fn := <-updates:
			fn()
		case <-time.After(5 * time.Second):
			t.Fatal("no answer from the site")
		}
	}
	tt := ui.NewTester(m.View, 1100, 700)

	// Before another server of its group.
	b, _ := tt.Find("web-01")
	a, _ := tt.Find("web-02")
	dragTo(tt, a, b.X+b.W/2, b.Y+1)
	apply()
	if len(requests) != 1 || requests[0] != `{"id":"s2","index":0,"parentId":"g1","type":"server"}` {
		t.Fatalf("requests %q", requests)
	}
	if g, _, _ := findNode(s.tree, "", "group", "g1"); g.Children[0].ID != "s2" {
		t.Errorf("the tree is not the site's after: %+v", s.tree)
	}
	tt.Frame()
	// Where it is already: nothing to do.
	drag(t, tt, "web-02", "web-02")
	// Into a group, last.
	drag(t, tt, "数据库主库", "Web 集群")
	apply()
	if len(requests) != 2 || requests[1] != `{"id":"s3","index":2,"parentId":"g1","type":"server"}` {
		t.Fatalf("requests %q", requests)
	}
	if n, parent, _ := findNode(s.tree, "", "server", "s3"); parent != "g1" || n.Name != "数据库主库" {
		t.Errorf("the tree is not the site's after: %+v", s.tree)
	}

	m.openGroupForm(s, "", "g1", "")
	m.groupForm.name = "新组"
	m.saveGroup()
	apply()
	if len(requests) != 3 || requests[2] != `{"name":"新组","parent_id":"g1"}` || m.groupFormOpen {
		t.Errorf("requests %q, error %q", requests, m.groupForm.err)
	}
}

func TestEditSyncedServer(t *testing.T) {
	bodies := make(chan map[string]any, 4)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/servers/s1":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			bodies <- body
			io.WriteString(w, `{"server":{}}`)
		case r.URL.Path == "/api/v1/me":
			io.WriteString(w, `{"user":{"id":"u"},"authMode":"basic"}`)
		case r.URL.Path == "/api/v1/servers/tree":
			io.WriteString(w, `{"tree":[]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer site.Close()
	m, st := testModel(t)
	withSites(t, m, st)
	s := m.sites[1]
	s.client, _ = ternssh.New(site.URL, m.authOf(s.site))
	// Updates run here, as on the main thread.
	updates := make(chan func(), 8)
	m.backend.Update = func(fn func()) { updates <- fn }
	tt := ui.NewTester(m.View, 1100, 760)

	m.editSynced(s, s.tree[0].Children[0])
	f := &m.serverForm
	if f.group != "Web 集群" || f.host != "10.0.0.11" || f.authType != 0 {
		t.Fatalf("form %+v", f)
	}
	tt.Frame()
	if !tt.HasText("编辑服务器") || !tt.HasText("保存到站点「生产环境」") {
		t.Errorf("texts %q", tt.Texts())
	}
	snapshot(t, tt, "edit-site-server")

	// Changing to a key needs the key.
	f.authType = 1
	m.saveServer()
	if f.err != "请输入私钥" {
		t.Fatalf("error %q", f.err)
	}
	f.authType, f.name, f.err = 0, "web-01 新", ""
	m.saveServer()
	select {
	case body := <-bodies:
		if body["name"] != "web-01 新" || body["group_id"] != "g1" || body["auth_type"] != "password" {
			t.Errorf("body %v", body)
		}
		if _, ok := body["credential"]; ok {
			t.Errorf("an empty password replaced the one kept: %v", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("the site was not told: %q", f.err)
	}
	select {
	case fn := <-updates:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("no answer")
	}
	if m.serverFormOpen || f.err != "" {
		t.Errorf("open %v, error %q", m.serverFormOpen, f.err)
	}
}

// snapshot writes the last frame over a wallpaper, as the window's
// material would show it, to $SNAPSHOT_DIR.
func snapshot(t *testing.T, tt *ui.Tester, name string) {
	dir := os.Getenv("SNAPSHOT_DIR")
	if dir == "" {
		return
	}
	tt.SetVibrancy(true)
	tt.Frame()
	img := tt.Image()
	b := img.Bounds()
	out := image.NewRGBA(b)
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			fx, fy := float64(x)/float64(b.Dx()), float64(y)/float64(b.Dy())
			// A wallpaper under macOS's under-window material, which
			// mostly veils it.
			r, g, bl := 90+120*fx, 120+60*fy, 200-80*fx
			veil, base := 0.62, 236.0
			if strings.HasSuffix(name, "-dark") {
				veil, base = 0.72, 30
			}
			out.Set(x, y, color.RGBA{uint8(r*(1-veil) + base*veil), uint8(g*(1-veil) + base*veil), uint8(bl*(1-veil) + base*veil), 255})
		}
	}
	draw.Draw(out, b, img, b.Min, draw.Over)
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, out)
	tt.SetVibrancy(false)
}

// scriptConn is a connection whose shell prints out, then waits.
type scriptConn struct {
	fakeConn
	out []byte
}

func (s *scriptConn) Read(p []byte) (int, error) {
	if len(s.out) == 0 {
		select {}
	}
	n := copy(p, s.out)
	s.out = s.out[n:]
	return n, nil
}

func TestTerminalSnapshot(t *testing.T) {
	if os.Getenv("SNAPSHOT_DIR") == "" {
		t.Skip("renders a real terminal for snapshots")
	}
	m, st := testModel(t)
	withSites(t, m, st)
	m.active = 0
	m.backend.NewTerminal = func(conn Conn) (*terminal.Terminal, error) {
		return terminal.New(terminal.Options{Conn: conn, Transparent: true, Padding: terminal.Padding{X: 10, Y: 8}})
	}
	m.backend.Dial = func(sshconn.LocalOptions, sshconn.Events) Conn {
		return &scriptConn{fakeConn: fakeConn{status: sshconn.StatusReady}, out: []byte(
			"\x1b[2m正在从本机连接 pi@192.168.1.20:22…\x1b[0m\r\n" +
				"Linux raspberrypi 6.6.31 aarch64\r\n\r\n" +
				"\x1b[1;32mpi@raspberrypi\x1b[0m:\x1b[1;34m~\x1b[0m$ ls -la\r\n" +
				"drwxr-xr-x 5 pi pi 4096 Nov 15 10:02 \x1b[1;34m.\x1b[0m\r\n" +
				"-rw-r--r-- 1 pi pi  220 Nov 15 10:02 .bashrc\r\n" +
				"-rwxr-xr-x 1 pi pi 1834 Nov 15 10:02 \x1b[1;32mdeploy.sh\x1b[0m\r\n" +
				"\x1b[1;32mpi@raspberrypi\x1b[0m:\x1b[1;34m~\x1b[0m$ ")}
	}
	s := m.sites[0]
	m.openLocal(s, s.local[0])
	s.sessions[0].status = sshconn.StatusReady
	tt := ui.NewTester(m.View, 1100, 700)
	time.Sleep(500 * time.Millisecond)
	tt.Frame()
	snapshot(t, tt, "terminal")
	tt.SetDark(true)
	time.Sleep(200 * time.Millisecond)
	tt.Frame()
	snapshot(t, tt, "terminal-dark")
}
