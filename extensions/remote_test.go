package extensions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/peb"
)

func changeFixtureManifest(t testing.TB, directory string, change func(*Manifest)) {
	t.Helper()
	name := filepath.Join(directory, "resources", "extension.json")
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	change(&manifest)
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteExtensionInstallEnableDisableAndRevoke(t *testing.T) {
	_, sourceDirectory := actionFixture(t)
	source := filepath.Join(sourceDirectory, "test")
	target := t.TempDir()
	backend := extensionBackend(t, target)
	item, bundle, err := Install(t.Context(), backend, source)
	if err != nil {
		t.Fatal(err)
	}
	if item.Path != filepath.Join(target, "test") || bundle.Actions.remote == nil {
		t.Fatal("installation did not publish a server-owned registration")
	}
	old := bundle.Actions.remote
	session := bundle.Actions.Start(nil)
	defer session.Close()
	select {
	case result := <-session.Results:
		if result.Err != nil || result.Change.Action.Text != "Ready" {
			t.Fatal(result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("remote action did not activate")
	}
	if _, _, err := SetEnabled(t.Context(), backend, "test", false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("disabled extension retained its runtime")
	}
	if _, err := old.OpenProcess(t.Context(), ""); err == nil {
		t.Fatal("disabled registration opened a process")
	}
	if _, err := old.ReadResource(t.Context(), "resources/extension.json", 64<<10); err == nil {
		t.Fatal("disabled registration retained its filesystem")
	}
	catalog, err := backend.ListExtensions(t.Context())
	if err != nil || len(catalog.Items) != 1 || !catalog.Items[0].Disabled || catalog.Directory != target {
		t.Fatal(catalog, err)
	}
	item, bundle, err = SetEnabled(t.Context(), backend, "test", true)
	if err != nil || item.Disabled || bundle.Actions.remote.Handle == old.Handle {
		t.Fatal("reenable did not create a fresh registration", err)
	}
	if err := backend.RemoveExtension(t.Context(), "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal("installation modified the source", err)
	}
	catalog, err = backend.ListExtensions(t.Context())
	if err != nil || len(catalog.Items) != 0 {
		t.Fatal("uninstall did not remove the server package", catalog, err)
	}
}

func TestRemoteExtensionFailedValidationRollsBack(t *testing.T) {
	_, sourceDirectory := actionFixture(t)
	source := filepath.Join(sourceDirectory, "test")
	changeFixtureManifest(t, source, func(m *Manifest) {
		m.Args = append(m.Args[:len(m.Args)-1], "peb-bad-icon", "peb-action-helper")
	})
	if err := os.WriteFile(filepath.Join(source, "resources", "broken.svg"), []byte("broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	backend := extensionBackend(t, target)
	if _, _, err := Install(t.Context(), backend, source); err == nil {
		t.Fatal("published an extension with corrupt resources")
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed installation left a package or staging directory", err)
	}
	changeFixtureManifest(t, source, func(m *Manifest) { m.Disabled = true })
	if _, bundle, err := Install(t.Context(), backend, source); err != nil || bundle != nil {
		t.Fatal("disabled package did not install without activation", err)
	}
	if _, _, err := SetEnabled(t.Context(), backend, "test", true); err == nil {
		t.Fatal("enabled a package with invalid resources")
	}
	catalog, err := backend.ListExtensions(t.Context())
	if err != nil || len(catalog.Items) != 1 || !catalog.Items[0].Disabled {
		t.Fatal("failed enable changed the persisted manifest", catalog, err)
	}
	entries, err = os.ReadDir(target)
	if err != nil || len(entries) != 1 || entries[0].Name() != "test" {
		t.Fatal("failed enable left a pending installation", err)
	}
}

func TestRemoteExtensionResourcesPagingAndConfinement(t *testing.T) {
	backend, directory := actionFixture(t)
	const size = 9 << 20
	names := []string{"resources/first.bin", "resources/second.bin"}
	for _, name := range names {
		f, err := os.Create(filepath.Join(directory, "test", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		err = f.Truncate(size)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
	}
	remote, err := backend.LoadExtension(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}
	resources, err := remote.ReadResources(t.Context(), names, size)
	if err != nil || len(resources) != len(names) {
		t.Fatal("resource paging failed", err)
	}
	for _, name := range names {
		if len(resources[name]) != size {
			t.Fatal("resource was truncated", name)
		}
	}
	if _, err := remote.ReadResource(t.Context(), names[0], size-1); err == nil {
		t.Fatal("cached resource ignored the new size limit")
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "test", "resources", "escaped")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := remote.ReadResource(t.Context(), "resources/escaped", 1024); err == nil {
		t.Fatal("resource read followed a link outside the package")
	}
}

func TestRemoteDisabledExtensionLoadDoesNotRetainHandles(t *testing.T) {
	backend, _ := actionFixture(t)
	if _, err := backend.DisableExtension(t.Context(), "test"); err != nil {
		t.Fatal(err)
	}
	for range 70 {
		remote, err := backend.LoadExtension(t.Context(), "test")
		if err != nil || remote.Handle != 0 || !remote.Info.Disabled {
			t.Fatal("disabled package retained a registration", err)
		}
	}
	if _, _, err := SetEnabled(t.Context(), backend, "test", true); err != nil {
		t.Fatal("disabled loads exhausted registration slots", err)
	}
}

func TestRemoteLanguageHelperProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "pencode-language-helper" {
		return
	}
	directory, err := os.Getwd()
	if err != nil {
		os.Exit(1)
	}
	fmt.Fprint(os.Stdout, directory)
	os.Exit(0)
}

func TestRemoteExtensionRelativeLanguageProcessAfterPublish(t *testing.T) {
	_, sourceDirectory := actionFixture(t)
	source := filepath.Join(sourceDirectory, "test")
	if err := os.Mkdir(filepath.Join(source, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	changeFixtureManifest(t, source, func(m *Manifest) {
		if err := os.Rename(filepath.Join(source, m.Entry), filepath.Join(source, "bin", "helper")); err != nil {
			t.Fatal(err)
		}
		m.Entry = "bin/helper"
		m.Args = append(m.Args[:len(m.Args)-1], "peb-relative-language", "peb-action-helper")
	})
	backend := extensionBackend(t, t.TempDir())
	_, bundle, err := Install(t.Context(), backend, source)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := backend.OpenWorkspace(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	process, err := bundle.LanguageServers[0].remote.OpenLanguageServer(t.Context(), workspace.Path, "relative")
	if err != nil {
		t.Fatal("relative executable did not resolve after staging rename", err)
	}
	defer process.Close()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = io.Copy(io.Discard, process.Stderr()) }()
	data, err := io.ReadAll(process)
	<-done
	if err != nil || string(data) != workspace.Path {
		t.Fatal("language process did not use the server workspace", string(data), err)
	}
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteExtensionUploadConfinementAndDisconnectCleanup(t *testing.T) {
	target := t.TempDir()
	backend := extensionBackend(t, target)
	upload, err := backend.BeginExtension(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := upload.Write(t.Context(), "../escaped", 0, 0o644, true, []byte("bad")); err == nil {
		t.Fatal("upload escaped staging root")
	}
	if err := upload.Write(t.Context(), "resources/file", 0, 0o644, false, []byte("abc")); err != nil {
		t.Fatal(err)
	}
	if err := upload.Write(t.Context(), "resources/file", 1, 0o644, true, []byte("bad")); err == nil {
		t.Fatal("upload accepted an out-of-order chunk")
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		entries, err := os.ReadDir(target)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnected upload retained staging files")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRemoteEditorResourceCacheAndPaths(t *testing.T) {
	backend, directory := actionFixture(t)
	name := filepath.Join(directory, "test", "resources", "preview.svg")
	data := []byte("<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"16\" height=\"16\"><rect width=\"16\" height=\"16\"/></svg>")
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatal(err)
	}
	remote, err := backend.LoadExtension(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}
	resources := viewResources{remote: remote, cache: make(map[string]viewResource)}
	view := peb.EditorView{HTML: "<img src=\"resources/preview.svg\" alt=\"中文 &amp; smile\"><textarea> &lt;🙂&gt;\n</textarea>"}
	assets, err := resources.prepare(t.Context(), &view, "")
	if err != nil || string(assets["peb://test/resources/preview.svg"]) != string(data) || !strings.Contains(view.HTML, "peb://test/resources/preview.svg") {
		t.Fatal("remote image bridge failed", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	// Both relative and canonical URLs reuse the immutable session cache, even
	// after disconnection. No disk or RPC fallback can satisfy these views.
	for _, source := range []string{"resources/preview.svg", "peb://test/resources/preview.svg"} {
		next := peb.EditorView{HTML: "<img src=\"" + source + "\">"}
		if _, err := resources.prepare(context.Background(), &next, ""); err != nil {
			t.Fatal("cached view issued another remote request", err)
		}
	}
	for _, source := range []string{"/tmp/local.svg", "file:///tmp/local.svg", "https://example.com/x.svg", "resources/../private.svg", "peb://other/resources/preview.svg", "resources/preview.svg?x=1"} {
		invalid := peb.EditorView{HTML: "<img src=\"" + source + "\">"}
		if _, err := resources.prepare(context.Background(), &invalid, ""); err == nil {
			t.Fatalf("accepted invalid resource URL %q", source)
		}
	}
}
