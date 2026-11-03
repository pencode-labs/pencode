package peb

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestLifecycleServiceWithoutViews(t *testing.T) {
	for _, handled := range []bool{false, true} {
		var input, output bytes.Buffer
		w := Writer{Target: &input}
		hello, _ := EncodeHello(nil, Hello{Host: "test", Capabilities: Capabilities})
		_ = w.Write(Initialize, 1, hello)
		_ = w.Write(ActionActivate, 0, nil)
		events := [...]LifecycleEvent{{Kind: WindowClosing}, {Kind: ProjectClosing, ProjectPath: "/project"}, {Kind: ProjectClosed, ProjectPath: "/project"}}
		for i, event := range events {
			data, _ := EncodeLifecycleEvent(nil, event)
			_ = w.Write(LifecycleRequest, uint32(i+2), data)
		}
		_ = w.Write(Shutdown, 0, nil)
		handlers := Handlers{ActivateActions: func(_ctx context.Context, _p *ActionPublisher) {}}
		calls := 0
		if handled {
			handlers.Lifecycle = func(event LifecycleEvent) (LifecycleReply, error) {
				if event != events[calls] {
					t.Fatal("wrong lifecycle event", event)
				}
				calls++
				return LifecycleReply{Message: "running"}, nil
			}
		}
		if err := ServeExtension(&input, &output, Contributions{}, handlers); err != nil {
			t.Fatal(err)
		}
		r := Reader{Source: &output}
		if f, err := r.Read(); err != nil || f.Kind != Register {
			t.Fatal(f, err)
		}
		for i := range events {
			f, err := r.Read()
			if err != nil || f.Kind != LifecycleResponse || f.ID != uint32(i+2) {
				t.Fatal(f, err)
			}
			reply, err := DecodeLifecycleReply(f.Payload)
			if err != nil || (reply.Message != "") != handled {
				t.Fatal(reply, err)
			}
		}
	}
}

func TestLifecycleRejectsInvalidMessagesAndReusesBuffer(t *testing.T) {
	event := LifecycleEvent{Kind: ProjectClosing, ProjectPath: "/project"}
	data, _ := EncodeLifecycleEvent(nil, event)
	for n := range len(data) {
		if _, err := DecodeLifecycleEvent(data[:n]); !errors.Is(err, ErrProtocol) {
			t.Fatalf("accepted truncated event at %d", n)
		}
	}
	if _, err := EncodeLifecycleEvent(nil, LifecycleEvent{Kind: ProjectClosed + 1}); !errors.Is(err, ErrProtocol) {
		t.Fatal("accepted invalid kind")
	}
	data[0] = byte(ProjectClosed + 1)
	if _, err := DecodeLifecycleEvent(data); !errors.Is(err, ErrProtocol) {
		t.Fatal("decoded invalid kind")
	}
	if n := testing.AllocsPerRun(100, func() { data, _ = EncodeLifecycleEvent(data, event) }); n != 0 {
		t.Fatalf("warm lifecycle codec allocated %g times", n)
	}
}
