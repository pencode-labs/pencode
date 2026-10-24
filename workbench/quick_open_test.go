package workbench

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

func TestTitlebarSearchResultLayout(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	bounds := func(id string) htmlui.Rect {
		t.Helper()
		node, ok := model.fontResources.HTMLNode(model.surface, id)
		if !ok {
			t.Fatalf("missing node %s", id)
		}
		rect, ok := node.Bounds()
		if !ok || rect.Width <= 0 || rect.Height <= 0 {
			t.Fatalf("missing bounds for %s: %+v", id, rect)
		}
		return rect
	}
	model.quickQuery = "main.go"
	for _, width := range [...]int{720, 1440} {
		model.app.GetViewport = func() (int, int) { return width, 900 }
		ecs.InsertResource(app.World, scene.ViewportSize{Width: float32(width), Height: 900, ScaleFactor: 1})
		for _, zoom := range [...]int{70, 100, 150} {
			model.setZoom(model.fontResources, zoom)
			model.openQuickOpen()
			settleQuick(t, model)
			markPencodeRootDirtyByModel(model.fontResources, model)
			if _, err := app.Step(); err != nil {
				t.Fatal(err)
			}
			search, panel := bounds("file-search"), bounds("quick-open-panel")
			if math.Abs(float64(search.X+search.Width/2-panel.X-panel.Width/2)) > 1 || panel.Y < search.Y+search.Height {
				t.Fatalf("dropdown is not centered below search at width %d zoom %d: search=%+v panel=%+v", width, zoom, search, panel)
			}
			if panel.X < 0 || (panel.X+panel.Width)*float32(zoom)/100 > float32(width) {
				t.Fatalf("dropdown exceeds viewport: %+v", panel)
			}
			row, icon, label := bounds("quick-row-0"), bounds("quick-icon-0"), bounds("quick-label-0")
			for _, rect := range [...]htmlui.Rect{icon, label} {
				if math.Abs(float64(row.Y+row.Height/2-rect.Y-rect.Height/2)) > 1 {
					t.Fatalf("result content is not vertically centered: row=%+v child=%+v", row, rect)
				}
			}
			if icon.Width != 16 || icon.Height != 16 || label.X <= icon.X+icon.Width {
				t.Fatalf("invalid file icon layout: icon=%+v label=%+v", icon, label)
			}
		}
	}
}

func TestTitlebarSearchOpensFileAtLine(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	model.app.GetViewport = func() (int, int) { return 1440, 900 }
	ecs.InsertResource(app.World, scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	model.syncZoom(model.fontResources)
	const name = "location.go"
	content := strings.Repeat("// 中文🙂\r\n", 100)
	if err := os.WriteFile(filepath.Join(model.projectPath, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	project := model.activeProjectState()
	model.startProjectScan(project, false)
	waitPencodeProjectScan(t, project, func() { model.pollProjectScans() })
	for _, query := range [...]string{"location.go", "location.go:51", "location.go", "location.go:1", "location.go:999"} {
		model.quickQuery = query
		model.openQuickOpen()
		settleQuick(t, model)
		if model.quickCount != 1 || model.quickMatches[0] != name {
			t.Fatalf("location query did not match file: %q", query)
		}
		markPencodeRootDirtyByModel(model.fontResources, model)
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
		model.openQuickMatch(model.fontResources, 0)
		_ = awaitFileIO(t, model)
		markPencodeRootDirtyByModel(model.fontResources, model)
		for range 2 {
			if _, err := app.Step(); err != nil {
				t.Fatal(err)
			}
		}
		want := 50 * utf8.RuneCountInString("// 中文🙂\r\n")
		if query == name || strings.HasSuffix(query, ":1") {
			want = 0
		} else if strings.HasSuffix(query, ":999") {
			want = utf8.RuneCountInString(content)
		}
		field, _ := module.ContentGet[ui.TextField](model.fontResources, model.editor.control)
		if model.activePath != name || !field.Focused || field.Caret != want || field.Anchor != want || model.quickTargetLine != 0 || model.quickOpen {
			t.Fatalf("%s: caret=%d anchor=%d focused=%v pending=%d open=%v focusPending=%v, want %d", query, field.Caret, field.Anchor, field.Focused, model.quickTargetLine, model.quickOpen, model.quickNeedsFocus, want)
		}
		text, _ := module.ContentGet[scene.Text2D](model.fontResources, model.editor.text)
		textarea, _ := module.ContentGet[ui.Textarea](model.fontResources, model.editor.control)
		link, _ := module.ContentGet[htmlui.ControlText](model.fontResources, model.editor.text)
		_, y, _, height, ok := scene.TextCaretRect2D(&text, content, want)
		if !ok || y < textarea.ScrollY || y+height > textarea.ScrollY+link.Content.Height {
			t.Fatalf("%s: target line is outside viewport: y=%v height=%v scroll=%v viewport=%v", query, y, height, textarea.ScrollY, link.Content.Height)
		}
		if strings.HasSuffix(query, ":51") && math.Abs(float64(y+height/2-textarea.ScrollY-link.Content.Height/2)) > 1 {
			t.Fatalf("%s: target line is not centered: y=%v height=%v scroll=%v viewport=%v", query, y, height, textarea.ScrollY, link.Content.Height)
		}
	}
	history := loadPencodeQuickHistory(filepath.Dir(session.quickHistory.file))
	if history.count != 1 || history.rank(model.projectPath, name) != 0 {
		t.Fatal("successful search navigation did not persist a deduplicated history entry")
	}
	model.quickIndex = 7
	model.openQuickOpen()
	settleQuick(t, model)
	if model.quickQuery != "" || model.quickMatches[0] != name || model.quickIndex != 0 {
		t.Fatal("reopening search did not show the most recent file first")
	}
}

func TestTitlebarSearchMatchesPaths(t *testing.T) {
	model, project := resourceTestModel(t)
	const name = "workbench/exec_windows_test.go"
	resourceTestWrite(t, project, name, "package workbench\n")
	resourceTestWrite(t, project, "other/exec_windows_test.go", "package other\n")
	model.startProjectScan(project, false)
	waitPencodeProjectScan(t, project, func() { model.pollProjectScans() })
	for _, query := range []string{
		name,
		`workbench\exec_windows_test.go`,
		`WORKBENCH\EXEC_WINDOWS_TEST.GO:51`,
		filepath.Join(project.Path, filepath.FromSlash(name)),
		filepath.ToSlash(filepath.Join(project.Path, filepath.FromSlash(name))) + ":51",
	} {
		model.quickQuery = query
		model.openQuickOpen()
		settleQuick(t, model)
		if model.quickCount != 1 || model.quickMatches[0] != name {
			t.Fatalf("path query %q: matches=%v count=%d", query, model.quickMatches, model.quickCount)
		}
	}
}

func TestPencodeQuickLocation(t *testing.T) {
	for _, fixture := range []struct {
		query, path string
		line        int
	}{
		{"main.go:51", "main.go", 51},
		{"src/中文.go:2", "src/中文.go", 2},
		{"main.go", "main.go", 0},
		{"main.go:", "main.go:", 0},
		{"main.go:0", "main.go:0", 0},
		{"main.go:-1", "main.go:-1", 0},
		{"main.go:abc", "main.go:abc", 0},
		{"main.go:99999999999999999999999999999", "main.go:99999999999999999999999999999", 0},
	} {
		if path, line := pencodeQuickLocation(fixture.query); path != fixture.path || line != fixture.line {
			t.Fatalf("parse %q = %q:%d, want %q:%d", fixture.query, path, line, fixture.path, fixture.line)
		}
	}
}

func TestTitlebarSearchFailedOpenDoesNotRecordHistory(t *testing.T) {
	_, session := newPencodeTestApp(t)
	model := session.active.model
	if session.quickHistory.count != 0 {
		t.Fatal("ordinary file opens should not enter search history")
	}
	const missing = "missing.go"
	project := model.activeProjectState()
	project.Entries = append(project.Entries, treeEntry{Path: missing, Name: missing, Openable: true})
	model.quickQuery = missing
	model.openQuickOpen()
	settleQuick(t, model)
	model.openQuickMatch(model.fontResources, 0)
	_ = awaitFileIO(t, model)
	if session.quickHistory.count != 0 || !model.quickOpen || model.quickQuery != missing {
		t.Fatal("failed file open recorded history or dismissed search")
	}
}

func TestTitlebarSearchFocusesFiltersAndOpensFile(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	model.app.GetViewport = func() (int, int) { return 1440, 900 }
	ecs.InsertResource(app.World, scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	model.syncZoom(model.fontResources)
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	originalControl, ok := model.fontResources.HTMLControl(model.surface, "quick-open-query")
	if !ok {
		t.Fatal("titlebar input is missing before opening search")
	}
	path := model.activePath
	model.settingsOpen, model.market.open = true, true
	dispatch := func(ref, value string, kind htmlui.EventKind) {
		t.Helper()
		context := module.ContentEventContext[appProps, appState]{World: model.fontResources, Component: model.root, Props: &appProps{Model: model}, Ref: ref, Event: htmlui.Event{Kind: kind, Value: value}}
		handlePencodeRootEvent(&context)
		markPencodeRootDirtyByModel(model.fontResources, model)
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
	}
	dispatch("open-quick-search", "", htmlui.EventClick)
	if !model.quickOpen || model.settingsOpen || model.market.open {
		t.Fatal("titlebar search did not open exclusively")
	}
	control, ok := model.fontResources.HTMLControl(model.surface, "quick-open-query")
	if control != originalControl {
		t.Fatal("opening search replaced the titlebar input")
	}
	node, _ := model.fontResources.HTMLNode(model.surface, "quick-open-query")
	if bounds, ok := node.Bounds(); !ok || bounds.Y >= pencodeTitlebarHeight+pencodeTitlebarInset {
		t.Fatal("search input moved out of the titlebar")
	}
	field, fieldOK := module.ContentGet[ui.TextField](model.fontResources, control)
	if !ok || !fieldOK || !field.Focused {
		t.Fatalf("titlebar search did not focus the query input: control=%v exists=%v field=%+v pending=%v", control, ok, field, model.quickNeedsFocus)
	}
	inputState, _ := ecs.GetResource[*input.State](app.World)
	inputState.Enqueue(input.Event{Kind: input.EventTextInput, Text: "MAIN"})
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	settleQuick(t, model)
	if model.quickQuery != "MAIN" || model.quickCount != 1 {
		t.Fatal("typing after the first click did not filter project files")
	}
	dispatch("quick-open-query", "no-matching-file-name", htmlui.EventInput)
	settleQuick(t, model)
	if model.quickCount != 0 {
		t.Fatal("nonmatching query returned files")
	}
	dispatch("quick-open-query", filepath.Base(path), htmlui.EventInput)
	settleQuick(t, model)
	if model.quickCount != 1 || model.quickMatches[0] != filepath.Base(path) {
		t.Fatalf("query did not find the project file: %v", model.quickMatches[:model.quickCount])
	}
	state := input.NewState()
	state.PressKey(input.KeyEnter)
	if !model.handleShortcutKeys(model.fontResources, state, "darwin") || model.quickOpen || model.activePath != path {
		t.Fatal("Enter did not open the selected file and dismiss search")
	}
	markPencodeRootDirtyByModel(model.fontResources, model)
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	queryControl, _ := module.ContentGet[htmlui.Control](model.fontResources, control)
	if model.quickQuery != "" || queryControl.Value != "" {
		t.Fatalf("successful navigation did not clear search: query=%q input=%q", model.quickQuery, queryControl.Value)
	}
	state = input.NewState()
	state.PressKey(input.KeyLeftSuper)
	state.PressKey(input.KeyP)
	if !model.handleShortcutKeys(model.fontResources, state, "darwin") || !model.quickOpen {
		t.Fatal("Cmd+P did not open file search")
	}
	markPencodeRootDirtyByModel(model.fontResources, model)
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	inputState.Enqueue(input.Event{Kind: input.EventTextInput, Text: "main"})
	if _, err := app.Step(); err != nil {
		t.Fatal(err)
	}
	settleQuick(t, model)
	if model.quickQuery != "main" || model.quickCount != 1 {
		t.Fatalf("new search retained previous input: query=%q matches=%d", model.quickQuery, model.quickCount)
	}
}

func TestTitlebarSearchOutsideClickAndResultSelection(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	model.app.GetViewport = func() (int, int) { return 1440, 900 }
	ecs.InsertResource(app.World, scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	for _, camera := range ecs.Query[scene.Camera](app.World).All {
		camera.Mut().UpdateViewport(1440, 900)
	}
	const selectedFile = "selected.go"
	if err := os.WriteFile(filepath.Join(model.projectPath, selectedFile), []byte("package selected\n"), 0644); err != nil {
		t.Fatal(err)
	}
	project := model.activeProjectState()
	model.startProjectScan(project, false)
	waitPencodeProjectScan(t, project, func() { model.pollProjectScans() })
	model.quickQuery = selectedFile
	state, _ := ecs.GetResource[*input.State](app.World)
	step := func() {
		t.Helper()
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
	}
	click := func(id string) {
		t.Helper()
		node, ok := model.fontResources.HTMLNode(model.surface, id)
		if !ok {
			t.Fatalf("missing click target %s", id)
		}
		bounds, ok := node.Bounds()
		if !ok {
			t.Fatalf("missing bounds for %s", id)
		}
		scale := float64(model.settings.Zoom) / 100
		x, y := float64(bounds.X+bounds.Width/2)*scale, float64(bounds.Y+bounds.Height/2)*scale
		state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
		state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
		step()
	}
	for _, zoom := range [...]int{70, 100, 150} {
		openFileAndWait(t, model, "main.go")
		model.setZoom(model.fontResources, zoom)
		markPencodeRootDirtyByModel(model.fontResources, model)
		step()
		click("quick-open-query")
		settleQuick(t, model)
		markPencodeRootDirtyByModel(model.fontResources, model)
		step()
		if !model.quickOpen {
			t.Fatalf("clicking titlebar input did not open search at zoom %d", zoom)
		}
		click("quick-row-0")
		if err := awaitFileIO(t, model); err != nil {
			t.Fatal(err)
		}
		if model.quickOpen || model.activePath != selectedFile {
			t.Fatalf("clicking a result did not select a file at zoom %d", zoom)
		}
		click("quick-open-query")
		settleQuick(t, model)
		markPencodeRootDirtyByModel(model.fontResources, model)
		step()
		if !model.quickOpen {
			t.Fatalf("search did not reopen at zoom %d", zoom)
		}
		state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: 1438, Y: 400})
		state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: 1438, Y: 400})
		step()
		if model.quickOpen {
			t.Fatalf("clicking empty space did not dismiss search at zoom %d", zoom)
		}
		click("quick-open-query")
		settleQuick(t, model)
		markPencodeRootDirtyByModel(model.fontResources, model)
		step()
		click("settings-button")
		if model.quickOpen || !model.settingsOpen {
			t.Fatalf("outside click did not close search and activate settings at zoom %d", zoom)
		}
		model.settingsOpen = false
		markPencodeRootDirtyByModel(model.fontResources, model)
		step()
	}
}

func TestPencodePlatformShortcuts(t *testing.T) {
	for _, fixture := range []struct {
		action pencodeShortcutAction
		goos   string
		label  string
		key    input.KeyCode
		shift  bool
		alt    bool
	}{
		{pencodeShortcutSave, "darwin", "Cmd+S", input.KeyS, false, false},
		{pencodeShortcutQuickOpen, "darwin", "Cmd+P", input.KeyP, false, false},
		{pencodeShortcutFind, "darwin", "Cmd+F", input.KeyF, false, false},
		{pencodeShortcutRedo, "darwin", "Cmd+Shift+Z", input.KeyZ, true, false},
		{pencodeShortcutReplace, "darwin", "Cmd+Option+F", input.KeyF, false, true},
		{pencodeShortcutQuickOpen, "windows", "Ctrl+P", input.KeyP, false, false},
		{pencodeShortcutSave, "linux", "Ctrl+S", input.KeyS, false, false},
	} {
		t.Run(fixture.label+"/"+fixture.goos, func(t *testing.T) {
			binding := defaultPencodeBinding(fixture.action, fixture.goos)
			if got := binding.label(fixture.goos); got != fixture.label {
				t.Fatalf("shortcut label = %q, want %q", got, fixture.label)
			}
			for _, primary := range []input.KeyCode{input.KeyLeftSuper, input.KeyRightSuper, input.KeyLeftControl, input.KeyRightControl} {
				state := input.NewState()
				state.PressKey(primary)
				if fixture.shift {
					state.PressKey(input.KeyLeftShift)
				}
				if fixture.alt {
					state.PressKey(input.KeyLeftAlt)
				}
				state.PressKey(fixture.key)
				command := primary == input.KeyLeftSuper || primary == input.KeyRightSuper
				if got, want := binding.matches(state, fixture.goos), command == (fixture.goos == "darwin"); got != want {
					t.Fatalf("shortcut match with modifier %v = %v, want %v", primary, got, want)
				}
			}
		})
	}
	if label := (pencodeKeybinding{Key: input.KeyP, Super: true}).label("darwin"); label != "Cmd+P" {
		t.Fatalf("explicit Command shortcut = %q", label)
	}
	if label := defaultPencodeBinding(pencodeShortcutNext, "darwin").label("darwin"); label != "Ctrl+Tab" {
		t.Fatalf("macOS tab switching = %q", label)
	}
}
