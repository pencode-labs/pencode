package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDistributionPreservesDesktopArtifacts(t *testing.T) {
	for _, format := range []string{".zip", ".tar.gz", ".app"} {
		t.Run(format, func(t *testing.T) {
			workspace := t.TempDir()
			root := filepath.Join(workspace, "pencode")
			writeTestFile(t, filepath.Join(workspace, "onix", "LICENSE"), "Onix license", 0o644)
			for _, name := range []string{"LICENSE", "README.md", "README.zh-CN.md", "resources/icons/CODICONS-LICENSE.txt", "resources/icons/LUCIDE-LICENSE.txt", "workbench/fonts/NERD-FONTS-LICENSE.txt"} {
				writeTestFile(t, filepath.Join(root, name), name, 0o644)
			}
			bundle := filepath.Join(root, "pencode.app")
			binary, catalog, data := "pencode/pencode", "pencode/resources/catalog.bin", "pencode/resources/base.dat"
			if format == ".app" {
				binary = "pencode/pencode.app/Contents/MacOS/pencode"
				catalog = "pencode/pencode.app/Contents/Resources/catalog.bin"
				data = "pencode/pencode.app/Contents/Resources/base.dat"
			}
			payloads := map[string]string{binary: "executable", catalog: "ONIXCAT2", data: "ONIXDAT5"}
			for name, content := range payloads {
				relative := strings.TrimPrefix(name, "pencode/")
				if format == ".app" {
					relative = strings.TrimPrefix(relative, "pencode.app/")
				}
				mode := os.FileMode(0o644)
				if name == binary {
					mode = 0o755
				}
				writeTestFile(t, filepath.Join(bundle, relative), content, mode)
			}
			artifact := bundle
			if format != ".app" {
				artifact = filepath.Join(root, "engine"+format)
				file, err := os.Create(artifact)
				if err != nil {
					t.Fatal(err)
				}
				if format == ".zip" {
					writer := zip.NewWriter(file)
					for name, content := range payloads {
						header := &zip.FileHeader{Name: name, Method: zip.Deflate}
						header.SetMode(0o755)
						entry, err := writer.CreateHeader(header)
						if err != nil {
							t.Fatal(err)
						}
						if _, err := io.WriteString(entry, content); err != nil {
							t.Fatal(err)
						}
					}
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
				} else {
					compressed := gzip.NewWriter(file)
					writer := tar.NewWriter(compressed)
					for name, content := range payloads {
						if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
							t.Fatal(err)
						}
						if _, err := io.WriteString(writer, content); err != nil {
							t.Fatal(err)
						}
					}
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
					if err := compressed.Close(); err != nil {
						t.Fatal(err)
					}
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			output := filepath.Join(root, "distribution")
			if err := stageDistribution(output, artifact, root, "v1.0.0"); err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			if err := filepath.WalkDir(output, func(file string, entry fs.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				rel, err := filepath.Rel(output, file)
				if err != nil {
					return err
				}
				name := filepath.ToSlash(rel)
				seen[name] = true
				content, err := os.ReadFile(file)
				if err != nil {
					return err
				}
				if expected, ok := payloads[name]; ok && string(content) != expected {
					t.Errorf("%s: payload changed", name)
				}
				if name == "pencode/pencode-version" && string(content) != "v1.0.0\n" {
					t.Errorf("release version: %q", content)
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if name == binary && runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
					t.Error("executable mode lost")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{binary, catalog, data, "pencode/pencode-version", "pencode/LICENSE", "pencode/README.md", "pencode/README.zh-CN.md", "pencode/licenses/CODICONS-LICENSE.txt", "pencode/licenses/LUCIDE-LICENSE.txt", "pencode/licenses/NERD-FONTS-LICENSE.txt", "pencode/licenses/ONIX-LICENSE.txt"} {
				if !seen[name] {
					t.Errorf("missing %s", name)
				}
			}
		})
	}
}

func TestDistributionRejectsMissingArtifact(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "stage")
	if err := stageDistribution(output, filepath.Join(root, "missing.app"), root, "v1.0.0"); err == nil {
		t.Fatal("missing artifact accepted")
	}
}

func writeTestFile(t *testing.T, name, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
