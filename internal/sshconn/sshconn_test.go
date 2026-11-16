package sshconn

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

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
// password "pw" and reports the size of its pseudo-terminal.
func sshServer(t *testing.T) (host string, port int) {
	t.Helper()
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
	return "127.0.0.1", addr.Port
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
