package ternssh

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// QuickCommand is a command kept to send to a terminal with a click.
type QuickCommand struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Command string `json:"command"`
}

// A site keeps its quick commands in the widgets of its dashboard of type
// quickCommandsWidget, each with its own list in its config.
const quickCommandsWidget = "quick_commands"

type widget struct {
	ID         string  `json:"id"`
	Type       string  `json:"type"`
	ConfigJSON *string `json:"config_json"`
	GridX      int     `json:"grid_x"`
	GridY      int     `json:"grid_y"`
	GridW      int     `json:"grid_w"`
	GridH      int     `json:"grid_h"`
}

type dashboard struct {
	Widgets []widget `json:"widgets"`
}

// commandsOf returns the commands of a widget's config, as the site reads
// them: those with a label and a command.
func commandsOf(w widget) []QuickCommand {
	if w.ConfigJSON == nil {
		return nil
	}
	var cfg struct {
		CustomCommands []QuickCommand `json:"customCommands"`
	}
	if json.Unmarshal([]byte(*w.ConfigJSON), &cfg) != nil {
		return nil
	}
	var out []QuickCommand
	for _, q := range cfg.CustomCommands {
		q.Label = strings.TrimSpace(q.Label)
		q.Command = strings.TrimSpace(strings.ReplaceAll(q.Command, "\r\n", "\n"))
		if q.ID != "" && q.Label != "" && q.Command != "" {
			out = append(out, q)
		}
	}
	return out
}

// setCommands writes cmds into a widget's config, keeping what else it
// holds.
func setCommands(w *widget, cmds []QuickCommand) error {
	cfg := map[string]json.RawMessage{}
	if w.ConfigJSON != nil {
		json.Unmarshal([]byte(*w.ConfigJSON), &cfg)
	}
	if cmds == nil {
		cmds = []QuickCommand{}
	}
	b, err := json.Marshal(cmds)
	if err != nil {
		return err
	}
	cfg["customCommands"] = b
	if _, ok := cfg["targetMode"]; !ok {
		cfg["targetMode"] = json.RawMessage(`"current"`)
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	s := string(out)
	w.ConfigJSON = &s
	return nil
}

func (c *Client) dashboard(ctx context.Context) (*dashboard, error) {
	var d dashboard
	if err := c.do(ctx, http.MethodGet, "/api/v1/dashboards", nil, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func allCommands(d *dashboard) []QuickCommand {
	var out []QuickCommand
	seen := map[string]bool{}
	for _, w := range d.Widgets {
		if w.Type != quickCommandsWidget {
			continue
		}
		for _, q := range commandsOf(w) {
			if !seen[q.ID] {
				seen[q.ID] = true
				out = append(out, q)
			}
		}
	}
	return out
}

// QuickCommands returns the quick commands of the site: those of all its
// quick command widgets.
func (c *Client) QuickCommands(ctx context.Context) ([]QuickCommand, error) {
	d, err := c.dashboard(ctx)
	if err != nil {
		return nil, err
	}
	return allCommands(d), nil
}

// editCommands changes the commands of the widget edit picks, adding a
// quick command widget below the others when there is none, and returns
// the site's commands after.
func (c *Client) editCommands(ctx context.Context, edit func(d *dashboard) (int, []QuickCommand, error)) ([]QuickCommand, error) {
	d, err := c.dashboard(ctx)
	if err != nil {
		return nil, err
	}
	i, cmds, err := edit(d)
	if err != nil {
		return nil, err
	}
	if i < 0 {
		bottom := 0
		for _, w := range d.Widgets {
			bottom = max(bottom, w.GridY+w.GridH)
		}
		d.Widgets = append(d.Widgets, widget{Type: quickCommandsWidget, GridY: bottom, GridW: 3, GridH: 6})
		i = len(d.Widgets) - 1
	}
	if err := setCommands(&d.Widgets[i], cmds); err != nil {
		return nil, err
	}
	// A widget not yet saved, without an ID, gets one from the site.
	ws := make([]map[string]any, len(d.Widgets))
	for j, w := range d.Widgets {
		ws[j] = map[string]any{"type": w.Type, "config_json": w.ConfigJSON, "grid_x": w.GridX, "grid_y": w.GridY, "grid_w": w.GridW, "grid_h": w.GridH}
		if w.ID != "" {
			ws[j]["id"] = w.ID
		}
	}
	body := map[string]any{"widgets": ws}
	var out dashboard
	if err := c.do(ctx, http.MethodPut, "/api/v1/dashboards", body, &out); err != nil {
		return nil, err
	}
	return allCommands(&out), nil
}

// SaveQuickCommand adds q to the site, or replaces the command with its
// ID, and returns the site's commands.
func (c *Client) SaveQuickCommand(ctx context.Context, q QuickCommand) ([]QuickCommand, error) {
	return c.editCommands(ctx, func(d *dashboard) (int, []QuickCommand, error) {
		first := -1
		for i, w := range d.Widgets {
			if w.Type != quickCommandsWidget {
				continue
			}
			if first < 0 {
				first = i
			}
			cmds := commandsOf(w)
			for j := range cmds {
				if cmds[j].ID == q.ID {
					cmds[j] = q
					return i, cmds, nil
				}
			}
		}
		if first < 0 {
			return -1, []QuickCommand{q}, nil
		}
		return first, append(commandsOf(d.Widgets[first]), q), nil
	})
}

var errNoCommand = errors.New("站点上没有这条快捷指令，可能已被删除")

// DeleteQuickCommand removes a command from the site, and returns the
// site's commands.
func (c *Client) DeleteQuickCommand(ctx context.Context, id string) ([]QuickCommand, error) {
	return c.editCommands(ctx, func(d *dashboard) (int, []QuickCommand, error) {
		for i, w := range d.Widgets {
			if w.Type != quickCommandsWidget {
				continue
			}
			cmds := commandsOf(w)
			for j := range cmds {
				if cmds[j].ID == id {
					return i, append(cmds[:j], cmds[j+1:]...), nil
				}
			}
		}
		return 0, nil, errNoCommand
	})
}
