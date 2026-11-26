package workbench

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/onix-engine/onix"
	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	htmlui "github.com/onix-engine/onix/ui/html"
)

func fileContextRightClick(t *testing.T, app *onix.App, model *workspaceModel, state *input.State, ref string) {
	t.Helper()
	r := editorContextBounds(t, model, ref)
	zoom := float64(model.settings.Zoom) / 100
	x, y := float64(r.X+r.Width/2)*zoom, float64(r.Y+r.Height/2)*zoom
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseRight, X: x, Y: y})
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseRight, X: x, Y: y})
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
}

func TestFileContextNativeRenameCreateDeleteAndRefresh(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	project := model.activeProjectState()
	model.updateActiveContent("unsaved 中文🙂\n")
	model.setEditorValueAndCaret(model.fontResources, model.activeContent(), 4)
	for _, zoom := range []int{100, 150, 70} {
		model.setZoom(model.fontResources, zoom)
		languageInteractionStep(t, app)
		row := model.projectTreeRowID(project, "main.go")
		fileContextRightClick(t, app, model, state, row)
		if !model.fileContext.open || model.fileContext.path != "main.go" || model.activePath != "main.go" || model.languageUI.contextOpen {
			t.Fatalf("right click did not target the file at zoom %d: %+v", zoom, model.fileContext)
		}
		menu := editorContextBounds(t, model, "file-context")
		if menu.Width < 271.9 || menu.Width > 272.1 || menu.X < 0 || menu.Y < 0 {
			t.Fatalf("file menu is not a compact popup: %+v", menu)
		}
		for _, hint := range pencodeFileContextHints {
			bounds := editorContextBounds(t, model, hint.ref)
			if bounds.X < menu.X || bounds.X+bounds.Width > menu.X+menu.Width {
				t.Fatalf("shortcut hint %s overflows the menu at zoom %d: %+v in %+v", hint.ref, zoom, bounds, menu)
			}
		}
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEscape})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEscape})
		languageInteractionStep(t, app)
	}
	model.setZoom(model.fontResources, 100)
	languageInteractionStep(t, app)
	clipboard := ""
	state.SetClipboard(func(value string) bool { clipboard = value; return true }, func() (string, bool) { return clipboard, true })
	fileContextRightClick(t, app, model, state, model.projectTreeRowID(project, "main.go"))
	editorContextClick(t, app, model, state, "file-context-copy-relative")
	if clipboard != "main.go" {
		t.Fatalf("copied relative path = %q", clipboard)
	}
	rowID := model.projectTreeRowID(project, "main.go")
	originalRow := editorContextBounds(t, model, rowID)
	key := func(code input.KeyCode) {
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: code})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: code})
		languageInteractionStep(t, app)
	}
	world := model.fontResources
	for _, finish := range []input.KeyCode{input.KeyEnter, input.KeyEscape} {
		row, ok := world.HTMLControl(model.surface, rowID)
		if !ok || !world.Focus(row) {
			t.Fatal("could not focus the tree row")
		}
		key(input.KeyF2)
		languageInteractionStep(t, app)
		if model.fileContext.dialog != "rename" {
			t.Fatal("F2 did not start inline rename")
		}
		if finish == input.KeyEscape {
			state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "cancelled"})
			languageInteractionStep(t, app)
		}
		key(finish)
		if model.fileContext.dialog != "" || model.fileIO != nil && len(model.fileIO.pending) != 0 {
			t.Fatalf("%v did not dismiss the rename without submitting", finish)
		}
	}
	fileContextRightClick(t, app, model, state, rowID)
	editorContextClick(t, app, model, state, "file-context-rename")
	languageInteractionStep(t, app)
	if model.fileContext.dialog != "rename" || model.fileContext.name != "main.go" {
		t.Fatalf("rename did not prefill the inline input: %+v", model.fileContext)
	}
	if row := editorContextBounds(t, model, "file-tree-rename"); row != originalRow {
		t.Fatalf("rename moved the tree row: before=%+v during=%+v", originalRow, row)
	}
	control, _ := world.HTMLControl(model.surface, "file-tree-name")
	if field := world.Ref[engineui.TextField](control); field == nil || !field.Focused || field.Anchor != 0 || field.Caret != 4 {
		t.Fatalf("rename did not focus and select the basename: %+v", field)
	}
	if editor := world.Ref[htmlui.Control](model.editor.control); editor == nil || editor.Disabled {
		t.Fatal("inline rename disabled the editor")
	}
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "renamed"})
	languageInteractionStep(t, app)
	if model.fileContext.name != "renamed.go" {
		t.Fatalf("rename input did not accept typing: %+v", model.fileContext)
	}
	key(input.KeyEnter)
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	languageInteractionStep(t, app)
	if model.fileContext.dialog != "" || model.activePath != "renamed.go" || model.activeContent() != "unsaved 中文🙂\n" || !model.activeFile().Dirty || len(model.activeFile().undo) == 0 {
		t.Fatalf("rename lost the active unsaved buffer: path=%q content=%q dialog=%+v", model.activePath, model.activeContent(), model.fileContext)
	}
	if _, err := os.Stat(filepath.Join(project.Path, "main.go")); !os.IsNotExist(err) {
		t.Fatalf("old path still exists after rename: %v", err)
	}
	fileContextRightClick(t, app, model, state, model.projectTreeRowID(project, "renamed.go"))
	editorContextClick(t, app, model, state, "file-context-refresh")
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	if !model.activeFile().Dirty || model.activeContent() != "unsaved 中文🙂\n" {
		t.Fatal("refresh discarded unsaved edits")
	}
	languageInteractionStep(t, app)
	fileContextRightClick(t, app, model, state, model.projectTreeRowID(project, "renamed.go"))
	editorContextClick(t, app, model, state, "file-context-new-file")
	languageInteractionStep(t, app)
	if model.fileContext.dialog != "new-file" {
		t.Fatalf("new file did not start inline creation: %+v", model.fileContext)
	}
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "created.txt"})
	languageInteractionStep(t, app)
	if model.fileContext.name != "created.txt" {
		t.Fatalf("new file input did not accept typing: %+v", model.fileContext)
	}
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEnter})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEnter})
	languageInteractionStep(t, app)
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	languageInteractionStep(t, app)
	if model.activePath != "created.txt" {
		t.Fatalf("new file was not opened: active=%q error=%q", model.activePath, model.fileContext.err)
	}
	openDelete := func() {
		t.Helper()
		fileContextRightClick(t, app, model, state, model.projectTreeRowID(project, "created.txt"))
		if !model.fileContext.open || model.fileContext.path != "created.txt" {
			t.Fatalf("wrong context menu for created file: %+v", model.fileContext)
		}
		editorContextClick(t, app, model, state, "file-context-delete")
		if model.fileContext.dialog != "delete" || !slices.Equal(model.fileContext.paths, []string{"created.txt"}) {
			t.Fatalf("wrong delete confirmation: %+v", model.fileContext)
		}
	}
	openDelete()
	if _, err := os.Stat(filepath.Join(project.Path, "created.txt")); err != nil {
		t.Fatal("file was deleted before confirmation")
	}
	editorContextClick(t, app, model, state, "file-dialog-cancel")
	if model.fileContext.dialog != "" {
		t.Fatalf("cancel left delete confirmation open: %+v", model.fileContext)
	}
	openDelete()
	editorContextClick(t, app, model, state, "file-dialog-submit")
	if model.fileContext.dialog != "" {
		t.Fatalf("submit left delete confirmation open: %+v", model.fileContext)
	}
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project.Path, "created.txt")); !os.IsNotExist(err) || project.Files["created.txt"] != nil {
		t.Fatalf("delete did not remove file and tab: %v", err)
	}
}

func TestFileManagerCommands(t *testing.T) {
	target := filepath.Join(string(filepath.Separator), "work space", "project", "main.go")
	for _, test := range []struct {
		goos, label string
		args        []string
		directory   bool
	}{
		{goos: "windows", label: "在文件资源管理器中显示", args: []string{"explorer", "/select," + target}},
		{goos: "darwin", label: "在 Finder 中显示", args: []string{"open", "-R", target}},
		{goos: "linux", label: "在文件管理器中显示", args: []string{"xdg-open", filepath.Dir(target)}},
		{goos: "linux", label: "在文件管理器中显示", directory: true, args: []string{"xdg-open", target}},
	} {
		t.Run(test.goos, func(t *testing.T) {
			if label := fileManagerLabel(test.goos); label != test.label {
				t.Fatalf("label = %q, want %q", label, test.label)
			}
			if args := fileManagerCommand(test.goos, target, test.directory).Args; !slices.Equal(args, test.args) {
				t.Fatalf("args = %q, want %q", args, test.args)
			}
		})
	}
	binding := defaultPencodeSettings().Bindings["file-context-reveal"]
	if binding.Key != input.KeyR || !binding.Shift || !binding.Alt || binding.Control || binding.Primary || binding.Super {
		t.Fatalf("reveal binding = %+v, want Shift+Alt+R", binding)
	}
}

func TestFileContextTargetsScrolledRowsAndInactiveProjects(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	project := model.activeProjectState()
	for i := range 50 {
		if err := os.WriteFile(filepath.Join(project.Path, fmt.Sprintf("item-%02d.txt", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	model.startProjectScan(project, false)
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	languageInteractionStep(t, app)
	rowID := model.projectTreeRowID(project, "item-35.txt")
	treeID := treeRowID(project.Path)
	treeNode, _ := model.fontResources.HTMLNode(model.surface, treeID)
	area, ok := treeNode.ScrollContainer()
	if !ok {
		t.Fatal("missing file tree scroll area")
	}
	scroll := model.fontResources.Mut[scene.UIScroll](area)
	for index, entry := range project.visibleTreeRows(model) {
		if project.Entries[entry].Path == "item-35.txt" {
			scroll.Position[1] = 40 - 5 - float32(index*pencodeTreeRowHeight)
			break
		}
	}
	languageInteractionStep(t, app)
	row := editorContextBounds(t, model, rowID)
	x, y := float64(row.X+row.Width/2), float64(row.Y+scroll.Position.Y()+row.Height/2)
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseRight, X: x, Y: y})
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseRight, X: x, Y: y})
	languageInteractionStep(t, app)
	if model.fileContext.path != "item-35.txt" || model.activePath != "main.go" {
		t.Fatalf("scrolled tree targeted the wrong file: %+v", model.fileContext)
	}
	model.fileContext = pencodeFileContext{}
	other := &pencodeProject{Path: t.TempDir(), Name: "other", Files: make(map[string]*openFile)}
	model.projects = append(model.projects, other)
	model.activateProject(other)
	markPencodeRootDirtyByModel(model.fontResources, model)
	languageInteractionStep(t, app)
	fileContextRightClick(t, app, model, state, "sidebar-actions")
	if model.fileContext.project != other || model.fileContext.path != "" || model.activeProjectState() != other {
		t.Fatal("right click switched the active project or lost the target root")
	}
}

func TestFileContextIdleDoesNotAllocate(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	local, _ := model.fontResources.Resource[*input.State]()
	if allocations := testing.AllocsPerRun(100, func() { model.updateFileContext(model.fontResources, local) }); allocations != 0 {
		t.Fatalf("idle file context allocated %g times", allocations)
	}
	fileContextRightClick(t, app, model, state, model.projectTreeRowID(model.activeProjectState(), "main.go"))
	local, _ = model.fontResources.Resource[*input.State]()
	if allocations := testing.AllocsPerRun(100, func() { model.updateFileContext(model.fontResources, local) }); allocations != 0 {
		t.Fatalf("open idle file context allocated %g times", allocations)
	}
	if allocations := testing.AllocsPerRun(100, func() { model.handleFileContextKeys(model.fontResources, local, "linux") }); allocations != 0 {
		t.Fatalf("idle file context shortcuts allocated %g times", allocations)
	}
	field := model.fontResources.Ref[engineui.TextField](model.editor.control)
	if field == nil {
		t.Fatal("file context replaced the native editor")
	}
}

func TestFileContextKeyboardActions(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			app, model, _, platform := languageInteractionFixture(t)
			world := model.fontResources
			project := model.activeProjectState()
			clipboard := ""
			platform.SetClipboard(func(value string) bool { clipboard = value; return true }, nil)
			for _, test := range []struct {
				name       string
				dialog     string
				copied     string
				key        input.KeyCode
				primary    bool
				shift, alt bool
			}{
				{name: "new-file", key: input.KeyN, primary: true, dialog: "new-file"},
				{name: "new-folder", key: input.KeyN, primary: true, alt: true, dialog: "new-folder"},
				{name: "find", key: input.KeyF, primary: true, shift: true},
				{name: "cut", key: input.KeyX, primary: true, copied: filepath.Join(project.Path, "main.go")},
				{name: "copy", key: input.KeyC, primary: true, copied: filepath.Join(project.Path, "main.go")},
				{name: "copy-path", key: input.KeyC, primary: true, shift: true, copied: filepath.Join(project.Path, "main.go")},
				{name: "copy-relative", key: input.KeyC, primary: true, alt: true, copied: "main.go"},
				{name: "rename", key: input.KeyF2, dialog: "rename"},
				{name: "delete", key: input.KeyDelete, dialog: "delete"},
				{name: "refresh", key: input.KeyF5},
			} {
				t.Run(test.name, func(t *testing.T) {
					model.fileContext = pencodeFileContext{project: project, path: "main.go", open: true}
					model.fileClipboard = pencodeFileClipboard{}
					clipboard = ""
					state := input.NewState()
					if test.primary {
						modifier := input.KeyLeftControl
						if goos == "darwin" {
							modifier = input.KeyLeftSuper
						}
						state.PressKey(modifier)
					}
					if test.shift {
						state.PressKey(input.KeyLeftShift)
					}
					if test.alt {
						state.PressKey(input.KeyLeftAlt)
					}
					state.PressKey(test.key)
					if !model.handleShortcutKeys(world, state, goos) || model.fileContext.open {
						t.Fatalf("shortcut did not activate %s: %+v", test.name, model.fileContext)
					}
					if model.fileContext.dialog != test.dialog || clipboard != test.copied {
						t.Fatalf("wrong action: dialog=%q clipboard=%q", model.fileContext.dialog, clipboard)
					}
					switch test.name {
					case "cut", "copy":
						if model.fileClipboard.project != project || !slices.Equal(model.fileClipboard.paths, []string{"main.go"}) || model.fileClipboard.cut != (test.name == "cut") {
							t.Fatalf("wrong file clipboard: %+v", model.fileClipboard)
						}
					case "find":
						if !model.folderSearch.open || model.folderSearch.project != project || model.folderSearch.directory != "" {
							t.Fatal("shortcut did not search the target folder")
						}
						model.closeFolderSearch(world)
					case "delete":
						if _, err := os.Stat(filepath.Join(project.Path, "main.go")); err != nil {
							t.Fatal("Delete bypassed the confirmation dialog")
						}
					case "refresh":
						waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
					}
				})
			}
		})
	}
}

func TestFileContextKeyboardPasteAndDisabledItems(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	project := model.activeProjectState()
	if err := os.Mkdir(filepath.Join(project.Path, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	model.startProjectScan(project, false)
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	languageInteractionStep(t, app)
	press := func(key input.KeyCode) {
		modifier := input.KeyLeftControl
		if runtime.GOOS == "darwin" {
			modifier = input.KeyLeftSuper
		}
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: modifier})
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
		languageInteractionStep(t, app)
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: modifier})
		languageInteractionStep(t, app)
	}
	root := "sidebar-actions"
	fileContextRightClick(t, app, model, state, root)
	press(input.KeyV)
	if !model.fileContext.open || model.fileIO != nil && len(model.fileIO.pending) != 0 {
		t.Fatal("disabled paste started a file transfer or dismissed the menu")
	}
	press(input.KeyX)
	if !model.fileContext.open || model.fileClipboard.project != nil {
		t.Fatal("disabled cut copied the project root")
	}
	fileContextRightClick(t, app, model, state, model.projectTreeRowID(project, "main.go"))
	content := model.activeContent()
	press(input.KeyC)
	if model.fileContext.open || !slices.Equal(model.fileClipboard.paths, []string{"main.go"}) || model.activeContent() != content {
		t.Fatal("copy shortcut failed or edited the active buffer")
	}
	fileContextRightClick(t, app, model, state, model.projectTreeRowID(project, "target"))
	press(input.KeyV)
	if model.fileContext.open {
		t.Fatal("paste shortcut did not dismiss the menu")
	}
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	copied, err := os.ReadFile(filepath.Join(project.Path, "target", "main.go"))
	if err != nil || string(copied) != content || model.activeContent() != content {
		t.Fatalf("paste did not copy the target resource intact: %v", err)
	}
}
