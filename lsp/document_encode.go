package lsp

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"unicode/utf8"

	"github.com/diiyw/onix/text/piecetree"
)

// Document notifications encode pieces directly into the buffer owned by the
// connection writer. Even full sync never constructs a second flat document.
func (c *Conn) notifyDocumentText(ctx context.Context, method string, uri URI, language string, version int, value piecetree.Snapshot, start, end int, change *Range) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.Err()
	default:
	}
	data := messageBuffers.Get().(*bytes.Buffer)
	data.Grow(256)
	data.WriteString(`{"jsonrpc":"2.0","method":`)
	writeJSONText(data, piecetree.FromString(method), 0, len(method))
	data.WriteString(`,"params":{"textDocument":{"uri":`)
	writeJSONText(data, piecetree.FromString(string(uri)), 0, len(uri))
	var scratch [32]byte
	switch method {
	case "textDocument/didOpen":
		data.WriteString(`,"languageId":`)
		writeJSONText(data, piecetree.FromString(language), 0, len(language))
		data.WriteString(`,"version":`)
		data.Write(strconv.AppendInt(scratch[:0], int64(version), 10))
		data.WriteString(`,"text":`)
	case "textDocument/didChange":
		data.WriteString(`,"version":`)
		data.Write(strconv.AppendInt(scratch[:0], int64(version), 10))
		data.WriteString(`},"contentChanges":[{`)
		if change != nil {
			data.WriteString(`"range":{"start":`)
			writeJSONPosition(data, change.Start)
			data.WriteString(`,"end":`)
			writeJSONPosition(data, change.End)
			data.WriteString(`},`)
		}
		data.WriteString(`"text":`)
	case "textDocument/didSave":
		data.WriteString(`},"text":`)
	default:
		releaseMessage(data)
		return errors.New("lsp: invalid document notification")
	}
	// Escaping can grow source code substantially (newlines, tabs, HTML).
	// Reserve the encoded size once so a large notification never doubles its
	// document buffer while escaping the final chunks.
	size := jsonTextSize(value, start, end)
	if size+data.Len() > MaxMessage {
		releaseMessage(data)
		return errors.New("lsp: message too large")
	}
	data.Grow(size + 4)
	writeJSONText(data, value, start, end)
	switch method {
	case "textDocument/didOpen":
		data.WriteString(`}}}`)
	case "textDocument/didChange":
		data.WriteString(`}]}}`)
	case "textDocument/didSave":
		data.WriteString(`}}`)
	}
	if data.Len() > MaxMessage {
		releaseMessage(data)
		return errors.New("lsp: message too large")
	}
	return c.enqueue(ctx, outbound{data: data})
}

func jsonTextSize(value piecetree.Snapshot, start, end int) int {
	size := end - start + 2
	for part := range value.Chunks(start, end) {
		for i := 0; i < len(part); {
			b := part[i]
			if b >= utf8.RuneSelf {
				r, width := utf8.DecodeRuneInString(part[i:])
				if r == utf8.RuneError && width == 1 {
					size += 5
				} else if r == '\u2028' || r == '\u2029' {
					size += 3
				}
				i += width
				continue
			}
			switch b {
			case '"', '\\', '\b', '\f', '\n', '\r', '\t':
				size++
			case '<', '>', '&':
				size += 5
			default:
				if b < 0x20 {
					size += 5
				}
			}
			i++
		}
	}
	return size
}
func writeJSONPosition(data *bytes.Buffer, p Position) {
	var scratch [32]byte
	data.WriteString(`{"line":`)
	data.Write(strconv.AppendInt(scratch[:0], int64(p.Line), 10))
	data.WriteString(`,"character":`)
	data.Write(strconv.AppendInt(scratch[:0], int64(p.Character), 10))
	data.WriteByte('}')
}
func writeJSONText(data *bytes.Buffer, value piecetree.Snapshot, start, end int) {
	const hex = "0123456789abcdef"
	data.WriteByte('"')
	for part := range value.Chunks(start, end) {
		begin := 0
		for i := 0; i < len(part); {
			b := part[i]
			if b >= utf8.RuneSelf {
				r, size := utf8.DecodeRuneInString(part[i:])
				if r == utf8.RuneError && size == 1 || r == '\u2028' || r == '\u2029' {
					data.WriteString(part[begin:i])
					if size == 1 {
						data.WriteString(`\ufffd`)
					} else if r == '\u2028' {
						data.WriteString(`\u2028`)
					} else {
						data.WriteString(`\u2029`)
					}
					i += size
					begin = i
					continue
				}
				i += size
				continue
			}
			if b >= 0x20 && b != '"' && b != '\\' && b != '<' && b != '>' && b != '&' {
				i++
				continue
			}
			data.WriteString(part[begin:i])
			data.WriteByte('\\')
			switch b {
			case '"', '\\':
				data.WriteByte(b)
			case '\n':
				data.WriteByte('n')
			case '\r':
				data.WriteByte('r')
			case '\t':
				data.WriteByte('t')
			case '\b':
				data.WriteByte('b')
			case '\f':
				data.WriteByte('f')
			default:
				data.WriteString("u00")
				data.WriteByte(hex[b>>4])
				data.WriteByte(hex[b&15])
			}
			i++
			begin = i
		}
		data.WriteString(part[begin:])
	}
	data.WriteByte('"')
}
