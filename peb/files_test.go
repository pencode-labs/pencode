package peb

import (
	"reflect"
	"testing"
)

func TestFileChangesRoundTrip(t *testing.T) {
	changes := FileChanges{Root: "/项目", Reset: true, Files: []FileEvent{{Path: "src/文件.go", Op: FileCreated | FileWritten}, {Path: "removed", Op: FileRemoved, Directory: true}}}
	data, err := EncodeFileChanges(nil, changes)
	if err != nil {
		t.Fatal(err)
	}
	result, err := DecodeFileChanges(data)
	if err != nil || !reflect.DeepEqual(changes, result) {
		t.Fatalf("file changes: %+v, %v", result, err)
	}
	if _, err = DecodeFileChanges(data[:len(data)-1]); err == nil {
		t.Fatal("accepted truncated file event")
	}
	for _, frame := range []Kind{FileChangesRequest, FileChangesResponse} {
		if !validFrame(frame, 2, 0) || validFrame(frame, 0, 0) {
			t.Fatal("file frames must have request IDs")
		}
	}
}
