package gui

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"ternssh-desktop/internal/sshconn"
	"ternssh-desktop/internal/store"
	"ternssh-desktop/internal/ternssh"
	"ternssh-desktop/locales"
)

// The tabs of the pane below a terminal.
const (
	tabFiles = iota
	tabCommands
)

// builtinCommands are offered on every server, as the site's quick
// command widget offers them. Their labels are said in the language the
// app shows, so they are made as they are asked for.
func builtinCommands() []ternssh.QuickCommand {
	return []ternssh.QuickCommand{
		{ID: "builtin/uptime", Label: locales.BuiltinUptime(), Command: "uptime"},
		{ID: "builtin/whoami", Label: locales.BuiltinWhoami(), Command: "whoami"},
		{ID: "builtin/pwd", Label: locales.BuiltinPwd(), Command: "pwd"},
		{ID: "builtin/disk", Label: locales.BuiltinDisk(), Command: "df -h"},
		{ID: "builtin/memory", Label: locales.BuiltinMemory(), Command: "free -h"},
		{ID: "builtin/top-mem", Label: locales.BuiltinTopMem(), Command: "ps -eo pid,user,pcpu,pmem,rss,stat,args --sort=-pmem 2>/dev/null | head -11"},
		{ID: "builtin/top-cpu", Label: locales.BuiltinTopCpu(), Command: "ps -eo pid,user,pcpu,pmem,rss,stat,args --sort=-pcpu 2>/dev/null | head -11"},
		{ID: "builtin/docker", Label: locales.BuiltinDocker(), Command: "docker ps"},
		{ID: "builtin/compose", Label: locales.BuiltinCompose(), Command: "docker compose ps 2>/dev/null || docker-compose ps"},
		{ID: "builtin/ports", Label: locales.BuiltinPorts(), Command: "ss -tlnp 2>/dev/null || netstat -tlnp"},
		{ID: "builtin/logs", Label: locales.BuiltinLogs(), Command: "journalctl -n 50 --no-pager 2>/dev/null || tail -n 50 /var/log/syslog 2>/dev/null || dmesg | tail -n 50"},
	}
}

// input sends text to the terminal of a session, as typed or pasted, and
// reports whether it went.
var input = func(sess *session, text string, paste bool) bool {
	if sess.term == nil {
		return false
	}
	if paste {
		sess.term.Paste(text)
	} else {
		sess.term.Send([]byte(text))
	}
	return true
}

// bottomPanel is the pane below a terminal: its files, the server's or
// this machine's, and the quick commands, in tabs.
func (m *Model) bottomPanel(c *ui.Context, sess *session) {
	ui.Row(c).Padding(6, 8, 0, 8).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
		paneTab(c, locales.CmdTabFiles(), &sess.bottomTab, tabFiles)
		paneTab(c, locales.CmdTabCommands(), &sess.bottomTab, tabCommands)
	})
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		if sess.bottomTab == tabFiles {
			m.filesPanel(c, sess)
		} else {
			m.commandsPanel(c, sess)
		}
	})
}

func paneTab(c *ui.Context, label string, tab *int, i int) {
	t := c.Theme()
	b := ui.ButtonBase(c).Height(24).Padding(0, 10).Radius(6).Center().Label(label)
	b.Children(func() {
		l := ui.Text(c, label).FontSize(12)
		if *tab == i {
			l.Bold()
		} else {
			l.TextColor(t.TextMuted)
		}
	})
	switch {
	case *tab == i:
		b.Background(hover(c))
	case b.Hovered():
		b.Background(hover(c).Alpha(0.5))
	}
	if b.Clicked() {
		*tab = i
	}
}

// loadCommands reads the quick commands of a tab: this machine's, or its
// site's.
func (m *Model) loadCommands(s *siteState) {
	if s.home() {
		s.commands = fromStore(m.store.QuickCommands())
		s.commandsLoaded, s.commandsErr = true, ""
		return
	}
	if s.commandsLoading {
		return
	}
	s.commandsLoading = true
	client := s.client
	go func() {
		ctx, cancel := request()
		defer cancel()
		cmds, err := client.QuickCommands(ctx)
		m.update(func() {
			s.commandsLoading, s.commandsLoaded = false, true
			if err != nil {
				s.commandsErr = err.Error()
				return
			}
			s.commands, s.commandsErr = cmds, ""
		})
	}()
}

func fromStore(qs []store.QuickCommand) []ternssh.QuickCommand {
	out := make([]ternssh.QuickCommand, len(qs))
	for i, q := range qs {
		out[i] = ternssh.QuickCommand(q)
	}
	return out
}

func (m *Model) commandsPanel(c *ui.Context, sess *session) {
	t := c.Theme()
	s := m.current()
	if !s.commandsLoaded {
		m.loadCommands(s)
	}
	ui.Row(c).Padding(6, 8, 4, 12).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
		source := locales.CmdSourceLocal()
		if !s.home() {
			source = locales.CmdSourceSite(s.displayName())
		}
		ui.Text(c, source).FontSize(11).TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0)
		ui.Spacer(c)
		if !s.home() {
			b := iconButton(c, refreshIcon, locales.CmdRefresh())
			if s.commandsLoading {
				b.Disabled(true)
			}
			if b.Clicked() {
				m.loadCommands(s)
			}
		}
		if iconButton(c, plusIcon, locales.CmdAdd()).Clicked() {
			m.openCommandForm(s, nil)
		}
	})
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(2, 12, 10, 12).Gap(6).Children(func() {
			switch {
			case s.commandsErr != "":
				ui.Text(c, s.commandsErr).FontSize(12).TextColor(t.Danger)
			case !s.commandsLoaded:
				ui.Row(c).Padding(8).Center().Children(func() { ui.Spinner(c).Size(14, 14) })
			case len(s.commands) == 0:
				ui.Text(c, locales.CmdEmpty()).FontSize(12).TextColor(t.TextMuted)
			}
			commandChips(c, func() {
				for _, q := range s.commands {
					m.commandChip(c, s, sess, q, true)
				}
			})
			if sess.hasPanels() {
				ui.Text(c, locales.CmdBuiltin()).FontSize(11).Bold().TextColor(t.TextMuted).Padding(6, 0, 0, 0)
				commandChips(c, func() {
					for _, q := range builtinCommands() {
						m.commandChip(c, s, sess, q, false)
					}
				})
			}
		})
	})
}

func commandChips(c *ui.Context, fn func()) {
	ui.Row(c).Wrap().Gap(6).Children(fn)
}

// commandChip is a command, which runs as it is clicked; its menu pastes,
// copies, edits and deletes it, and its tip shows it.
func (m *Model) commandChip(c *ui.Context, s *siteState, sess *session, q ternssh.QuickCommand, editable bool) {
	chip := ui.ButtonBase(c).Key(q.ID).Height(28).Padding(0, 10, 0, 8).Radius(7).Gap(6).AlignItems(ui.Center).
		MaxWidth(240).Label(q.Label).Tooltip(q.Command).Border(1, hairline(c))
	if chip.Hovered() {
		chip.Background(hover(c))
	} else {
		chip.Background(frost(c, 0.5))
	}
	if chip.Clicked() {
		m.sendCommand(sess, q)
	}
	chip.ContextMenu(func(menu *ui.Menu) {
		if menu.Item(locales.CmdRunCurrent()).Chosen() {
			m.runCommand(sess, q.Command, false)
		}
		if menu.Item(locales.CmdRunAll()).Chosen() {
			m.runEverywhere(sess, q.Command)
		}
		if menu.Item(locales.CmdPaste()).Chosen() {
			m.runCommand(sess, q.Command, true)
		}
		if menu.Item(locales.CmdCopyCommand()).Chosen() {
			c.WriteClipboard(q.Command)
		}
		if editable {
			menu.Separator()
			if menu.Item(locales.Edit()).Chosen() {
				m.openCommandForm(s, &q)
			}
			if menu.Item(locales.Delete()).Danger().Chosen() {
				m.deleteCommand(s, q)
			}
		}
	})
	chip.Children(func() {
		ui.Icon(c, zapIcon).Size(12, 12).TextColor(accentInk(c)).Shrink(0)
		ui.Text(c, q.Label).FontSize(12).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
	})
}

// sendCommand runs a quick command clicked where the settings say: in
// the terminal of sess, in every terminal connected, or where the user
// says.
func (m *Model) sendCommand(sess *session, q ternssh.QuickCommand) {
	switch m.store.Settings().CommandTarget {
	case store.CommandTargetAll:
		m.runEverywhere(sess, q.Command)
	case store.CommandTargetAsk:
		m.commandAsk, m.commandAskOpen = commandAsk{sess: sess, command: q}, true
	default:
		m.runCommand(sess, q.Command, false)
	}
}

// connected returns the sessions of every tab whose terminal is up.
func (m *Model) connected() []*session {
	var out []*session
	for _, s := range m.sites {
		for _, sess := range s.sessions {
			if sess.status == sshconn.StatusReady {
				out = append(out, sess)
			}
		}
	}
	return out
}

// runEverywhere runs a command in every terminal connected, giving the
// keyboard back to the one of sess, the one shown.
func (m *Model) runEverywhere(sess *session, command string) {
	text := strings.ReplaceAll(strings.ReplaceAll(command, "\r\n", "\n"), "\n", "\r") + "\r"
	n := 0
	for _, to := range m.connected() {
		if input(to, text, false) {
			n++
		}
	}
	if n == 0 {
		m.notify(locales.CmdNoSessions())
		return
	}
	sess.refocus = true
	m.notify(locales.CmdSentAll(n))
}

// runCommand sends a command to the terminal of a session, run, or
// pasted for the user to finish and run.
func (m *Model) runCommand(sess *session, command string, paste bool) {
	if sess.status != sshconn.StatusReady {
		m.notify(locales.CmdNotConnected())
		return
	}
	command = strings.ReplaceAll(command, "\r\n", "\n")
	text := command
	if !paste {
		text = strings.ReplaceAll(command, "\n", "\r") + "\r"
	}
	if !input(sess, text, paste) {
		m.notify(locales.CmdNotConnected())
		return
	}
	sess.refocus = true
}

// --- Asking where a command goes

type commandAsk struct {
	sess    *session // the session shown as the command was clicked
	command ternssh.QuickCommand
}

func (m *Model) commandAskDialog(c *ui.Context) {
	a := m.commandAsk
	if a.sess == nil {
		return
	}
	dialog(c, &m.commandAskOpen, 400, func() {
		dialogTitle(c, locales.CmdAskTitle(a.command.Label), "")
		ui.Text(c, a.command.Command).Font("monospace").FontSize(12).TextColor(c.Theme().TextMuted).MaxLines(6).Ellipsis("…")
		n := len(m.connected())
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c, locales.Cancel()).Clicked() {
				m.commandAskOpen = false
			}
			if ui.Button(c, locales.CmdAskAll(n)).Disabled(n == 0).Clicked() {
				m.commandAskOpen = false
				m.runEverywhere(a.sess, a.command.Command)
			}
			b := ui.PrimaryButton(c, locales.CmdAskCurrent()).Disabled(a.sess.status != sshconn.StatusReady)
			if b.Clicked() || c.Shortcut(0, ui.KeyEnter) {
				m.commandAskOpen = false
				m.runCommand(a.sess, a.command.Command, false)
			}
		})
	})
}

// --- The form of a command

type commandForm struct {
	site           *siteState
	editing        string // the ID of the command edited, empty for a new one
	label, command string
	err            string
	busy           bool
}

func (m *Model) openCommandForm(s *siteState, q *ternssh.QuickCommand) {
	m.commandForm = commandForm{site: s}
	if q != nil {
		m.commandForm.editing, m.commandForm.label, m.commandForm.command = q.ID, q.Label, q.Command
	}
	m.commandFormOpen = true
}

func (m *Model) commandDialog(c *ui.Context) {
	f := &m.commandForm
	if f.site == nil {
		return
	}
	title := locales.CmdAdd()
	if f.editing != "" {
		title = locales.CmdEditTitle()
	}
	where := locales.CmdSourceLocal()
	if !f.site.home() {
		where = locales.CmdSaveToSite(f.site.displayName())
	}
	dialog(c, &m.commandFormOpen, 460, func() {
		dialogTitle(c, title, where)
		ui.Field(c, locales.CmdName(), func() { ui.TextInput(c, &f.label).Placeholder(locales.CmdNamePlaceholder()).AutoFocus() })
		ui.Field(c, locales.CmdCommand(), func() {
			ui.Column(c).Gap(6).Children(func() {
				ui.TextArea(c, &f.command).Lines(3, 8).Font("monospace").FontSize(12).Placeholder("df -h")
				ui.Text(c, locales.CmdHint()).FontSize(11).TextColor(c.Theme().TextMuted)
			})
		})
		errorText(c, f.err)
		dialogButtons(c, f.busy, locales.Save(), func() { m.commandFormOpen = false }, m.saveCommand)
	})
}

func (m *Model) saveCommand() {
	f := &m.commandForm
	q := ternssh.QuickCommand{
		ID:      f.editing,
		Label:   strings.TrimSpace(f.label),
		Command: strings.TrimSpace(strings.ReplaceAll(f.command, "\r\n", "\n")),
	}
	switch {
	case q.Label == "":
		f.err = locales.CmdNameRequired()
		return
	case q.Command == "":
		f.err = locales.CmdCommandRequired()
		return
	}
	if q.ID == "" {
		q.ID = store.NewID()
	}
	s := f.site
	if s.home() {
		if err := m.store.PutQuickCommand(store.QuickCommand(q)); err != nil {
			f.err = err.Error()
			return
		}
		m.loadCommands(s)
		m.commandFormOpen = false
		return
	}
	f.busy, f.err = true, ""
	go func() {
		ctx, cancel := request()
		defer cancel()
		cmds, err := s.client.SaveQuickCommand(ctx, q)
		m.update(func() {
			f.busy = false
			if err != nil {
				f.err = err.Error()
				return
			}
			s.commands, s.commandsLoaded, s.commandsErr = cmds, true, ""
			m.commandFormOpen = false
		})
	}()
}

func (m *Model) deleteCommand(s *siteState, q ternssh.QuickCommand) {
	message := locales.CmdDeleteLocalMessage()
	if !s.home() {
		message = locales.CmdDeleteSiteMessage(s.displayName())
	}
	m.ask(confirmation{
		title:   locales.CmdDeleteTitle(q.Label),
		message: message,
		action:  locales.Delete(),
		do: func() {
			if s.home() {
				if err := m.store.DeleteQuickCommand(q.ID); err != nil {
					m.notify(locales.CmdDeleteFailed(err.Error()))
				}
				m.loadCommands(s)
				return
			}
			go func() {
				ctx, cancel := request()
				defer cancel()
				cmds, err := s.client.DeleteQuickCommand(ctx, q.ID)
				m.update(func() {
					if err != nil {
						m.notify(locales.CmdDeleteFailed(err.Error()))
						return
					}
					s.commands = cmds
				})
			}()
		},
	})
}
