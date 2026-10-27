package peb

import (
	"context"
	"slices"
	"sync"
)

// ActionLocation identifies workbench button slots, including document views.
type ActionLocation uint32

const (
	StatusBar ActionLocation = iota
	LeftContextMenu
	RightContextMenu
	TitleBarRight
	OpenWith
)

// Action is a complete button description. Updates replace the description;
// empty fields clear their previous values. Icon is an SVG resource path.
// OpenWith actions use Selectors to match documents and Default to opt into
// automatic activation. Their view is served by Handlers.Editor, not Action.
type Action struct {
	ID, Text, Tooltip, Icon, Color, IconColor, IconStyle string
	Location                                             ActionLocation
	Disabled, Hidden                                     bool
	Default                                              bool
	Selectors                                            []DocumentSelector
}

// Equal compares full descriptions, including document associations.
func (a Action) Equal(b Action) bool {
	rules := slices.Equal(a.Selectors, b.Selectors)
	return rules && a.ID == b.ID && a.Text == b.Text && a.Tooltip == b.Tooltip && a.Icon == b.Icon && a.Color == b.Color && a.IconColor == b.IconColor && a.IconStyle == b.IconStyle && a.Location == b.Location && a.Disabled == b.Disabled && a.Hidden == b.Hidden && a.Default == b.Default
}

// ActionContext uses absolute paths; Caret and Anchor are Unicode rune offsets.
type ActionContext struct {
	ID, ProjectPath, Path string
	Directory             bool
	Caret, Anchor         uint32
}

type ActionChange struct {
	Action Action
	Remove bool
}

func encodeAction(e *encoder, a Action) {
	if a.Location > OpenWith {
		e.err = ErrProtocol
	}
	e.str(a.ID)
	e.str(a.Text)
	e.str(a.Tooltip)
	e.str(a.Icon)
	e.str(a.Color)
	e.str(a.IconColor)
	e.str(a.IconStyle)
	e.number(uint32(a.Location))
	e.number(boolNumber(a.Disabled))
	e.number(boolNumber(a.Hidden))
	e.number(boolNumber(a.Default))
	e.count(len(a.Selectors))
	for _, rule := range a.Selectors {
		e.number(uint32(rule.Match))
		e.str(rule.Pattern)
		e.str(rule.LanguageID)
	}
}
func decodeAction(d *decoder) Action {
	a := Action{ID: d.str(), Text: d.str(), Tooltip: d.str(), Icon: d.str(), Color: d.str(), IconColor: d.str(), IconStyle: d.str(), Location: ActionLocation(d.number()), Disabled: d.boolean(), Hidden: d.boolean()}
	a.Default = d.boolean()
	if n := d.count(); n != 0 {
		a.Selectors = make([]DocumentSelector, n)
	}
	for i := range a.Selectors {
		a.Selectors[i] = DocumentSelector{Match: Match(d.number()), Pattern: d.str(), LanguageID: d.str()}
	}
	if a.Location > OpenWith {
		d.err = ErrProtocol
	}
	return a
}
func EncodeActionChange(dst []byte, v ActionChange) ([]byte, error) {
	e := encoder{data: dst[:0]}
	encodeAction(&e, v.Action)
	e.number(boolNumber(v.Remove))
	return e.data, e.err
}
func DecodeActionChange(data []byte) (ActionChange, error) {
	d := decoder{data: data}
	v := ActionChange{Action: decodeAction(&d), Remove: d.boolean()}
	return v, d.done()
}
func EncodeActionContext(dst []byte, v ActionContext) ([]byte, error) {
	e := encoder{data: dst[:0]}
	encodeActionContext(&e, v)
	return e.data, e.err
}
func encodeActionContext(e *encoder, v ActionContext) {
	e.str(v.ID)
	e.str(v.ProjectPath)
	e.str(v.Path)
	e.number(boolNumber(v.Directory))
	e.number(v.Caret)
	e.number(v.Anchor)
}
func DecodeActionContext(data []byte) (ActionContext, error) {
	d := decoder{data: data}
	v := decodeActionContext(&d)
	return v, d.done()
}

func decodeActionContext(d *decoder) ActionContext {
	return ActionContext{ID: d.str(), ProjectPath: d.str(), Path: d.str(), Directory: d.boolean(), Caret: d.number(), Anchor: d.number()}
}

// ActionPublisher serializes unsolicited updates with request replies. It may
// be used from background goroutines until the activation context is canceled.
type ActionPublisher struct {
	mu     sync.Mutex
	writer *Writer
	data   []byte
	ctx    context.Context
	ui     bool
	action ActionContext
}

func (p *ActionPublisher) Update(action Action) error { return p.change(ActionChange{Action: action}) }
func (p *ActionPublisher) Remove(id string) error {
	return p.change(ActionChange{Action: Action{ID: id}, Remove: true})
}
func (p *ActionPublisher) change(change ActionChange) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ctx.Err(); err != nil {
		return err
	}
	var err error
	p.data, err = EncodeActionChange(p.data, change)
	if err != nil {
		return err
	}
	return p.writer.Write(ActionUpdate, 0, p.data)
}

// Handlers shares one SDK for editor requests and persistent workbench actions.
// ActivateActions runs once, only in the workbench action process. Start any
// background work there; return promptly and stop when ctx is canceled.
type Handlers struct {
	// Settings runs before activation/first document request and on each change.
	// The snapshot owns its data and may be retained by the extension.
	Settings        func(SettingValues) error
	Editor          func(EditorMessage) (EditorReply, error)
	Action          func(ActionContext) error
	UIEvent         func(UIEvent) error
	ActivateActions func(context.Context, *ActionPublisher)
}
