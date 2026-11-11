package desktopopen

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeDocumentEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "document-events")
	if output, err := exec.CommandContext(ctx, "xcrun", "clang", "-Werror", "tests/open_darwin_test.m", "open_darwin.m", "-framework", "AppKit", "-framework", "ApplicationServices", "-o", binary).CombinedOutput(); err != nil {
		t.Fatalf("compile document event test: %v: %s", err, output)
	}
	if output, err := exec.CommandContext(ctx, binary).CombinedOutput(); err != nil {
		t.Fatalf("native document event test: %v: %s", err, output)
	}
}
