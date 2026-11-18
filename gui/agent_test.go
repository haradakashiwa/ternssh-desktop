package gui

import (
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"ternssh-desktop/internal/sshconn"
)

func TestParseAgentTitle(t *testing.T) {
	// Titles Claude Code 2.1 set in a terminal.
	for _, c := range []struct {
		title, name, task string
		running, ok       bool
	}{
		{"✳ Claude Code", "Claude", "Claude Code", false, true},
		{"◐ Claude Code", "Claude", "Claude Code", true, true},
		{"◑ Shell sleep and echo command", "Claude", "Shell sleep and echo command", true, true},
		{"⠐ Fix the build", "Claude", "Fix the build", true, true},
		{"", "", "", false, false},
		{"pi@raspberrypi: ~", "", "", false, false},
		{"✳Claude", "", "", false, false},
	} {
		name, task, running, ok := parseAgentTitle(c.title)
		if name != c.name || task != c.task || running != c.running || ok != c.ok {
			t.Errorf("parseAgentTitle(%q) = %q, %q, %v, %v", c.title, name, task, running, ok)
		}
	}
}

func TestAsksUser(t *testing.T) {
	prompt := "⏺ Bash(curl -sI https://example.com | head -1)\n\n Do you want to proceed?\n ❯ 1. Yes\n   2. No\n\n Esc to cancel\n"
	if !asksUser("old output\n" + prompt) {
		t.Error("a permission prompt does not ask")
	}
	// A question answered long ago, far up the scrollback, does not.
	old := prompt
	for i := 0; i < 40; i++ {
		old += "line\n"
	}
	if asksUser(old + "✻ Sautéed for 5s\n❯ \n") {
		t.Error("an old prompt asks")
	}
}

func TestAgentStates(t *testing.T) {
	m, st := testModel(t)
	withSites(t, m, st)
	active := true
	var notified []string
	m.backend.Active = func() bool { return active }
	m.backend.Notify = func(title, body string, onClick func()) { notified = append(notified, title+"|"+body) }
	badge := -1
	m.backend.SetBadge = func(n int) { badge = n }

	m.active = 0
	s := m.sites[0]
	m.openLocal(s, s.local[0])
	m.openLocal(s, s.local[0])
	claude, other := s.sessions[0], s.sessions[1]
	if s.current != 1 {
		t.Fatalf("current %d", s.current)
	}

	m.agentTitle(s, claude, "✳ Claude Code")
	if claude.agent.state != agentIdle || claude.agent.name != "Claude" {
		t.Fatalf("started: %+v", claude.agent)
	}
	m.agentTitle(s, claude, "◐ Fix the build")
	if claude.agent.state != agentRunning || claude.agent.task != "Fix the build" {
		t.Fatalf("working: %+v", claude.agent)
	}

	// A moment idle between steps is not the end of the turn.
	m.agentTitle(s, claude, "✳ Fix the build")
	seq := claude.agent.seq
	m.agentTitle(s, claude, "◑ Fix the build")
	m.agentSettled(s, claude, seq)
	if claude.agent.state != agentRunning {
		t.Fatalf("settled on an old title: %+v", claude.agent)
	}

	// Done while another session shows, the app in front: marked, no
	// notification.
	m.agentTitle(s, claude, "✳ Fix the build")
	m.agentSettled(s, claude, claude.agent.seq)
	if claude.agent.state != agentDone || !claude.agent.unread || len(notified) != 0 {
		t.Fatalf("done: %+v %q", claude.agent, notified)
	}
	m.markSeen()
	if badge != 1 || s.attention() != 1 {
		t.Errorf("badge %d, attention %d", badge, s.attention())
	}

	// In the background: a notification, which shows the session.
	active = false
	m.agentTitle(s, claude, "◐ Fix the build")
	m.agentTitle(s, claude, "✳ Fix the build")
	m.agentSettled(s, claude, claude.agent.seq)
	if len(notified) != 1 || notified[0] != "Claude 已完成|Fix the build\n"+claude.title {
		t.Fatalf("notified %q", notified)
	}
	active = true
	m.showSession(s, claude)
	m.markSeen()
	if s.sessions[s.current] != claude || claude.agent.unread || badge != 0 {
		t.Errorf("shown: current %d, %+v, badge %d", s.current, claude.agent, badge)
	}

	// Seen as it finishes: nothing to mark.
	m.agentTitle(s, claude, "◐ Fix the build")
	m.agentTitle(s, claude, "✳ Fix the build")
	m.agentSettled(s, claude, claude.agent.seq)
	if claude.agent.state != agentDone || claude.agent.unread {
		t.Errorf("seen: %+v", claude.agent)
	}

	// Quitting clears its title.
	m.agentTitle(s, claude, "")
	if claude.agent.name != "" || claude.agent.state != agentNone {
		t.Errorf("quit: %+v", claude.agent)
	}

	// Another program's notification, as Codex's.
	m.agentNotified(s, other, "Codex", "Approval requested: rm -rf build")
	if other.agent.name != "Codex" || other.agent.state != agentWaiting {
		t.Errorf("codex: %+v", other.agent)
	}
}

func TestAgentTabsSnapshot(t *testing.T) {
	if os.Getenv("SNAPSHOT_DIR") == "" {
		t.Skip("renders real terminals for snapshots")
	}
	m, st := testModel(t)
	withSites(t, m, st)
	m.active = 0
	m.backend.NewTerminal = func(conn Conn, _ TermEvents) (*terminal.Terminal, error) {
		return terminal.New(terminal.Options{Conn: conn, Transparent: true, Padding: terminal.Padding{X: 10, Y: 8}})
	}
	m.backend.Dial = func(sshconn.LocalOptions, sshconn.Events) Conn {
		return &scriptConn{fakeConn: fakeConn{status: sshconn.StatusReady}}
	}
	s := m.sites[0]
	for range 4 {
		m.openLocal(s, s.local[0])
	}
	for i, sess := range s.sessions {
		sess.status = sshconn.StatusReady
		sess.title = []string{"running", "waiting", "done", "plain"}[i]
	}
	m.agentTitle(s, s.sessions[0], "◐ Fix the build")
	m.agentTitle(s, s.sessions[1], "◐ Deploy")
	s.sessions[1].agent.state, s.sessions[1].agent.unread = agentWaiting, true
	m.agentTitle(s, s.sessions[2], "◐ Write tests")
	s.sessions[2].agent.state, s.sessions[2].agent.unread = agentDone, true
	tt := ui.NewTester(m.View, 1100, 700)
	time.Sleep(200 * time.Millisecond)
	tt.Frame()
	snapshot(t, tt, "agents")
}
