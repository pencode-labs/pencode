package server

import (
	"context"
	"unicode/utf8"

	"github.com/diiyw/onix/projects/pencode/protocol"
	"github.com/diiyw/onix/syntax"
	"github.com/odvcencio/gotreesitter"
	grammarruntime "github.com/odvcencio/gotreesitter/grammars/runtime"
)

type syntaxSession struct {
	document syntax.Document
	source   string
	version  uint64
	captures map[string]uint32
	previous []gotreesitter.HighlightRange
}

func (s *services) analyze(ctx context.Context, op protocol.Operation, d *protocol.Decoder, e *protocol.Encoder) error {
	id := d.U64()
	s.syntaxMu.Lock()
	defer s.syntaxMu.Unlock()
	if op == protocol.SyntaxRelease {
		trim := d.U32()
		if trim > 1 {
			return protocol.ErrProtocol
		}
		if err := d.Done(); err != nil {
			return err
		}
		if p := s.syntax[id]; p != nil {
			p.document.Close()
			delete(s.syntax, id)
		}
		if trim != 0 {
			syntax.ReleaseParserMemory()
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
		p = &syntaxSession{captures: make(map[string]uint32)}
		s.syntax[id] = p
	}
	if version != p.version {
		return &protocol.Error{Code: protocol.VersionMismatch, Message: "syntax version mismatch"}
	}
	if start < 0 || start > end || end > len(p.source) || !utf8.ValidString(text) || start > 0 && start < len(p.source) && !utf8.RuneStart(p.source[start]) || end < len(p.source) && !utf8.RuneStart(p.source[end]) || len(p.source)-(end-start)+len(text) > protocol.MaxMessage-1024 {
		return protocol.ErrProtocol
	}
	source := p.source[:start] + text + p.source[end:]
	if err := p.document.UpdateRange(path, source, start, end, start+len(text)); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		p.document.Close()
		delete(s.syntax, id)
		return err
	}
	shift := len(source) - len(p.source)
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
		captureID, exists := p.captures[token.Capture]
		if !exists {
			captureID = uint32(len(p.captures))
			p.captures[token.Capture] = captureID
		}
		e.U32(captureID)
		if !exists {
			e.String(token.Capture)
		}
	}
	p.previous = append(p.previous[:0], captures...)
	return nil
}

// Active parsers retain their language independently of the decoded cache.
func configureSyntaxMemory() { grammarruntime.SetEmbeddedLanguageCacheLimit(4) }

// Compare actual captures after parsing: edits such as multiline comments may
// invalidate highlighting far beyond the textual edit. A suffix can be kept
// only when both its ranges and capture names match after the byte shift.
func syntaxCaptureDelta(before, after []gotreesitter.HighlightRange, shift int) (prefix, suffix int) {
	for prefix < min(len(before), len(after)) && before[prefix] == after[prefix] {
		prefix++
	}
	for suffix < min(len(before), len(after))-prefix {
		old, next := before[len(before)-suffix-1], after[len(after)-suffix-1]
		if int(old.StartByte)+shift != int(next.StartByte) || int(old.EndByte)+shift != int(next.EndByte) || old.Capture != next.Capture {
			break
		}
		suffix++
	}
	return
}
