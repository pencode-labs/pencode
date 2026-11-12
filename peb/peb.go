// Package peb implements Pencode Extension Binary protocol version 20.
// Frames and payloads use little endian integers; stdout is reserved for frames.
package peb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"unicode/utf8"
)

const (
	Version    = 20
	HeaderSize = 20
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
	FileChangesRequest
	FileChangesResponse
	LifecycleRequest
	LifecycleResponse
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
	LanguagePacks
	Capabilities = FileIcons | EditorThemes | CustomEditors | Fonts | LanguageServers | Actions | ExtensionUI | DebugAdapters | ExtensionSettings | LanguagePacks
)

var ErrProtocol = errors.New("peb: invalid protocol message")

// Frame.Payload is borrowed until the next Read. Session identifies a window action
// session or a document view; zero addresses the process control session.
// Request IDs correlate replies;
// Initialize/Register/Failure use the same nonzero ID. Shutdown, ActionActivate
// and unsolicited ActionUpdate/UIUpdate notifications use zero.
type Frame struct {
	Payload []byte
	ID      uint32
	Session uint32
	Kind    Kind
}

type Reader struct {
	Source io.Reader
	buffer []byte
	header [HeaderSize]byte
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
	return Frame{Kind: kind, ID: id, Session: binary.LittleEndian.Uint32(h[16:]), Payload: r.buffer}, nil
}

type Writer struct {
	Target io.Writer
	header [HeaderSize]byte
	mu     sync.Mutex
}

// Write handles short writes on the process control session.
func (w *Writer) Write(kind Kind, id uint32, payload []byte) error {
	return w.WriteSession(0, kind, id, payload)
}

// WriteSession serializes complete frames from independent session publishers.
func (w *Writer) WriteSession(session uint32, kind Kind, id uint32, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !validFrame(kind, id, len(payload)) {
		return ErrProtocol
	}
	h := w.header[:]
	copy(h, "peb\x00")
	binary.LittleEndian.PutUint16(h[4:], Version)
	binary.LittleEndian.PutUint16(h[6:], uint16(kind))
	binary.LittleEndian.PutUint32(h[8:], id)
	binary.LittleEndian.PutUint32(h[12:], uint32(len(payload)))
	binary.LittleEndian.PutUint32(h[16:], session)
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
	if kind == FileChangesResponse || kind == ActionResponse || kind == UIResponse || kind == SettingsResponse {
		return id != 0 && size == 0
	}
	if kind == LifecycleRequest || kind == LifecycleResponse || kind == FileChangesRequest || kind == UIRequest || kind == SettingsRequest {
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

// Match selects basenames or suffixes. Suffix includes its leading dot; the longest
// suffix wins, after an exact basename match. Directory names use FolderName.
type Match uint32

const (
	DefaultFile Match = iota
	FileName
	FileSuffix
	DefaultFolder
	FolderName
)

// IconRule matches file and folder names case-insensitively.
type IconRule struct {
	Pattern, Path string
	Match         Match
}
type IconTheme struct {
	ID, Label string
	Rules     []IconRule
}

// LanguagePack points to a JSON language pack under resources/.
type LanguagePack struct{ ID, Label, Path string }

type Contributions struct {
	LanguagePacks   []LanguagePack
	Actions         []Action
	Themes          []Theme
	IconThemes      []IconTheme
	Fonts           []Font
	LanguageServers []LanguageServer
	DebugAdapters   []DebugAdapter
	Settings        []Setting
	// ActionService keeps a live process even when no buttons are registered yet.
	ActionService bool
}

type encoder struct {
	err  error
	data []byte
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
	err  error
	data []byte
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
	e.count(len(v.LanguagePacks))
	for _, pack := range v.LanguagePacks {
		e.str(pack.ID)
		e.str(pack.Label)
		e.str(pack.Path)
	}
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
	if n := d.count(); n != 0 {
		v.LanguagePacks = make([]LanguagePack, n)
	}
	for i := range v.LanguagePacks {
		v.LanguagePacks[i] = LanguagePack{d.str(), d.str(), d.str()}
	}
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

// ServeEditors shares a stateless handler across document views until Shutdown.
// Stateful editors use Handlers.NewEditor with ServeExtension.
func ServeEditors(input io.Reader, output io.Writer, contributions Contributions, handle func(EditorMessage) (EditorReply, error)) error {
	return ServeExtension(input, output, contributions, Handlers{Editor: handle})
}

// ServeExtension supports static registration, editors and live actions. Logs
// belong on stderr. Action updates can be published independently of clicks.
func ServeExtension(input io.Reader, output io.Writer, contributions Contributions, handlers Handlers) error {
	contributions.ActionService = handlers.Action != nil || handlers.UIEvent != nil || handlers.ActivateActions != nil || handlers.FileChanges != nil || handlers.Lifecycle != nil
	r, w := Reader{Source: input}, Writer{Target: output}
	sessions := make(map[uint32]*extensionSession)
	defer func() {
		for _, s := range sessions {
			s.cancel()
		}
	}()
	for {
		f, err := r.Read()
		if err != nil {
			return err
		}
		if f.Kind == Shutdown {
			if f.Session == 0 {
				return nil
			}
			if s := sessions[f.Session]; s != nil {
				s.cancel()
				delete(sessions, f.Session)
			}
			continue
		}
		if f.Kind == Initialize {
			if sessions[f.Session] != nil || len(sessions) >= MaxSessions {
				return ErrProtocol
			}
			ctx, cancel := context.WithCancel(context.Background())
			s := &extensionSession{ctx: ctx, cancel: cancel, handlers: handlers, settings: contributions.Settings, actionService: contributions.ActionService,
				publisher: ActionPublisher{writer: &w, ctx: ctx, session: f.Session, ui: handlers.UIEvent != nil}}
			sessions[f.Session] = s
			if err := registerSession(&w, f, contributions, handlers); err != nil {
				return err
			}
			continue
		}
		s := sessions[f.Session]
		if s == nil {
			return ErrProtocol
		}
		if err := s.dispatch(f); err != nil {
			return err
		}
	}
}

const MaxSessions = 256

type extensionSession struct {
	settings      []Setting
	ctx           context.Context
	cancel        context.CancelFunc
	editor        func(EditorMessage) (EditorReply, error)
	data          []byte
	handlers      Handlers
	publisher     ActionPublisher
	active        bool
	actionService bool
}

func registerSession(w *Writer, f Frame, contributions Contributions, handlers Handlers) error {
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
		len(contributions.LanguagePacks) > 0 && hello.Capabilities&LanguagePacks == 0 ||
		len(contributions.Settings) > 0 && hello.Capabilities&ExtensionSettings == 0 ||
		(contributions.ActionService || len(contributions.Actions) > 0) && hello.Capabilities&Actions == 0 ||
		hasActions && handlers.Action == nil ||
		handlers.UIEvent != nil && hello.Capabilities&ExtensionUI == 0 ||
		hasViews && (hello.Capabilities&CustomEditors == 0 || handlers.Editor == nil && handlers.NewEditor == nil) {
		data, _ := EncodeFailure(nil, "unsupported extension capabilities")
		return w.WriteSession(f.Session, Failure, f.ID, data)
	}
	data, err := EncodeContributions(nil, contributions)
	if err != nil {
		return err
	}
	if err = w.WriteSession(f.Session, Register, f.ID, data); err != nil {
		return err
	}
	return nil
}

func (s *extensionSession) dispatch(f Frame) error {
	data := s.data
	var err error
	defer func() { s.data = data }()
	if f.Kind == ActionActivate {
		if s.active || !s.actionService {
			return ErrProtocol
		}
		s.active = true
		if s.handlers.ActivateActions != nil {
			s.handlers.ActivateActions(s.ctx, &s.publisher)
		}
		return nil
	}
	kind := EditorResponse
	var handleErr error
	switch f.Kind {
	case LifecycleRequest:
		if !s.active {
			return ErrProtocol
		}
		event, decodeErr := DecodeLifecycleEvent(f.Payload)
		if decodeErr != nil {
			return decodeErr
		}
		var reply LifecycleReply
		if s.handlers.Lifecycle != nil {
			reply, handleErr = s.handlers.Lifecycle(event)
		}
		kind = LifecycleResponse
		if handleErr == nil {
			data, err = EncodeLifecycleReply(data, reply)
		}
	case FileChangesRequest:
		message, decodeErr := DecodeFileChanges(f.Payload)
		if decodeErr != nil {
			return decodeErr
		}
		if s.handlers.FileChanges != nil {
			handleErr = s.handlers.FileChanges(message)
		}
		kind, data = FileChangesResponse, data[:0]
	case SettingsRequest:
		values, decodeErr := DecodeSettingValues(f.Payload)
		if decodeErr != nil {
			return decodeErr
		}
		if len(values) != len(s.settings) {
			return ErrProtocol
		}
		for _, setting := range s.settings {
			if setting.Validate(values[setting.ID]) != nil {
				return ErrProtocol
			}
		}
		if s.handlers.Settings != nil {
			handleErr = s.handlers.Settings(values)
		}
		kind, data = SettingsResponse, data[:0]
	case EditorRequest:
		if s.handlers.Editor == nil && s.handlers.NewEditor == nil {
			return ErrProtocol
		}
		message, decodeErr := DecodeEditorMessage(f.Payload)
		if decodeErr != nil {
			return decodeErr
		}
		if s.editor == nil {
			s.editor = s.handlers.Editor
			if s.handlers.NewEditor != nil {
				s.editor = s.handlers.NewEditor(s.ctx)
			}
		}
		if s.editor == nil {
			return ErrProtocol
		}
		reply, callErr := s.editor(message)
		handleErr = callErr
		if handleErr == nil {
			data, err = EncodeEditorReply(data, reply)
		}
	case UIRequest:
		if !s.active || s.handlers.UIEvent == nil {
			return ErrProtocol
		}
		message, decodeErr := DecodeUIEvent(f.Payload)
		if decodeErr != nil {
			return decodeErr
		}
		var handled bool
		handled, handleErr = s.publisher.handleViewEvent(message)
		if !handled && handleErr == nil {
			handleErr = s.handlers.UIEvent(message)
		}
		kind, data = UIResponse, data[:0]
	case ActionInvoke:
		if !s.active || s.handlers.Action == nil {
			return ErrProtocol
		}
		message, decodeErr := DecodeActionContext(f.Payload)
		if decodeErr != nil {
			return decodeErr
		}
		s.publisher.mu.Lock()
		s.publisher.action = message
		s.publisher.mu.Unlock()
		handleErr = s.handlers.Action(message)
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
	s.publisher.mu.Lock()
	err = s.publisher.writer.WriteSession(f.Session, kind, f.ID, data)
	s.publisher.mu.Unlock()
	if err != nil {
		return err
	}
	return nil
}
