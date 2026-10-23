package workbench

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/diiyw/onix/module"
	"github.com/pencode-labs/pencode/actions"
	"github.com/pencode-labs/pencode/peb"
	htmlui "github.com/diiyw/onix/ui/html"
)

func TestPencodeActionsRenderClickUpdateAndRemove(t *testing.T) {
	// In-memory HTML/input systems only: no native window or screenshot.
	app, model, _, state := languageInteractionFixture(t)
	var calls []peb.ActionContext
	for location := peb.StatusBar; location <= peb.TitleBarRight; location++ {
		a := peb.Action{ID: fmt.Sprintf("native/%d", location), Text: "Run", Tooltip: "Run action", Location: location, Icon: "icons/refresh-cw.svg"}
		if err := model.session.Actions.Register(actions.Definition{Action: a, Run: func(c peb.ActionContext) error { calls = append(calls, c); return nil }}); err != nil {
			t.Fatal(err)
		}
	}
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
	for _, id := range []string{"action-native/0", "action-native/3"} {
		editorContextClick(t, app, model, state, id)
	}
	if len(calls) != 2 {
		t.Fatalf("toolbar clicks: %+v", calls)
	}
	project := model.activeProjectState()
	fileContextRightClick(t, app, model, state, model.projectTreeRowID(project, "main.go"))
	model.fileContext.index = len(model.fileContextRefs()) - 1
	markPencodeRootDirtyByModel(model.fontResources, model)
	languageInteractionStep(t, app)
	editorContextClick(t, app, model, state, "action-native/1")
	if len(calls) != 3 || model.fileContext.open || calls[2].Path != filepath.Join(project.Path, "main.go") || calls[2].Directory {
		t.Fatalf("left context click: %+v", calls)
	}
	editorContextOpen(t, app, model, 5, 2, 100, 20)
	editorContextClick(t, app, model, state, "action-native/2")
	if len(calls) != 4 || model.languageUI.contextOpen || calls[3].Caret != 5 || calls[3].Anchor != 2 {
		t.Fatalf("right context click: %+v", calls)
	}
	updated := peb.Action{ID: "native/0", Text: "Updated", Icon: "icons/folder.svg", Color: "#abcdef", IconColor: "#ff8800", IconStyle: "opacity:.5", Location: peb.TitleBarRight, Disabled: true}
	if err := model.session.Actions.Update(updated); err != nil {
		t.Fatal(err)
	}
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
	var button htmlui.Control
	found := false
	for _, access := range module.ContentQuery[htmlui.Component[pencodeActionProps, struct{}]](model.fontResources) {
		props := access.Get().Props
		if props.Action.ID != updated.ID {
			continue
		}
		if props.Action.Text != "Updated" || props.Icon != "icons/folder.svg?foreground=ff8800" || props.Action.IconStyle != "opacity:.5" {
			t.Fatalf("stale appearance: %+v", props)
		}
		entity, ok := model.fontResources.HTMLControl(model.surface, props.Ref)
		if !ok {
			t.Fatal("missing action control")
		}
		button, _ = module.ContentGet[htmlui.Control](model.fontResources, entity)
		found = true
	}
	if !found || !button.Disabled {
		t.Fatal("disabled appearance did not reach the control")
	}
	if allocs := testing.AllocsPerRun(100, func() { model.syncActions() }); allocs != 0 {
		t.Fatalf("unchanged actions allocated %g", allocs)
	}
	model.session.Actions.RemovePrefix("native/")
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
	if _, ok := model.fontResources.HTMLNode(model.surface, "action-native/0"); ok {
		t.Fatal("removed action retained DOM")
	}
	if len(model.fileContextRefs()) != len(pencodeFileContextRefs) || len(model.editorContextRefs()) != len(pencodeEditorContextRefs) {
		t.Fatal("removed action retained keyboard references")
	}
}
