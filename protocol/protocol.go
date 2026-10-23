// Package protocol defines the headless Pencode client/server wire format.
// Payloads are borrowed until the reader's next Read. Readers and writers each
// have one owner; callers transfer owned data across goroutines.
package protocol

import (
	"encoding/binary"
	"errors"
	"io"
)

const (
	Version    = 3
	HeaderSize = 24
	MaxPayload = 64 << 10
	MaxMessage = 64 << 20
	ChunkSize  = 32 << 10
	// StreamWindow bounds both buffered process output and each stream RPC.
	StreamWindow = 256 << 10
)

type Kind uint16

const (
	Hello Kind = iota + 1
	WorkspaceOpen
	WorkspaceClose
	DocumentOpen
	DocumentChange
	DocumentSave
	DocumentClose
	DocumentMove
	DirectoryRead
	ResourceCreate
	ResourceRemove
	ResourceRename
	ResourceTransfer
	Service
	Cancel
	Reply
	Data
	Failure
)

type Code uint32

const (
	Invalid Code = iota + 1
	NotFound
	Conflict
	VersionMismatch
	Busy
	IO
)

var ErrProtocol = errors.New("pencode: invalid client/server message")

type Error struct {
	Code    Code
	Message string
}

func (e *Error) Error() string { return e.Message }

type Frame struct {
	Kind    Kind
	ID      uint64
	Stream  uint32
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
	kind := Kind(binary.LittleEndian.Uint16(h[6:]))
	size := binary.LittleEndian.Uint32(h[20:])
	if string(h[:4]) != "pcs\x00" || binary.LittleEndian.Uint16(h[4:]) != Version || kind < Hello || kind > Failure || size > MaxPayload || binary.LittleEndian.Uint64(h[8:]) == 0 || binary.LittleEndian.Uint32(h[16:]) != 0 {
		return Frame{}, ErrProtocol
	}
	if int(size) > cap(r.buffer) {
		r.buffer = make([]byte, size)
	}
	r.buffer = r.buffer[:size]
	if _, err := io.ReadFull(r.Source, r.buffer); err != nil {
		return Frame{}, err
	}
	return Frame{Kind: kind, ID: binary.LittleEndian.Uint64(h[8:]), Payload: r.buffer}, nil
}

type Writer struct {
	Target io.Writer
	header [HeaderSize]byte
}

func (w *Writer) Write(kind Kind, id uint64, payload []byte) error {
	if kind < Hello || kind > Failure || id == 0 || len(payload) > MaxPayload {
		return ErrProtocol
	}
	h := w.header[:]
	copy(h, "pcs\x00")
	binary.LittleEndian.PutUint16(h[4:], Version)
	binary.LittleEndian.PutUint16(h[6:], uint16(kind))
	binary.LittleEndian.PutUint64(h[8:], id)
	binary.LittleEndian.PutUint32(h[16:], 0)
	binary.LittleEndian.PutUint32(h[20:], uint32(len(payload)))
	if err := writeFull(w.Target, h); err != nil {
		return err
	}
	return writeFull(w.Target, payload)
}

func writeFull(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if n < 0 || n > len(p) {
			return io.ErrShortWrite
		}
		p = p[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// Encoder appends into caller-owned storage and can be reset without allocating.
type Encoder struct{ Buffer []byte }

func (e *Encoder) Reset()          { e.Buffer = e.Buffer[:0] }
func (e *Encoder) U32(v uint32)    { e.Buffer = binary.LittleEndian.AppendUint32(e.Buffer, v) }
func (e *Encoder) U64(v uint64)    { e.Buffer = binary.LittleEndian.AppendUint64(e.Buffer, v) }
func (e *Encoder) String(v string) { e.U32(uint32(len(v))); e.Buffer = append(e.Buffer, v...) }
func (e *Encoder) Bytes(v []byte)  { e.U32(uint32(len(v))); e.Buffer = append(e.Buffer, v...) }

type Decoder struct {
	Buffer []byte
	Err    error
}

func (d *Decoder) take(n int) []byte {
	if d.Err != nil {
		return nil
	}
	if n < 0 || n > len(d.Buffer) {
		d.Err = ErrProtocol
		return nil
	}
	p := d.Buffer[:n]
	d.Buffer = d.Buffer[n:]
	return p
}
func (d *Decoder) U32() uint32 {
	p := d.take(4)
	if p == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(p)
}
func (d *Decoder) U64() uint64 {
	p := d.take(8)
	if p == nil {
		return 0
	}
	return binary.LittleEndian.Uint64(p)
}
func (d *Decoder) Bytes() []byte  { return d.take(int(d.U32())) }
func (d *Decoder) String() string { return string(d.Bytes()) }
func (d *Decoder) Done() error {
	if d.Err != nil {
		return d.Err
	}
	if len(d.Buffer) != 0 {
		return ErrProtocol
	}
	return nil
}

type Workspace struct {
	ID         uint64
	Path, Name string
}

type Document struct {
	ID, Version uint64
	Content     string
}

type Entry struct {
	Name string
	Mode uint32
}
