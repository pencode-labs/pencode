package workbench

import (
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/diiyw/onix"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/project"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/ui/html"
)

func TestRetainedWorkspaceKeepsPrivateViewAndSharedProjectState(t *testing.T) {
	for _, preload := range []bool{false, true} {
		name := "on-demand"
		if preload {
			name = "preloaded"
		}
		t.Run(name, func(t *testing.T) { testRetainedWorkspace(t, preload) })
	}
}

func testRetainedWorkspace(t *testing.T, preload bool) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	source := os.DirFS("..")
	plan, err := project.CompileProject(source, ".")
	if err != nil {
		t.Fatal(err)
	}
	session := newPencodeTestSession(t, newPencodeBackend(t))
	registry := module.Registry{}
	if err := session.Register(&registry); err != nil {
		t.Fatal(err)
	}
	for _, id := range [...]project.SceneID{"main.json", "workspace.json"} {
		err = registry.RegisterScene(id, func() struct{} { return struct{}{} }, module.Lifecycle[struct{}]{
			OnMessage: func(c *module.Context, _ *struct{}, message module.Message) error {
				if message.Topic == "pencode.navigate" {
					workspace, _ := message.Value[bool]()
					target := project.SceneID("main.json")
					if workspace {
						target = "workspace.json"
					}
					return c.Switch(module.SceneSwitchRequest{Scene: target, Retain: true})
				}
				if message.Topic == "scene.switch.result" {
					result, _ := message.Value[module.SceneSwitchResult]()
					return result.Err
				}
				return nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	app := onix.NewApp()
	t.Cleanup(app.Destroy)
	if err := app.InstallSystems[onix.UIHTML](); err != nil {
		t.Fatal(err)
	}
	entry, err := app.LoadModules[module.FullContent](plan, registry, module.Options{Source: source})
	if err != nil {
		t.Fatal(err)
	}
	runtime, manager := app.Modules(), app.Modules().Manager()
	launcher := session.active
	launcher.model.selectTheme("light-plus")
	if len(session.views) != 1 {
		t.Fatal("launcher installed an unused workspace view")
	}
	var prepared *module.Scene
	if preload {
		prepared, err = manager.Preload(module.ScenePreloadRequest{Scene: "workspace.json"})
		if err != nil {
			t.Fatal(err)
		}
		if session.active != launcher || prepared.State() != module.SceneReady {
			t.Fatal("preload changed foreground model")
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	step := func() {
		t.Helper()
		if err := runtime.UpdateFrame(1.0 / 60); err != nil {
			t.Fatal(err)
		}
		if err := runtime.Dispatch(); err != nil {
			t.Fatal(err)
		}
		content := session.active.model.fontResources
		if err := content.SyncHTML(); err != nil {
			t.Fatal(err)
		}
		if err := content.SyncUI(); err != nil {
			t.Fatal(err)
		}
		session.syncLayout()
	}
	if err := loadProjectAndWait(t, launcher.model, dir); err != nil {
		t.Fatal(err)
	}
	loadingProject := launcher.model.activeProjectState()
	loadingProject.stopScan()
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	slowSource := pencodeScanTestFS{FS: os.DirFS(dir), beforeReadDir: func(name string) error {
		if name == "." {
			close(entered)
			<-release
		}
		return nil
	}}
	loadingProject.scan = newPencodeProjectScan(func(name string) ([]fs.DirEntry, error) { return fs.ReadDir(slowSource, name) }, true, launcher.model.app.RequestRender)
	t.Cleanup(loadingProject.stopScan)
	waitPencodeScanSignal(t, entered)
	step()
	workspace := session.active
	if !preload {
		prepared = manager.Active()
	}
	if manager.Active() != prepared || workspace == launcher || entry.State() != module.SceneSuspended {
		t.Fatal("project did not activate separate workspace")
	}
	root, surface := workspace.model.root, workspace.model.surface
	border := workspace.model.windowBorder
	if root == launcher.model.root || surface == launcher.model.surface || border == launcher.model.windowBorder || workspace.model.projectPath != dir {
		t.Fatal("view identity or project state leaked across scenes")
	}
	if value, _ := workspace.model.fontResources.Get[html.Surface](surface); value.CSS != workspace.model.workbenchTheme.css || workspace.model.settings.Theme != "light-plus" {
		t.Fatal("launcher theme did not follow the workspace")
	}
	if loadingProject.scan == nil || workspace.model.activeFile() != nil || !workspace.model.fontResources.IsAlive(root) {
		t.Fatal("workspace did not open while the directory read was blocked")
	}
	unblock()
	waitPencodeProjectScan(t, workspace.model.activeProjectState(), step)
	if workspace.model.activeFile() != nil {
		t.Fatal("project scan opened a file")
	}
	openFileAndWait(t, workspace.model, "main.go")
	markPencodeRootDirtyByModel(workspace.model.fontResources, workspace.model)
	file := workspace.model.files[workspace.model.activePath]
	if file == nil {
		t.Fatal("project file was not loaded")
	}
	// Reconcile the editor, then bind its code font and decorations.
	step()
	step()
	content, editor := workspace.model.fontResources, &workspace.model.editor
	if binding, ok := content.Get[html.ControlTextFont](editor.control); !ok || binding.Font != workspace.model.codeFont() {
		t.Fatal("editor code font was not bound for HTML reconciliation")
	}
	if !content.IsAlive(editor.guide) || !content.IsAlive(editor.gutter) || !content.IsAlive(editor.numbers) {
		t.Fatal("editor decorations were not committed after the first update")
	}
	assertGuide := func(percent int) {
		t.Helper()
		link, ok := content.Get[html.ControlText](editor.text)
		if !ok {
			t.Fatal("editor text has no layout link")
		}
		controlSize, _ := content.Get[scene.Size2D](editor.control)
		transform := content.Mut[scene.UITransform](editor.guide)
		sprite := content.Mut[scene.Sprite2D](editor.guide)
		size, _ := content.Get[scene.Size2D](editor.guide)
		visibility, _ := content.Get[scene.Visibility](editor.guide)
		parent, _ := content.Get[scene.Parent](editor.guide)
		width := link.Content.Width * float32(percent) / 100
		if transform == nil || sprite == nil || transform.Translation.X.Value != link.Content.X+width ||
			sprite.Width != 1 || sprite.Height != controlSize.Height || size.Width != 1 || size.Height != controlSize.Height ||
			visibility.Visible != (width > 0 && width < link.Content.Width) || parent.Entity != editor.control {
			t.Fatal("width guide did not synchronize geometry, visibility or hierarchy")
		}
	}
	assertGuide(workspace.model.settings.EditorWidth)
	guide, gutter, numbers := editor.guide, editor.gutter, editor.numbers
	step()
	if editor.guide != guide || editor.gutter != gutter || editor.numbers != numbers {
		t.Fatal("editor recreated committed decorations")
	}
	if visibility, _ := content.Get[scene.Visibility](editor.gutter); !visibility.Visible {
		t.Fatal("line number gutter remained hidden after initialization")
	}
	if value, _ := content.Get[scene.Text2D](editor.numbers); value.Value == "" {
		t.Fatal("line numbers were not synchronized")
	}
	for _, width := range [...]int{0, 80, 100} {
		editor.syncWidthGuide(content, width, workspace.model.workbenchTheme.guide)
		assertGuide(width)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		editor.syncWidthGuide(content, workspace.model.settings.EditorWidth, workspace.model.workbenchTheme.guide)
	}); allocs != 0 {
		t.Fatalf("width guide synchronization allocated %g times", allocs)
	}
	highlightWorker := workspace.model.highlightWorker
	if highlightWorker == nil {
		t.Fatal("workspace did not start its private syntax worker")
	}
	file.Content = "package edited\n"
	workspace.model.selectTheme("dark-plus")
	workspace.model.screen = pencodeLauncher
	step()
	if launcher.model.highlightWorker != nil {
		t.Fatal("launcher started a syntax worker for its hidden editor")
	}
	if session.active != launcher || manager.Active() != entry || launcher.model.files[launcher.model.activePath] != file {
		t.Fatal("return lost project state")
	}
	if value, _ := launcher.model.fontResources.Get[html.Surface](launcher.model.surface); value.CSS != launcher.model.workbenchTheme.css || launcher.model.settings.Theme != "dark-plus" {
		t.Fatal("workspace theme did not follow the launcher")
	}
	if err := loadProjectAndWait(t, launcher.model, dir); err != nil {
		t.Fatal(err)
	}
	step()
	if session.active != workspace || manager.Active() != prepared || workspace.model.root != root || workspace.model.surface != surface || workspace.model.windowBorder != border || workspace.model.files[workspace.model.activePath].Content != "package edited\n" {
		t.Fatal("resume replaced retained workspace or lost edits")
	}
	if workspace.model.highlightWorker != highlightWorker {
		t.Fatal("resume replaced the retained syntax worker")
	}
	workspace.model.closeProject(workspace.model.activeProjectState())
	step()
	if session.active != launcher || manager.Active() != entry || len(launcher.model.projects) != 0 {
		t.Fatal("closing final project did not return to launcher")
	}
	if err := loadProjectAndWait(t, launcher.model, dir); err != nil {
		t.Fatal(err)
	}
	step()
	if session.active != workspace || manager.Active() != prepared {
		t.Fatal("reopen did not reuse retained workspace")
	}
}
