// Package peb implements Pencode Extension Binary protocol version 10.
// Frames and payloads use little endian integers; stdout is reserved for frames.
package peb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

const (
	Version    = 10
	HeaderSize = 16
	MaxPayload = 4 << 20
	MaxString  = 64 << 10
	MaxItems   = 16384
)

type Kind uint16

const (
	Initialize Kind = 1 + iota
	Register
	Shutdown
	Failure
	EditorRequest
	EditorResponse
	ActionActivate
	ActionInvoke
	ActionResponse
	ActionUpdate
	UIUpdate
	UIRequest
	UIResponse
	SettingsRequest
	SettingsResponse
)

type Capability uint32

const (
	FileIcons Capability = 1 << iota
	EditorThemes
	CustomEditors
	Fonts
	LanguageServers
	Actions
	ExtensionUI
	DebugAdapters
	ExtensionSettings
	Capabilities = FileIcons | EditorThemes | CustomEditors | Fonts | LanguageServers | Actions | ExtensionUI | DebugAdapters | ExtensionSettings
)

var ErrProtocol = errors.New("peb: invalid protocol message")

// Frame.Payload is borrowed until the next Read. Request IDs correlate replies;
// Initialize/Register/Failure use the same nonzero ID. Shutdown, ActionActivate
// and unsolicited ActionUpdate/UIUpdate notifications use zero.
type Frame struct {
	Kind    Kind
	ID      uint32
	Payload []byte
}

type Reader struct {
	Source io.Reader
	header [HeaderSize]byte
	buffer []byte
}

func (r *Reader) Read() (Frame, error) {
	if _, err := io.ReadFull(r.Source, r.header[:]); err != nil {
		return Frame{}, err
	}
	h := r.header[:]
	if string(h[:4]) != "peb\x00" || binary.LittleEndian.Uint16(h[4:]) != Version {
		return Frame{}, ErrProtocol
	}
	kind, id := Kind(binary.LittleEndian.Uint16(h[6:])), binary.LittleEndian.Uint32(h[8:])
	size := binary.LittleEndian.Uint32(h[12:])
	if !validFrame(kind, id, int(size)) {
		return Frame{}, ErrProtocol
	}
	if int(size) > cap(r.buffer) {
		r.buffer = make([]byte, size)
	}
	r.buffer = r.buffer[:size]
	if _, err := io.ReadFull(r.Source, r.buffer); err != nil {
		return Frame{}, err
	}
	return Frame{kind, id, r.buffer}, nil
}

type Writer struct {
	Target io.Writer
	header [HeaderSize]byte
}

// Write handles short writes. A Writer must have one owner.
func (w *Writer) Write(kind Kind, id uint32, payload []byte) error {
	if !validFrame(kind, id, len(payload)) {
		return ErrProtocol
	}
	h := w.header[:]
	copy(h, "peb\x00")
	binary.LittleEndian.PutUint16(h[4:], Version)
	binary.LittleEndian.PutUint16(h[6:], uint16(kind))
	binary.LittleEndian.PutUint32(h[8:], id)
	binary.LittleEndian.PutUint32(h[12:], uint32(len(payload)))
	if err := writeFull(w.Target, h); err != nil {
		return err
	}
	return writeFull(w.Target, payload)
}

func validFrame(kind Kind, id uint32, size int) bool {
	if size < 0 || size > MaxPayload {
		return false
	}
	if kind == Shutdown {
		return id == 0 && size == 0
	}
	if kind == ActionActivate {
		return id == 0 && size == 0
	}
	if kind == ActionUpdate || kind == UIUpdate {
		return id == 0
	}
	if kind == ActionResponse || kind == UIResponse || kind == SettingsResponse {
		return id != 0 && size == 0
	}
	if kind == UIRequest || kind == SettingsRequest {
		return id != 0
	}
	return kind >= Initialize && kind <= ActionInvoke && id != 0
}

func writeFull(w io.Writer, data []byte) error {
	for len(data) != 0 {
		n, err := w.Write(data)
		if n < 0 || n > len(data) {
			return io.ErrShortWrite
		}
		data = data[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

type Hello struct {
	Host         string
	Capabilities Capability
}
type Theme struct{ ID, Label, Path string }

// Font contributes a regular face and an optional bold face from resources/.
type Font struct{ ID, Label, Path, BoldPath string }

// Match rules are case sensitive. Suffix includes its leading dot; the longest
// suffix wins, after an exact basename match. Directory names use FolderName.
type Match uint32

const (
	DefaultFile Match = iota
	FileName
	FileSuffix
	DefaultFolder
	FolderName
)

type IconRule struct {
	Match         Match
	Pattern, Path string
}
type IconTheme struct {
	ID, Label string
	Rules     []IconRule
}
type Contributions struct {
	// ActionService keeps a live process even when no buttons are registered yet.
	ActionService   bool
	Actions         []Action
	Themes          []Theme
	IconThemes      []IconTheme
	Fonts           []Font
	LanguageServers []LanguageServer
	DebugAdapters   []DebugAdapter
	Settings        []Setting
}

type encoder struct {
	data []byte
	err  error
}

func (e *encoder) number(n uint32) {
	if e.err != nil {
		return
	}
	if len(e.data) > MaxPayload-4 {
		e.err = ErrProtocol
		return
	}
	e.data = binary.LittleEndian.AppendUint32(e.data, n)
}

func (e *encoder) count(n int) {
	if n > MaxItems {
		e.err = ErrProtocol
		return
	}
	e.number(uint32(n))
}

func (e *encoder) str(s string) {
	if len(s) > MaxString || !utf8.ValidString(s) || len(e.data)+4+len(s) > MaxPayload {
		e.err = ErrProtocol
		return
	}
	e.number(uint32(len(s)))
	if e.err == nil {
		e.data = append(e.data, s...)
	}
}

type decoder struct {
	data []byte
	err  error
}

func (d *decoder) number() uint32 {
	if d.err != nil {
		return 0
	}
	if len(d.data) < 4 {
		d.err = ErrProtocol
		return 0
	}
	n := binary.LittleEndian.Uint32(d.data)
	d.data = d.data[4:]
	return n
}

func (d *decoder) count() int {
	n := d.number()
	if n > MaxItems || n > uint32(len(d.data)/4) {
		d.err = ErrProtocol
		return 0
	}
	return int(n)
}

func (d *decoder) str() string {
	n := d.number()
	if d.err != nil {
		return ""
	}
	if n > MaxString || n > uint32(len(d.data)) || !utf8.Valid(d.data[:n]) {
		d.err = ErrProtocol
		return ""
	}
	s := string(d.data[:n])
	d.data = d.data[n:]
	return s
}

func (d *decoder) done() error {
	if d.err != nil || len(d.data) != 0 {
		return ErrProtocol
	}
	return nil
}

// Encode functions reset and reuse dst. Decoders own their returned strings.
func EncodeHello(dst []byte, v Hello) ([]byte, error) {
	e := encoder{data: dst[:0]}
	e.str(v.Host)
	e.number(uint32(v.Capabilities))
	return e.data, e.err
}

func DecodeHello(data []byte) (Hello, error) {
	d := decoder{data: data}
	v := Hello{d.str(), Capability(d.number())}
	return v, d.done()
}

func EncodeContributions(dst []byte, v Contributions) ([]byte, error) {
	e := encoder{data: dst[:0]}
	if ValidateSettings(v.Settings) != nil {
		return e.data, ErrProtocol
	}
	e.count(len(v.Themes))
	for _, t := range v.Themes {
		e.str(t.ID)
		e.str(t.Label)
		e.str(t.Path)
	}
	e.count(len(v.IconThemes))
	for _, t := range v.IconThemes {
		e.str(t.ID)
		e.str(t.Label)
		e.count(len(t.Rules))
		for _, r := range t.Rules {
			if r.Match > FolderName {
				e.err = ErrProtocol
			}
			e.number(uint32(r.Match))
			e.str(r.Pattern)
			e.str(r.Path)
		}
	}
	e.count(len(v.Fonts))
	for _, f := range v.Fonts {
		e.str(f.ID)
		e.str(f.Label)
		e.str(f.Path)
		e.str(f.BoldPath)
	}
	encodeLanguageServers(&e, v.LanguageServers)
	e.count(len(v.Actions))
	for _, a := range v.Actions {
		encodeAction(&e, a)
	}
	e.number(boolNumber(v.ActionService))
	encodeDebugAdapters(&e, v.DebugAdapters)
	encodeSettings(&e, v.Settings)
	return e.data, e.err
}

func DecodeContributions(data []byte) (Contributions, error) {
	if len(data) > MaxPayload {
		return Contributions{}, ErrProtocol
	}
	d := decoder{data: data}
	v := Contributions{Themes: make([]Theme, d.count())}
	for i := range v.Themes {
		v.Themes[i] = Theme{d.str(), d.str(), d.str()}
	}
	v.IconThemes = make([]IconTheme, d.count())
	for i := range v.IconThemes {
		t := &v.IconThemes[i]
		t.ID, t.Label = d.str(), d.str()
		t.Rules = make([]IconRule, d.count())
		for j := range t.Rules {
			r := &t.Rules[j]
			r.Match, r.Pattern, r.Path = Match(d.number()), d.str(), d.str()
			if r.Match > FolderName {
				d.err = ErrProtocol
			}
		}
	}
	if n := d.count(); n != 0 {
		v.Fonts = make([]Font, n)
	}
	for i := range v.Fonts {
		v.Fonts[i] = Font{d.str(), d.str(), d.str(), d.str()}
	}
	v.LanguageServers = decodeLanguageServers(&d)
	if n := d.count(); n != 0 {
		v.Actions = make([]Action, n)
	}
	for i := range v.Actions {
		v.Actions[i] = decodeAction(&d)
	}
	v.ActionService = d.boolean()
	v.DebugAdapters = decodeDebugAdapters(&d)
	v.Settings = decodeSettings(&d)
	return v, d.done()
}

func EncodeFailure(dst []byte, message string) ([]byte, error) {
	e := encoder{data: dst[:0]}
	e.str(message)
	return e.data, e.err
}

func DecodeFailure(data []byte) (string, error) {
	d := decoder{data: data}
	s := d.str()
	return s, d.done()
}

// Serve registers static resources, then waits for Shutdown. Logs belong on
// stderr. The host closes the process after registration; no per-file RPC runs.
func Serve(input io.Reader, output io.Writer, contributions Contributions) error {
	return ServeEditors(input, output, contributions, nil)
}

// ServeEditors keeps an editor process alive until Shutdown. The handler runs
// sequentially; it owns its state and must keep logs off stdout.
func ServeEditors(input io.Reader, output io.Writer, contributions Contributions, handle func(EditorMessage) (EditorReply, error)) error {
	return ServeExtension(input, output, contributions, Handlers{Editor: handle})
}

// ServeExtension supports static registration, editors and live actions. Logs
// belong on stderr. Action updates can be published independently of clicks.
func ServeExtension(input io.Reader, output io.Writer, contributions Contributions, handlers Handlers) error {
	contributions.ActionService = handlers.Action != nil || handlers.UIEvent != nil || handlers.ActivateActions != nil
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, w := Reader{Source: input}, Writer{Target: output}
	publisher := ActionPublisher{writer: &w, ctx: ctx, ui: handlers.UIEvent != nil}
	f, err := r.Read()
	if err != nil {
		return err
	}
	if f.Kind != Initialize {
		return ErrProtocol
	}
	hello, err := DecodeHello(f.Payload)
	if err != nil {
		return err
	}
	hasViews, hasActions := false, false
	for _, a := range contributions.Actions {
		if a.Location == OpenWith {
			hasViews = true
		} else {
			hasActions = true
		}
	}
	if len(contributions.Themes) > 0 && hello.Capabilities&EditorThemes == 0 ||
		len(contributions.IconThemes) > 0 && hello.Capabilities&FileIcons == 0 ||
		len(contributions.Fonts) > 0 && hello.Capabilities&Fonts == 0 ||
		len(contributions.LanguageServers) > 0 && hello.Capabilities&LanguageServers == 0 ||
		len(contributions.DebugAdapters) > 0 && hello.Capabilities&DebugAdapters == 0 ||
		len(contributions.Settings) > 0 && hello.Capabilities&ExtensionSettings == 0 ||
		(contributions.ActionService || len(contributions.Actions) > 0) && hello.Capabilities&Actions == 0 ||
		hasActions && handlers.Action == nil ||
		handlers.UIEvent != nil && hello.Capabilities&ExtensionUI == 0 ||
		hasViews && (hello.Capabilities&CustomEditors == 0 || handlers.Editor == nil) {
		data, _ := EncodeFailure(nil, "unsupported extension capabilities")
		return w.Write(Failure, f.ID, data)
	}
	data, err := EncodeContributions(nil, contributions)
	if err != nil {
		return err
	}
	if err = w.Write(Register, f.ID, data); err != nil {
		return err
	}
	active := false
	for {
		f, err = r.Read()
		if err != nil {
			return err
		}
		if f.Kind == Shutdown {
			return nil
		}
		if f.Kind == ActionActivate {
			if active || !contributions.ActionService {
				return ErrProtocol
			}
			active = true
			if handlers.ActivateActions != nil {
				handlers.ActivateActions(ctx, &publisher)
			}
			continue
		}
		kind := EditorResponse
		var handleErr error
		switch f.Kind {
		case SettingsRequest:
			values, decodeErr := DecodeSettingValues(f.Payload)
			if decodeErr != nil {
				return decodeErr
			}
			if len(values) != len(contributions.Settings) {
				return ErrProtocol
			}
			for _, setting := range contributions.Settings {
				if setting.Validate(values[setting.ID]) != nil {
					return ErrProtocol
				}
			}
			if handlers.Settings != nil {
				handleErr = handlers.Settings(values)
			}
			kind, data = SettingsResponse, data[:0]
		case EditorRequest:
			if handlers.Editor == nil {
				return ErrProtocol
			}
			message, decodeErr := DecodeEditorMessage(f.Payload)
			if decodeErr != nil {
				return decodeErr
			}
			reply, callErr := handlers.Editor(message)
			handleErr = callErr
			if handleErr == nil {
				data, err = EncodeEditorReply(data, reply)
			}
		case UIRequest:
			if !active || handlers.UIEvent == nil {
				return ErrProtocol
			}
			message, decodeErr := DecodeUIEvent(f.Payload)
			if decodeErr != nil {
				return decodeErr
			}
			publisher.mu.Lock()
			publisher.action = message.Context
			publisher.mu.Unlock()
			handleErr = handlers.UIEvent(message)
			kind, data = UIResponse, data[:0]
		case ActionInvoke:
			if !active || handlers.Action == nil {
				return ErrProtocol
			}
			message, decodeErr := DecodeActionContext(f.Payload)
			if decodeErr != nil {
				return decodeErr
			}
			publisher.mu.Lock()
			publisher.action = message
			publisher.mu.Unlock()
			handleErr = handlers.Action(message)
			kind, data = ActionResponse, data[:0]
		default:
			return fmt.Errorf("%w: unexpected request", ErrProtocol)
		}
		if handleErr != nil {
			kind = Failure
			data, err = EncodeFailure(data, handleErr.Error())
		}
		if err != nil {
			return err
		}
		publisher.mu.Lock()
		err = w.Write(kind, f.ID, data)
		publisher.mu.Unlock()
		if err != nil {
			return err
		}
	}
}
