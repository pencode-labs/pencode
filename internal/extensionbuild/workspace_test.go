package extensionbuild

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEnvironmentBuildsExtensionWithLocalSDK(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "sdk")
	extension := filepath.Join(root, "extension")
	for path, content := range map[string]string{
		filepath.Join(sdk, "go.mod"):             "module example.com/sdk\n\ngo 1.27.0\n",
		filepath.Join(sdk, "sdk.go"):             "package sdk\nconst Value = 1\n",
		filepath.Join(extension, "go.mod"):       "module example.com/extension\n\ngo 1.27.0\n\nrequire example.com/sdk v0.0.0\n",
		filepath.Join(extension, "extension.go"): "package extension\nimport \"example.com/sdk\"\nconst Value = sdk.Value\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	environment, err := Environment(root, sdk, extension)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "build", "-mod=readonly", ".")
	command.Dir, command.Env = extension, environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build extension with local SDK: %v\n%s", err, output)
	}
}
