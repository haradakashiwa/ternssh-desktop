package ternssh

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

// fakeDashboard is a site's dashboard as ternssh keeps it: a PUT replaces
// every widget, giving an ID to those without.
type fakeDashboard struct {
	mu      sync.Mutex
	widgets []map[string]any
	next    int
}

func (d *fakeDashboard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if r.Method == http.MethodPut {
		var body struct{ Widgets []map[string]any }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(400)
			return
		}
		for _, wd := range body.Widgets {
			if wd["id"] == nil {
				d.next++
				wd["id"] = fmt.Sprintf("new%d", d.next)
			}
			wd["dashboard_id"] = "d1"
		}
		d.widgets = body.Widgets
	}
	json.NewEncoder(w).Encode(map[string]any{"dashboard": map[string]any{"id": "d1"}, "widgets": d.widgets})
}

func TestQuickCommands(t *testing.T) {
	d := &fakeDashboard{widgets: []map[string]any{
		{"id": "w1", "type": "terminal", "config_json": nil, "grid_x": 0, "grid_y": 0, "grid_w": 6, "grid_h": 8},
	}}
	site := httptest.NewServer(d)
	defer site.Close()
	ctx := context.Background()
	c, _ := New(site.URL, Auth{})

	cmds, err := c.QuickCommands(ctx)
	if err != nil || len(cmds) != 0 {
		t.Fatalf("commands %v, %v", cmds, err)
	}
	// The first command adds a widget for them, below the others.
	cmds, err = c.SaveQuickCommand(ctx, QuickCommand{ID: "q1", Label: "磁盘", Command: "df -h"})
	if err != nil || len(cmds) != 1 {
		t.Fatalf("commands %v, %v", cmds, err)
	}
	if len(d.widgets) != 2 || d.widgets[1]["type"] != "quick_commands" || d.widgets[1]["grid_y"] != float64(8) || d.widgets[0]["id"] != "w1" {
		t.Fatalf("widgets %v", d.widgets)
	}
	// A config's other settings stay.
	cfg := `{"customCommands":[{"id":"q2","label":"日志","command":"journalctl -n 50"},{"id":"bad","label":"","command":"x"}],"targetMode":"all"}`
	d.widgets = append(d.widgets, map[string]any{"id": "w3", "type": "quick_commands", "config_json": cfg, "grid_x": 3, "grid_y": 8, "grid_w": 3, "grid_h": 6})

	cmds, _ = c.SaveQuickCommand(ctx, QuickCommand{ID: "q2", Label: "日志", Command: "journalctl -n 100"})
	want := []QuickCommand{{"q1", "磁盘", "df -h"}, {"q2", "日志", "journalctl -n 100"}}
	if !reflect.DeepEqual(cmds, want) {
		t.Fatalf("commands %v", cmds)
	}
	var got struct{ TargetMode string }
	json.Unmarshal([]byte(d.widgets[2]["config_json"].(string)), &got)
	if got.TargetMode != "all" {
		t.Errorf("config %v", d.widgets[2]["config_json"])
	}

	cmds, err = c.DeleteQuickCommand(ctx, "q1")
	if err != nil || !reflect.DeepEqual(cmds, want[1:]) {
		t.Fatalf("commands %v, %v", cmds, err)
	}
	if _, err := c.DeleteQuickCommand(ctx, "q1"); err == nil {
		t.Error("deleted twice")
	}
}
