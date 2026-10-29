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
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/dap"
	"github.com/pencode-labs/pencode/debugger"
	"github.com/pencode-labs/pencode/output"
	"github.com/pencode-labs/pencode/peb"
)

func waitDebugConfigs(t *testing.T, m *workspaceModel) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for m.debug.loading {
		m.pollDebugger(nil)
		if time.Now().After(deadline) {
			t.Fatal("debug configuration load timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDebugConfigsRequireExplicitLaunchers(t *testing.T) {
	for _, filename := range []string{"main.go", "index.php"} {
		t.Run(filename, func(t *testing.T) {
			m, p := resourceTestModel(t)
			t.Cleanup(m.closeDebugger)
			resourceTestWrite(t, p, filename, "source")
			openProjectFile(p, filename)
			m.syncActiveProjectFields()
			m.startDebug()
			waitDebugConfigs(t, m)
			if len(m.debug.configs) != 0 || len(m.debug.options) != 0 || m.debug.session != nil || m.debug.startPending || m.debug.loading {
				t.Fatalf("missing config supplied a launcher or retried: %+v", m.debug)
			}
			if _, err := os.Stat(filepath.Join(p.Path, ".pencode")); !os.IsNotExist(err) {
				t.Fatal("loading missing config created files", err)
			}
			if allocs := testing.AllocsPerRun(50, func() { m.pollDebugger(nil) }); allocs != 0 {
				t.Fatal("idle empty debugger allocated", allocs)
			}
		})
	}
}

func TestDebugAddLauncherOpensProjectConfig(t *testing.T) {
	m, p := resourceTestModel(t)
	t.Cleanup(m.closeDebugger)
	m.debugEvent(nil, "debug-add", "")
	m.debugEvent(nil, "debug-template:edit", "")
	waitDebugConfigs(t, m)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	f := m.activeFile()
	if f == nil || f.Path != ".pencode/debug.json" || f.Content != "[]\n" || len(m.debug.configs) != 0 {
		t.Fatalf("add did not open an empty project config: %+v", f)
	}
	config := `[{"label":"PHP","adapter":"Xdebug","request":"launch","configuration":{"port":9003,"cwd":"${workspaceFolder}"}}]`
	m.updateActiveContent(config)
	// Reopening the add entry must retain unsaved editor content.
	m.debugEvent(nil, "debug-add", "")
	m.debugEvent(nil, "debug-template:edit", "")
	waitDebugConfigs(t, m)
	if m.activeFile() != f || f.Content != config || !f.Dirty {
		t.Fatal("add overwrote the existing config buffer")
	}
	if err := saveFileAndWait(t, m, p.Path, f); err != nil {
		t.Fatal(err)
	}
	m.debugEvent(nil, "debug-reload", "")
	waitDebugConfigs(t, m)
	if len(m.debug.configs) != 1 || m.debug.configs[0].Adapter != "Xdebug" || len(m.debug.options) != 1 || m.debug.message != "" {
		t.Fatalf("saved launcher did not replace the empty list: %+v", m.debug)
	}
	data, err := os.ReadFile(filepath.Join(p.Path, f.Path))
	if err != nil || string(data) != config {
		t.Fatal("launcher file was not saved verbatim", string(data), err)
	}
}

func TestDebugInvalidConfigsRemainEditable(t *testing.T) {
	m, p := resourceTestModel(t)
	t.Cleanup(m.closeDebugger)
	resourceTestWrite(t, p, ".pencode/debug.json", "invalid json")
	m.debugEvent(nil, "debug-add", "")
	m.debugEvent(nil, "debug-template:edit", "")
	waitDebugConfigs(t, m)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if m.activeFile() == nil || m.activeFile().Content != "invalid json" || m.debug.message == "" || len(m.debug.configs) != 0 {
		t.Fatal("invalid config was overwritten or could not be opened")
	}
}

func TestDebugReloadClearsRemovedLaunchers(t *testing.T) {
	m, p := resourceTestModel(t)
	t.Cleanup(m.closeDebugger)
	resourceTestWrite(t, p, ".pencode/debug.json", `[{"label":"PHP","adapter":"Xdebug","request":"launch","configuration":{}}]`)
	m.loadDebugConfigs()
	waitDebugConfigs(t, m)
	if len(m.debug.configs) != 1 {
		t.Fatal("project launcher missing")
	}
	for _, content := range []string{"[]", "invalid json"} {
		resourceTestWrite(t, p, ".pencode/debug.json", content)
		m.loadDebugConfigs()
		waitDebugConfigs(t, m)
		m.startDebug()
		if len(m.debug.configs) != 0 || len(m.debug.options) != 0 || m.debug.session != nil || m.debug.loading || m.debug.startPending {
			t.Fatalf("stale launcher retained after %q: %+v", content, m.debug)
		}
	}
}

func TestDebugProjectSwitchCancelsPendingLaunch(t *testing.T) {
	m, p := resourceTestModel(t)
	t.Cleanup(m.closeDebugger)
	resourceTestWrite(t, p, ".pencode/debug.json", `[{"label":"PHP","adapter":"Xdebug","request":"launch","configuration":{}}]`)
	next := &pencodeProject{Path: t.TempDir(), Files: make(map[string]*openFile)}
	attachPencodeProject(t, next, p.backend)
	m.projects = append(m.projects, next)
	m.startDebug()
	m.activateProject(next)
	m.pollDebugger(nil)
	waitDebugConfigs(t, m)
	if m.debug.configProject != next || len(m.debug.configs) != 0 || len(m.debug.options) != 0 || m.debug.startPending || m.debug.session != nil {
		t.Fatalf("switching project retained a launcher or pending start: %+v", m.debug)
	}
}

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
	field := world.Mut[engineui.TextField](m.editor.control)
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
	if len(m.debug.configs) != 0 || len(m.debug.options) != 0 || m.bottom.active != pencodePanelDebug {
		t.Fatalf("debug panel not loaded: %+v", m.debug)
	}
	for _, ref := range []string{"debug-config", "debug-add", "debug-start", "debug-stop", "debug-pane", "debug-expression"} {
		editorContextBounds(t, m, ref)
	}
	selector, ok := world.HTMLControl(m.surface, "debug-config")
	control, exists := world.Get[htmlui.Control](selector)
	if !ok || !exists || len(control.Options) != 1 || control.Options[0].Label != "暂无启动器" || !control.Disabled {
		t.Fatalf("empty selector placeholder missing: %+v", control)
	}
	press(input.KeyF5)
	if m.debug.loading || m.debug.startPending || m.debug.session != nil || m.debug.message != "" {
		t.Fatal("F5 with no launchers attempted to start or reload the debugger")
	}
	resourceTestWrite(t, project, ".pencode/debug.json", `[{"label":"PHP","adapter":"Xdebug","request":"launch","configuration":{"port":9003}},{"label":"PHP attach","adapter":"Xdebug","request":"attach","configuration":{}}]`)
	editorContextClick(t, app, m, state, "debug-reload")
	for m.debug.loading {
		languageInteractionStep(t, app)
		if time.Now().After(deadline) {
			t.Fatal("debug configuration reload timed out")
		}
		time.Sleep(time.Millisecond)
	}
	if len(m.debug.configs) != 2 || m.debug.configs[0].Adapter != "Xdebug" {
		t.Fatalf("project launchers not loaded: %+v", m.debug.configs)
	}
	control, _ = world.Get[htmlui.Control](selector)
	if len(control.Options) != 2 || control.Options[0].Label != "PHP" || control.Disabled || m.debug.message != "" {
		t.Fatalf("launcher did not replace the placeholder: %+v", control)
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
	text := world.Mut[scene.Text2D](m.editor.text)
	transform, _ := world.Get[scene.UITransform](m.editor.text)
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
	if visible, _ := m.fontResources.Get[scene.Visibility](m.editor.debugPaint.execution); visible.Visible {
		t.Fatal("execution line retained after stop")
	}
}

func TestDebugTemplatesGenerateAndAppendProjectJSON(t *testing.T) {
	for _, opened := range []bool{false, true} {
		t.Run(strconv.FormatBool(opened), func(t *testing.T) {
			m, p := resourceTestModel(t)
			t.Cleanup(m.closeDebugger)
			const templates = `[{"label":"Go","adapter":"Delve","request":"launch","configuration":{"program":"${workspaceFolder}","args":["a b"],"stopOnEntry":true}}]`
			m.importDebugTemplates("go", []peb.DebugAdapter{{ID: "Delve", Templates: templates}})
			m.debugEvent(nil, "debug-add", "")
			if !m.debug.adding {
				t.Fatal("add did not offer registered templates")
			}
			if _, err := os.Stat(filepath.Join(p.Path, ".pencode")); !os.IsNotExist(err) {
				t.Fatal("opening template picker wrote files", err)
			}
			m.debugEvent(nil, "debug-template:0", "")
			waitDebugConfigs(t, m)
			if err := awaitFileIO(t, m); err != nil {
				t.Fatal(err)
			}
			f := m.activeFile()
			if f == nil || f.Path != pencodeDebugConfigPath || f.Dirty || !strings.Contains(f.Content, "${workspaceFolder}") || len(m.debug.configs) != 1 || m.debug.session != nil || m.debug.adding {
				t.Fatalf("template was not written and opened for editing: file=%+v debug=%+v", f, m.debug)
			}
			var entries []json.RawMessage
			if err := json.Unmarshal([]byte(f.Content), &entries); err != nil {
				t.Fatal(err)
			}
			// Extra fields and other adapter entries must survive appending.
			config := `[{"label":"Other","adapter":"Other","request":"attach","configuration":{"cwd":"${workspaceFolder}"},"custom":true}]`
			m.updateActiveContent(config)
			if err := saveFileAndWait(t, m, p.Path, f); err != nil {
				t.Fatal(err)
			}
			waitDebugConfigs(t, m)
			if !opened {
				m.closeProjectTab(p, pencodeDebugConfigPath)
			}
			m.debugEvent(nil, "debug-add", "")
			m.debugEvent(nil, "debug-template:0", "")
			waitDebugConfigs(t, m)
			if err := awaitFileIO(t, m); err != nil {
				t.Fatal(err)
			}
			waitDebugConfigs(t, m)
			f = m.activeFile()
			data, err := os.ReadFile(filepath.Join(p.Path, pencodeDebugConfigPath))
			if err != nil || f.Content != string(data) || f.Dirty || len(m.debug.configs) != 2 || !strings.Contains(string(data), `"custom": true`) || !strings.Contains(string(data), "${workspaceFolder}") || m.debug.session != nil {
				t.Fatalf("append lost configuration or failed to save: file=%+v configs=%+v err=%v", f, m.debug.configs, err)
			}
			m.updateActiveContent(f.Content + " ")
			before := f.Content
			m.debugEvent(nil, "debug-add", "")
			m.debugEvent(nil, "debug-template:0", "")
			if f.Content != before || !f.Dirty || m.debug.message == "" {
				t.Fatal("template overwrote unsaved editor content")
			}
			unchanged, err := os.ReadFile(filepath.Join(p.Path, pencodeDebugConfigPath))
			if err != nil || string(unchanged) != string(data) {
				t.Fatal("unsaved config changed on disk", err)
			}
		})
	}
}

func TestDebugTemplatesInvalidConfigAndExtensionRemoval(t *testing.T) {
	m, p := resourceTestModel(t)
	t.Cleanup(m.closeDebugger)
	adapter := peb.DebugAdapter{ID: "Delve", Templates: `[{"label":"Go","adapter":"Delve","request":"launch","configuration":{}}]`}
	m.importDebugTemplates("go", []peb.DebugAdapter{adapter})
	m.importDebugTemplates("go", []peb.DebugAdapter{adapter})
	if len(m.debugTemplates) != 1 {
		t.Fatal("reimport duplicated templates")
	}
	resourceTestWrite(t, p, pencodeDebugConfigPath, "invalid json")
	m.debugEvent(nil, "debug-add", "")
	m.debugEvent(nil, "debug-template:0", "")
	waitDebugConfigs(t, m)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if m.activeFile() == nil || m.activeFile().Content != "invalid json" || m.debug.message == "" {
		t.Fatal("invalid file was replaced or was not editable")
	}
	m.debugEvent(nil, "debug-add", "")
	m.removeDebugTemplates("extensions/go/")
	if len(m.debugTemplates) != 0 || m.debug.adding {
		t.Fatal("unloaded extension retained templates")
	}
}

func TestDebugTemplateMenuOpensFromButton(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	t.Cleanup(m.closeDebugger)
	m.importDebugTemplates("php", []peb.DebugAdapter{{ID: "Xdebug", Templates: `[{"label":"监听 Xdebug","adapter":"Xdebug","request":"launch","configuration":{"port":9003}}]`}})
	editorContextClick(t, app, m, state, "status-debug")
	wait := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for m.debug.loading || m.fileIO != nil && len(m.fileIO.pending) != 0 {
			languageInteractionStep(t, app)
			if time.Now().After(deadline) {
				t.Fatal("debug file operation timed out")
			}
			time.Sleep(time.Millisecond)
		}
	}
	wait()
	before := editorContextBounds(t, m, "debug-add")
	editorContextClick(t, app, m, state, "debug-add")
	menu := editorContextBounds(t, m, "debug-template-menu")
	root := editorContextBounds(t, m, "pencode-root")
	if !m.debug.adding || len(m.debugTemplateRows) != 2 || m.debugTemplateRows[0].label != "监听 Xdebug" {
		t.Fatal("button did not open template menu")
	}
	if menu.X < root.X || menu.Y < root.Y || menu.X+menu.Width > root.X+root.Width || menu.Y+menu.Height > root.Y+root.Height {
		t.Fatalf("menu escaped viewport: menu=%+v root=%+v", menu, root)
	}
	if after := editorContextBounds(t, m, "debug-add"); after != before {
		t.Fatalf("menu moved toolbar: before=%+v after=%+v", before, after)
	}
	if _, exists := m.fontResources.HTMLNode(m.surface, "debug-template"); exists {
		t.Fatal("extra template selector remains")
	}
	editorContextBounds(t, m, "debug-template:0")
	editorContextBounds(t, m, "debug-template:edit")
	editorContextClick(t, app, m, state, "debug-template:0")
	wait()
	if m.debug.adding || m.activeFile() == nil || m.activeFile().Path != pencodeDebugConfigPath || len(m.debug.configs) != 1 {
		t.Fatal("menu selection did not write and open template")
	}
	editorContextClick(t, app, m, state, "debug-add")
	if allocs := testing.AllocsPerRun(50, func() { m.updateDebugTemplateMenu(m.fontResources, state) }); allocs != 0 {
		t.Fatal("idle template menu allocated", allocs)
	}
	press := func(key input.KeyCode) {
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
		languageInteractionStep(t, app)
	}
	press(input.KeyDown)
	if m.debug.templateIndex != 1 {
		t.Fatal("arrow key did not select menu row")
	}
	press(input.KeyEnter)
	wait()
	if m.debug.adding || len(m.debug.configs) != 1 {
		t.Fatal("manual edit menu action appended a template")
	}
	editorContextClick(t, app, m, state, "debug-add")
	press(input.KeyEscape)
	if m.debug.adding {
		t.Fatal("Escape did not dismiss menu")
	}
	editorContextClick(t, app, m, state, "debug-add")
	editorContextClick(t, app, m, state, "debug-add")
	if m.debug.adding {
		t.Fatal("second click did not dismiss menu")
	}
	editorContextClick(t, app, m, state, "debug-add")
	editorContextClick(t, app, m, state, "debug-reload")
	wait()
	if m.debug.adding {
		t.Fatal("outside click did not dismiss menu")
	}
	m.removeDebugTemplates("extensions/php/")
	if allocs := testing.AllocsPerRun(50, func() { m.pollDebugger(nil) }); allocs != 0 {
		t.Fatal("idle debugger allocated", allocs)
	}
}
