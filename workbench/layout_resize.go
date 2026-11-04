package workbench

import (
	"strconv"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	htmlui "github.com/diiyw/onix/ui/html"
)

type pencodeResizeTarget uint8

const (
	pencodeResizeNone pencodeResizeTarget = iota
	pencodeResizeSidebar
	pencodeResizeBottom
)

type pencodeWorkbenchResize struct {
	target       pencodeResizeTarget
	origin, size float32
}

func (m *workspaceModel) updateWorkbenchResize(world *module.Content, state *input.State) bool {
	r := &m.resize
	previous := r.target
	if m.screen != pencodeWorkspace || m.settingsOpen || m.quickOpen || m.folderSearch.open || m.fileContext.open || m.fileContext.dialog != "" || m.closing.action != pencodeCloseNone || m.market != nil && m.market.open || m.actions.ui.overlay != nil || m.sourceControl.modal() {
		r.target = pencodeResizeNone
		return previous != r.target
	}
	p := state.Pointer()
	zoom := max(float32(m.settings.Zoom)/100, .01)
	x, y := p.X/zoom, p.Y/zoom
	if p.Pressed {
		r.target = pencodeResizeNone
		px, py := p.PressX/zoom, p.PressY/zoom
		ref := ""
		switch {
		case !m.sidebarHidden && m.pointerInsideRefs(world, px, py, []string{"sidebar-resize"}):
			r.target, r.origin, ref = pencodeResizeSidebar, px, "sidebar"
		case m.bottom.active != pencodePanelClosed && m.pointerInsideRefs(world, px, py, []string{"panel-resize"}):
			r.target, r.origin, ref = pencodeResizeBottom, py, "bottom-panel"
		}
		if r.target != pencodeResizeNone {
			node, _ := world.HTMLNode(m.surface, ref)
			bounds, _ := node.Bounds()
			r.size = bounds.Width
			if r.target == pencodeResizeBottom {
				r.size = bounds.Height
			}
		}
	}
	if r.target == pencodeResizeSidebar && m.sidebarHidden || r.target == pencodeResizeBottom && m.bottom.active == pencodePanelClosed || !p.Down && !p.Released {
		r.target = pencodeResizeNone
	}
	changed := previous != r.target
	if r.target == pencodeResizeNone {
		return changed
	}
	workspace, _ := world.HTMLNode(m.surface, "workspace")
	bounds, _ := workspace.Bounds()
	switch r.target {
	case pencodeResizeSidebar:
		limit := max(220, min(bounds.Width*.6, bounds.Width-206))
		width := int(min(max(220, r.size+x-r.origin), limit))
		style := paneSizeStyle(m.sidebarWidthStyle, "flex-basis:", width, "px;")
		changed = changed || style != m.sidebarWidthStyle
		m.sidebarWidthStyle = style
	case pencodeResizeBottom:
		limit := max(100, min(bounds.Height*.75, bounds.Height-160))
		height := int(min(max(100, r.size+r.origin-y), limit))
		style := paneSizeStyle(m.bottom.heightStyle, "height:", height, "px;max-height:75%;")
		changed = changed || style != m.bottom.heightStyle
		m.bottom.heightStyle = style
	}
	if p.Released {
		r.target = pencodeResizeNone
		changed = true
	}
	return changed
}

// Reuse the string on stationary frames; changing a dimension allocates once.
func paneSizeStyle(previous, property string, size int, suffix string) string {
	var buffer [80]byte
	b := append(buffer[:0], property...)
	b = strconv.AppendInt(b, int64(size), 10)
	b = append(b, suffix...)
	if previous == string(b) {
		return previous
	}
	return string(b)
}

func (m *workspaceModel) renderWorkbenchResize(view *htmlui.View) {
	sidebar, bottom := "pencode-sidebar-resize", "pencode-panel-resize"
	switch m.resize.target {
	case pencodeResizeSidebar:
		sidebar = "pencode-sidebar-resize resizing"
	case pencodeResizeBottom:
		bottom = "pencode-panel-resize resizing"
	}
	view.Attr("sidebar-resize", "class", sidebar)
	view.Attr("panel-resize", "class", bottom)
}
