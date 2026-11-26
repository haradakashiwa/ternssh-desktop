package gui

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"ternssh-desktop/locales"
)

// TermEvents are what a terminal tells of the program in it, on a
// goroutine of the terminal.
type TermEvents struct {
	OnTitle  func(title string)
	OnNotify func(title, body string)
	OnBell   func()
	OnExit   func(code int)
	// OnClear is told the user clears the terminal, before it does.
	OnClear func()
}

// agentState is what a coding agent in a terminal, as Claude Code, is
// doing, as the terminal shows it.
type agentState int

const (
	agentNone    agentState = iota // no agent
	agentIdle                      // one waits for a prompt
	agentRunning                   // one works
	agentWaiting                   // one asks the user, as for a permission
	agentDone                      // one finished a turn
)

// agent is the coding agent of a session.
type agent struct {
	name   string // "Claude", or what notified
	task   string // what it works on, as its title says
	state  agentState
	since  time.Time // when it started working
	unread bool      // it finished, or asks, while the session was not seen
	seq    int       // counts the titles, so a late check sees a newer one
}

// attention reports whether the agent waits for the user to look.
func (a agent) attention() bool {
	return a.unread && (a.state == agentWaiting || a.state == agentDone)
}

// Claude Code's title is a glyph and its task: ✳ while it waits for a
// prompt (or the user), and a turning circle while it works.
const (
	claudeIdle    = '✳'
	claudeSpinner = "◐◓◑◒"
)

// parseAgentTitle reads a terminal title an agent set: who, whether it
// works, and on what; ok is false for a title of something else.
func parseAgentTitle(title string) (name, task string, running, ok bool) {
	r, size := utf8.DecodeRuneInString(title)
	rest, spaced := strings.CutPrefix(title[size:], " ")
	switch {
	case !spaced:
		return "", "", false, false
	case r == claudeIdle:
		return "Claude", rest, false, true
	case strings.ContainsRune(claudeSpinner, r), r >= 0x2801 && r <= 0x28ff: // braille, as earlier versions spun
		return "Claude", rest, true, true
	}
	return "", "", false, false
}

// asksUser reports whether the end of a terminal's text is an agent's
// question to the user, as Claude Code's permission prompts.
func asksUser(text string) bool {
	lines := strings.Split(strings.TrimRight(text, "\n "), "\n")
	if len(lines) > 30 {
		lines = lines[len(lines)-30:]
	}
	tail := strings.Join(lines, "\n")
	return strings.Contains(tail, "Do you want to") || strings.Contains(tail, "Would you like to")
}

// settleDelay is how long an agent stays idle before it counts as done:
// Claude Code shows ✳ for a moment between steps, and draws its question
// after the title.
const settleDelay = 1500 * time.Millisecond

// termEvents are the events of the terminal of sess, in s.
func (m *Model) termEvents(s *siteState, sess *session) TermEvents {
	return TermEvents{
		OnTitle:  func(title string) { m.update(func() { m.agentTitle(s, sess, title) }) },
		OnNotify: func(title, body string) { m.update(func() { m.agentNotified(s, sess, title, body) }) },
		OnBell:   func() { m.update(func() { m.agentBell(s, sess) }) },
		OnClear: func() {
			if sess.tap != nil {
				sess.tap.resyncSoon()
			}
		},
	}
}

// agentTitle follows the agent of a session by the titles it sets.
func (m *Model) agentTitle(s *siteState, sess *session, title string) {
	a := &sess.agent
	a.seq++
	name, task, running, ok := parseAgentTitle(title)
	if !ok {
		// It quit, as Claude Code clears its title, or the shell is back.
		if a.name == "Claude" {
			sess.agent = agent{seq: a.seq}
		}
		return
	}
	a.name, a.task = name, task
	switch {
	case running:
		if a.state != agentRunning {
			a.since = time.Now()
		}
		a.state, a.unread = agentRunning, false
	case a.state == agentRunning:
		seq := a.seq
		time.AfterFunc(settleDelay, func() { m.update(func() { m.agentSettled(s, sess, seq) }) })
	case a.state == agentNone:
		a.state = agentIdle
	}
}

// agentSettled finishes the turn of an agent that stopped working, when
// no title came since the one with seq: done, or waiting when the screen
// asks the user.
func (m *Model) agentSettled(s *siteState, sess *session, seq int) {
	if sess.agent.seq != seq || sess.agent.state != agentRunning {
		return
	}
	state := agentDone
	if sess.term != nil && asksUser(sess.term.Text()) {
		state = agentWaiting
	}
	m.agentFinished(s, sess, state, "")
}

// agentNotified takes a program's desktop notification (OSC 9, OSC 777),
// as Codex's and those Claude Code can send, as its turn ending.
func (m *Model) agentNotified(s *siteState, sess *session, title, body string) {
	a := &sess.agent
	if a.name == "" {
		a.name = "Codex"
		if !strings.Contains(strings.ToLower(title+" "+body), "codex") {
			a.name = strings.TrimSpace(title)
		}
	}
	state := agentDone
	if asksUser(body) || strings.Contains(strings.ToLower(body), "approv") || strings.Contains(strings.ToLower(body), "permission") {
		state = agentWaiting
	}
	msg := strings.TrimSpace(body)
	if msg == "" {
		msg = strings.TrimSpace(title)
	}
	m.agentFinished(s, sess, state, msg)
}

// agentBell marks a session that rang the bell while not seen, as an
// agent does when it asks, without a desktop notification: shells ring it
// too.
func (m *Model) agentBell(s *siteState, sess *session) {
	if a := &sess.agent; a.name != "" && !m.seen(s, sess) {
		if a.state == agentRunning || a.state == agentIdle {
			a.state = agentWaiting
		}
		a.unread = true
	}
}

// agentFinished puts the agent of a session in state, done or waiting,
// and marks it for the user when they do not see it, with a desktop
// notification of msg, or of what the agent works on, when the app is not
// in front.
func (m *Model) agentFinished(s *siteState, sess *session, state agentState, msg string) {
	a := &sess.agent
	a.state = state
	if m.seen(s, sess) {
		return
	}
	a.unread = true
	if m.backend.Notify == nil || m.backend.Active == nil || m.backend.Active() {
		return
	}
	name := a.name
	if name == "" {
		name = sess.title
	}
	title := locales.AgentDoneTitle(name)
	if state == agentWaiting {
		title = locales.AgentWaitingTitle(name)
	}
	body := sess.title
	if !s.home() {
		body = s.displayName() + " · " + sess.title
	}
	if msg == "" {
		msg = a.task
	}
	if msg != "" {
		body = msg + "\n" + body
	}
	m.backend.Notify(title, body, func() { m.showSession(s, sess) })
}

// seen reports whether the user sees a session: the one shown, of the tab
// shown, in the app in front.
func (m *Model) seen(s *siteState, sess *session) bool {
	if m.current() != s || s.current < 0 || s.current >= len(s.sessions) || s.sessions[s.current] != sess {
		return false
	}
	return m.backend.Active == nil || m.backend.Active()
}

// showSession shows a session, as its notification is clicked.
func (m *Model) showSession(s *siteState, sess *session) {
	i := m.indexOf(s)
	if i < 0 {
		return
	}
	for j, other := range s.sessions {
		if other == sess {
			m.selectSite(i)
			s.current = j
			sess.refocus = true
			return
		}
	}
}

// markSeen clears what the session shown has to tell, and keeps the
// app's badge counting the sessions that ask for the user.
func (m *Model) markSeen() {
	if s := m.current(); s != nil && s.current >= 0 && s.current < len(s.sessions) {
		if sess := s.sessions[s.current]; sess.agent.unread && m.seen(s, sess) {
			sess.agent.unread = false
		}
	}
	n := 0
	for _, s := range m.sites {
		n += s.attention()
	}
	if n != m.badge && m.backend.SetBadge != nil {
		m.backend.SetBadge(n)
	}
	m.badge = n
}

// attention counts the sessions of the tab whose agent waits for the
// user to look.
func (s *siteState) attention() int {
	n := 0
	for _, sess := range s.sessions {
		if sess.agent.attention() {
			n++
		}
	}
	return n
}

// The colors of an agent asking for the user, and of one done.
var (
	attentionColor = ui.Hex("#ff9f0a")
	doneColor      = ui.Hex("#30d158")
)

// agentMark is what a session's tab shows before its title: what its
// agent does, or else the state of its connection.
func agentMark(c *ui.Context, sess *session) {
	a := sess.agent
	switch {
	case a.state == agentRunning:
		ui.Spinner(c).Size(12, 12).Shrink(0).Label(locales.AgentWorking(a.name))
	case a.state == agentWaiting:
		dot(c, attentionColor)
	case a.state == agentDone && a.unread:
		ui.Icon(c, checkIcon).Size(12, 12).Shrink(0).TextColor(doneColor)
	default:
		dot(c, sessionColor(sess.status))
	}
}

// agentTip is the tooltip of a session's tab with an agent in it.
func agentTip(a agent) string {
	switch {
	case a.name == "":
		return ""
	case a.state == agentRunning:
		return locales.AgentTipRunning(a.name, a.task)
	case a.state == agentWaiting:
		return locales.AgentTipWaiting(a.name, a.task)
	case a.state == agentDone:
		return locales.AgentTipDone(a.name, a.task)
	}
	return locales.AgentTipIdle(a.name, a.task)
}
