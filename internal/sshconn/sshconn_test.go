package sshconn

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"

	"golang.org/x/crypto/ssh"
)

// readUntil reads conn until its output holds want.
func readUntil(t *testing.T, r io.Reader, want string) string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			b.Write(buf[:n])
			if strings.Contains(b.String(), want) || err != nil {
				got <- b.String()
				return
			}
		}
	}()
	select {
	case s := <-got:
		if !strings.Contains(s, want) {
			t.Fatalf("output %q, want %q", s, want)
		}
		return s
	case <-time.After(10 * time.Second):
		t.Fatalf("no %q in time", want)
	}
	return ""
}

// sshServer is an SSH server echoing its shell's input, which accepts the
// password "pw" and reports the size of its pseudo-terminal. Commands it
// runs print a Linux server's status, its counters going up each time,
// and its SFTP serves a directory of its own.
func sshServer(t *testing.T) (host string, port int) {
	host, port, _ = sshServerDir(t)
	return host, port
}

func sshServerDir(t *testing.T) (host string, port int, dir string) {
	t.Helper()
	dir = t.TempDir()
	var runs atomic.Int64
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	cfg := &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
		if string(pw) == "pw" {
			return nil, nil
		}
		return nil, fmt.Errorf("denied")
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nch := range chans {
					ch, reqs, _ := nch.Accept()
					go func() {
						for req := range reqs {
							switch req.Type {
							case "pty-req", "shell":
								req.Reply(true, nil)
								if req.Type == "shell" {
									io.WriteString(ch, "welcome\r\n")
									go func() {
										r := bufio.NewReader(ch)
										for {
											line, err := r.ReadString('\n')
											if err != nil {
												return
											}
											if strings.TrimSpace(line) == "exit" {
												ch.SendRequest("exit-status", false, ssh.Marshal(struct{ C uint32 }{0}))
												ch.Close()
												return
											}
											io.WriteString(ch, "echo:"+line)
										}
									}()
								}
							case "exec":
								req.Reply(true, nil)
								n := runs.Add(1)
								io.WriteString(ch, statsFixture(n))
								ch.SendRequest("exit-status", false, ssh.Marshal(struct{ C uint32 }{0}))
								ch.Close()
							case "subsystem":
								req.Reply(true, nil)
								srv, _ := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(dir))
								go func() { srv.Serve(); ch.Close() }()
							case "window-change":
								var w struct{ Cols, Rows, W, H uint32 }
								ssh.Unmarshal(req.Payload, &w)
								fmt.Fprintf(ch, "size:%dx%d\r\n", w.Cols, w.Rows)
							default:
								req.Reply(false, nil)
							}
						}
					}()
				}
			}()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port, dir
}

// statsFixture is what statsCommand prints on a Linux server at its nth
// run: each run, 100 more jiffies, 25 busy, of which nginx used 20 and
// "my (app) worker" 4; 1 MB more received and 0.5 MB more sent on eth0,
// 1000 bytes more each way on docker0.
func statsFixture(n int64) string {
	busy, idle := 1000+25*n, 5000+75*n
	return fmt.Sprintf(`@@host web-01
@@stat
cpu  %d 0 0 %d 0 0 0 0 0 0
cpu0 1 0 0 1 0 0 0 0 0 0
cpu1 1 0 0 1 0 0 0 0 0 0
intr 1
@@meminfo
MemTotal:        2048000 kB
MemFree:          100000 kB
MemAvailable:     512000 kB
SwapTotal:       1024000 kB
SwapFree:         768000 kB
@@net/dev
Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 999999 10 0 0 0 0 0 0 999999 10 0 0 0 0 0 0
  eth0: %d 10 0 0 0 0 0 0 %d 10 0 0 0 0 0 0
docker0: %d 1 0 0 0 0 0 0 %d 1 0 0 0 0 0 0
@@loadavg
0.52 0.40 0.31 1/123 4567
@@uptime
266400.50 500000.00
@@pagesize
4096
@@procs
1 (systemd) S 0 1 1 0 -1 4194560 1 1 0 0 50 20 0 0 20 0 1 0 1 1000 2560 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0
812 (nginx) S 1 812 812 0 -1 4194624 1 1 0 0 %d %d 0 0 20 0 1 0 2 5000 25600 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 1 0 0 0 0 0
900 (my (app) worker) R 1 900 900 0 -1 4194624 1 1 0 0 %d 0 0 0 20 0 1 0 3 9000 51200 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 1 0 0 0 0 0
@@df
Filesystem     1024-blocks    Used Available Capacity Mounted on
/dev/vda1         41152736 8388608  30651220      22%% /
tmpfs              1015000       0   1015000       0%% /dev/shm
/dev/vdb1        103081248 51540624 46297416      53%% /data/my files
/dev/vda1         41152736 8388608  30651220      22%% /var/lib/docker/overlay2/x
`, busy, idle, 1000000*n, 500000*n, 1000*n, 1000*n, 15*n, 5*n, 4*n)
}

func TestParseSample(t *testing.T) {
	a, err := parseSample(statsFixture(1), time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := parseSample(statsFixture(3), time.Unix(104, 0))
	st := stats(a, b)
	if st.Host != "web-01" || st.Cores != 2 || st.Load != [3]float64{0.52, 0.40, 0.31} || st.Uptime.Hours() < 73 {
		t.Errorf("stats %+v", st)
	}
	if st.CPU != 0.25 {
		t.Errorf("cpu %v, want 0.25", st.CPU)
	}
	if st.MemTotal != 2048000*1024 || st.MemUsed != (2048000-512000)*1024 || st.SwapUsed != 256000*1024 {
		t.Errorf("memory %d/%d, swap %d", st.MemUsed, st.MemTotal, st.SwapUsed)
	}
	if st.RX != 500500 || st.TX != 250500 {
		t.Errorf("network ↓%v ↑%v, want 500500 and 250500 bytes a second", st.RX, st.TX)
	}
	// Two cores: 20 of 100 jiffies is 40% of one, as top shows it.
	if len(st.Procs) != 3 || st.Procs[0].Name != "nginx" || st.Procs[0].CPU != 40 || st.Procs[0].Mem != 25600*4096 ||
		st.Procs[1].Name != "my (app) worker" || st.Procs[1].CPU != 8 || st.Procs[2].Name != "systemd" || st.Procs[2].CPU != 0 {
		t.Errorf("procs %+v", st.Procs)
	}
	if len(st.Ifaces) != 2 || st.Ifaces[0] != (Iface{"docker0", 500, 500}) || st.Ifaces[1] != (Iface{"eth0", 500000, 250000}) {
		t.Errorf("interfaces %+v", st.Ifaces)
	}
	if rx, tx := st.Net("docker0"); rx != 500 || tx != 500 {
		t.Errorf("docker0 %v %v", rx, tx)
	}
	if rx, _ := st.Net(""); rx != 500500 {
		t.Errorf("all %v", rx)
	}
	if len(st.Disks) != 2 || st.Disks[0].Mount != "/" || st.Disks[1].Mount != "/data/my files" || st.Disks[1].Used != 51540624*1024 {
		t.Errorf("disks %+v", st.Disks)
	}
	// Counters that went back give no rate.
	if st := stats(b, a); st.RX != 0 || st.CPU != 0 {
		t.Errorf("backwards %+v", st)
	}
	if _, err := parseSample("@@host mac\n@@stat\n", time.Now()); err != errNotLinux {
		t.Errorf("error %v", err)
	}
}

// dialReady connects to the test server and waits for its shell.
func dialReady(t *testing.T, host string, port int) *Local {
	t.Helper()
	l := DialLocal(LocalOptions{Host: host, Port: port, Username: "u", Password: "pw"}, Events{})
	t.Cleanup(func() { l.Close() })
	readUntil(t, l, "welcome")
	if l.Client() == nil {
		t.Fatal("no client once ready")
	}
	return l
}

func TestMonitor(t *testing.T) {
	host, port := sshServer(t)
	l := dialReady(t, host, port)
	updates := make(chan struct{}, 10)
	m := StartMonitor(l.Client(), 20*time.Millisecond, func() { updates <- struct{}{} })
	defer m.Stop()
	for range 3 {
		select {
		case <-updates:
		case <-time.After(5 * time.Second):
			t.Fatalf("no sample: %v", m.Err())
		}
	}
	h := m.History()
	if len(h) < 2 || h[0].Host != "web-01" || h[0].CPU != 0.25 || h[0].RX <= 0 {
		t.Fatalf("history %+v, error %v", h, m.Err())
	}
}

func TestFiles(t *testing.T) {
	host, port, dir := sshServerDir(t)
	l := dialReady(t, host, port)
	f, err := OpenFiles(l.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	home, err := f.Home()
	if err != nil || home != dir {
		t.Fatalf("home %q, %v; want %q", home, err, dir)
	}
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("bee"), 0o644)
	if err := f.Mkdir(path.Join(home, "zeta")); err != nil {
		t.Fatal(err)
	}

	local := filepath.Join(t.TempDir(), "a.txt")
	os.WriteFile(local, bytes.Repeat([]byte("a"), 100000), 0o644)
	var done, total int64
	to, err := f.Upload(local, home, func(d, n int64) { done, total = d, n })
	if err != nil || to != path.Join(home, "a.txt") || done != 100000 || total != 100000 {
		t.Fatalf("upload to %q: %v, %d/%d", to, err, done, total)
	}
	entries, err := f.List(home)
	var names []string
	for _, e := range entries {
		names = append(names, fmt.Sprint(e.Name, map[bool]string{true: "/"}[e.Dir]))
	}
	if err != nil || strings.Join(names, " ") != "zeta/ a.txt b.txt" {
		t.Fatalf("list %q, %v", names, err)
	}

	back := filepath.Join(t.TempDir(), "b.txt")
	if err := f.Download(path.Join(home, "b.txt"), back, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(back); string(b) != "bee" {
		t.Errorf("downloaded %q", b)
	}
	if err := f.Rename(path.Join(home, "b.txt"), path.Join(home, "c.txt")); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "zeta", "inner"), []byte("x"), 0o644)
	if err := f.Remove(path.Join(home, "zeta")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "zeta")); !os.IsNotExist(err) {
		t.Errorf("the directory is still there: %v", err)
	}
}

func TestLocal(t *testing.T) {
	host, port := sshServer(t)
	var fp string
	statuses := make(chan Status, 8)
	l := DialLocal(LocalOptions{Host: host, Port: port, Username: "u", Password: "pw", OnHostKey: func(f string) { fp = f }},
		Events{OnStatus: func(s Status, _ error) { statuses <- s }})
	defer l.Close()
	readUntil(t, l, "welcome")
	if fp == "" {
		t.Error("the host key was not reported")
	}
	l.Resize(120, 40)
	readUntil(t, l, "size:120x40")
	l.Write([]byte("hi\n"))
	readUntil(t, l, "echo:hi")
	l.Write([]byte("exit\n"))
	readUntil(t, l, "连接已关闭")
	if _, err := l.Read(make([]byte, 10)); err != io.EOF {
		t.Errorf("read after the end: %v", err)
	}
	if l.Status() != StatusClosed {
		t.Errorf("status %v", l.Status())
	}

	// Another key than the one remembered is refused.
	l2 := DialLocal(LocalOptions{Host: host, Port: port, Username: "u", Password: "pw", HostKey: "SHA256:other"}, Events{})
	readUntil(t, l2, "主机密钥与上次不符")
	if l2.Status() != StatusError {
		t.Errorf("status %v", l2.Status())
	}

	l3 := DialLocal(LocalOptions{Host: host, Port: port, Username: "u", Password: "wrong"}, Events{})
	readUntil(t, l3, "连接失败")
}

func TestResolve(t *testing.T) {
	host, port := sshServer(t)
	l := DialLocal(LocalOptions{Resolve: func(_ context.Context, o *LocalOptions) error {
		o.Host, o.Port, o.Username, o.Password = host, port, "u", "pw"
		return nil
	}}, Events{})
	defer l.Close()
	out := readUntil(t, l, "welcome")
	if !strings.Contains(out, "正在从站点获取连接信息") || !strings.Contains(out, "u@"+host) {
		t.Errorf("output %q", out)
	}

	l2 := DialLocal(LocalOptions{Resolve: func(context.Context, *LocalOptions) error {
		return errors.New("认证失败")
	}}, Events{})
	readUntil(t, l2, "认证失败")
	if l2.Status() != StatusError {
		t.Errorf("status %v", l2.Status())
	}
}
