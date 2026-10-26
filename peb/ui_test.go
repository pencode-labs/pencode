package peb

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"reflect"
	"testing"
)

func TestUICodecsAndFrameRules(t *testing.T) {
	ui := UI{ID: "tools", Title: "工具", Anchor: "run", Kind: Popup, CloseOnOutsideClick: true, Placement: UIPlacement{Mode: PositionAnchor, X: -240.5, Y: 8, Width: 480, Height: 320}, View: &EditorView{HTML: `<button data-ref="run" role="menuitem">Run</button>`, CSS: `button { padding: 4px; }`, Events: []EditorBinding{{Ref: "run", Kind: "click"}}}}
	data, err := EncodeUI(nil, ui)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeUI(data)
	if err != nil || !reflect.DeepEqual(decoded, ui) {
		t.Fatal(decoded, err)
	}
	kindOffset := 12 + len(ui.ID) + len(ui.Title) + len(ui.Anchor)
	invalid := bytes.Clone(data)
	binary.LittleEndian.PutUint32(invalid[kindOffset:], 99)
	if _, err := DecodeUI(invalid); err == nil {
		t.Fatal("accepted unknown decoded UI kind")
	}
	invalid = bytes.Clone(data)
	binary.LittleEndian.PutUint32(invalid[kindOffset+4:], 2)
	if _, err := DecodeUI(invalid); err == nil {
		t.Fatal("accepted nonboolean close flag")
	}
	invalid = bytes.Clone(data)
	binary.LittleEndian.PutUint32(invalid[kindOffset+28:], 2)
	if _, err := DecodeUI(invalid); err == nil {
		t.Fatal("accepted nonboolean outside-close flag")
	}
	for _, mutation := range []struct {
		offset int
		value  uint32
	}{
		{8, 99}, // Unknown placement mode.
		{12, math.Float32bits(float32(math.NaN()))},
		{16, math.Float32bits(float32(math.Inf(1)))},
		{20, math.Float32bits(-1)},
		{24, math.Float32bits(float32(math.Inf(-1)))},
	} {
		invalid = bytes.Clone(data)
		binary.LittleEndian.PutUint32(invalid[kindOffset+mutation.offset:], mutation.value)
		if _, err := DecodeUI(invalid); err == nil {
			t.Fatalf("accepted invalid placement at %d", mutation.offset)
		}
	}
	for _, placement := range []UIPlacement{
		{Mode: 99}, {X: float32(math.NaN())}, {Y: float32(math.Inf(-1))},
		{Width: -1}, {Height: -1}, {Width: float32(math.Inf(1))}, {Height: float32(math.NaN())},
	} {
		bad := ui
		bad.Placement = placement
		if _, err := EncodeUI(nil, bad); err == nil {
			t.Fatalf("accepted invalid placement %+v", placement)
		}
	}
	badPanel := ui
	badPanel.Kind = Panel
	if _, err := EncodeUI(nil, badPanel); err == nil {
		t.Fatal("accepted positioned workbench panel")
	}
	for i := range len(data) {
		if _, err := DecodeUI(data[:i]); err == nil {
			t.Fatalf("accepted UI truncation %d", i)
		}
	}
	if _, err := DecodeUI(append(bytes.Clone(data), 0)); err == nil {
		t.Fatal("accepted trailing data")
	}
	event := UIEvent{ID: "tools", Reason: "escape", Context: ActionContext{ID: "run", ProjectPath: "/项目", Path: "/项目/main.go", Caret: 7, Anchor: 2}, Event: EditorEvent{Kind: "input", Ref: "query", Value: "搜索", Data: "索", InputType: "insertText", Checked: true, Selected: 2, Anchor: 1, Focus: 3}}
	eventData, err := EncodeUIEvent(nil, event)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeUIEvent(eventData)
	if err != nil || got != event {
		t.Fatal(got, err)
	}
	for i := range len(eventData) {
		if _, err := DecodeUIEvent(eventData[:i]); err == nil {
			t.Fatalf("accepted event truncation %d", i)
		}
	}
	if _, err := DecodeUIEvent(append(bytes.Clone(eventData), 0)); err == nil {
		t.Fatal("accepted trailing event data")
	}
	if n := testing.AllocsPerRun(100, func() { data, _ = EncodeUI(data, ui); eventData, _ = EncodeUIEvent(eventData, event) }); n != 0 {
		t.Fatalf("reused codecs allocated %g", n)
	}
	for _, test := range []struct {
		kind  Kind
		id    uint32
		size  int
		valid bool
	}{
		{UIUpdate, 0, 10, true}, {UIUpdate, 1, 10, false}, {UIRequest, 0, 10, false}, {UIRequest, 2, 10, true}, {UIResponse, 2, 0, true}, {UIResponse, 0, 0, false}, {UIResponse, 2, 1, false},
	} {
		if validFrame(test.kind, test.id, test.size) != test.valid {
			t.Fatal(test)
		}
	}
	ui.Kind = 99
	if _, err := EncodeUI(data, ui); err == nil {
		t.Fatal("accepted invalid UI kind")
	}
	ui.Kind = Dialog
	ui.ID = ""
	if _, err := EncodeUI(data, ui); err == nil {
		t.Fatal("accepted empty UI ID")
	}
}

func TestUIServiceUpdatesDuringEventsAndCancelsPublisher(t *testing.T) {
	input, hostWriter := io.Pipe()
	hostReader, output := io.Pipe()
	defer hostWriter.Close()
	defer hostReader.Close()
	done := make(chan error, 1)
	var publisher *ActionPublisher
	go func() {
		defer output.Close()
		defer input.Close()
		done <- ServeExtension(input, output, Contributions{}, Handlers{
			ActivateActions: func(_ context.Context, p *ActionPublisher) { publisher = p },
			UIEvent: func(event UIEvent) error {
				if event.ID != "tools" || event.Event.Kind != "click" {
					t.Errorf("unexpected UI event: %+v", event)
				}
				return publisher.Show(UI{ID: "result", Kind: Panel, Title: "Result", View: &EditorView{HTML: `<p>Done</p>`}})
			},
		})
	}()
	w, r := Writer{Target: hostWriter}, Reader{Source: hostReader}
	hello, _ := EncodeHello(nil, Hello{Host: "pencode", Capabilities: Capabilities})
	if err := w.Write(Initialize, 1, hello); err != nil {
		t.Fatal(err)
	}
	frame, err := r.Read()
	if err != nil || frame.Kind != Register {
		t.Fatal(frame, err)
	}
	registration, err := DecodeContributions(frame.Payload)
	if err != nil || !registration.ActionService {
		t.Fatal(registration, err)
	}
	if err := w.Write(ActionActivate, 0, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := EncodeUIEvent(nil, UIEvent{ID: "tools", Event: EditorEvent{Kind: "click"}})
	if err := w.Write(UIRequest, 2, data); err != nil {
		t.Fatal(err)
	}
	frame, err = r.Read()
	if err != nil || frame.Kind != UIUpdate || frame.ID != 0 {
		t.Fatal(frame, err)
	}
	ui, err := DecodeUI(frame.Payload)
	if err != nil || ui.ID != "result" || ui.Kind != Panel {
		t.Fatal(ui, err)
	}
	frame, err = r.Read()
	if err != nil || frame.Kind != UIResponse || frame.ID != 2 {
		t.Fatal(frame, err)
	}
	if err := w.Write(Shutdown, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := publisher.CloseUI("result"); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestNotificationCodec(t *testing.T) {
	ui := UI{ID: "build", Title: "构建完成", Kind: Notification, AutoCloseMS: 5000, View: &EditorView{
		HTML:   `<p>构建完成</p><button data-ref="open">查看结果</button>`,
		Events: []EditorBinding{{Ref: "open", Kind: "click"}},
	}}
	data, err := EncodeUI(nil, ui)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeUI(data)
	if err != nil || !reflect.DeepEqual(got, ui) {
		t.Fatal(got, err)
	}
	if n := testing.AllocsPerRun(100, func() { data, _ = EncodeUI(data, ui) }); n != 0 {
		t.Fatalf("notification encoding allocated %g", n)
	}
	for i := range len(data) {
		if _, err := DecodeUI(data[:i]); err == nil {
			t.Fatalf("accepted notification truncation %d", i)
		}
	}
	// Notifications use host placement; expiry belongs only to notifications.
	ui.Placement.X = 1
	if _, err := EncodeUI(nil, ui); err == nil {
		t.Fatal("accepted positioned notification")
	}
	ui.Placement = UIPlacement{}
	ui.Kind = Popup
	if _, err := EncodeUI(nil, ui); err == nil {
		t.Fatal("accepted expiring popup")
	}
}
