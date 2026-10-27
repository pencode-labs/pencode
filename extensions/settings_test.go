package extensions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/peb"
)

func TestSettingsHelperProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "peb-settings-helper" {
		return
	}
	var publisher *peb.ActionPublisher
	value := "unset"
	action := peb.Action{ID: "run", Text: "Run", Location: peb.StatusBar}
	err := peb.ServeExtension(os.Stdin, os.Stdout, peb.Contributions{
		Settings: []peb.Setting{{ID: "name", Label: "Name", Default: json.RawMessage(`"default"`)}},
		Actions:  []peb.Action{action, {ID: "source", Text: "Source", Location: peb.OpenWith, Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".txt"}}}},
	}, peb.Handlers{
		Settings: func(values peb.SettingValues) error {
			var err error
			value, err = peb.DecodeSetting[string](values, "name")
			if publisher != nil && err == nil {
				action.Text = value
				err = publisher.Update(action)
			}
			return err
		},
		ActivateActions: func(_ context.Context, p *peb.ActionPublisher) {
			publisher = p
			action.Text = value
			_ = p.Update(action)
		},
		Action: func(peb.ActionContext) error { return nil },
		Editor: func(peb.EditorMessage) (peb.EditorReply, error) { return peb.EditorReply{Content: &value}, nil },
	})
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestRemoteExtensionSettingsInitialAndLiveSessions(t *testing.T) {
	backend, directory := actionFixture(t)
	changeFixtureManifest(t, filepath.Join(directory, "test"), func(m *Manifest) {
		m.Name = "Settings fixture"
		m.Args = []string{"-test.run=^TestSettingsHelperProcess$", "--", "peb-settings-helper"}
	})
	bundle, err := Load(t.Context(), backend, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	if bundle.ID != "test" || bundle.Label != "Settings fixture" || len(bundle.Settings) != 1 {
		t.Fatalf("missing settings metadata: %+v", bundle)
	}
	values := func(name string) peb.SettingValues {
		data, _ := json.Marshal(name)
		return peb.SettingValues{"name": data}
	}
	session := bundle.Actions.Start(values("saved"), nil)
	defer session.Close()
	waitAction := func(want string) {
		t.Helper()
		select {
		case result := <-session.Results:
			if result.Err != nil || result.Change.Action.Text != want {
				t.Fatalf("action settings want %q: %+v", want, result)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("action settings timed out")
		}
	}
	waitAction("saved")
	if !session.SubmitSettings(values("changed")) {
		t.Fatal("live action settings rejected")
	}
	waitAction("changed")

	editor := bundle.Editors[0].Start("", values("editor"), nil)
	defer editor.Close()
	waitEditor := func() string {
		t.Helper()
		if !editor.Submit(peb.EditorMessage{Editor: "source", Event: peb.EditorEvent{Kind: "open"}}) {
			t.Fatal("editor request rejected")
		}
		select {
		case result := <-editor.Results:
			if result.Err != nil || result.Reply.Content == nil {
				t.Fatalf("editor settings: %+v", result)
			}
			return *result.Reply.Content
		case <-time.After(5 * time.Second):
			t.Fatal("editor settings timed out")
			return ""
		}
	}
	if got := waitEditor(); got != "editor" {
		t.Fatalf("initial editor setting = %q", got)
	}
	if !editor.SubmitSettings(values("updated")) {
		t.Fatal("live editor settings rejected")
	}
	for range 10 {
		if waitEditor() == "updated" {
			return
		}
	}
	t.Fatal("editor did not receive live settings")
}

func TestSettingsQueueCoalescesWithoutAllocations(t *testing.T) {
	queue := make(chan peb.SettingValues, 1)
	done := make(chan struct{})
	values := peb.SettingValues{"name": json.RawMessage(`"newest"`)}
	if allocations := testing.AllocsPerRun(100, func() { submitSettings(queue, done, values) }); allocations != 0 {
		t.Fatalf("settings submission allocated %g times", allocations)
	}
	if got := <-queue; string(got["name"]) != `"newest"` {
		t.Fatal(got)
	}
	close(done)
	if submitSettings(queue, done, values) {
		t.Fatal("closed session accepted settings")
	}
}
