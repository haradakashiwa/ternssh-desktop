package gui

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"ternssh-desktop/internal/sshconn"
	"ternssh-desktop/internal/store"
	"ternssh-desktop/internal/ternssh"
)

// The tabs of the pane below a terminal.
const (
	tabFiles = iota
	tabCommands
)

// builtinCommands are offered on every server, as the site's quick
// command widget offers them.
var builtinCommands = []ternssh.QuickCommand{
	{ID: "builtin/uptime", Label: "运行时间", Command: "uptime"},
	{ID: "builtin/whoami", Label: "当前用户", Command: "whoami"},
	{ID: "builtin/pwd", Label: "当前目录", Command: "pwd"},
	{ID: "builtin/disk", Label: "磁盘", Command: "df -h"},
	{ID: "builtin/memory", Label: "内存", Command: "free -h"},
	{ID: "builtin/top-mem", Label: "内存占用最高", Command: "ps -eo pid,user,pcpu,pmem,rss,stat,args --sort=-pmem 2>/dev/null | head -11"},
	{ID: "builtin/top-cpu", Label: "CPU 占用最高", Command: "ps -eo pid,user,pcpu,pmem,rss,stat,args --sort=-pcpu 2>/dev/null | head -11"},
	{ID: "builtin/docker", Label: "Docker 容器", Command: "docker ps"},
	{ID: "builtin/compose", Label: "Compose 服务", Command: "docker compose ps 2>/dev/null || docker-compose ps"},
	{ID: "builtin/ports", Label: "监听端口", Command: "ss -tlnp 2>/dev/null || netstat -tlnp"},
	{ID: "builtin/logs", Label: "最近日志", Command: "journalctl -n 50 --no-pager 2>/dev/null || tail -n 50 /var/log/syslog 2>/dev/null || dmesg | tail -n 50"},
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
		paneTab(c, "文件", &sess.bottomTab, tabFiles)
		paneTab(c, "快捷指令", &sess.bottomTab, tabCommands)
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
		source := "保存在本机"
		if !s.home() {
			source = "来自站点「" + s.displayName() + "」"
		}
		ui.Text(c, source).FontSize(11).TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0)
		ui.Spacer(c)
		if !s.home() {
			b := iconButton(c, refreshIcon, "刷新快捷指令")
			if s.commandsLoading {
				b.Disabled(true)
			}
			if b.Clicked() {
				m.loadCommands(s)
			}
		}
		if iconButton(c, plusIcon, "添加快捷指令").Clicked() {
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
				ui.Text(c, "还没有快捷指令，点右上角的「+」添加").FontSize(12).TextColor(t.TextMuted)
			}
			commandChips(c, func() {
				for _, q := range s.commands {
					m.commandChip(c, s, sess, q, true)
				}
			})
			if sess.hasPanels() {
				ui.Text(c, "内置").FontSize(11).Bold().TextColor(t.TextMuted).Padding(6, 0, 0, 0)
				commandChips(c, func() {
					for _, q := range builtinCommands {
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
		m.runCommand(sess, q.Command, false)
	}
	chip.ContextMenu(func(menu *ui.Menu) {
		if menu.Item("运行").Chosen() {
			m.runCommand(sess, q.Command, false)
		}
		if menu.Item("粘贴到终端（不运行）").Chosen() {
			m.runCommand(sess, q.Command, true)
		}
		if menu.Item("复制命令").Chosen() {
			c.WriteClipboard(q.Command)
		}
		if editable {
			menu.Separator()
			if menu.Item("编辑…").Chosen() {
				m.openCommandForm(s, &q)
			}
			if menu.Item("删除").Danger().Chosen() {
				m.deleteCommand(s, q)
			}
		}
	})
	chip.Children(func() {
		ui.Icon(c, zapIcon).Size(12, 12).TextColor(accentInk(c)).Shrink(0)
		ui.Text(c, q.Label).FontSize(12).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
	})
}

// runCommand sends a command to the terminal of a session, run, or
// pasted for the user to finish and run.
func (m *Model) runCommand(sess *session, command string, paste bool) {
	if sess.status != sshconn.StatusReady {
		m.notify("终端未连接，无法发送命令")
		return
	}
	command = strings.ReplaceAll(command, "\r\n", "\n")
	text := command
	if !paste {
		text = strings.ReplaceAll(command, "\n", "\r") + "\r"
	}
	if !input(sess, text, paste) {
		m.notify("终端未连接，无法发送命令")
		return
	}
	sess.refocus = true
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
	title := "添加快捷指令"
	if f.editing != "" {
		title = "编辑快捷指令"
	}
	where := "保存在本机"
	if !f.site.home() {
		where = "保存到站点「" + f.site.displayName() + "」，网页版的快捷指令里也会显示"
	}
	dialog(c, &m.commandFormOpen, 460, func() {
		dialogTitle(c, title, where)
		ui.Field(c, "名称", func() { ui.TextInput(c, &f.label).Placeholder("例如：查看磁盘").AutoFocus() })
		ui.Field(c, "命令", func() {
			ui.Column(c).Gap(6).Children(func() {
				ui.TextArea(c, &f.command).Lines(3, 8).Font("monospace").FontSize(12).Placeholder("df -h")
				ui.Text(c, "点击即在当前终端运行；多行会逐行执行。").FontSize(11).TextColor(c.Theme().TextMuted)
			})
		})
		errorText(c, f.err)
		dialogButtons(c, f.busy, "保存", func() { m.commandFormOpen = false }, m.saveCommand)
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
		f.err = "请填写名称"
		return
	case q.Command == "":
		f.err = "请填写命令"
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
	message := "只删除本机保存的这条快捷指令。"
	if !s.home() {
		message = "会从站点「" + s.displayName() + "」上删除，网页版也不再显示。"
	}
	m.ask(confirmation{
		title:   "删除快捷指令「" + q.Label + "」？",
		message: message,
		action:  "删除",
		do: func() {
			if s.home() {
				if err := m.store.DeleteQuickCommand(q.ID); err != nil {
					m.notify("删除失败：" + err.Error())
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
						m.notify("删除失败：" + err.Error())
						return
					}
					s.commands = cmds
				})
			}()
		},
	})
}
