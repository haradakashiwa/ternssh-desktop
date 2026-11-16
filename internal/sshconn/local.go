package sshconn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"ternssh-desktop/locales"
)

// LocalOptions is a server this machine connects to itself.
type LocalOptions struct {
	Host     string
	Port     int
	Username string
	// Password, or PrivateKey (PEM or OpenSSH) with its Passphrase when
	// it is encrypted.
	Password   string
	PrivateKey string
	Passphrase string
	// HostKey is the SHA-256 fingerprint of the server's key seen before,
	// empty the first time. A server showing another key is refused.
	HostKey string
	// OnHostKey is told the fingerprint of a server seen the first time,
	// to remember.
	OnHostKey func(fingerprint string)
	// Resolve, when set, completes the options before connecting, as by
	// asking the ternssh site that keeps the server for its credential.
	Resolve func(ctx context.Context, opts *LocalOptions) error
}

// Local is a terminal on a server this machine connects to with SSH.
type Local struct {
	*stream
	opts LocalOptions

	mu      sync.Mutex
	client  *ssh.Client
	session *ssh.Session
	stdin   interface{ Write([]byte) (int, error) }
	size    [2]int
}

// DialLocal connects to the server. It returns at once: the connection is
// made in the background, and what happens shows in the terminal.
func DialLocal(opts LocalOptions, events Events) *Local {
	l := &Local{stream: newStream(events), opts: opts, size: [2]int{80, 24}}
	go l.run()
	return l
}

func (l *Local) authMethods() ([]ssh.AuthMethod, error) {
	if l.opts.PrivateKey != "" {
		var signer ssh.Signer
		var err error
		if l.opts.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(l.opts.PrivateKey), []byte(l.opts.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(l.opts.PrivateKey))
		}
		if err != nil {
			var missing *ssh.PassphraseMissingError
			if errors.As(err, &missing) {
				return nil, errors.New(locales.SshKeyEncrypted())
			}
			return nil, fmt.Errorf("%s: %w", locales.SshKeyUnreadable(), err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	}
	pw := l.opts.Password
	return []ssh.AuthMethod{
		ssh.Password(pw),
		ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range answers {
				answers[i] = pw
			}
			return answers, nil
		}),
	}, nil
}

func (l *Local) hostKey(_ string, _ net.Addr, key ssh.PublicKey) error {
	fp := ssh.FingerprintSHA256(key)
	switch {
	case l.opts.HostKey == "":
		l.notice(locales.SshHostKeyRecorded(fp))
		if l.opts.OnHostKey != nil {
			l.opts.OnHostKey(fp)
		}
		return nil
	case l.opts.HostKey != fp:
		return errors.New(locales.SshHostKeyMismatch(l.opts.HostKey, fp))
	}
	return nil
}

func (l *Local) run() {
	l.status(StatusConnecting, "")
	if l.opts.Resolve != nil {
		l.notice(locales.SshFetchingCredentials())
		ctx, cancel := context.WithTimeout(l.ctx, 20*time.Second)
		err := l.opts.Resolve(ctx, &l.opts)
		cancel()
		if l.ctx.Err() != nil {
			return
		}
		if err != nil {
			l.fail(err)
			return
		}
	}
	addr := net.JoinHostPort(l.opts.Host, strconv.Itoa(l.opts.Port))
	l.notice(locales.SshConnecting(l.opts.Username, addr))

	auth, err := l.authMethods()
	if err != nil {
		l.fail(err)
		return
	}
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            l.opts.Username,
		Auth:            auth,
		HostKeyCallback: l.hostKey,
		Timeout:         15 * time.Second,
	})
	if err != nil {
		l.fail(fmt.Errorf("%s: %w", locales.SshConnectFailed(), err))
		return
	}
	session, err := client.NewSession()
	if err != nil {
		client.Close()
		l.fail(fmt.Errorf("%s: %w", locales.SshSessionFailed(), err))
		return
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		client.Close()
		l.fail(err)
		return
	}
	session.Stdout = writerFunc(func(p []byte) (int, error) { l.output(p); return len(p), nil })
	session.Stderr = session.Stdout

	l.mu.Lock()
	size := l.size
	l.mu.Unlock()
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := session.RequestPty("xterm-256color", size[1], size[0], modes); err != nil {
		client.Close()
		l.fail(fmt.Errorf("%s: %w", locales.SshPtyFailed(), err))
		return
	}
	if err := session.Shell(); err != nil {
		client.Close()
		l.fail(fmt.Errorf("%s: %w", locales.SshShellFailed(), err))
		return
	}

	l.mu.Lock()
	l.client, l.session, l.stdin = client, session, stdin
	// The terminal may have changed its size while the shell started.
	resized := l.size != size
	size = l.size
	l.mu.Unlock()
	if resized {
		session.WindowChange(size[1], size[0])
	}
	l.ready()
	go l.keepalive(client)

	err = session.Wait()
	client.Close()
	if l.ctx.Err() != nil {
		return
	}
	var exit *ssh.ExitError
	if err == nil || errors.As(err, &exit) {
		l.end()
		return
	}
	l.fail(fmt.Errorf("%s: %w", locales.SshDisconnected(), err))
}

func (l *Local) keepalive(client *ssh.Client) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-t.C:
			if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				return
			}
		}
	}
}

// Client returns the SSH connection once the shell is ready, for SFTP and
// the server's status over it; nil before.
func (l *Local) Client() *ssh.Client {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.session == nil {
		return nil
	}
	return l.client
}

// Write sends what is typed to the shell.
func (l *Local) Write(p []byte) (int, error) {
	l.mu.Lock()
	stdin := l.stdin
	l.mu.Unlock()
	if stdin == nil {
		return len(p), nil
	}
	return stdin.Write(p)
}

// Resize changes the size of the server's pseudo-terminal.
func (l *Local) Resize(cols, rows int) error {
	l.mu.Lock()
	l.size = [2]int{cols, rows}
	session := l.session
	l.mu.Unlock()
	if session != nil {
		return session.WindowChange(rows, cols)
	}
	return nil
}

// Close ends the connection.
func (l *Local) Close() error {
	l.mu.Lock()
	client := l.client
	l.mu.Unlock()
	err := l.stream.Close()
	if client != nil {
		client.Close()
	}
	return err
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
