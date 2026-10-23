package peb

import (
	"context"
	"sync"
)

// ActionLocation identifies the four workbench button slots.
type ActionLocation uint32

const (
	StatusBar ActionLocation = iota
	LeftContextMenu
	RightContextMenu
	TitleBarRight
)

// Action is a complete button description. Updates replace the description;
// empty fields clear their previous values. Icon is an SVG resource path.
type Action struct {
	ID, Text, Tooltip, Icon, Color, IconColor, IconStyle string
	Location                                             ActionLocation
	Disabled, Hidden                                     bool
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
	if a.Location > TitleBarRight {
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
}
func decodeAction(d *decoder) Action {
	a := Action{ID: d.str(), Text: d.str(), Tooltip: d.str(), Icon: d.str(), Color: d.str(), IconColor: d.str(), IconStyle: d.str(), Location: ActionLocation(d.number()), Disabled: d.boolean(), Hidden: d.boolean()}
	if a.Location > TitleBarRight {
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
	Editor          func(EditorMessage) (EditorReply, error)
	Action          func(ActionContext) error
	UIEvent         func(UIEvent) error
	ActivateActions func(context.Context, *ActionPublisher)
}
