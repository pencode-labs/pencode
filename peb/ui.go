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
	EditorTab    // Document-independent tab in the editor area.
)

// NotificationSeverity selects the notification's theme colors and status icon.
type NotificationSeverity uint32

const (
	NotificationInfo NotificationSeverity = iota
	NotificationWarning
	NotificationError
)

// UIPosition selects the origin for Placement.X/Y, in workspace CSS pixels.
type UIPosition uint32

const (
	PositionDefault     UIPosition = iota // Center dialogs/popups; anchor context menus.
	PositionAbsolute                      // Workspace top-left.
	PositionAnchor                        // Action's bottom-left, falling back to its captured location.
	PositionAnchorAbove                   // Above the anchor's top-left; falls below when space is insufficient.
)

// UIPlacement controls an overlay's position and size. X/Y offset the selected
// origin. Zero Width/Height use the surface's default size. The host constrains
// overlays to the viewport. Panels use their workbench layout instead.
type UIPlacement struct {
	Mode                UIPosition
	X, Y, Width, Height float32
}

func (p UIPlacement) valid(kind UIKind) bool {
	if p.Mode > PositionAnchorAbove || p.Width < 0 || p.Height < 0 || (kind == Panel || kind == Notification || kind == EditorTab) && p != (UIPlacement{}) {
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
// and scroll. Anchor is a local action ID, or "ui:<surface ID>#<data-ref>" for
// a focusable control in another surface owned by this extension.
// Close removes the surface.
// Empty Context.ID inherits the current Action/UIEvent context; background
// workers should pass their saved Context explicitly. Title names the surface.
// EditorTab opens selected. Updates with a View retain selection; a nil View
// explicitly reveals an existing tab. Closing a tab emits the usual close event.
type UI struct {
	ID, Title, Anchor string
	Kind              UIKind
	Placement         UIPlacement
	View              *EditorView
	// ViewVersion identifies a complete view or the result of Patch. Patches
	// replace only existing region contents; structure, CSS or event changes
	// require a full View. ActionPublisher manages these fields automatically.
	ViewVersion uint32
	Patch       *ViewPatch
	Context     ActionContext
	Close       bool
	// CloseOnOutsideClick dismisses a Popup on an outside pointer press.
	// False keeps it open. ContextMenu always dismisses on outside presses.
	CloseOnOutsideClick bool
	// AutoCloseMS dismisses a Notification after this many milliseconds. Zero
	// keeps it open until closed by the user or extension. Updates reset the timer.
	AutoCloseMS uint32
	// Severity applies only to Notification. Zero uses the information style.
	Severity NotificationSeverity
	// Busy replaces an EditorTab's close icon with a processing animation.
	Busy bool
}

// UIEvent carries DOM events and "open", "close", "visibilitychange", "projectchange".
// visibilitychange uses Event.Value="visible" or "hidden".
// projectchange notifies EditorTab surfaces when the active project changes.
// Its Context contains the new ProjectPath and clears file-specific fields.
// Close reasons are "extension", "button", "escape", "outside", "replace", "timeout".
// Context is the action invocation that opened this surface, updated by projectchange.
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
	e.number(uint32(v.Severity))
	e.number(boolNumber(v.Busy))
	encodeActionContext(&e, v.Context)
	encodeView(&e, v.View)
	e.number(v.ViewVersion)
	e.number(boolNumber(v.Patch != nil))
	if v.Patch != nil {
		e.number(v.Patch.Base)
		e.count(len(v.Patch.Regions))
		for _, region := range v.Patch.Regions {
			e.str(region.Ref)
			e.text(region.HTML)
		}
	}
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
	v.Severity = NotificationSeverity(d.number())
	v.Busy = d.boolean()
	v.Context, v.View = decodeActionContext(&d), decodeView(&d)
	v.ViewVersion = d.number()
	if d.boolean() {
		v.Patch = &ViewPatch{Base: d.number(), Regions: make([]ViewRegion, d.count())}
		for i := range v.Patch.Regions {
			v.Patch.Regions[i] = ViewRegion{Ref: d.str(), HTML: d.text()}
		}
	}
	if !v.valid() {
		d.err = ErrProtocol
	}
	return v, d.done()
}

func (v UI) valid() bool {
	return v.Kind <= EditorTab && v.Placement.valid(v.Kind) && v.ID != "" && (v.Close || v.Title != "") && (v.AutoCloseMS == 0 || v.Kind == Notification) &&
		v.Severity <= NotificationError && (v.Severity == NotificationInfo || v.Kind == Notification) &&
		(v.Patch == nil || v.View == nil && !v.Close && v.Patch.Base != 0 && v.Patch.Base < ^uint32(0) && v.ViewVersion == v.Patch.Base+1)
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
	return p.show(ui)
}

// show holds mu through encoding, writing and committing the new base. A failed
// write must never advance the delta state ahead of the receiver.
func (p *ActionPublisher) show(ui UI) error {
	wire, state, err := p.views.prepare(ui)
	if err != nil {
		return err
	}
	p.data, err = EncodeUI(p.data, wire)
	if err != nil {
		return err
	}
	if err = p.writer.Write(UIUpdate, 0, p.data); err != nil {
		return err
	}
	p.views.commit(ui.ID, ui.Close, state)
	if !ui.Close && ui.View == nil {
		p.views.metadata(ui)
	}
	return nil
}

func (p *ActionPublisher) CloseUI(id string) error {
	return p.Show(UI{ID: id, Close: true})
}
