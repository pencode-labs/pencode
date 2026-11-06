package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
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
			extension := ".tar.gz"
			if format == ".zip" {
				extension = format
			}
			output := filepath.Join(root, "distribution"+extension)
			if err := writeDistribution(output, artifact, root, "v1.0.0"); err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			if err := copyArtifact(output, func(header *tar.Header, source io.Reader) error {
				seen[header.Name] = true
				content, err := io.ReadAll(source)
				if err != nil {
					return err
				}
				if expected, ok := payloads[header.Name]; ok && string(content) != expected {
					t.Errorf("%s: payload changed", header.Name)
				}
				if header.Name == "pencode/pencode-version" && string(content) != "v1.0.0\n" {
					t.Errorf("release version: %q", content)
				}
				if header.Name == binary && (format != ".app" || runtime.GOOS != "windows") && header.Mode&0o111 == 0 {
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

func TestDistributionFailureKeepsExistingArchive(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "release.tar.gz")
	writeTestFile(t, output, "previous release", 0o644)
	if err := writeDistribution(output, filepath.Join(root, "missing.app"), root, "v1.0.0"); err == nil {
		t.Fatal("missing artifact accepted")
	}
	content, err := os.ReadFile(output)
	if err != nil || string(content) != "previous release" {
		t.Fatalf("previous archive lost: %q, %v", content, err)
	}
	staging, err := filepath.Glob(filepath.Join(root, ".pencode-release-*"))
	if err != nil || len(staging) != 0 {
		t.Fatalf("staging files remain: %v, %v", staging, err)
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
