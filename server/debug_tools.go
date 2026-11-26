package server

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pencode-labs/pencode/internal/languagetool"
)

const phpDebugScript = "extension/out/phpDebug.js"

func defaultLanguageTool(ctx context.Context, tool *languagetool.Tool) (string, string, error) {
	switch tool.ID {
	case languagetool.Debugpy:
		return systemLanguageTool(tool, "debugpy-adapter")
	case languagetool.LLDB, "clangd", "sourcekit-lsp":
		command, err := discoverLLVMTool(ctx, tool.ID)
		return command, "", err
	case "rust-analyzer":
		command, err := rustAnalyzerCommand(ctx)
		return command, "", err
	case languagetool.Delve:
		command, err := exec.LookPath("dlv")
		if err == nil || !errors.Is(err, exec.ErrNotFound) {
			return command, "", err
		}
		bin := workspaceToolCache("PENCODE_GO_TOOLS_DIR", "go/bin")
		if bin != "" {
			if cached, err := exec.LookPath(filepath.Join(bin, "dlv")); err == nil {
				return cached, "", nil
			}
		}
		return "", "", err
	case languagetool.Xdebug:
		if script := os.Getenv("PENCODE_PHP_XDEBUG_PATH"); script != "" {
			return xdebugCommand(script)
		}
		cache := workspaceToolCache("PENCODE_PHP_TOOLS_DIR", "php")
		if cache != "" {
			command, entry, err := xdebugCommand(filepath.Join(cache, "xdebug", phpDebugScript))
			if !errors.Is(err, fs.ErrNotExist) {
				return command, entry, err
			}
		}
		return "", "", exec.ErrNotFound
	default:
		if tool.Manager == "" {
			return systemLanguageTool(tool, tool.Package)
		}
		return systemLanguageTool(tool, tool.ID)
	}
}

func debugpyPython() string {
	if runtime.GOOS == "windows" {
		return "python.exe"
	}
	return "python3"
}

func installedDebugpy(build string) (string, string, error) {
	entry := filepath.Join(build, "debugpy", "adapter")
	if info, err := os.Stat(filepath.Join(entry, "__main__.py")); err != nil || !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("missing debugpy adapter: %s", entry)
	}
	command, err := exec.LookPath(debugpyPython())
	return command, entry, err
}

func workspaceToolCache(env, subdir string) string {
	if directory := os.Getenv(env); directory != "" {
		directory, _ = filepath.Abs(directory)
		return directory
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cache, "pencode", subdir)
}

func xdebugCommand(script string) (string, string, error) {
	if !filepath.IsAbs(script) {
		return "", "", fmt.Errorf("expected an absolute path to phpDebug.js: %s", script)
	}
	info, err := os.Stat(script)
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", "", fmt.Errorf("expected a nonempty adapter script: %s", script)
	}
	node, err := exec.LookPath("node")
	return node, script, err
}

func getDebugAsset(ctx context.Context, address string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", address, resp.Status)
	}
	return resp.Body, nil
}

func installXdebug(ctx context.Context, build string) error {
	if _, err := exec.LookPath("node"); err != nil {
		return fmt.Errorf("Xdebug adapter requires Node.js: %w", err)
	}
	body, err := getDebugAsset(ctx, "https://api.github.com/repos/xdebug/vscode-php-debug/releases/latest")
	if err != nil {
		return err
	}
	var release struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string
			URL  string `json:"browser_download_url"`
		}
	}
	err = json.NewDecoder(io.LimitReader(body, 2<<20)).Decode(&release)
	body.Close()
	if err != nil {
		return err
	}
	name := "php-debug-" + strings.TrimPrefix(release.Tag, "v") + ".vsix"
	for _, asset := range release.Assets {
		if asset.Name == name && asset.URL != "" {
			return installDebugVSIX(ctx, build, asset.URL)
		}
	}
	return fmt.Errorf("Xdebug release has no asset %s", name)
}

func installDebugVSIX(ctx context.Context, build, address string) error {
	body, err := getDebugAsset(ctx, address)
	if err != nil {
		return err
	}
	defer body.Close()
	archive, err := os.CreateTemp(build, "*.vsix")
	if err != nil {
		return err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	const maxSize = 128 << 20
	size, err := io.Copy(archive, io.LimitReader(body, maxSize+1))
	if err != nil {
		return err
	}
	if size > maxSize {
		return fmt.Errorf("VSIX exceeds 128 MiB")
	}
	reader, err := zip.NewReader(archive, size)
	if err != nil {
		return err
	}
	if len(reader.File) > 32768 {
		return fmt.Errorf("too many files in VSIX")
	}
	var remaining uint64 = 256 << 20
	for _, f := range reader.File {
		if !fs.ValidPath(strings.TrimSuffix(f.Name, "/")) || strings.ContainsAny(f.Name, "\\:") ||
			(!f.Mode().IsRegular() && !f.Mode().IsDir()) || f.UncompressedSize64 > remaining {
			return fmt.Errorf("invalid or oversized VSIX entry %q", f.Name)
		}
		remaining -= f.UncompressedSize64
	}
	files, err := fs.Sub(reader, "extension")
	if err != nil {
		return err
	}
	return os.CopyFS(filepath.Join(build, "extension"), files)
}
