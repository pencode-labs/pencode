package client

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUneditedDocumentsMemoryBudget(t *testing.T) {
	c, w := peer(t, nil)
	var documents [64]*Document
	var names [64]string
	for i := range names {
		names[i] = fmt.Sprintf("%03d.txt", i)
		if err := os.WriteFile(filepath.Join(w.Path, names[i]), []byte("text\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for _, doc := range documents {
			if doc != nil {
				doc.Close()
			}
		}
	}()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i, name := range names {
		doc, _, err := c.OpenDocument(t.Context(), w.ID, name, true)
		if err != nil {
			t.Fatal(err)
		}
		documents[i] = doc
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	// Includes the in-process server and document workers. Leave ample room for
	// runtime noise, but catch per-document edit/RPC buffers allocated on open.
	if retained := int64(after.HeapAlloc) - int64(before.HeapAlloc); retained > 2<<20 {
		t.Fatalf("64 small unedited documents retained %d bytes", retained)
	}
}
