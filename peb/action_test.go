package peb

import (
	"bytes"
	"context"
	"io"
	"slices"
	"testing"
)

func TestActionCodecAndReusableEncoding(t *testing.T) {
	change := ActionChange{Action: Action{ID: "build", Text: "构建", Icon: "resources/build.svg", Tooltip: "Build project", Color: "#112233", IconColor: "#abcdef", IconStyle: "opacity:.5", Location: TitleBarRight, Disabled: true}}
	data, err := EncodeActionChange(nil, change)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeActionChange(data)
	if err != nil || got != change {
		t.Fatalf("%+v %v", got, err)
	}
	for n := range len(data) {
		if _, err := DecodeActionChange(data[:n]); err == nil {
			t.Fatalf("accepted truncation %d", n)
		}
	}
	if _, err := DecodeActionChange(append(bytes.Clone(data), 0)); err == nil {
		t.Fatal("accepted trailing byte")
	}
	message := ActionContext{ID: "build", ProjectPath: "/项目", Path: "/项目/main.go", Caret: 42, Anchor: 7, Directory: true}
	contextData, _ := EncodeActionContext(nil, message)
	decoded, err := DecodeActionContext(contextData)
	if err != nil || decoded != message {
		t.Fatal(decoded, err)
	}
	contributions := Contributions{Actions: []Action{change.Action}, ActionService: true}
	registration, _ := EncodeContributions(nil, contributions)
	decodedRegistration, err := DecodeContributions(registration)
	if err != nil || !decodedRegistration.ActionService || !slices.Equal(decodedRegistration.Actions, contributions.Actions) {
		t.Fatal(decodedRegistration, err)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		data, _ = EncodeActionChange(data, change)
		contextData, _ = EncodeActionContext(contextData, message)
	}); allocs != 0 {
		t.Fatalf("codec allocated %g", allocs)
	}
	change.Action.Location = 99
	if _, err := EncodeActionChange(data, change); err == nil {
		t.Fatal("accepted unknown location")
	}
}
func TestActionServicePublishesWithoutClickAndHandlesInvocation(t *testing.T) {
	input, hostWriter := io.Pipe()
	hostReader, output := io.Pipe()
	done := make(chan error, 1)
	a := Action{ID: "build", Text: "Build", Location: StatusBar}
	go func() {
		defer output.Close()
		defer input.Close()
		var publisher *ActionPublisher
		done <- ServeExtension(input, output, Contributions{}, Handlers{
			ActivateActions: func(ctx context.Context, p *ActionPublisher) { publisher = p; _ = p.Update(a) },
			Action: func(c ActionContext) error {
				if c.ID != "build" {
					t.Errorf("wrong click: %+v", c)
				}
				updated := a
				updated.Text = "Done"
				return publisher.Update(updated)
			},
		})
	}()
	defer hostWriter.Close()
	defer hostReader.Close()
	w, r := Writer{Target: hostWriter}, Reader{Source: hostReader}
	hello, _ := EncodeHello(nil, Hello{Host: "pencode", Capabilities: Capabilities})
	if err := w.Write(Initialize, 1, hello); err != nil {
		t.Fatal(err)
	}
	frame, err := r.Read()
	if err != nil || frame.Kind != Register {
		t.Fatal(frame, err)
	}
	c, err := DecodeContributions(frame.Payload)
	if err != nil || !c.ActionService {
		t.Fatal(c, err)
	}
	// Activation may immediately publish. Write in parallel to avoid pipe rendezvous.
	activated := make(chan error, 1)
	go func() { activated <- w.Write(ActionActivate, 0, nil) }()
	frame, err = r.Read()
	if err != nil || frame.Kind != ActionUpdate || frame.ID != 0 {
		t.Fatal(frame, err)
	}
	if err := <-activated; err != nil {
		t.Fatal(err)
	}
	change, err := DecodeActionChange(frame.Payload)
	if err != nil || change.Action != a {
		t.Fatal(change, err)
	}
	payload, _ := EncodeActionContext(nil, ActionContext{ID: a.ID, Path: "/project/main.go"})
	if err := w.Write(ActionInvoke, 2, payload); err != nil {
		t.Fatal(err)
	}
	frame, err = r.Read()
	if err != nil || frame.Kind != ActionUpdate {
		t.Fatal(frame, err)
	}
	change, err = DecodeActionChange(frame.Payload)
	if err != nil || change.Action.Text != "Done" {
		t.Fatal(change, err)
	}
	frame, err = r.Read()
	if err != nil || frame.Kind != ActionResponse || frame.ID != 2 {
		t.Fatal(frame, err)
	}
	if err := w.Write(Shutdown, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
