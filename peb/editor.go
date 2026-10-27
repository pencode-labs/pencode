package peb

import "unicode/utf8"

type DocumentSelector struct {
	Match   Match
	Pattern string
	// LanguageID overrides a language server's default for this selector.
	LanguageID string
}

// EditorMessage contains a host-owned document snapshot. Binary files supply
// only Path and Binary=true; this first API permits text edits only.
type EditorMessage struct {
	Editor, Path, Content string
	Binary                bool
	Event                 EditorEvent
}

type EditorEvent struct {
	Kind, Ref, Value, InputType, Data string
	Checked                           bool
	Selected, Anchor, Focus           uint32
}

type EditorBinding struct{ Ref, Kind string }

// A nil View preserves the retained DOM, caret and scroll. A non-nil View
// replaces the fragment. Content=nil preserves the host document; &"" clears it.
type EditorReply struct {
	View    *EditorView
	Content *string
}

// Extension UI fragments can use img with data-ref and data-pencode-icon="name"
// to reuse a built-in IDE icon with its theme color, raster scale and texture
// cache; omit src.
// EditorTab fragments can use data-pencode-layout="fill" to fill the editor
// without host padding or scrolling. An element with data-ref and
// data-pencode-scroll="end" follows new output until the user scrolls up;
// changing its data-pencode-scroll-key reveals the end of a different transcript.
// data-pencode-scroll="preserve" retains another scroll area's position while
// its data-pencode-scroll-key stays the same.
// A container with data-ref and data-pencode-region updates its children
// independently. Changes confined to regions preserve the surrounding DOM,
// including native input focus and selection. Nested regions update together.
// A container with data-ref and data-pencode-focus="input-ref" focuses the
// named control when its blank area is clicked, preserving child control focus.
// A textarea with data-ref and data-pencode-autosize grows with its native text
// layout. Use border-box sizing and CSS min/max-height to bound its height.
// data-pencode-submit-on-enter="form" submits the named form binding on Enter;
// Shift+Enter inserts a newline, and IME confirmation keeps its native behavior.
type EditorView struct {
	HTML, CSS string
	Events    []EditorBinding
}

func boolNumber(v bool) uint32 {
	if v {
		return 1
	}
	return 0
}
func (d *decoder) boolean() bool {
	n := d.number()
	if n > 1 {
		d.err = ErrProtocol
	}
	return n == 1
}

const MaxDocument = 2 << 20

func (e *encoder) text(s string) {
	if len(s) > MaxDocument || !utf8.ValidString(s) || len(e.data)+4+len(s) > MaxPayload {
		e.err = ErrProtocol
		return
	}
	e.number(uint32(len(s)))
	if e.err == nil {
		e.data = append(e.data, s...)
	}
}

func (d *decoder) text() string {
	n := d.number()
	if d.err != nil {
		return ""
	}
	if n > MaxDocument || n > uint32(len(d.data)) || !utf8.Valid(d.data[:n]) {
		d.err = ErrProtocol
		return ""
	}
	s := string(d.data[:n])
	d.data = d.data[n:]
	return s
}

func EncodeEditorMessage(dst []byte, v EditorMessage) ([]byte, error) {
	e := encoder{data: dst[:0]}
	e.str(v.Editor)
	e.str(v.Path)
	e.text(v.Content)
	e.number(boolNumber(v.Binary))
	encodeEvent(&e, v.Event)
	return e.data, e.err
}

func DecodeEditorMessage(data []byte) (EditorMessage, error) {
	if len(data) > MaxPayload {
		return EditorMessage{}, ErrProtocol
	}
	d := decoder{data: data}
	v := EditorMessage{Editor: d.str(), Path: d.str(), Content: d.text(), Binary: d.boolean()}
	v.Event = decodeEvent(&d)
	return v, d.done()
}

func EncodeEditorReply(dst []byte, v EditorReply) ([]byte, error) {
	e := encoder{data: dst[:0]}
	encodeView(&e, v.View)
	e.number(boolNumber(v.Content != nil))
	if v.Content != nil {
		e.text(*v.Content)
	}
	return e.data, e.err
}

func DecodeEditorReply(data []byte) (EditorReply, error) {
	if len(data) > MaxPayload {
		return EditorReply{}, ErrProtocol
	}
	d := decoder{data: data}
	var v EditorReply
	v.View = decodeView(&d)
	if d.boolean() {
		content := d.text()
		v.Content = &content
	}
	return v, d.done()
}

func encodeEvent(e *encoder, v EditorEvent) {
	e.str(v.Kind)
	e.str(v.Ref)
	e.text(v.Value)
	e.str(v.InputType)
	e.text(v.Data)
	e.number(boolNumber(v.Checked))
	e.number(v.Selected)
	e.number(v.Anchor)
	e.number(v.Focus)
}
func decodeEvent(d *decoder) EditorEvent {
	return EditorEvent{Kind: d.str(), Ref: d.str(), Value: d.text(), InputType: d.str(), Data: d.text(), Checked: d.boolean(), Selected: d.number(), Anchor: d.number(), Focus: d.number()}
}

func encodeView(e *encoder, v *EditorView) {
	e.number(boolNumber(v != nil))
	if v != nil {
		e.text(v.HTML)
		e.text(v.CSS)
		e.count(len(v.Events))
		for _, event := range v.Events {
			e.str(event.Ref)
			e.str(event.Kind)
		}
	}
}
func decodeView(d *decoder) *EditorView {
	var v *EditorView
	if d.boolean() {
		v = &EditorView{HTML: d.text(), CSS: d.text(), Events: make([]EditorBinding, d.count())}
		for i := range v.Events {
			v.Events[i] = EditorBinding{d.str(), d.str()}
		}
	}
	return v
}
