package extensions

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/internal/extensionbuild"
	"github.com/pencode-labs/pencode/peb"
)

func TestDockerRegistersTitleBarAndOpensContainerList(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "docker")
	if err := os.MkdirAll(filepath.Join(directory, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(directory, "resources"), os.DirFS("docker/resources")); err != nil {
		t.Fatal(err)
	}
	entry := "bin/docker"
	if runtime.GOOS == "windows" {
		entry += ".exe"
	}
	manifest, err := json.Marshal(Manifest{ID: "docker", Name: "Docker 管理器", Entry: entry})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "resources", "extension.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-p", "1", "-o", filepath.Join(directory, filepath.FromSlash(entry)), ".")
	command.Dir = "docker"
	command.Env, err = extensionbuild.Environment(t.TempDir(), "..", command.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build docker: %v\n%s", err, output)
	}
	bundle, err := Load(ctx, extensionBackend(t, filepath.Dir(directory)), "docker")
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	if bundle.Actions == nil || len(bundle.Actions.Actions) != 1 {
		t.Fatalf("missing Docker title bar action: %+v", bundle.Actions)
	}
	action := bundle.Actions.Actions[0]
	if action.ID != "extensions/docker/containers" || action.Location != peb.TitleBarRight || action.Text != "" || len(bundle.Icons[action.Icon]) == 0 {
		t.Fatalf("expected icon-only Docker title bar action: %+v", action)
	}
	session := bundle.Actions.Start(nil, nil)
	defer session.Close()
	if !session.Submit(peb.ActionContext{ID: "containers", ProjectPath: "/workspace"}) {
		t.Fatal("Docker title bar click rejected")
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case result, ok := <-session.Results:
			if !ok || result.Err != nil {
				t.Fatalf("Docker extension failed: %+v", result)
			}
			if result.UI != nil {
				ui := result.UI
				if ui.ID != "containers" || ui.Kind != peb.Popup || ui.View == nil || ui.Context.ProjectPath != "/workspace" {
					t.Fatalf("unexpected container list surface: %+v", ui)
				}
				return
			}
		case <-deadline.C:
			t.Fatal("Docker container list did not open")
		}
	}
}
