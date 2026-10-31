package lsp

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/diiyw/onix/text/piecetree"
)

func BenchmarkSnapshotDocumentSync(b *testing.B) {
	for _, test := range []struct {
		name string
		kind int
		size int
	}{{"incremental", 2, 64 << 10}, {"incremental", 2, 2 << 20}, {"full", 1, 64 << 10}, {"full", 1, 2 << 20}} {
		b.Run(test.name+"/"+strconv.Itoa(test.size), func(b *testing.B) {
			local, peer := net.Pipe()
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				_, _ = io.Copy(io.Discard, peer)
			}()
			conn := NewConn(local, local, func() { local.Close() }, nil)
			b.Cleanup(func() { conn.Close(); peer.Close(); <-finished })
			c := &Client{Conn: conn, documents: make(map[URI]document), syncKind: test.kind, Encoding: UTF16}
			base := piecetree.FromString(strings.Repeat("value\n", test.size/6))
			middle := base.Len() / 2
			a := base.Replace(middle, middle+1, "a")
			views := [2]piecetree.Snapshot{a, a.Replace(middle, middle+1, "b")}
			version := 0
			sync := func() {
				version++
				if err := c.SyncDocument(b.Context(), "file:///tmp/test.go", "go", views[version%2], version); err != nil {
					b.Fatal(err)
				}
			}
			for range 128 {
				sync()
			}
			if err := c.Flush(b.Context()); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				sync()
			}
			if views[0].Materialized() || views[1].Materialized() {
				b.Fatal("LSP sync materialized a document")
			}
		})
	}
}

func TestJSONTextPiecesMatchEncoder(t *testing.T) {
	initial := strings.Repeat("中文🙂\r\n", 500) + "<>&\"\\\b\f\t\x00\x1f\u2028\u2029"
	source := piecetree.FromString(initial).Replace(3, 3, "edited🙂")
	var encoded bytes.Buffer
	writeJSONText(&encoded, source, 0, source.Len())
	expected := initial[:3] + "edited🙂" + initial[3:]
	want, err := json.Marshal(expected)
	if err != nil || !bytes.Equal(encoded.Bytes(), want) {
		t.Fatalf("piece encoding differs: %v", err)
	}
	if size := jsonTextSize(source, 0, source.Len()); size != len(want) {
		t.Fatalf("encoded text size = %d, want %d", size, len(want))
	}
	if source.Materialized() {
		t.Fatal("encoder materialized snapshot")
	}
}
func TestDocumentSnapshotsKeepRangesAndVersions(t *testing.T) {
	for _, kind := range []int{1, 2} {
		for _, encoding := range []PositionEncoding{UTF8, UTF16, UTF32} {
			t.Run(string(encoding)+string(rune('0'+kind)), func(t *testing.T) {
				local, peer := net.Pipe()
				defer peer.Close()
				peer.SetDeadline(time.Now().Add(5 * time.Second))
				conn := NewConn(local, local, func() { local.Close() }, nil)
				defer conn.Close()
				c := &Client{Conn: conn, documents: make(map[URI]document), syncKind: kind, Encoding: encoding, openClose: true, save: true, saveText: true}
				reader := NewReader(peer)
				read := func(method string) json.RawMessage {
					t.Helper()
					raw, err := reader.Read()
					var message Message
					if err != nil || json.Unmarshal(raw, &message) != nil || message.Method != method {
						t.Fatalf("expected %s: %s, %v", method, raw, err)
					}
					return message.Params
				}
				text := "中🙂\r\ntext\n"
				source := piecetree.FromString(text).Replace(0, 3, "界")
				expected := "界" + text[3:]
				if err := c.SyncDocument(t.Context(), "file:///tmp/doc", "go", source, 1); err != nil {
					t.Fatal(err)
				}
				var opened struct {
					TextDocument struct {
						Text string `json:"text"`
					} `json:"textDocument"`
				}
				if err := json.Unmarshal(read("textDocument/didOpen"), &opened); err != nil || opened.TextDocument.Text != expected {
					t.Fatal("bad snapshot open", err)
				}
				// Delete CR only, insert CR before LF, and edit a supplementary rune. All
				// incremental positions must keep CRLF and UTF-8/16/32 character boundaries.
				views := []string{strings.Replace(expected, "\r\n", "\n", 1), expected, "界a\r\ntext\n"}
				for i, next := range views {
					start, a, b := piecetree.FromString(expected).ChangedRange(piecetree.FromString(next))
					source = source.Replace(start, a, next[start:b])
					if err := c.SyncDocument(t.Context(), "file:///tmp/doc", "go", source, i+2); err != nil {
						t.Fatal(err)
					}
					var params struct {
						ContentChanges []struct {
							Range *Range `json:"range"`
							Text  string `json:"text"`
						} `json:"contentChanges"`
					}
					if err := json.Unmarshal(read("textDocument/didChange"), &params); err != nil || len(params.ContentChanges) != 1 {
						t.Fatal("bad snapshot change", err)
					}
					change := params.ContentChanges[0]
					got := change.Text
					if kind == 2 {
						if change.Range == nil {
							t.Fatal("missing range")
						}
						var err error
						got, err = ApplyTextEdits(expected, []TextEdit{{Range: *change.Range, NewText: change.Text}}, encoding)
						if err != nil {
							t.Fatal(err)
						}
					}
					if got != next {
						t.Fatalf("change reconstructed %q, want %q", got, next)
					}
					expected = next
					if source.Materialized() {
						t.Fatal("sync materialized document")
					}
				}
				if err := c.DidSaveText(t.Context(), "file:///tmp/doc", source); err != nil {
					t.Fatal(err)
				}
				var saved struct {
					Text string `json:"text"`
				}
				if err := json.Unmarshal(read("textDocument/didSave"), &saved); err != nil || saved.Text != expected {
					t.Fatal("bad saved snapshot", err)
				}
				if source.Materialized() {
					t.Fatal("save materialized document")
				}
			})
		}
	}
}
