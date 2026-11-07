package clipboard

import (
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestFileURIs(t *testing.T) {
	prefix := "/tmp/"
	if runtime.GOOS == "windows" {
		prefix = "/C:/资料/"
	}
	data := "# copied resources\r\nfile://" + prefix + "hello%20world.txt\r\nfile://localhost" + prefix + "%E4%B8%AD%E6%96%87.txt\n" +
		"https://example.com/file\nfile:relative\nfile:///bad%00name\nfile:///bad%ZZ\nfile:///tmp/a?query=1\nfile:///tmp/a#fragment\n"
	wantPrefix := prefix
	if runtime.GOOS == "windows" {
		wantPrefix = prefix[1:]
	}
	want := []string{filepath.FromSlash(wantPrefix + "hello world.txt"), filepath.FromSlash(wantPrefix + "中文.txt")}
	if got := fileURIs(data); !slices.Equal(got, want) {
		t.Fatalf("files: %q, want %q", got, want)
	}
	if n := testing.AllocsPerRun(100, func() { fileURIs("") }); n != 0 {
		t.Fatalf("empty clipboard allocated %g", n)
	}
}
