package server

import (
	"os"
	"testing"

	"github.com/pencode-labs/pencode/protocol"
)

func TestExtensionResourcesReturnOrderedPrefix(t *testing.T) {
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	const size = 9 << 20
	data := make([]byte, size)
	s := extensionService{directory: directory, entries: map[uint64]*extensionEntry{1: {
		root: root, resourceData: map[string][]byte{"resources/first": data, "resources/second": data},
	}}}
	var request, result protocol.Encoder
	request.U64(1)
	request.U32(size)
	request.U32(2)
	request.String("resources/first")
	request.String("resources/second")
	d := protocol.Decoder{Buffer: request.Buffer}
	if err := s.dispatch(t.Context(), protocol.ExtensionResources, "", &d, &result); err != nil {
		t.Fatal(err)
	}
	d = protocol.Decoder{Buffer: result.Buffer}
	if d.U32() != 1 || d.String() != "resources/first" || len(d.Bytes()) != size || d.Done() != nil || len(result.Buffer) > extensionResourcePageBytes {
		t.Fatal("batch did not return a bounded, ordered prefix")
	}
}
