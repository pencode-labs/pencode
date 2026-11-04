package workbench

import (
	"math"
	"slices"
	"strconv"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
)

type sourceOverview struct {
	styles     []string
	thumbStyle string
	dragTab    *sourceTab
	grab       float32
}

func (m *workspaceModel) renderSourceOverview(b *sourceVNodeView, tab *sourceTab) {
	o := &m.sourceState().overview
	b.open("div", "overview", "id", "sc-diff-overview", "class", "sc-diff-overview", "aria-label", "File diff overview", "title", "Click a change to reveal it; drag the slider to scroll")
	b.open("div", "markers", "class", "sc-diff-overview-markers", "aria-hidden", "true")
	o.styles = slices.Grow(o.styles, max(0, len(tab.Blocks)-len(o.styles)))[:len(tab.Blocks)]
	for i, block := range tab.Blocks {
		class := "sc-diff-overview-marker modified"
		if block.LeftStart == block.LeftEnd {
			class = "sc-diff-overview-marker added"
		} else if block.RightStart == block.RightEnd {
			class = "sc-diff-overview-marker deleted"
		}
		o.styles[i] = sourceOverviewMarkerStyle(o.styles[i], block, len(tab.Rows))
		b.openKey("span", uint64(i+1), "class", class, "style", o.styles[i])
		b.close()
	}
	b.close()
	b.text("div", "thumb", "", "id", "sc-diff-overview-thumb", "class", "sc-diff-overview-thumb", "style", o.thumbStyle, "aria-hidden", "true")
	b.close()
}

func sourceOverviewMarkerStyle(previous string, block sourceDiffBlock, rows int) string {
	var buffer [80]byte
	b := append(buffer[:0], "top:"...)
	b = strconv.AppendFloat(b, float64(block.FirstRow)*100/float64(max(1, rows)), 'f', 5, 64)
	b = append(b, "%;height:"...)
	b = strconv.AppendFloat(b, float64(block.EndRow-block.FirstRow)*100/float64(max(1, rows)), 'f', 5, 64)
	b = append(b, "%;"...)
	if previous == string(b) {
		return previous
	}
	return string(b)
}

type sourceOverviewGeometry struct {
	height, limit, thumb, top float32
}

func sourceOverviewLayout(height, viewport, content, scrollY float32) sourceOverviewGeometry {
	g := sourceOverviewGeometry{height: max(0, height), limit: max(0, content-viewport)}
	thumb := max(min(24, g.height/2), g.height*viewport/max(1, content))
	g.thumb = min(g.height, float32(math.Ceil(float64(thumb))))
	if g.limit > 0 {
		g.top = min(g.height-g.thumb, max(0, -scrollY/g.limit*(g.height-g.thumb)))
	}
	return g
}

func (g sourceOverviewGeometry) scrollY(top float32) float32 {
	if g.height <= g.thumb {
		return 0
	}
	return -min(1, max(0, top/(g.height-g.thumb))) * g.limit
}

func sourceOverviewMarkerBounds(block sourceDiffBlock, rows int, height float32) (float32, float32) {
	// The marker track leaves three pixels below its percentage scale, keeping
	// even a one-line change at EOF visible in a very large file.
	scale := max(0, height-3) / float32(max(1, rows))
	top := float32(block.FirstRow) * scale
	return top, min(height-top, max(3, float32(block.EndRow-block.FirstRow)*scale))
}

func (m *workspaceModel) sourceOverviewGeometry(world *module.Content, tab *sourceTab) (htmlui.Rect, sourceOverviewGeometry, bool) {
	node, ok := world.HTMLNode(m.surface, "sc-diff-overview")
	if !ok {
		return htmlui.Rect{}, sourceOverviewGeometry{}, false
	}
	bounds, ok := node.Bounds()
	code, codeOK := world.HTMLNode(m.surface, "sc-code-right")
	area, areaOK := code.ScrollContainer()
	scroll := world.Ref[scene.UIScroll](area)
	if !ok || !codeOK || !areaOK || scroll == nil || bounds.Height <= 0 {
		return bounds, sourceOverviewGeometry{}, false
	}
	return bounds, sourceOverviewLayout(bounds.Height, scroll.ViewportSize[1], scroll.ContentSize[1], tab.ScrollY), true
}

func (m *workspaceModel) syncSourceOverview(world *module.Content) {
	s := m.sourceState()
	tab := s.activeTab()
	if s.overview.dragTab != tab {
		s.overview.dragTab = nil
	}
	if !s.visible() || tab == nil || tab.Binary || tab.Kind == "merge" || tab.SubmoduleSummary != "" {
		s.overview.dragTab = nil
		return
	}
	_, g, ok := m.sourceOverviewGeometry(world, tab)
	if !ok {
		return
	}
	style := paneSizeStyle(s.overview.thumbStyle, "height:", int(g.thumb), "px;")
	if style != s.overview.thumbStyle {
		s.overview.thumbStyle = style
		if node, ok := world.HTMLNode(m.surface, "sc-diff-overview-thumb"); ok {
			node.SetAttribute("style", style)
		}
		if m.app != nil {
			m.app.RequestRender()
		}
	}
	world.HTMLSetOffset(m.surface, "sc-diff-overview-thumb", 0, g.top)
}

func (m *workspaceModel) updateSourceOverview(world *module.Content, state *input.State) bool {
	s := m.sourceState()
	o, tab := &s.overview, s.activeTab()
	if world == nil || tab == nil || !s.visible() || s.modal() || m.settingsOpen || m.quickOpen || m.folderSearch.open || tab.Binary || tab.Kind == "merge" || tab.SubmoduleSummary != "" {
		o.dragTab = nil
		return false
	}
	p := state.Pointer()
	if o.dragTab != tab || !p.Down && !p.Released {
		o.dragTab = nil
	}
	if !p.Pressed && o.dragTab == nil {
		return false
	}
	bounds, g, ok := m.sourceOverviewGeometry(world, tab)
	if !ok {
		o.dragTab = nil
		return false
	}
	zoom := max(float32(m.settings.Zoom)/100, .01)
	y := p.Y/zoom - bounds.Y
	next := tab.ScrollY
	if p.Pressed {
		o.dragTab = nil
		px, py := p.PressX/zoom, p.PressY/zoom
		if !languageRectContains(bounds, px, py) {
			return false
		}
		pressY := py - bounds.Y
		// A separate marker lane remains clickable even under the viewport thumb.
		index, distance := -1, float32(math.MaxFloat32)
		if px >= bounds.X+6 {
			for i, block := range tab.Blocks {
				top, height := sourceOverviewMarkerBounds(block, len(tab.Rows), g.height)
				d := float32(math.Abs(float64(pressY - top - height/2)))
				if pressY >= top-2 && pressY <= top+height+2 && d < distance {
					index, distance = i, d
				}
			}
		}
		if index >= 0 {
			next = -float32(max(0, tab.Blocks[index].FirstRow-3) * 20)
		} else {
			o.dragTab, o.grab = tab, pressY-g.top
			if pressY < g.top || pressY > g.top+g.thumb {
				o.grab = g.thumb / 2
			}
		}
	}
	if o.dragTab == tab {
		next = g.scrollY(y - o.grab)
	}
	if p.Released {
		o.dragTab = nil
	}
	next = min(0, max(-g.limit, next))
	for _, id := range [...]string{"sc-code-left", "sc-code-right"} {
		if node, ok := world.HTMLNode(m.surface, id); ok {
			if area, ok := node.ScrollContainer(); ok {
				if scroll := world.Mut[scene.UIScroll](area); scroll != nil {
					scroll.Position[1], scroll.Momentum[1] = next, 0
				}
			}
		}
	}
	if next == tab.ScrollY {
		return false
	}
	tab.ScrollY = next
	if first := sourceFirstVisibleRow(next); first != tab.FirstRow {
		tab.FirstRow = first
		m.sourceChanged()
		return true
	}
	if m.app != nil {
		m.app.RequestRender()
	}
	return false
}
