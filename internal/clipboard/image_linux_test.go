package clipboard

import (
	"errors"
	"testing"
)

func TestPNGType(t *testing.T) {
	for _, types := range []string{"image/png", "text/plain\nimage/png\n", "image/png\r\n"} {
		if !hasPNGType(types) {
			t.Fatalf("missing PNG in %q", types)
		}
	}
	for _, types := range []string{"", "text/plain", "image/png-extra", "application/image/png"} {
		if hasPNGType(types) {
			t.Fatalf("false PNG in %q", types)
		}
	}
}

func TestImageBufferLimit(t *testing.T) {
	var output imageBuffer
	output.buffer.Grow(maxImageBytes)
	output.buffer.Write(make([]byte, maxImageBytes))
	if _, err := output.Write([]byte{1}); !errors.Is(err, errImageTooLarge) {
		t.Fatal(err)
	}
	if output.buffer.Len() != maxImageBytes {
		t.Fatal("buffer exceeded limit")
	}
}
