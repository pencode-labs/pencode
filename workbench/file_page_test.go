package workbench

import (
	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pencode-labs/pencode/protocol"
)

func TestPagedFileSwitchingEditingAndSave(t *testing.T) {
	m, p, _, _ := delayedFileModel(t)
	original := strings.Repeat("entry 中文🙂\n", protocol.TextPagingThreshold/len("entry 中文🙂\n")+1000)
	resourceTestWrite(t, p, "large.txt", original)
	openProjectFileAndWait(t, m, p, "large.txt")
	f := m.activeFile()
	if f == nil || !f.page.Paged || f.snapshot().Len() > protocol.TextPageSize {
		t.Fatal("large file was loaded in full")
	}
	m.updateActiveContent("edited\n" + f.content())
	if err := m.requestFilePage(200000, false); err != nil {
		t.Fatal(err)
	}
	if !f.pageLoading || m.updateActiveContent("late typing") || m.undoActiveContent(nil) {
		t.Fatal("page switch did not freeze editing")
	}
	if _, err := m.requestFileSave(p, f); err == nil {
		t.Fatal("save accepted during page transition")
	}
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if f.pageLoading || !f.Dirty || !f.pageDirty || f.page.Offset == 0 {
		t.Fatal("page switch lost dirty state")
	}
	if m.undoActiveContent(nil) {
		t.Fatal("previous page undo was applied to current page")
	}
	pageOffset := f.page.Offset
	m.updateActiveContent("second\n" + f.content())
	if err := saveFileAndWait(t, m, p.Path, f); err != nil {
		t.Fatal(err)
	}
	if f.Dirty || f.pageDirty {
		t.Fatal("save left file dirty")
	}
	want := "edited\n" + original
	want = want[:pageOffset] + "second\n" + want[pageOffset:]
	disk, err := os.ReadFile(filepath.Join(p.Path, "large.txt"))
	if err != nil || string(disk) != want {
		t.Fatal("save lost unloaded content or earlier edit", err)
	}
	if err := m.requestFilePage(0, false); err != nil {
		t.Fatal(err)
	}
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if f.Dirty || !strings.HasPrefix(f.content(), "edited\n") || f.page.Offset != 0 {
		t.Fatal("revisit changed saved state")
	}
}

func TestPagedFileResultStaysWithOriginalTab(t *testing.T) {
	m, p, g, unblock := delayedFileModel(t)
	resourceTestWrite(t, p, "large.txt", strings.Repeat("line\n", protocol.TextPagingThreshold/5+1000))
	resourceTestWrite(t, p, "other.txt", "other")
	openProjectFileAndWait(t, m, p, "other.txt")
	other := m.activeFile()
	openProjectFileAndWait(t, m, p, "large.txt")
	large := m.activeFile()
	g.pause.Store(true)
	if err := m.requestFilePage(200000, false); err != nil {
		t.Fatal(err)
	}
	awaitFileGate(t, g)
	m.selectProjectTab(p, "other.txt")
	unblock()
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if m.activeFile() != other || other.content() != "other" || large.page.Offset == 0 || large.pageLoading {
		t.Fatal("page result changed selected tab")
	}
}

func TestPagedEditorRendersOnlyLoadedPageAndLoadsAtScrollEdge(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	m.app.GetViewport = func() (int, int) { return 1440, 900 }
	app.World.InsertResource(scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	p := m.activeProjectState()
	resourceTestWrite(t, p, "large.txt", strings.Repeat("row of text\n", protocol.TextPagingThreshold/12+1000))
	openProjectFileAndWait(t, m, p, "large.txt")
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	f := m.activeFile()
	world := m.fontResources
	editorContextBounds(t, m, "editor-page")
	value := world.Ref[scene.Text2D](m.editor.text)
	if !f.page.Paged || value.Text().Len() > protocol.TextPageSize || f.page.Offset != 0 || f.pageLoading {
		t.Fatal("rendering loaded the rest of the file")
	}
	state, _ := app.World.GetResource[*input.State]()
	bounds := editorContextBounds(t, m, "editor")
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(bounds.X + bounds.Width/2), Y: float64(bounds.Y + bounds.Height/2)})
	languageInteractionStep(t, app)
	area := world.Mut[engineui.Textarea](m.editor.control)
	area.RestoreScroll(0, 1e9)
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventMouseWheel, WheelY: -1})
	languageInteractionStep(t, app)
	if !f.pageLoading && f.page.Offset == 0 {
		t.Fatal("wheel at the trailing edge did not request the next page")
	}
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	markPencodeRootDirtyByModel(world, m)
	languageInteractionStep(t, app)
	if f.page.Offset == 0 || world.Ref[scene.Text2D](m.editor.text).Text().Len() > protocol.TextPageSize {
		t.Fatal("next page did not replace the rendered window")
	}
	// Scrolling within a loaded page does not allocate or queue more reads.
	area = world.Mut[engineui.Textarea](m.editor.control)
	area.ScrollY = 100
	local, _ := world.Resource[*input.State]()
	local.ScrollMouse(0, -1)
	if n := testing.AllocsPerRun(50, func() { m.updateFilePageWheel(world, local) }); n != 0 {
		t.Fatalf("warm page scrolling allocated %g times", n)
	}
	if f.pageLoading {
		t.Fatal("scrolling inside the page loaded another window")
	}
	m.selectProjectTab(p, "main.go")
	markPencodeRootDirtyByModel(world, m)
	languageInteractionStep(t, app)
	node, ok := world.HTMLNode(m.surface, "editor-page")
	if !ok {
		t.Fatal("missing pager")
	}
	if _, visible := node.Bounds(); visible {
		t.Fatal("pager visible for ordinary document")
	}
}
