package updater

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type installPlan struct {
	Root       string
	Executable string
	Directory  string
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
	if !filepath.IsAbs(p.Root) || filepath.Dir(stage) != filepath.Dir(p.Root) || !strings.HasPrefix(filepath.Base(stage), ".pencode-update-") {
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
	return replaceInstallation(stage, p.Root, restart)
}

func replaceInstallation(stage, root string, restart func() error) error {
	backup := filepath.Join(stage, "previous")
	// Antivirus and child processes can briefly retain Windows file handles.
	for attempt := 0; ; attempt++ {
		err := os.Rename(root, backup)
		if err == nil {
			break
		}
		if attempt == 119 {
			return errors.Join(fmt.Errorf("replace installation: %w", err), restart())
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err := os.Rename(filepath.Join(stage, "pencode"), root); err != nil {
		rollback := os.Rename(backup, root)
		if rollback == nil {
			_ = restart()
		}
		return errors.Join(err, rollback)
	}
	if err := restart(); err != nil {
		moveErr := os.Rename(root, filepath.Join(stage, "failed"))
		if moveErr != nil {
			return errors.Join(err, moveErr)
		}
		rollback := os.Rename(backup, root)
		if rollback == nil {
			_ = restart()
		}
		return errors.Join(err, rollback)
	}
	if err := os.RemoveAll(backup); err != nil {
		return err
	}
	// On Windows the running helper itself is released when this process exits.
	_ = os.RemoveAll(stage)
	return nil
}
