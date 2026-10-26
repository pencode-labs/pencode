package extensions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/output"
	"github.com/pencode-labs/pencode/peb"
)

func TestActionHelperProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "peb-action-helper" {
		return
	}
	fmt.Fprintln(os.Stderr, "extension startup")
	var publisher *peb.ActionPublisher
	action := peb.Action{ID: "run", Text: "Run", Location: peb.StatusBar}
	if slices.Contains(os.Args, "peb-bad-icon") {
		action.Icon = "resources/broken.svg"
	}
	contributions := peb.Contributions{Actions: []peb.Action{action}}
	if slices.Contains(os.Args, "peb-relative-language") {
		contributions.LanguageServers = []peb.LanguageServer{{
			ID: "relative", Label: "Relative", LanguageID: "fixture", Command: "bin/helper",
			Args:      []string{"-test.run=^TestRemoteLanguageHelperProcess$", "--", "pencode-language-helper"},
			Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".fixture"}},
		}}
	}
	err := peb.ServeExtension(os.Stdin, os.Stdout, contributions, peb.Handlers{
		ActivateActions: func(_ context.Context, p *peb.ActionPublisher) {
			publisher = p
			fmt.Fprintln(os.Stderr, "action activated")
			action.Text = "Ready"
			_ = p.Update(action)
		},
		Action: func(c peb.ActionContext) error {
			fmt.Fprintln(os.Stderr, "action clicked")
			action.Text = c.Path
			if err := publisher.Update(action); err != nil {
				return err
			}
			return publisher.Remove(action.ID)
		},
	})
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
func TestActionSessionLiveUpdateClickAndClose(t *testing.T) {
	backend, _ := actionFixture(t)
	var logs output.Store
	bundle, err := Load(output.WithStore(context.Background(), &logs), backend, "test")
	if err != nil {
		t.Fatal(err)
	}
	extension := bundle.Actions
	session := extension.Start(nil)
	defer session.Close()
	receive := func() ActionResult {
		t.Helper()
		select {
		case result, ok := <-session.Results:
			if !ok || result.Err != nil {
				t.Fatalf("session closed or failed: %+v", result)
			}
			return result
		case <-time.After(10 * time.Second):
			t.Fatal("action update timed out")
			return ActionResult{}
		}
	}
	result := receive()
	if result.Change.Action.ID != "extensions/test/run" || result.Change.Action.Text != "Ready" {
		t.Fatal(result)
	}
	if !session.Submit(peb.ActionContext{ID: "run", Path: "/project/main.go"}) {
		t.Fatal("click rejected")
	}
	result = receive()
	if result.Change.Action.Text != "/project/main.go" {
		t.Fatal(result)
	}
	result = receive()
	if !result.Change.Remove || result.Change.Action.ID != "extensions/test/run" {
		t.Fatal(result)
	}
	session.Close()
	select {
	case <-session.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("process was not reaped")
	}
	text, _ := extension.Output.Snapshot()
	for _, line := range []string{"extension startup", "action activated", "action clicked"} {
		if !strings.Contains(text, line) {
			t.Fatalf("missing live stderr %q: %q", line, text)
		}
	}
}
func TestActionResourcesAndValidation(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "resources"), 0755); err != nil {
		t.Fatal(err)
	}
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16"><path fill="currentColor" d="M0 0h16v16H0z"/></svg>`)
	if err := os.WriteFile(filepath.Join(directory, "resources", "run.svg"), svg, 0644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	a := peb.Action{ID: "run", Text: "Run", Icon: "resources/run.svg", Location: peb.TitleBarRight, IconColor: "#ff0000"}
	bundle, err := loadBundle(root.FS(), "test", peb.Contributions{Actions: []peb.Action{a}})
	if err != nil || bundle.Actions == nil || bundle.Actions.Actions[0].Icon != "peb://test/resources/run.svg" || len(bundle.Icons) != 1 {
		t.Fatal(bundle, err)
	}
	for _, modify := range []func(*peb.Action){
		func(a *peb.Action) { a.ID = "../escape" }, func(a *peb.Action) { a.Icon = "../run.svg" },
		func(a *peb.Action) { a.Icon = "run.svg" }, func(a *peb.Action) { a.Color = "red;display:none" },
		func(a *peb.Action) { a.Location = 99 }, func(a *peb.Action) { a.Text = "" },
	} {
		invalid := a
		modify(&invalid)
		if _, err := loadBundle(root.FS(), "test", peb.Contributions{Actions: []peb.Action{invalid}}); err == nil {
			t.Fatalf("accepted %+v", invalid)
		}
	}
	if _, err := loadBundle(root.FS(), "test", peb.Contributions{Actions: []peb.Action{a, a}}); err == nil {
		t.Fatal("accepted duplicate action")
	}
	bundle, err = loadBundle(root.FS(), "test", peb.Contributions{ActionService: true})
	if err != nil || bundle.Actions == nil {
		t.Fatal("empty live provider was lost", err)
	}
}

func TestExtensionRegistrationCapturesOutput(t *testing.T) {
	backend, _ := actionFixture(t)
	var logs output.Store
	_, err := Load(output.WithStore(context.Background(), &logs), backend, "test")
	if err != nil {
		t.Fatal(err)
	}
	text, _ := logs.Channel("Extension: test").Snapshot()
	if !strings.Contains(text, "extension startup") {
		t.Fatalf("missing registration stderr: %q", text)
	}
}

func TestOpenWithActionResourcesAndAssociations(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "resources"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "resources", "preview.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16"><path d="M0 0h16v16H0z"/></svg>`), 0644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	a := peb.Action{ID: "preview", Text: "Preview", Location: peb.OpenWith, Icon: "resources/preview.svg", Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".tsx"}}}
	bundle, err := loadBundle(root.FS(), "test", peb.Contributions{Actions: []peb.Action{a}})
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Actions != nil || len(bundle.Editors) != 1 || bundle.Editors[0].ID != "extensions/test/preview" || bundle.Editors[0].LocalID != "preview" || bundle.Editors[0].Icon != "peb://test/resources/preview.svg" || len(bundle.Icons) != 1 {
		t.Fatalf("incorrect view registration: %+v", bundle)
	}
	for _, rule := range []peb.DocumentSelector{{Match: peb.FolderName, Pattern: "docs"}, {Match: peb.FileSuffix, Pattern: "."}, {Match: peb.FileName, Pattern: ".."}, {Match: peb.FileSuffix, Pattern: "md"}, {Match: peb.FileName, Pattern: "a\x00b"}, {Match: peb.FileName, Pattern: "../README"}} {
		invalid := a
		invalid.Selectors = []peb.DocumentSelector{rule}
		if _, err := loadBundle(root.FS(), "test", peb.Contributions{Actions: []peb.Action{invalid}}); err == nil {
			t.Fatalf("accepted invalid association: %+v", rule)
		}
	}
	a.Selectors = nil
	if _, err := loadBundle(root.FS(), "test", peb.Contributions{Actions: []peb.Action{a}}); err == nil {
		t.Fatal("accepted open-with action with no associations")
	}
}
