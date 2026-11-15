package workbench

import (
	"math"
	"strconv"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	htmlui "github.com/onix-engine/onix/ui/html"
)

const imageZoomStep = 1.2

type pencodeImageView struct {
	style, label                  string
	width, height                 float32
	viewportWidth, viewportHeight float32
	scale, x, y                   float32
	fit, dragging                 bool
}

func (v *pencodeImageView) layout(width, height float32) {
	v.viewportWidth, v.viewportHeight = width, height
	if v.fit {
		v.scale = min(1, max(1, width-48)/v.width, max(1, height-48)/v.height)
		v.x, v.y = 0, 0
	}
	v.x = imagePan(v.x, v.width*v.scale, width)
	v.y = imagePan(v.y, v.height*v.scale, height)
}

func imagePan(offset, size, viewport float32) float32 {
	limit := max(0, (size-viewport)/2)
	return min(limit, max(-limit, offset))
}

func (v *pencodeImageView) zoom(scale, x, y float32) {
	// A fitted large image may start below 1%; keep its first zoom step smooth.
	scale = min(32, max(min(.01, v.scale), scale))
	if v.scale <= 0 || scale == v.scale {
		return
	}
	ratio := scale / v.scale
	v.x = x - v.viewportWidth/2 - (x-v.viewportWidth/2-v.x)*ratio
	v.y = y - v.viewportHeight/2 - (y-v.viewportHeight/2-v.y)*ratio
	v.scale, v.fit = scale, false
	v.layout(v.viewportWidth, v.viewportHeight)
}

func (v *pencodeImageView) action(ref string) {
	switch ref {
	case "image-zoom-in":
		v.zoom(v.scale*imageZoomStep, v.viewportWidth/2, v.viewportHeight/2)
	case "image-zoom-out":
		v.zoom(v.scale/imageZoomStep, v.viewportWidth/2, v.viewportHeight/2)
	case "image-actual":
		v.scale, v.x, v.y, v.fit = 1, 0, 0, false
	case "image-fit":
		v.fit = true
	}
	v.layout(v.viewportWidth, v.viewportHeight)
}

func (v *pencodeImageView) render(target *htmlui.View) {
	target.Attr("image", "style", v.style)
	target.Text("image-zoom", v.label)
	for _, ref := range [...]string{"image-zoom-out", "image-zoom-in", "image-actual", "image-fit"} {
		target.Event(ref, htmlui.EventClick)
	}
}

func (m *workspaceModel) imageDocument() *pencodeCustomDocument {
	if m.screen != pencodeWorkspace || m.sourceState().visible() {
		return nil
	}
	doc := m.customDocuments[m.activeFile()]
	if doc == nil || doc.provider != &pencodeImageEditor || doc.definition == nil || doc.image == nil {
		return nil
	}
	return doc
}

// Update retained geometry only when it changes. Idle frames reuse both strings.
func (m *workspaceModel) syncImagePreview(world *module.Content) bool {
	doc := m.imageDocument()
	if doc == nil {
		return false
	}
	node, ok := world.HTMLNode(m.surface, "pencode-extension-image-container")
	bounds, visible := node.Bounds()
	if !ok || !visible || bounds.Width <= 0 || bounds.Height <= 0 {
		return false
	}
	v := doc.image
	v.layout(bounds.Width, bounds.Height)
	var buffer [160]byte
	b := buffer[:0]
	for _, field := range [...]struct {
		name  string
		value float32
	}{
		{"width:", v.width * v.scale},
		{"height:", v.height * v.scale},
		{"left:", (bounds.Width-v.width*v.scale)/2 + v.x},
		{"top:", (bounds.Height-v.height*v.scale)/2 + v.y},
	} {
		b = append(b, field.name...)
		b = strconv.AppendFloat(b, float64(field.value), 'f', 3, 32)
		b = append(b, "px;"...)
	}
	changed := false
	if v.style != string(b) {
		v.style = string(b)
		if image, ok := world.HTMLNode(m.surface, "pencode-extension-image"); ok {
			changed = image.SetAttribute("style", v.style)
		}
	}
	b = strconv.AppendFloat(buffer[:0], float64(v.scale*100), 'f', 1, 32)
	b = append(b, '%')
	if v.label != string(b) {
		v.label = string(b)
		if label, ok := world.HTMLNode(m.surface, "pencode-extension-image-zoom"); ok {
			changed = label.SetText(v.label) || changed
		}
	}
	return changed
}

func (m *workspaceModel) updateImagePointer(world *module.Content, state *input.State) {
	doc := m.imageDocument()
	if doc == nil {
		return
	}
	v := doc.image
	p := state.Pointer()
	blocked := m.settingsOpen || m.quickOpen || m.findOpen || m.closing.action != pencodeCloseNone || m.fileContext.open || m.languageUI.contextOpen || m.tabContext.open || m.folderSearch.open || m.market != nil && m.market.open
	if !p.Down && !p.Released || blocked {
		v.dragging = false
	}
	if blocked {
		return
	}
	if p.WheelY == 0 && !p.Pressed && !v.dragging {
		return
	}
	node, ok := world.HTMLNode(m.surface, "pencode-extension-image-container")
	bounds, visible := node.Bounds()
	if !ok || !visible || bounds.Width <= 0 || bounds.Height <= 0 {
		v.dragging = false
		return
	}
	uiScale := max(float32(m.settings.Zoom)/100, .01)
	x, y := p.X/uiScale, p.Y/uiScale
	inside := languageRectContains(bounds, x, y)
	if p.Pressed && languageRectContains(bounds, p.PressX/uiScale, p.PressY/uiScale) {
		v.dragging = true
	}
	if p.WheelY != 0 && inside {
		v.zoom(v.scale*float32(math.Pow(imageZoomStep, float64(min(20, max(-20, p.WheelY))))), x-bounds.X, y-bounds.Y)
		state.ConsumePointerWheel()
	}
	if v.dragging && !p.Pressed {
		v.x += p.DeltaX / uiScale
		v.y += p.DeltaY / uiScale
		v.layout(bounds.Width, bounds.Height)
	}
	if p.Released {
		v.dragging = false
	}
}
