//go:build codex_integration

package workbench

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexViewedImagesExpandAndPreview(t *testing.T) {
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 320, 200))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "viewed.png")
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PENCODE_CODEX_PATH", codexFixtureExecutable(t))
	t.Setenv("PENCODE_CODEX_FIXTURE", "1")
	t.Setenv("PENCODE_CODEX_VIEWED_IMAGE", path)
	testCodexStatusBarWorkspace(t, "")
}
