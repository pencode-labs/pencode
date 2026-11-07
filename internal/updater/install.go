package updater

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type installPlan struct {
	Root       string
	Executable string
	Directory  string
	Installer  string
	Args       []string
	PID        int
}

// Launch starts a helper outside the installation so Windows can release all package files.
func Launch(stage string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	root, _, err := Installation(executable)
	if err != nil {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	plan := installPlan{Root: root, Executable: executable, Directory: dir, Args: os.Args[1:], PID: os.Getpid()}
	name, err := os.ReadFile(filepath.Join(stage, "installer"))
	if err != nil {
		return err
	}
	plan.Installer = string(name)
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "install.json"), data, 0o600); err != nil {
		return err
	}
	helper := filepath.Join(stage, filepath.Base(executable))
	source, err := os.Open(executable)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(helper, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(target, source)
	if err := errors.Join(copyErr, target.Close()); err != nil {
		return err
	}
	cmd := exec.Command(helper, "--pencode-install-update", filepath.Join(stage, "install.json"))
	cmd.Dir = stage
	configureHelper(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// RunHelper runs before graphics, resource loading, or backend startup.
func RunHelper() (bool, error) {
	if len(os.Args) != 3 || os.Args[1] != "--pencode-install-update" {
		return false, nil
	}
	manifest, err := filepath.Abs(os.Args[2])
	if err != nil {
		return true, err
	}
	stage := filepath.Dir(manifest)
	data, err := os.ReadFile(manifest)
	if err != nil {
		return true, err
	}
	var p installPlan
	if err := json.Unmarshal(data, &p); err != nil {
		return true, err
	}
	err = install(stage, p)
	if err != nil {
		_ = os.WriteFile(filepath.Join(stage, "update-error.log"), []byte(err.Error()), 0o600)
	}
	return true, err
}

func install(stage string, p installPlan) error {
	if !filepath.IsAbs(p.Root) || !strings.HasPrefix(filepath.Base(stage), ".pencode-update-") ||
		!filepath.IsLocal(p.Installer) || filepath.Base(p.Installer) != p.Installer ||
		!strings.HasSuffix(p.Installer, installerExtension(runtime.GOOS)) {
		return errors.New("invalid update installation path")
	}
	root, _, err := Installation(p.Executable)
	if err != nil || root != p.Root {
		return errors.New("invalid installation")
	}
	rel, err := filepath.Rel(p.Root, p.Executable)
	if err != nil || !filepath.IsLocal(rel) {
		return errors.New("invalid executable path")
	}
	if err := waitParent(p.PID); err != nil {
		return err
	}
	restart := func() error {
		cmd := exec.Command(p.Executable, p.Args...)
		cmd.Dir = p.Directory
		if cmd.Dir == "" {
			cmd.Dir = p.Root
		}
		if _, err := os.Stat(cmd.Dir); err != nil {
			cmd.Dir = p.Root
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	command, err := installerCommand(runtime.GOOS, filepath.Join(stage, p.Installer), p.Root)
	if err != nil {
		return errors.Join(err, restart())
	}
	// Native installers own registration, elevation and uninstall metadata.
	// Restart the previous installation too if the user cancels the wizard.
	if err := command.Run(); err != nil {
		return errors.Join(fmt.Errorf("run installer: %w", err), restart())
	}
	if err := restart(); err != nil {
		return err
	}
	_ = os.RemoveAll(stage)
	return nil
}

func installerCommand(platform, installer, root string) (*exec.Cmd, error) {
	switch platform {
	case "windows":
		return exec.Command(installer, "/DIR="+root, "/SILENT", "/NORESTART", "/NORESTARTAPPLICATIONS"), nil
	case "darwin":
		return exec.Command("/usr/bin/open", "-W", "-a", "Installer", installer), nil
	case "linux":
		return exec.Command("pkexec", "/usr/bin/apt-get", "install", "--yes", installer), nil
	default:
		return nil, fmt.Errorf("unsupported installer platform %s", platform)
	}
}
