package workbench

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/server"
)

// Tests exercise the production protocol boundary without launching a GUI or
// requiring an installed server binary.
func newPencodeBackend(t testing.TB) *client.Client {
	t.Helper()
	a, b := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(context.Background(), b, server.Options{ExtensionDirectory: t.TempDir()})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := client.New(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); <-done })
	return c
}

func connectPencodeProject(t testing.TB, p *pencodeProject) {
	t.Helper()
	attachPencodeProject(t, p, newPencodeBackend(t))
}

func attachPencodeProject(t testing.TB, p *pencodeProject, backend *client.Client) {
	t.Helper()
	p.backend = backend
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w, err := p.backend.OpenWorkspace(ctx, p.Path)
	if err != nil {
		t.Fatal(err)
	}
	p.workspace = w
	p.Path = w.Path
}

func TestPencodeBackendOwnsEditedAndMovedDocuments(t *testing.T) {
	model, project := resourceTestModel(t)
	resourceTestWrite(t, project, "main.go", "package original\n")
	openProjectFile(project, "main.go")
	model.syncActiveProjectFields()
	file := model.activeFile()
	if file == nil || file.document == nil {
		t.Fatal("file has no remote document")
	}
	model.updateActiveContent("package edited\n// 中文🙂\n")
	model.fileContext = pencodeFileContext{project: project, path: "main.go", dialog: "rename", name: "moved.go"}
	if err := fileDialogAndWait(t, model); err != nil {
		t.Fatal(err)
	}
	file = model.activeFile()
	if err := saveFileAndWait(t, model, project.Path, file); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(project.Path, "moved.go"))
	if err != nil || string(data) != file.content() || file.Dirty {
		t.Fatal("moved document was not saved through server", err)
	}
	model.updateActiveContent("package retained\n")
	project.backend.Close()
	if err := saveFileAndWait(t, model, project.Path, file); err == nil {
		t.Fatal("disconnected GUI saved directly to disk")
	}
	data, _ = os.ReadFile(filepath.Join(project.Path, "moved.go"))
	if string(data) == file.content() || !file.Dirty {
		t.Fatal("connection loss overwrote disk or cleared dirty state")
	}
}

func TestPencodeBackendLazyDocumentPreservesDiskConflict(t *testing.T) {
	model, project := resourceTestModel(t)
	resourceTestWrite(t, project, "main.go", "package external\n")
	file := &openFile{Path: "main.go", Name: "main.go", contentSnapshot: snapshotText("package edited\n"), savedContent: snapshotText("package original\n"), Dirty: true}
	if err := saveFileAndWait(t, model, project.Path, file); err == nil {
		t.Fatal("late document activation overwrote an external change")
	}
	data, _ := os.ReadFile(filepath.Join(project.Path, file.Path))
	if string(data) != "package external\n" || !file.Dirty {
		t.Fatal("conflict changed disk or dirty state")
	}
}

func settleQuick(t testing.TB, model *workspaceModel) {
	t.Helper()
	t.Cleanup(model.closeQuickWorker)
	deadline := time.Now().Add(5 * time.Second)
	for {
		model.refreshQuickMatches()
		model.pollQuickMatches()
		if model.quickWorker == nil {
			return
		}
		if model.quickApplied == model.quickRevision {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("remote quick open did not complete")
		}
		time.Sleep(time.Millisecond)
	}
}

func settleHover(t testing.TB, model *workspaceModel) {
	t.Helper()
	model.highlightLanguageHover()
	deadline := time.Now().Add(5 * time.Second)
	for model.hoverApplied != model.hoverRevision {
		model.pollHoverMarkup()
		if time.Now().After(deadline) {
			t.Fatal("hover markup did not complete")
		}
		time.Sleep(time.Millisecond)
	}
}

func settleReferences(t testing.TB, model *workspaceModel) {
	t.Helper()
	t.Cleanup(model.closeReferenceWorker)
	deadline := time.Now().Add(5 * time.Second)
	for model.referenceApplied != model.referenceRevision {
		model.pollReferencePreviews()
		if time.Now().After(deadline) {
			t.Fatal("reference previews did not complete")
		}
		time.Sleep(time.Millisecond)
	}
}

func awaitFileIO(t testing.TB, model *workspaceModel) error {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	jobs := make(map[*fileIOJob]struct{})
	for model.fileIO != nil && len(model.fileIO.pending) > 0 {
		for _, job := range model.fileIO.pending {
			jobs[job] = struct{}{}
		}
		model.pollFileIO()
		if time.Now().After(deadline) {
			t.Fatal("file operation did not complete")
		}
		time.Sleep(time.Millisecond)
	}
	for job := range jobs {
		if job.err != nil && job.err != context.Canceled {
			return job.err
		}
	}
	return nil
}
func fileDialogAndWait(t testing.TB, model *workspaceModel) error {
	t.Helper()
	if err := model.applyFileDialog(); err != nil {
		return err
	}
	return awaitFileIO(t, model)
}
func pasteResourceAndWait(t testing.TB, model *workspaceModel, p *pencodeProject, directory string) error {
	t.Helper()
	if err := model.pasteProjectResource(p, directory); err != nil {
		return err
	}
	return awaitFileIO(t, model)
}
func saveFileAndWait(t testing.TB, model *workspaceModel, root string, file *openFile) error {
	t.Helper()
	var project *pencodeProject
	for _, p := range model.projects {
		if p.Path == root {
			project = p
			break
		}
	}
	job, err := model.requestFileSave(project, file)
	if err != nil {
		return err
	}
	if err := awaitFileIO(t, model); err != nil {
		return err
	}
	return job.err
}
func loadProjectAndWait(t testing.TB, model *workspaceModel, root string) error {
	t.Helper()
	if err := model.loadProject(root); err != nil {
		return err
	}
	t.Cleanup(model.stopFileIO)
	return awaitFileIO(t, model)
}
func openFileAndWait(t testing.TB, model *workspaceModel, relative string) {
	t.Helper()
	model.openFile(relative)
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
}
func openProjectFileAndWait(t testing.TB, model *workspaceModel, p *pencodeProject, relative string) {
	t.Helper()
	model.openProjectFile(p, relative)
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
}
func moveResourceAndWait(t testing.TB, model *workspaceModel, source *pencodeProject, relative string, target *pencodeProject, directory string) error {
	t.Helper()
	if err := model.moveProjectResource(source, relative, target, directory); err != nil {
		return err
	}
	return awaitFileIO(t, model)
}
