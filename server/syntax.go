package server

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/onix-engine/onix/syntax"
	"github.com/onix-engine/onix/text/piecetree"
	"github.com/pencode-labs/pencode/protocol"
)

type syntaxSession struct {
	captures map[syntax.Capture]uint32
	source   piecetree.Snapshot
	previous []syntax.Capture
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
			p.document.Close()
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
		p = &syntaxSession{captures: make(map[syntax.Capture]uint32)}
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
	captures := p.document.Captures()
	prefix, suffix := syntaxCaptureDelta(p.previous, captures, shift)
	e.U32(uint32(prefix))
	e.U32(uint32(len(p.previous) - prefix - suffix))
	e.U32(uint32(len(captures) - prefix - suffix))
	for _, token := range captures[prefix : len(captures)-suffix] {
		e.U32(uint32(token.StartByte))
		e.U32(uint32(token.EndByte))
		name := syntax.Capture{Capture: token.Capture, Scopes: token.Scopes}
		captureID, exists := p.captures[name]
		if !exists {
			captureID = uint32(len(p.captures))
			p.captures[name] = captureID
		}
		e.U32(captureID)
		if !exists {
			e.String(token.Capture)
			e.String(token.Scopes)
		}
	}
	old := len(p.previous)
	p.previous = append(p.previous[:0], captures...)
	if len(p.previous) < old {
		clear(p.previous[len(p.previous):old])
	}
	if cap(p.previous) > max(4096, len(captures)*4) {
		p.previous = append([]syntax.Capture(nil), p.previous...)
	}
	return nil
}

// Compare actual captures after parsing: edits such as multiline comments may
// invalidate highlighting far beyond the textual edit. A suffix can be kept
// only when both its ranges and capture names match after the byte shift.
func syntaxCaptureDelta(before, after []syntax.Capture, shift int) (prefix, suffix int) {
	for prefix < min(len(before), len(after)) && before[prefix] == after[prefix] {
		prefix++
	}
	for suffix < min(len(before), len(after))-prefix {
		old, next := before[len(before)-suffix-1], after[len(after)-suffix-1]
		if int(old.StartByte)+shift != int(next.StartByte) || int(old.EndByte)+shift != int(next.EndByte) || old.Capture != next.Capture || old.Scopes != next.Scopes {
			break
		}
		suffix++
	}
	return
}
