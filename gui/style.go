package gui

import (
	"github.com/egoist/mygo/ui"
)

const (
	titleBarHeight = 52 // the height of the inset title bar, which holds the site tabs
	sidebarWidth   = 264
)

func icon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round">` + shapes + `</svg>`))
}

var (
	plusIcon       = icon(`<path d="M8 3v10M3 8h10"/>`)
	closeIcon      = icon(`<path d="M4.5 4.5l7 7M11.5 4.5l-7 7"/>`)
	refreshIcon    = icon(`<path d="M13 8a5 5 0 1 1-1.5-3.6"/><path d="M13 2.5v3h-3"/>`)
	serverIcon     = icon(`<rect x="2.5" y="2.5" width="11" height="4.5" rx="1.2"/><rect x="2.5" y="9" width="11" height="4.5" rx="1.2"/><path d="M5 4.75h.01M5 11.25h.01"/>`)
	folderIcon     = icon(`<path d="M2 4.5A1.5 1.5 0 0 1 3.5 3h2.6l1.4 1.5h5A1.5 1.5 0 0 1 14 6v5.5a1.5 1.5 0 0 1-1.5 1.5h-9A1.5 1.5 0 0 1 2 11.5z"/>`)
	laptopIcon     = icon(`<rect x="3" y="3.5" width="10" height="7" rx="1"/><path d="M1.5 12.5h13"/>`)
	terminalIcon   = icon(`<rect x="1.5" y="2.5" width="13" height="11" rx="2"/><path d="M4.5 6.5l2 1.5-2 1.5M8 10h3"/>`)
	searchIcon     = icon(`<circle cx="7" cy="7" r="4.5"/><path d="M10.5 10.5L14 14"/>`)
	moreIcon       = icon(`<path d="M3.5 8h.01M8 8h.01M12.5 8h.01" stroke-width="2.2"/>`)
	chevronIcon    = icon(`<path d="M6 4l4 4-4 4"/>`)
	expandIcon     = icon(`<path d="M5 6l3-3 3 3M5 10l3 3 3-3"/>`)
	collapseIcon   = icon(`<path d="M5 3l3 3 3-3M5 13l3-3 3 3"/>`)
	folderPlusIcon = icon(`<path d="M2 4.5A1.5 1.5 0 0 1 3.5 3h2.6l1.4 1.5h5A1.5 1.5 0 0 1 14 6v5.5a1.5 1.5 0 0 1-1.5 1.5h-9A1.5 1.5 0 0 1 2 11.5z"/><path d="M8 6.8v3.9M6.05 8.75h3.9"/>`)
	keyIcon        = icon(`<circle cx="5.5" cy="10.5" r="3"/><path d="M7.6 8.4L13.5 2.5M11.5 4.5l1.5 1.5"/>`)
)

// panel is the background of what floats over the window, dialogs and
// notices: nearly opaque. Glass would bend the text under its edges into
// smears, and over Windows' Mica, a transparent window, it draws a halo.
func panel(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.RGBA(44, 44, 50, 0.97)
	}
	return ui.RGBA(250, 250, 252, 0.97)
}

// frost is a panel straight over the window's material, which the system
// blurs from the desktop behind the window: a lighter (or darker) veil of
// it. Glass, which blurs what the window draws, would find nothing there.
func frost(c *ui.Context, strength float32) ui.Color {
	if c.Theme().Dark {
		return ui.RGBA(30, 30, 34, 0.30+0.45*strength)
	}
	return ui.RGBA(255, 255, 255, 0.30+0.45*strength)
}

// hairline is the border of glass panels.
func hairline(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.RGBA(255, 255, 255, 0.10)
	}
	return ui.RGBA(0, 0, 0, 0.08)
}

// hover is the background of a row under the pointer.
func hover(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.RGBA(255, 255, 255, 0.08)
	}
	return ui.RGBA(0, 0, 0, 0.05)
}

// selected is the background of the row chosen.
func selected(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.RGBA(255, 255, 255, 0.14)
	}
	return ui.RGBA(0, 0, 0, 0.09)
}

func healthColor(h health) ui.Color {
	switch h {
	case healthUp:
		return ui.Hex("#30d158")
	case healthDown:
		return ui.Hex("#ff453a")
	}
	return ui.Hex("#8e8e93")
}

// dot is a small round status light.
func dot(c *ui.Context, color ui.Color) ui.Element {
	return ui.Box(c).Size(7, 7).Radius(4).Background(color).Shrink(0)
}

// iconButton is a borderless button showing only an icon.
func iconButton(c *ui.Context, svg *ui.SVG, label string) ui.Element {
	b := ui.ButtonBase(c).Size(26, 26).Radius(7).Center().Label(label).Tooltip(label)
	if b.Hovered() {
		b.Background(hover(c))
	}
	b.Children(func() { ui.Icon(c, svg).Size(14, 14).TextColor(c.Theme().TextMuted) })
	return b
}
