package lsp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/diiyw/onix/text/piecetree"
)

type URI string

func FileURI(path string) URI {
	path = strings.ReplaceAll(path, "\\", "/")
	drive := len(path) >= 3 && path[1] == ':' && path[2] == '/'
	if !strings.HasPrefix(path, "/") && !drive {
		path, _ = filepath.Abs(path)
		path = filepath.ToSlash(path)
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return URI((&url.URL{Scheme: "file", Path: path}).String())
}
func (u URI) Path() (string, error) {
	parsed, err := url.Parse(string(u))
	if err != nil || parsed.Scheme != "file" || parsed.Host != "" && parsed.Host != "localhost" || parsed.RawQuery != "" || parsed.Fragment != "" || !strings.HasPrefix(parsed.Path, "/") || strings.ContainsRune(parsed.Path, 0) {
		return "", fmt.Errorf("lsp: invalid local file URI %q", u)
	}
	path := parsed.Path
	if len(path) > 3 && path[2] == ':' && path[3] == '/' {
		path = path[1:]
	}
	return path, nil
}

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}
type TextDocumentIdentifier struct {
	URI URI `json:"uri"`
}
type VersionedTextDocumentIdentifier struct {
	URI     URI  `json:"uri"`
	Version *int `json:"version"`
}
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}
type Location struct {
	URI   URI   `json:"uri"`
	Range Range `json:"range"`
}
type LocationLink struct {
	TargetURI            URI   `json:"targetUri"`
	TargetRange          Range `json:"targetRange"`
	TargetSelectionRange Range `json:"targetSelectionRange"`
}
type Diagnostic struct {
	Range              Range           `json:"range"`
	Severity           int             `json:"severity,omitempty"`
	Code               json.RawMessage `json:"code,omitempty"`
	Source             string          `json:"source,omitempty"`
	Message            string          `json:"message"`
	Tags               []int           `json:"tags,omitempty"`
	RelatedInformation json.RawMessage `json:"relatedInformation,omitempty"`
	Data               json.RawMessage `json:"data,omitempty"`
}
type PublishDiagnosticsParams struct {
	URI         URI          `json:"uri"`
	Version     *int         `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}
type Command struct {
	Title     string            `json:"title"`
	Command   string            `json:"command"`
	Arguments []json.RawMessage `json:"arguments,omitempty"`
}
type CompletionItem struct {
	Label               string          `json:"label"`
	Kind                int             `json:"kind,omitempty"`
	SortText            string          `json:"sortText,omitempty"`
	FilterText          string          `json:"filterText,omitempty"`
	Detail              string          `json:"detail,omitempty"`
	InsertText          string          `json:"insertText,omitempty"`
	InsertTextFormat    int             `json:"insertTextFormat,omitempty"`
	TextEdit            *TextEdit       `json:"textEdit,omitempty"`
	AdditionalTextEdits []TextEdit      `json:"additionalTextEdits,omitempty"`
	Command             *Command        `json:"command,omitempty"`
	Data                json.RawMessage `json:"data,omitempty"`
}
type CodeAction struct {
	Title    string         `json:"title"`
	Kind     string         `json:"kind,omitempty"`
	Edit     *WorkspaceEdit `json:"edit,omitempty"`
	Command  *Command       `json:"command,omitempty"`
	Disabled *struct {
		Reason string `json:"reason"`
	} `json:"disabled,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}
type TextDocumentEdit struct {
	TextDocument VersionedTextDocumentIdentifier `json:"textDocument"`
	Edits        []TextEdit                      `json:"edits"`
}
type WorkspaceEdit struct {
	Changes           map[URI][]TextEdit `json:"changes,omitempty"`
	DocumentChanges   []json.RawMessage  `json:"documentChanges,omitempty"`
	ChangeAnnotations json.RawMessage    `json:"changeAnnotations,omitempty"`
}
type WorkspaceFolder struct {
	URI  URI    `json:"uri"`
	Name string `json:"name"`
}
type Registration struct {
	ID              string          `json:"id"`
	Method          string          `json:"method"`
	RegisterOptions json.RawMessage `json:"registerOptions,omitempty"`
}
type ServerCapabilities map[string]json.RawMessage

func (c ServerCapabilities) Has(key string) bool {
	v := strings.TrimSpace(string(c[key]))
	return v != "" && v != "false" && v != "null"
}

// Offsets and positions are checked against rune boundaries. CRLF is one line
// ending; characters count UTF-16 code units by default, never UTF-8 bytes.
type PositionEncoding string

const (
	UTF8  PositionEncoding = "utf-8"
	UTF16 PositionEncoding = "utf-16"
	UTF32 PositionEncoding = "utf-32"
)

func units(r rune, size int, encoding PositionEncoding) int {
	if encoding == UTF8 {
		return size
	}
	if encoding != UTF32 && r > 0xffff {
		return 2
	}
	return 1
}
func PositionAt[V piecetree.Value](value V, offset int, encoding PositionEncoding) (Position, error) {
	text := piecetree.Text(value)
	if offset < 0 || offset > text.Len() || offset < text.Len() && !utf8.RuneStart(text.ByteAt(offset)) {
		return Position{}, fmt.Errorf("lsp: invalid byte offset %d", offset)
	}
	if offset > 0 && offset < text.Len() && text.ByteAt(offset-1) == '\r' && text.ByteAt(offset) == '\n' {
		return Position{}, fmt.Errorf("lsp: byte offset splits CRLF: %d", offset)
	}
	line, _ := text.Position(offset)
	p := Position{Line: line}
	for i := text.LineStart(line); i < offset; {
		r, size := text.DecodeRune(i)
		if r != '\r' || i+1 >= text.Len() || text.ByteAt(i+1) != '\n' {
			p.Character += units(r, size, encoding)
		}
		i += size
	}
	return p, nil
}
func OffsetAt[V piecetree.Value](value V, p Position, encoding PositionEncoding) (int, error) {
	text := piecetree.Text(value)
	if p.Line < 0 || p.Character < 0 {
		return 0, fmt.Errorf("lsp: negative position")
	}
	if p.Line >= text.Lines() {
		return 0, fmt.Errorf("lsp: position beyond document: %+v", p)
	}
	column := 0
	for i := text.LineStart(p.Line); ; {
		if column == p.Character {
			return i, nil
		}
		if i == text.Len() {
			return 0, fmt.Errorf("lsp: position beyond document: %+v", p)
		}
		r, size := text.DecodeRune(i)
		if r == '\n' || r == '\r' && i+1 < text.Len() && text.ByteAt(i+1) == '\n' {
			return 0, fmt.Errorf("lsp: position beyond line: %+v", p)
		}
		column += units(r, size, encoding)
		if column > p.Character {
			return 0, fmt.Errorf("lsp: position splits a character: %+v", p)
		}
		i += size
	}
}
