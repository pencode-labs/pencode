package peb

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func streamView(tail string) UI {
	return UI{ID: "chat", Title: "Chat", Kind: EditorTab, View: &EditorView{
		HTML: `<main><section data-ref="history" data-pencode-region>` + strings.Repeat("history", 1000) + `</section><section data-ref="tail" data-pencode-region>` + tail + `</section><textarea data-ref="prompt">draft</textarea></main>`,
		CSS:  "textarea {color:red}", Events: []EditorBinding{{Ref: "prompt", Kind: "input"}},
	}}
}

func TestViewStreamPatchesAndFullFallbacks(t *testing.T) {
	var wire bytes.Buffer
	p := ActionPublisher{ctx: t.Context(), ui: true, writer: &Writer{Target: &wire}}
	var receiver ViewReceiver
	send := func(ui UI, patch bool) UI {
		t.Helper()
		wire.Reset()
		if err := p.Show(ui); err != nil {
			t.Fatal(err)
		}
		reader := Reader{Source: &wire}
		frame, err := reader.Read()
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeUI(frame.Payload)
		if err != nil || (got.Patch != nil) != patch {
			t.Fatalf("patch=%v: %+v %v", patch, got, err)
		}
		if patch && len(frame.Payload) > 300 {
			t.Fatalf("sent unchanged history: %d bytes", len(frame.Payload))
		}
		full, err := receiver.Apply(got)
		if err != nil || (full.View == nil) != (ui.View == nil) || ui.View != nil && (full.View.HTML != ui.View.HTML || full.View.CSS != ui.View.CSS || !slices.Equal(full.View.Events, ui.View.Events)) {
			t.Fatal("round trip changed view", err)
		}
		return got
	}
	send(streamView("first"), false)
	patch := send(streamView("second中😀"), true)
	if len(patch.Patch.Regions) != 1 || patch.Patch.Regions[0].Ref != "tail" {
		t.Fatal(patch.Patch)
	}
	send(streamView(""), true) // Clearing a region must be distinguishable from omitting it.
	for _, change := range []func(*UI){
		func(ui *UI) { ui.View.CSS = "textarea {color:blue}" },
		func(ui *UI) { ui.View.Events = nil },
		func(ui *UI) { ui.View.HTML = strings.Replace(ui.View.HTML, "<main>", "<main hidden>", 1) },
	} {
		ui := streamView("changed")
		change(&ui)
		send(ui, false)
	}
	send(UI{ID: "chat", Close: true}, false)
	send(streamView("reopened"), false)
}

func TestViewReceiverRejectsInvalidPatchesTransactionally(t *testing.T) {
	base := streamView("first")
	base.ViewVersion = 1
	var receiver ViewReceiver
	if _, err := receiver.Apply(base); err != nil {
		t.Fatal(err)
	}
	good := UI{ID: "chat", Title: "Chat", Kind: EditorTab, ViewVersion: 2, Patch: &ViewPatch{Base: 1, Regions: []ViewRegion{{Ref: "tail", HTML: "next"}}}}
	for _, changes := range [][]ViewRegion{
		{{Ref: "unknown", HTML: "x"}},
		{{Ref: "tail", HTML: "x"}, {Ref: "tail", HTML: "y"}},
		{{Ref: "tail", HTML: `</section><section data-ref="escape">escaped`}},
		{{Ref: "tail", HTML: `</main><main>escaped ancestor`}},
		{{Ref: "tail", HTML: `<div>unclosed`}},
		{{Ref: "tail", HTML: strings.Repeat("x", MaxPayload)}},
	} {
		bad := good
		bad.Patch = &ViewPatch{Base: 1, Regions: changes}
		if _, err := receiver.Apply(bad); err == nil {
			t.Fatal("invalid patch accepted")
		}
	}
	if _, err := receiver.Apply(good); err != nil {
		t.Fatal("failed patch changed base", err)
	}
	if _, err := receiver.Apply(good); !errors.Is(err, ErrViewBase) {
		t.Fatal("duplicate patch accepted", err)
	}
	var fresh ViewReceiver
	if _, err := fresh.Apply(good); !errors.Is(err, ErrViewBase) {
		t.Fatal("missing base accepted", err)
	}
}

type failingViewWriter struct {
	fail bool
	bytes.Buffer
}

func (w *failingViewWriter) Write(p []byte) (int, error) {
	if w.fail {
		return 0, io.ErrClosedPipe
	}
	return w.Buffer.Write(p)
}

func TestViewPublisherWriteFailureResyncAndClose(t *testing.T) {
	w := &failingViewWriter{}
	p := ActionPublisher{ctx: t.Context(), ui: true, writer: &Writer{Target: w}}
	if err := p.Show(streamView("first")); err != nil {
		t.Fatal(err)
	}
	w.Reset()
	w.fail = true
	if err := p.Show(streamView("failed")); err == nil {
		t.Fatal("expected write failure")
	}
	if p.views["chat"].ui.ViewVersion != 1 {
		t.Fatal("failed write advanced base")
	}
	w.fail = false
	if err := p.Show(streamView("next")); err != nil {
		t.Fatal(err)
	}
	if err := p.Show(UI{ID: "chat", Title: "Renamed", Kind: EditorTab, Busy: true}); err != nil {
		t.Fatal(err)
	}
	w.Reset()
	if handled, err := p.handleViewEvent(UIEvent{ID: "chat", Event: EditorEvent{Kind: "resync"}}); !handled || err != nil {
		t.Fatal(handled, err)
	}
	r := Reader{Source: w}
	f, err := r.Read()
	if err != nil {
		t.Fatal(err)
	}
	ui, err := DecodeUI(f.Payload)
	if err != nil || ui.View == nil || ui.Patch != nil || ui.ViewVersion != 2 || ui.Title != "Renamed" || !ui.Busy || ui.View.HTML != streamView("next").View.HTML {
		t.Fatal("resync did not replay latest full view", err)
	}
	var receiver ViewReceiver
	if _, err := receiver.Apply(ui); err != nil {
		t.Fatal(err)
	}
	p.handleViewEvent(UIEvent{ID: "chat", Event: EditorEvent{Kind: "close"}})
	if len(p.views) != 0 {
		t.Fatal("close retained snapshot")
	}
}

func TestViewPatchCodec(t *testing.T) {
	ui := UI{ID: "chat", Title: "Chat", Kind: EditorTab, ViewVersion: 9, Patch: &ViewPatch{Base: 8, Regions: []ViewRegion{{Ref: "tail", HTML: "中😀"}}}}
	data, err := EncodeUI(nil, ui)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeUI(data)
	if err != nil || !reflect.DeepEqual(ui, got) {
		t.Fatal(got, err)
	}
	for i := range len(data) {
		if _, err := DecodeUI(data[:i]); err == nil {
			t.Fatal("accepted truncation", i)
		}
	}
	if _, err := DecodeUI(append(bytes.Clone(data), 0)); err == nil {
		t.Fatal("accepted trailing data")
	}
	for _, version := range []uint32{0, 8, 10} {
		ui.ViewVersion = version
		if _, err := EncodeUI(nil, ui); err == nil {
			t.Fatal("accepted invalid revision", version)
		}
	}
}

func TestViewStreamChangingOffsetsAndNestedRegions(t *testing.T) {
	var stream viewStream
	var receiver ViewReceiver
	for i := range 100 {
		ui := streamView(fmt.Sprintf(`<section data-ref="nested" data-pencode-region>%s</section>`, strings.Repeat("中😀", i%7)))
		if i%3 == 0 {
			ui.View.HTML = strings.Replace(ui.View.HTML, strings.Repeat("history", 1000), fmt.Sprint(i), 1)
		}
		wire, state, err := stream.prepare(ui)
		if err != nil {
			t.Fatal(err)
		}
		data, err := EncodeUI(nil, wire)
		if err != nil {
			t.Fatal(err)
		}
		wire, err = DecodeUI(data)
		if err != nil {
			t.Fatal(err)
		}
		got, err := receiver.Apply(wire)
		if err != nil || got.View.HTML != ui.View.HTML {
			t.Fatal("region offsets lost after patch", i, err)
		}
		stream.commit(ui.ID, false, state)
	}
}

func TestViewStreamUsesFullViewForOptionalEndTags(t *testing.T) {
	var stream viewStream
	for _, value := range []string{"first", "second"} {
		ui := streamView("<p>" + value)
		wire, state, err := stream.prepare(ui)
		if err != nil || wire.View == nil || wire.Patch != nil {
			t.Fatal("fragment needing parser context used a patch", err)
		}
		stream.commit(ui.ID, false, state)
	}
}

func TestViewSizeIncludesMetadataAndBindings(t *testing.T) {
	ui := streamView("")
	ui.View.HTML = strings.Repeat("x", MaxDocument)
	ui.View.CSS = ""
	data, err := EncodeUI(nil, ui)
	if err != nil {
		t.Fatal(err)
	}
	ui.View.CSS = strings.Repeat("x", MaxPayload-len(data))
	if !validFullView(ui) {
		t.Fatal("exact-size frame rejected")
	}
	if data, err = EncodeUI(data, ui); err != nil || len(data) != MaxPayload {
		t.Fatal("size calculation differs from codec", len(data), err)
	}
	ui.View.CSS += "x"
	if validFullView(ui) {
		t.Fatal("oversized reconstructed frame accepted")
	}
}

func BenchmarkViewTransport(b *testing.B) {
	for _, count := range []int{10, 100, 500} {
		var history strings.Builder
		for i := range count {
			fmt.Fprintf(&history, `<section data-ref="m%d" data-pencode-region><p>%s</p></section>`, i, strings.Repeat("history ", 30))
		}
		views := [2]UI{streamView("first"), streamView("second")}
		for i := range views {
			views[i].View.HTML = `<main>` + history.String() + `<section data-ref="tail" data-pencode-region>` + fmt.Sprint(i) + `</section></main>`
		}
		for _, patch := range []bool{false, true} {
			b.Run(fmt.Sprintf("%d/patch=%t", count, patch), func(b *testing.B) {
				var stream viewStream
				_, initial, err := stream.prepare(views[0])
				if err != nil {
					b.Fatal(err)
				}
				stream.commit("chat", false, initial)
				var data []byte
				var total int64
				i := 0
				b.ReportAllocs()
				for b.Loop() {
					i = 1 - i
					ui := views[i]
					if patch {
						var state *viewState
						ui, state, err = stream.prepare(ui)
						if err != nil {
							b.Fatal(err)
						}
						stream.commit(ui.ID, false, state)
					}
					data, err = EncodeUI(data, ui)
					if err != nil {
						b.Fatal(err)
					}
					total += int64(len(data))
				}
				b.ReportMetric(float64(total)/float64(b.N), "wire-B/op")
			})
		}
	}
}
