package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/pencode-labs/pencode/protocol"
)

func TestDocumentModesRejectBinaryBeforeCreatingTextModel(t *testing.T) {
	for _, content := range []string{"\x7fELF\x00executable", "\xff\xfeencoded", strings.Repeat("a", 1024) + "\x00tail"} {
		t.Run(content[:2], func(t *testing.T) {
			s, p := fixture(t)
			write(t, p, "fake.txt", content)
			doc, err := s.OpenDocument(p.ID, "fake.txt", protocol.DocumentText)
			if err != nil || doc.ID != 0 || doc.Content != "" || len(s.documents) != 0 || s.bytes != 0 {
				t.Fatalf("binary created a text model: %+v, %v", doc, err)
			}
			doc, err = s.OpenDocument(p.ID, "fake.txt", protocol.DocumentTextForced)
			if err != nil || doc.ID == 0 || !utf8.ValidString(doc.Content) || doc.Content != strings.ToValidUTF8(content, "\uFFFD") {
				t.Fatalf("explicit text open: %+v, %v", doc, err)
			}
			// Opening is read-only, including unsupported-encoding replacement.
			data, err := os.ReadFile(filepath.Join(p.Path, "fake.txt"))
			if err != nil || string(data) != content {
				t.Fatal("opening changed the original bytes", err)
			}
			if _, err := s.Change(doc.ID, doc.Version, 0, uint32(len(doc.Content)), uint32(len(doc.Content)), "!"); err != nil {
				t.Fatal(err)
			}
			if err := s.Save(doc.ID, doc.Version+1); err != nil {
				t.Fatal(err)
			}
			data, _ = os.ReadFile(filepath.Join(p.Path, "fake.txt"))
			if string(data) != doc.Content+"!" {
				t.Fatal("explicit text save failed")
			}
		})
	}
}
