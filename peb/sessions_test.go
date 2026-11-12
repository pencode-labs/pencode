package peb

import (
	"bytes"
	"context"
	"testing"
)

func TestSessionsIsolateViewStateAndCancellation(t *testing.T) {
	var input, output bytes.Buffer
	w := Writer{Target: &input}
	hello, _ := EncodeHello(nil, Hello{Host: "test", Capabilities: Capabilities})
	for _, session := range []uint32{1, 2} {
		_ = w.WriteSession(session, Initialize, 1, hello)
		message, _ := EncodeEditorMessage(nil, EditorMessage{Path: "same.txt"})
		_ = w.WriteSession(session, EditorRequest, 2, message)
	}
	_ = w.WriteSession(1, Shutdown, 0, nil)
	message, _ := EncodeEditorMessage(nil, EditorMessage{Path: "same.txt"})
	_ = w.WriteSession(2, EditorRequest, 2, message) // Request IDs are local to their session.
	_ = w.Write(Shutdown, 0, nil)
	var contexts []context.Context
	calls := 0
	err := ServeExtension(&input, &output, Contributions{}, Handlers{
		NewEditor: func(ctx context.Context) func(EditorMessage) (EditorReply, error) {
			contexts = append(contexts, ctx)
			count := 0
			return func(EditorMessage) (EditorReply, error) {
				if len(contexts) > 1 && contexts[1].Err() != nil {
					t.Fatal("closing view 1 canceled view 2")
				}
				count++
				calls++
				value := string(rune('0' + count))
				return EditorReply{Content: &value}, nil
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(contexts) != 2 || contexts[0].Err() == nil || contexts[1].Err() == nil {
		t.Fatal("session lifetimes were not released")
	}
	r := Reader{Source: &output}
	for i, want := range []struct {
		value   string
		session uint32
	}{{session: 1, value: "1"}, {session: 2, value: "1"}, {session: 2, value: "2"}} {
		f, err := r.Read()
		if i < 2 {
			if err != nil || f.Kind != Register || f.Session != want.session {
				t.Fatal(f, err)
			}
			f, err = r.Read()
		}
		if err != nil || f.Kind != EditorResponse || f.Session != want.session || f.ID != 2 {
			t.Fatal(f, err)
		}
		reply, err := DecodeEditorReply(f.Payload)
		if err != nil || reply.Content == nil || *reply.Content != want.value {
			t.Fatal(reply, err)
		}
	}
}
