package workbench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPencodeReadContentOwnership(t *testing.T) {
	dir := t.TempDir()
	for _, content := range []string{"", "package demo\n// 中文\n", strings.Repeat("a\x00中\n", 20000)} {
		filename := filepath.Join(dir, "source")
		if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		first, err := readPencodeContent(filename)
		if err != nil || first != content {
			t.Fatalf("read %d bytes: %v", len(content), err)
		}
		if err := os.WriteFile(filename, []byte("replacement"), 0600); err != nil {
			t.Fatal(err)
		}
		second, err := readPencodeContent(filename)
		if err != nil || second != "replacement" || first != content {
			t.Fatal("later reads overwrote an open file's content")
		}
	}
	if _, err := readPencodeContent(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing file succeeded")
	}
	if _, err := readPencodeContent(dir); err == nil {
		t.Fatal("reading a directory succeeded")
	}
}

var pencodeReadResult string

func BenchmarkPencodeReadContent(b *testing.B) {
	filename := filepath.Join(b.TempDir(), "main.go")
	content := strings.Repeat("// representative source 中文\n", 32768)
	if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
		b.Fatal(err)
	}
	for _, name := range []string{"ReadFileThenString", "OwnedString"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(content)))
			for b.Loop() {
				var value string
				var err error
				if name == "ReadFileThenString" {
					var data []byte
					data, err = os.ReadFile(filename)
					value = string(data)
				} else {
					value, err = readPencodeContent(filename)
				}
				if err != nil {
					b.Fatal(err)
				}
				pencodeReadResult = value
			}
		})
	}
	pencodeReadResult = ""
}
