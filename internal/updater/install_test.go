package updater

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceInstallationAndRollback(t *testing.T) {
	for _, failure := range []string{"", "replace", "restart"} {
		t.Run(failure, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "installed")
			stage, err := os.MkdirTemp(parent, ".pencode-update-*")
			if err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{root, filepath.Join(stage, "pencode")} {
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "binary"), []byte("old"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stage, "pencode", "binary"), []byte("new"), 0o755); err != nil {
				t.Fatal(err)
			}
			if failure == "replace" {
				if err := os.RemoveAll(filepath.Join(stage, "pencode")); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			err = replaceInstallation(stage, root, func() error {
				calls++
				if failure == "restart" && calls == 1 {
					return errors.New("restart failed")
				}
				return nil
			})
			if (err != nil) != (failure != "") {
				t.Fatalf("failure=%s err=%v", failure, err)
			}
			want := "old"
			if failure == "" {
				want = "new"
			}
			data, readErr := os.ReadFile(filepath.Join(root, "binary"))
			if readErr != nil || string(data) != want {
				t.Fatalf("installation=%q want=%q err=%v", data, want, readErr)
			}
			if calls == 0 {
				t.Fatal("application was not restarted")
			}
			if failure == "restart" && calls != 2 {
				t.Fatal("old application was not restarted after rollback")
			}
		})
	}
}
