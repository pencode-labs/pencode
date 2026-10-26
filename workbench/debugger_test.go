package workbench

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	"github.com/pencode-labs/pencode/dap"
	"github.com/pencode-labs/pencode/debugger"
	"github.com/pencode-labs/pencode/output"
)

func TestDebugBreakpointsTrackEditsUndoAndClosedFiles(t *testing.T) {
	m, p := resourceTestModel(t)
	text := "package main\n// 中文🙂\nfunc main() {}\n"
	resourceTestWrite(t, p, "main.go", text)
	openProjectFile(p, "main.go")
	m.syncActiveProjectFields()
	m.toggleDebugBreakpoint(nil, 3)
	check := func(line int) {
		t.Helper()
		bps := m.debugBreakpointSnapshot(p)[filepath.Join(p.Path, "main.go")]
		if len(bps) != 1 || bps[0].Line != line {
			t.Fatalf("breakpoints: %+v, want line %d", bps, line)
		}
	}
	check(3)
	m.updateActiveContent("// inserted\n" + text)
	check(4)
	m.undoActiveContent(nil)
	check(3)
	m.redoActiveContent(nil)
	check(4)
	if err := saveFileAndWait(t, m, p.Path, m.activeFile()); err != nil {
		t.Fatal(err)
	}
	m.closeProjectTab(p, "main.go")
	check(4)
	openProjectFile(p, "main.go")
	m.syncActiveProjectFields()
	m.toggleDebugBreakpoint(nil, 4)
	if len(m.debugBreakpointSnapshot(p)[filepath.Join(p.Path, "main.go")]) != 0 {
		t.Fatal("removing final breakpoint did not retain empty replacement")
	}
}

func TestDebugBreakpointsFollowDirectoryRenameAndDeletion(t *testing.T) {
	m, p := resourceTestModel(t)
	resourceTestWrite(t, p, "src/main.go", "package main\nfunc main() {}\n")
	openProjectFile(p, "src/main.go")
	m.syncActiveProjectFields()
	m.toggleDebugBreakpoint(nil, 2)
	m.closeProjectTab(p, "src/main.go")
	m.fileContext = pencodeFileContext{project: p, path: "src", directory: true, dialog: "rename", name: "lib"}
	if err := fileDialogAndWait(t, m); err != nil {
		t.Fatal(err)
	}
	bps := m.debugBreakpointSnapshot(p)
	if len(bps) != 1 || len(bps[filepath.Join(p.Path, "lib/main.go")]) != 1 || bps[filepath.Join(p.Path, "lib/main.go")][0].Line != 2 {
		t.Fatalf("renaming a closed file lost its breakpoint: %+v", bps)
	}
	m.debugRows()
	if len(m.debug.rows) != 1 || m.debug.rows[0].label != "lib/main.go" {
		t.Fatalf("breakpoint list did not follow rename: %+v", m.debug.rows)
	}
	m.fileContext = pencodeFileContext{project: p, path: "lib", directory: true, dialog: "delete"}
	if err := fileDialogAndWait(t, m); err != nil {
		t.Fatal(err)
	}
	m.debugRows()
	if len(p.debugBreakpoints) != 0 || len(m.debug.rows) != 0 {
		t.Fatal("deleting a directory retained breakpoints")
	}
}

func TestDebugPanelAndGutterInteractions(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	t.Cleanup(m.closeDebugger)
	world := m.fontResources
	press := func(key input.KeyCode) {
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
		languageInteractionStep(t, app)
	}
	field := module.ContentMut[engineui.TextField](world, m.editor.control)
	field.Caret, field.Anchor = 0, 0
	press(input.KeyF9)
	project, file := m.activeProjectState(), m.activeFile()
	if bps := project.debugBreakpoints[file.Path]; len(bps) != 1 || bps[0].line != 1 {
		t.Fatalf("F9: %+v", bps)
	}
	if len(m.editor.debugPaint.dots) != 1 {
		t.Fatalf("breakpoint dot not painted: %d", len(m.editor.debugPaint.dots))
	}
	if allocs := testing.AllocsPerRun(50, func() { m.editor.debugPaint.sync(world, m) }); allocs != 0 {
		t.Fatalf("warm breakpoint paint allocated: %g", allocs)
	}
	if allocs := testing.AllocsPerRun(50, func() { m.pollDebugger(world) }); allocs != 0 {
		t.Fatalf("idle debugger allocated: %g", allocs)
	}
	press(input.KeyF9)
	if len(project.debugBreakpoints[file.Path]) != 0 {
		t.Fatal("F9 did not clear breakpoint")
	}
	editorContextClick(t, app, m, state, "status-debug")
	deadline := time.Now().Add(5 * time.Second)
	for m.debug.loading {
		languageInteractionStep(t, app)
		if time.Now().After(deadline) {
			t.Fatal("debug configuration load timed out")
		}
		time.Sleep(time.Millisecond)
	}
	if len(m.debug.configs) != 2 || m.bottom.active != pencodePanelDebug {
		t.Fatalf("debug panel not loaded: %+v", m.debug)
	}
	for _, ref := range []string{"debug-config", "debug-start", "debug-stop", "debug-pane", "debug-expression"} {
		editorContextBounds(t, m, ref)
	}
	m.debug.view = debugger.Snapshot{Revision: 1, Threads: []dap.Thread{{ID: 1, Name: "main"}, {ID: 2, Name: "worker"}}, Frames: []dap.StackFrame{{ID: 10, Name: "main"}}}
	markPencodeRootDirtyByModel(world, m)
	languageInteractionStep(t, app)
	checkThreads := func(want int) {
		t.Helper()
		count := 0
		for _, row := range m.debug.rows {
			if row.slot == "debug-threads" {
				count++
			}
		}
		if count != want {
			t.Fatalf("thread rows = %d, want %d", count, want)
		}
		editorContextBounds(t, m, "debug-row:frame:10")
	}
	checkThreads(0)
	editorContextClick(t, app, m, state, "debug-toggle-threads")
	checkThreads(2)
	editorContextBounds(t, m, "debug-row:thread:1")
	editorContextClick(t, app, m, state, "debug-toggle-threads")
	checkThreads(0)
	m.debug.output = "console output"
	markPencodeRootDirtyByModel(world, m)
	languageInteractionStep(t, app)
	editorContextClick(t, app, m, state, "debug-clear")
	if m.debug.output != "" {
		t.Fatal("clear console button did not clear output")
	}
	m.debugEvent(world, "debug-config", "1")
	markPencodeRootDirtyByModel(world, m)
	languageInteractionStep(t, app)
	if m.debug.selected != 1 {
		t.Fatal("configuration selection lost")
	}
	// Click the dedicated breakpoint margin; normal line-number clicks stay free.
	gutter := editorContextBounds(t, m, "editor-gutter")
	text := module.ContentMut[scene.Text2D](world, m.editor.text)
	transform, _ := module.ContentGet[scene.UITransform](world, m.editor.text)
	x, y := float64(gutter.X+8), float64(gutter.Y+transform.Translation.Y.Value+text.LineHeight/2)
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
	languageInteractionStep(t, app)
	if len(project.debugBreakpoints[file.Path]) != 1 {
		t.Fatal("gutter click did not add breakpoint")
	}
	editorContextBounds(t, m, "debug-bp:"+file.Path+":1")
}

func TestDebugVariableAliasesHaveUniqueRows(t *testing.T) {
	m := &workspaceModel{}
	m.debug.view = debugger.Snapshot{Revision: 1, Scopes: []dap.Scope{{Name: "locals", VariablesReference: 1}}, Variables: map[int][]dap.Variable{1: {{Name: "a", VariablesReference: 2}, {Name: "b", VariablesReference: 2}}, 2: {{Name: "child", Value: "1"}}}}
	m.debug.expanded = map[int]bool{2: true}
	m.debugRows()
	seen := make(map[string]bool)
	for _, row := range m.debug.rows {
		if seen[row.ref] {
			t.Fatal("duplicate variable row", row.ref)
		}
		seen[row.ref] = true
	}
	if len(m.debug.rows) != 5 {
		t.Fatal("alias children missing", len(m.debug.rows))
	}
	if allocations := testing.AllocsPerRun(100, m.debugRows); allocations != 0 {
		t.Fatal("warm variable rows allocated", allocations)
	}
}

func TestDebugEvaluationRowsExpandAndClear(t *testing.T) {
	var store output.Store
	channel := store.Channel("debug")
	m := &workspaceModel{}
	m.debug.session = &debugger.Session{Output: channel}
	m.debug.view = debugger.Snapshot{
		Revision: 1,
		Scopes:   []dap.Scope{{Name: "locals", VariablesReference: 1}},
		Variables: map[int][]dap.Variable{
			1: {{Name: "opts", Value: "Options", VariablesReference: 2}},
			2: {{Name: "nested", Value: "Nested", VariablesReference: 3}},
			3: {{Name: "debug", Value: "true"}},
		},
		Evaluations: []dap.Variable{{Name: "opts", Value: "Options", Type: "Options", VariablesReference: 2}, {Name: "value", Value: "42"}},
	}
	m.debugRows()
	if !m.debugEvent(nil, "debug-row:evaluation:2:eval:0", "") {
		t.Fatal("evaluation expansion event was not handled")
	}
	m.debugRows()
	if !m.debugEvent(nil, "debug-row:variable:3:eval:0:0", "") {
		t.Fatal("nested evaluation expansion event was not handled")
	}
	m.debugRows()
	seen := make(map[string]bool)
	consoleRows := 0
	for _, row := range m.debug.rows {
		if seen[row.ref] {
			t.Fatal("evaluation and variable rows share a key", row.ref)
		}
		seen[row.ref] = true
		if row.slot == "debug-evaluations" {
			consoleRows++
			if row.label == "debug" && (row.depth != 2 || row.detail != "true") {
				t.Fatal("nested value rendered incorrectly", row)
			}
		}
	}
	if consoleRows != 4 {
		t.Fatal("missing nested or scalar evaluation rows", consoleRows)
	}
	if allocations := testing.AllocsPerRun(100, m.debugRows); allocations != 0 {
		t.Fatal("warm evaluation rows allocated", allocations)
	}
	m.debugEvent(nil, "debug-row:evaluation:2:eval:0", "")
	m.debugRows()
	for _, row := range m.debug.rows {
		if row.slot == "debug-evaluations" && row.depth > 0 {
			t.Fatal("collapsed evaluation retained child rows")
		}
	}
	_, _ = channel.Write([]byte("old output"))
	m.debug.output = "old output"
	m.debugEvent(nil, "debug-clear", "")
	m.debugRows()
	if m.debug.output != "" || len(m.debug.view.Evaluations) != 0 {
		t.Fatal("clearing console retained evaluation rows")
	}
}

func TestDebugWorkbenchWithDelve(t *testing.T) {
	dlv := os.Getenv("PENCODE_TEST_DLV")
	if dlv == "" {
		var err error
		dlv, err = exec.LookPath("dlv")
		if err != nil {
			t.Skip("Delve is not installed")
		}
	}
	app, m, _, state := languageInteractionFixture(t)
	t.Cleanup(m.closeDebugger)
	p := m.activeProjectState()
	text := "package main\nimport \"fmt\"\nfunc main() {\n value := 40; opts := struct{ Debug bool }{true}; _ = opts\n value += 2\n fmt.Println(value)\n}\n"
	m.updateActiveContent(text)
	if err := saveFileAndWait(t, m, p.Path, m.activeFile()); err != nil {
		t.Fatal(err)
	}
	resourceTestWrite(t, p, "go.mod", "module workbenchdebugfixture\ngo 1.27.0\n")
	config, _ := json.Marshal(map[string]any{"program": p.Path, "cwd": p.Path, "mode": "debug"})
	m.debug.configProject, m.debug.configs = p, []debugger.Config{{Label: "Fixture", Adapter: "Delve", Command: dlv, Request: "launch", Configuration: config}}
	m.toggleDebugBreakpoint(nil, 5)
	m.startDebug()
	deadline := time.Now().Add(90 * time.Second)
	wait := func(line int, value string) {
		t.Helper()
		for {
			languageInteractionStep(t, app)
			if m.debug.view.State == debugger.Failed {
				t.Fatalf("Delve: %s\n%s", m.debug.view.Error, m.debug.output)
			}
			ready := m.debug.view.State == debugger.Stopped && m.debug.view.FrameID != 0
			if ready {
				ready = false
				for _, frame := range m.debug.view.Frames {
					if frame.ID == m.debug.view.FrameID && frame.Line == line {
						ready = true
					}
				}
			}
			found := false
			for _, vars := range m.debug.view.Variables {
				for _, variable := range vars {
					if variable.Name == "value" && variable.Value == value {
						found = true
					}
				}
			}
			if ready && found {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("debug timeout: %+v\n%s", m.debug.view, m.debug.output)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	wait(5, "40")
	if allocs := testing.AllocsPerRun(50, func() { m.editor.debugPaint.sync(m.fontResources, m) }); allocs != 0 {
		t.Fatalf("warm execution paint allocated: %g", allocs)
	}
	if m.bottom.active != pencodePanelDebug {
		t.Fatal("stopped session did not open debugger")
	}
	editorContextBounds(t, m, "debug-row:frame:"+strconv.Itoa(m.debug.view.FrameID))
	for _, ref := range []string{"debug-row:scope:", "debug-leaf:"} {
		found := false
		for _, row := range m.debug.rows {
			if strings.HasPrefix(row.ref, ref) {
				found = true
			}
		}
		if !found {
			t.Fatal("missing rows", ref)
		}
	}
	if m.editor.debugPaint.execution == 0 {
		t.Fatal("execution line not painted")
	}
	m.debug.expression = "opts"
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	editorContextClick(t, app, m, state, "debug-evaluate")
	for len(m.debug.view.Evaluations) == 0 {
		languageInteractionStep(t, app)
		if time.Now().After(deadline) {
			t.Fatal("struct evaluation timed out")
		}
		time.Sleep(time.Millisecond)
	}
	result := m.debug.view.Evaluations[0]
	if result.VariablesReference <= 0 {
		t.Fatal("struct evaluation cannot be expanded", result)
	}
	editorContextClick(t, app, m, state, "debug-row:evaluation:"+strconv.Itoa(result.VariablesReference)+":eval:0")
	for {
		languageInteractionStep(t, app)
		found := false
		for _, row := range m.debug.rows {
			if row.slot == "debug-evaluations" && row.label == "Debug" && row.detail == "true" {
				editorContextBounds(t, m, row.ref)
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("struct children not rendered: %+v", m.debug.rows)
		}
		time.Sleep(time.Millisecond)
	}
	editorContextClick(t, app, m, state, "debug-clear")
	if len(m.debug.view.Evaluations) != 0 || m.debug.output != "" {
		t.Fatal("console clear retained output or evaluations")
	}
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyF10})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyF10})
	languageInteractionStep(t, app)
	wait(6, "42")
	m.debugEvent(m.fontResources, "debug-stop", "")
	for m.debug.view.State != debugger.Ended {
		languageInteractionStep(t, app)
		if time.Now().After(deadline) {
			t.Fatal("stop did not complete")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if visible, _ := module.ContentGet[scene.Visibility](m.fontResources, m.editor.debugPaint.execution); visible.Visible {
		t.Fatal("execution line retained after stop")
	}
}
