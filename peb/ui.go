package peb

import "math"

// UIKind selects a host-owned surface. Menus are anchored to an action; dialogs
// are modal; panels live in the resizable workbench bottom area.
type UIKind uint32

const (
	Dialog UIKind = iota
	ContextMenu
	Panel
	Popup        // Nonmodal floating window; workspace interaction remains available.
	Notification // Bottom-right message stack; never takes focus when opened.
)

// UIPosition selects the origin for Placement.X/Y, in workspace CSS pixels.
type UIPosition uint32

const (
	PositionDefault  UIPosition = iota // Center dialogs/popups; anchor context menus.
	PositionAbsolute                   // Workspace top-left.
	PositionAnchor                     // Action's bottom-left, falling back to its captured location.
)

// UIPlacement controls an overlay's position and size. X/Y offset the selected
// origin. Zero Width/Height use the surface's default size. The host constrains
// overlays to the viewport. Panels use their workbench layout instead.
type UIPlacement struct {
	Mode                UIPosition
	X, Y, Width, Height float32
}

func (p UIPlacement) valid(kind UIKind) bool {
	if p.Mode > PositionAnchor || p.Width < 0 || p.Height < 0 || (kind == Panel || kind == Notification) && p != (UIPlacement{}) {
		return false
	}
	for _, n := range [...]float32{p.X, p.Y, p.Width, p.Height} {
		if math.IsNaN(float64(n)) || math.IsInf(float64(n), 0) {
			return false
		}
	}
	return true
}

// UI replaces a surface's description. A nil View retains its controls, focus
// and scroll. Anchor is a local action ID. Close removes the surface.
// Empty Context.ID inherits the current Action/UIEvent context; background
// workers should pass their saved Context explicitly. Title names the surface.
type UI struct {
	ID, Title, Anchor string
	Kind              UIKind
	Placement         UIPlacement
	View              *EditorView
	Context           ActionContext
	Close             bool
	// CloseOnOutsideClick dismisses a Popup on an outside pointer press.
	// False keeps it open. ContextMenu always dismisses on outside presses.
	CloseOnOutsideClick bool
	// AutoCloseMS dismisses a Notification after this many milliseconds. Zero
	// keeps it open until closed by the user or extension. Updates reset the timer.
	AutoCloseMS uint32
}

// UIEvent carries DOM events and "open", "close", "visibilitychange".
// visibilitychange uses Event.Value="visible" or "hidden".
// Close reasons are "extension", "button", "escape", "outside", "replace", "timeout".
// Context is the action invocation that opened this surface.
type UIEvent struct {
	ID, Reason string
	Context    ActionContext
	Event      EditorEvent
}

func EncodeUI(dst []byte, v UI) ([]byte, error) {
	e := encoder{data: dst[:0]}
	if !v.valid() {
		e.err = ErrProtocol
	}
	e.str(v.ID)
	e.str(v.Title)
	e.str(v.Anchor)
	e.number(uint32(v.Kind))
	e.number(boolNumber(v.Close))
	e.number(uint32(v.Placement.Mode))
	e.number(math.Float32bits(v.Placement.X))
	e.number(math.Float32bits(v.Placement.Y))
	e.number(math.Float32bits(v.Placement.Width))
	e.number(math.Float32bits(v.Placement.Height))
	e.number(boolNumber(v.CloseOnOutsideClick))
	e.number(v.AutoCloseMS)
	encodeActionContext(&e, v.Context)
	encodeView(&e, v.View)
	return e.data, e.err
}

func DecodeUI(data []byte) (UI, error) {
	if len(data) > MaxPayload {
		return UI{}, ErrProtocol
	}
	d := decoder{data: data}
	v := UI{ID: d.str(), Title: d.str(), Anchor: d.str(), Kind: UIKind(d.number()), Close: d.boolean()}
	v.Placement = UIPlacement{Mode: UIPosition(d.number()), X: math.Float32frombits(d.number()), Y: math.Float32frombits(d.number()), Width: math.Float32frombits(d.number()), Height: math.Float32frombits(d.number())}
	v.CloseOnOutsideClick = d.boolean()
	v.AutoCloseMS = d.number()
	v.Context, v.View = decodeActionContext(&d), decodeView(&d)
	if !v.valid() {
		d.err = ErrProtocol
	}
	return v, d.done()
}

func (v UI) valid() bool {
	return v.Kind <= Notification && v.Placement.valid(v.Kind) && v.ID != "" && (v.Close || v.Title != "") && (v.AutoCloseMS == 0 || v.Kind == Notification)
}

func EncodeUIEvent(dst []byte, v UIEvent) ([]byte, error) {
	e := encoder{data: dst[:0]}
	e.str(v.ID)
	e.str(v.Reason)
	encodeActionContext(&e, v.Context)
	encodeEvent(&e, v.Event)
	return e.data, e.err
}

func DecodeUIEvent(data []byte) (UIEvent, error) {
	if len(data) > MaxPayload {
		return UIEvent{}, ErrProtocol
	}
	d := decoder{data: data}
	v := UIEvent{ID: d.str(), Reason: d.str(), Context: decodeActionContext(&d), Event: decodeEvent(&d)}
	return v, d.done()
}

// Show opens or updates a surface from an action handler or background worker.
func (p *ActionPublisher) Show(ui UI) error {
	if !p.ui {
		return ErrProtocol
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if ui.Context.ID == "" {
		ui.Context = p.action
	}
	var err error
	p.data, err = EncodeUI(p.data, ui)
	if err != nil {
		return err
	}
	return p.writer.Write(UIUpdate, 0, p.data)
}

func (p *ActionPublisher) CloseUI(id string) error {
	return p.Show(UI{ID: id, Close: true})
}
