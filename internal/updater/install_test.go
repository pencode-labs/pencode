package updater

import (
	"reflect"
	"testing"
)

func TestNativeInstallerCommands(t *testing.T) {
	for _, platform := range []string{"windows", "darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			installer, root := "/tmp/installer with spaces"+installerExtension(platform), "/custom/install directory"
			command, err := installerCommand(platform, installer, root)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			switch platform {
			case "windows":
				want = []string{installer, "/DIR=" + root, "/SILENT", "/NORESTART", "/NORESTARTAPPLICATIONS"}
			case "darwin":
				want = []string{"/usr/bin/open", "-W", "-a", "Installer", installer}
			case "linux":
				want = []string{"pkexec", "/usr/bin/apt-get", "install", "--yes", installer}
			}
			if !reflect.DeepEqual(command.Args, want) {
				t.Fatalf("command = %q, want %q", command.Args, want)
			}
		})
	}
	if _, err := installerCommand("unsupported", "package", "root"); err == nil {
		t.Fatal("unsupported platform accepted")
	}
}
