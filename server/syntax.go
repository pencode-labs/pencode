package server

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onix-engine/onix/syntax"
	"github.com/onix-engine/onix/text/piecetree"
	"github.com/pencode-labs/pencode/protocol"
)

type captureName struct {
	capture, scopes string
}

// The previous wire ranges only need the dictionary ID and byte offsets.
type captureRange struct {
	start, end, name uint32
}

type syntaxSession struct {
	captures map[captureName]uint32
	names    []captureName
	source   piecetree.Snapshot
	previous []captureRange
	document syntax.Document
	version  uint64
}

func (s *services) analyze(ctx context.Context, op protocol.Operation, d *protocol.Decoder, e *protocol.Encoder) error {
	id := d.U64()
	s.syntaxMu.Lock()
	defer s.syntaxMu.Unlock()
	if op == protocol.SyntaxRelease {
		if err := d.Done(); err != nil {
			return err
		}
		if p := s.syntax[id]; p != nil {
			p.document.CloseKeepingGrammar(15 * time.Second)
			delete(s.syntax, id)
		}
		return nil
	}
	version := d.U64()
	path := d.String()
	start, end := int(d.U32()), int(d.U32())
	text := d.String()
	if err := d.Done(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p := s.syntax[id]
	if p == nil {
		if len(s.syntax) >= 8 {
			return &protocol.Error{Code: protocol.Busy, Message: "syntax session budget exceeded"}
		}
		p = &syntaxSession{captures: make(map[captureName]uint32)}
		s.syntax[id] = p
	}
	if version != p.version {
		return &protocol.Error{Code: protocol.VersionMismatch, Message: "syntax version mismatch"}
	}
	if start < 0 || start > end || end > p.source.Len() || !utf8.ValidString(text) || start > 0 && start < p.source.Len() && !utf8.RuneStart(p.source.ByteAt(start)) || end < p.source.Len() && !utf8.RuneStart(p.source.ByteAt(end)) || p.source.Len()-(end-start)+len(text) > protocol.MaxMessage-1024 {
		return protocol.ErrProtocol
	}
	source := p.source.Replace(start, end, text)
	if p.source.Len() > 64<<10 && source.Len() < p.source.Len()/4 {
		source = piecetree.FromString(strings.Clone(source.String()))
	}
	if err := p.document.UpdateRange(path, source, start, end, start+len(text)); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		p.document.Close()
		delete(s.syntax, id)
		return err
	}
	shift := source.Len() - p.source.Len()
	p.source = source
	p.version++
	e.U64(p.version)
	e.String(p.document.Language())
	p.encodeCaptures(e, p.document.Captures(), shift)
	return nil
}

func (p *syntaxSession) encodeCaptures(e *protocol.Encoder, captures []syntax.Capture, shift int) {
	prefix, suffix := p.captureDelta(captures, shift)
	end := len(captures) - suffix
	e.U32(uint32(prefix))
	e.U32(uint32(len(p.previous) - prefix - suffix))
	e.U32(uint32(end - prefix))

	// Move retained ranges before filling the gap. A large deletion should
	// allocate only the surviving ranges.
	old := p.previous
	if cap(old) > max(4096, len(captures)*4) {
		p.previous = make([]captureRange, len(captures))
		copy(p.previous, old[:prefix])
	} else {
		p.previous = slices.Grow(old, max(0, len(captures)-len(old)))[:len(captures)]
	}
	tail := p.previous[end:]
	copy(tail, old[len(old)-suffix:])
	if shift != 0 {
		for i := range tail {
			tail[i].start = captures[end+i].StartByte
			tail[i].end = captures[end+i].EndByte
		}
	}
	for i := prefix; i < end; i++ {
		token := captures[i]
		name := captureName{token.Capture, token.Scopes}
		id, exists := p.captures[name]
		if !exists {
			id = uint32(len(p.names))
			p.captures[name] = id
			p.names = append(p.names, name)
		}
		p.previous[i] = captureRange{token.StartByte, token.EndByte, id}
		e.U32(token.StartByte)
		e.U32(token.EndByte)
		e.U32(id)
		if !exists {
			e.String(name.capture)
			e.String(name.scopes)
		}
	}
}

// Compare actual captures after parsing: edits such as multiline comments may
// invalidate highlighting far beyond the textual edit. A suffix can be kept
// only when both its ranges and capture names match after the byte shift.
func (p *syntaxSession) captureDelta(after []syntax.Capture, shift int) (prefix, suffix int) {
	before := p.previous
	matches := func(old captureRange, next syntax.Capture, shift int) bool {
		return int(old.start)+shift == int(next.StartByte) && int(old.end)+shift == int(next.EndByte) &&
			p.names[old.name] == (captureName{next.Capture, next.Scopes})
	}
	for prefix < min(len(before), len(after)) && matches(before[prefix], after[prefix], 0) {
		prefix++
	}
	for suffix < min(len(before), len(after))-prefix && matches(before[len(before)-suffix-1], after[len(after)-suffix-1], shift) {
		suffix++
	}
	return
}
