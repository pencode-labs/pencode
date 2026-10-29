package workbench

import (
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
	nethtml "golang.org/x/net/html"
)

const pencodeTooltipDelay = 450 * time.Millisecond

type pencodeTooltip struct {
	control    ecs.Entity
	revision   uint64
	id, text   string
	due        time.Time
	open       bool
	dismissed  bool
	x, y       float32
	positioned bool
}

// Give anonymous template buttons stable IDs so native hover state can resolve
// their retained nodes. Authored IDs and IDs supplied by renderers take priority.
func tooltipTemplate(source, prefix string) htmlui.Template {
	z := nethtml.NewTokenizer(strings.NewReader(source))
	var b strings.Builder
	index := 0
	for {
		kind := z.Next()
		if kind == nethtml.ErrorToken {
			break
		}
		raw := string(z.Raw())
		if kind == nethtml.StartTagToken {
			token := z.Token()
			if token.Data == "button" {
				id, ref := "", ""
				for _, a := range token.Attr {
					switch a.Key {
					case "id":
						id = a.Val
					case "data-ref":
						ref = a.Val
					}
				}
				if id == "" {
					if ref == "" {
						ref = "button-" + strconv.Itoa(index)
					}
					b.WriteString(strings.TrimSuffix(raw, ">"))
					b.WriteString(` id="`)
					b.WriteString(html.EscapeString(prefix + ref))
					b.WriteString(`">`)
					index++
					continue
				}
			}
		}
		b.WriteString(raw)
	}
	return htmlui.NewTemplate(b.String())
}

// Read the current title rather than retaining extension descriptions across
// view updates. Serialization is confined to entering or updating a button;
// stationary hover and positioning do not allocate.
func tooltipText(node htmlui.Node) string {
	source, err := node.Serialize()
	if err != nil {
		return ""
	}
	z := nethtml.NewTokenizer(strings.NewReader(source))
	if z.Next() != nethtml.StartTagToken {
		return ""
	}
	token := z.Token()
	var title, label string
	for _, a := range token.Attr {
		switch a.Key {
		case "class":
			for class := range strings.FieldsSeq(a.Val) {
				if class == "pencode-window-control" {
					return ""
				}
			}
		case "title":
			title = strings.TrimSpace(a.Val)
		case "aria-label":
			label = strings.TrimSpace(a.Val)
		}
	}
	if title != "" {
		return title
	}
	return label
}

func (model *workspaceModel) renderTooltip(view *htmlui.View) {
	t := &model.tooltip
	view.Text("tooltip", t.text)
	setPanelVisible(view, "tooltip", t.open)
}

func (model *workspaceModel) syncTooltip(world *module.Content, now time.Time) {
	state, ok := world.Resource[*input.State]()
	if !ok || state == nil {
		return
	}
	t := &model.tooltip
	previous := t.open
	p := state.Pointer()
	var control ecs.Entity
	var button htmlui.Control
	if p.Device == input.PointerDeviceMouse {
		for entity, pair := range world.Query2[htmlui.Control, scene.InputState2D]() {
			c, pointer := pair.A.Get(), pair.B.Get()
			if c.Owner == model.surface && c.Tag == "button" && c.ID != "" && pointer.Hovered {
				control, button = entity, c
				break
			}
		}
	}
	if control == 0 {
		*t = pencodeTooltip{}
	} else if control != t.control || button.RenderRevision != t.revision {
		node, found := world.HTMLNode(model.surface, button.ID)
		label := ""
		if found {
			label = tooltipText(node)
		}
		if control != t.control || label != t.text {
			*t = pencodeTooltip{control: control, id: button.ID, text: label, due: now.Add(pencodeTooltipDelay)}
		}
		t.revision = button.RenderRevision
	}
	if p.Down || p.Pressed || p.Released || p.WheelX != 0 || p.WheelY != 0 || model.uiKey(state, input.KeyEscape) {
		t.open, t.dismissed = false, true
	}
	if t.control != 0 && t.text != "" && !t.open && !t.dismissed {
		if now.Before(t.due) {
			if model.app != nil && model.app.WakeAt != nil {
				model.app.WakeAt(t.due)
			}
		} else {
			t.open = true
		}
	}
	popup, found := world.HTMLNode(model.surface, "pencode-tooltip")
	if !found {
		return
	}
	if previous != t.open {
		popup.SetText(t.text)
		popup.SetHidden(!t.open)
	}
	if !t.open {
		return
	}
	position := world.Ref[scene.UIGlobalTransform](t.control)
	buttonSize := world.Ref[scene.Size2D](t.control)
	root, _ := world.HTMLNode(model.surface, "pencode-root")
	viewport, _ := root.Bounds()
	size, measured := popup.Bounds()
	if position == nil || buttonSize == nil || !measured {
		return
	}
	zoom := max(float32(model.settings.Zoom)/100, .01)
	bounds := htmlui.Rect{X: position.Matrix[12] / zoom, Y: position.Matrix[13] / zoom, Height: buttonSize.Height * position.Matrix[5] / zoom}
	x := max(8, min(bounds.X, viewport.Width-size.Width-8))
	y := bounds.Y + bounds.Height + 6
	if y+size.Height > viewport.Height-8 {
		y = bounds.Y - size.Height - 6
	}
	y = max(8, y)
	if !t.positioned || t.x != x || t.y != y {
		t.x, t.y, t.positioned = x, y, true
		world.HTMLSetOffset(model.surface, "pencode-tooltip", x, y)
	}
}
