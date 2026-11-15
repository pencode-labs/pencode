package workbench

import (
	"testing"

	"github.com/pencode-labs/pencode/extensions"
)

func idleActionModel() *workspaceModel {
	model := &workspaceModel{}
	model.actions.sessions = map[string]*extensions.ActionSession{
		"long-extension-name-one":   {Results: make(chan extensions.ActionResult)},
		"long-extension-name-two":   {Results: make(chan extensions.ActionResult)},
		"long-extension-name-three": {Results: make(chan extensions.ActionResult)},
	}
	model.pollActions()
	return model
}

func TestPollActionsIdleNoAllocations(t *testing.T) {
	model := idleActionModel()
	if n := testing.AllocsPerRun(100, func() {
		if model.pollActions() {
			t.Fatal("idle action sessions changed the UI")
		}
	}); n != 0 {
		t.Fatalf("idle action polling allocated %g", n)
	}
}

func BenchmarkPollActionsIdle(b *testing.B) {
	model := idleActionModel()
	b.ReportAllocs()
	for b.Loop() {
		model.pollActions()
	}
}
