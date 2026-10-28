// Server packages, arguments and asset names adapted from zed-extensions/php
// (Apache-2.0); installation and process management rewritten for Pencode.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

type toolCommand struct {
	path string
	args []string
}

type toolManager struct {
	directory string
	client    *http.Client
	log       io.Writer
}

func newTools() (*toolManager, error) {
	directory := os.Getenv("PENCODE_PHP_TOOLS_DIR")
	if directory == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		directory = filepath.Join(cache, "pencode", "php")
	}
	directory, err := filepath.Abs(directory)
	return &toolManager{directory: directory, client: &http.Client{Timeout: 5 * time.Minute}, log: os.Stderr}, err
}

func toolFilename(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func executable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && (runtime.GOOS == "windows" || info.Mode()&0o111 != 0)
}

func phpToolsPath(goos, arch string) (string, error) {
	switch goos {
	case "windows":
		goos = "win32"
	case "linux", "darwin":
	default:
		return "", fmt.Errorf("unsupported PhpTools OS %q", goos)
	}
	switch arch {
	case "amd64":
		arch = "x64"
	case "arm64":
	default:
		return "", fmt.Errorf("unsupported PhpTools architecture %q", arch)
	}
	name := "devsense.php.ls"
	if goos == "win32" {
		name += ".exe"
	}
	return filepath.Join("node_modules", "devsense-php-ls-"+goos+"-"+arch, "dist", name), nil
}

func phpantomAsset(goos, arch string) (string, error) {
	platform, extension := "", ".tar.gz"
	switch goos {
	case "darwin":
		platform = "apple-darwin"
	case "linux":
		platform = "unknown-linux-gnu"
	case "windows":
		platform, extension = "pc-windows-msvc", ".zip"
	default:
		return "", fmt.Errorf("unsupported PHPantom OS %q", goos)
	}
	switch arch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	default:
		return "", fmt.Errorf("unsupported PHPantom architecture %q", arch)
	}
	return "phpantom_lsp-" + arch + "-" + platform + extension, nil
}

// ready checks only local files. Warm launches never query a package registry.
func (t *toolManager) ready(name, directory string) (toolCommand, bool) {
	var command toolCommand
	switch name {
	case "intelephense":
		script := filepath.Join(directory, "node_modules", "intelephense", "lib", "intelephense.js")
		if !regularFile(script) {
			return command, false
		}
		command.path, _ = exec.LookPath("node")
		command.args = []string{script, "--stdio"}
	case "phptools":
		file, err := phpToolsPath(runtime.GOOS, runtime.GOARCH)
		if err != nil {
			return command, false
		}
		command.path = filepath.Join(directory, file)
		command.args = []string{"--composerNodes", "true"}
	case "phpactor":
		phar := filepath.Join(directory, "phpactor.phar")
		if !regularFile(phar) {
			return command, false
		}
		command.path, _ = exec.LookPath("php")
		command.args = []string{phar, "language-server"}
	case "phpantom":
		command.path = filepath.Join(directory, toolFilename("phpantom_lsp"))
	case "xdebug":
		script := os.Getenv("PENCODE_PHP_XDEBUG_PATH")
		if script == "" {
			script = filepath.Join(directory, "extension", "out", "phpDebug.js")
		}
		if !regularFile(script) {
			return command, false
		}
		command.path, _ = exec.LookPath("node")
		command.args = []string{script}
	}
	return command, command.path != "" && executable(command.path)
}

func (t *toolManager) ensure(ctx context.Context, name string) (toolCommand, error) {
	binary, args := "", []string(nil)
	switch name {
	case "intelephense", "phptools":
		binary, args = name, []string{"--stdio"}
	case "phpactor":
		binary, args = name, []string{"language-server"}
	case "phpantom":
		binary = "phpantom_lsp"
	case "xdebug":
	default:
		return toolCommand{}, fmt.Errorf("unknown PHP tool %q", name)
	}
	if binary != "" {
		if path, err := exec.LookPath(binary); err == nil {
			if runtime.GOOS == "windows" && (name == "intelephense" || name == "phptools") {
				// Global npm commands are .cmd shims. Resolve the installed
				// package directly so launching never needs a command shell.
				if command, ok := t.ready(name, filepath.Dir(path)); ok {
					return command, nil
				}
			}
			return toolCommand{path, args}, nil
		}
	}
	destination := filepath.Join(t.directory, name)
	if command, ok := t.ready(name, destination); ok {
		return command, nil
	}
	runtimeCommand := ""
	switch name {
	case "intelephense", "xdebug":
		runtimeCommand = "node"
	case "phpactor":
		runtimeCommand = "php"
	}
	if runtimeCommand != "" {
		if _, err := exec.LookPath(runtimeCommand); err != nil {
			return toolCommand{}, fmt.Errorf("install %s on the workspace machine to run %s: %w", runtimeCommand, name, err)
		}
	}
	if name == "xdebug" && os.Getenv("PENCODE_PHP_XDEBUG_PATH") != "" {
		return toolCommand{}, fmt.Errorf("PENCODE_PHP_XDEBUG_PATH must point to an existing phpDebug.js")
	}
	if err := os.MkdirAll(t.directory, 0o755); err != nil {
		return toolCommand{}, err
	}
	stage, err := os.MkdirTemp(t.directory, ".install-"+name+"-")
	if err != nil {
		return toolCommand{}, err
	}
	defer os.RemoveAll(stage)
	fmt.Fprintf(t.log, "PHP: installing %s…\n", name)
	if err := t.install(ctx, name, stage); err != nil {
		return toolCommand{}, fmt.Errorf("install %s: %w", name, err)
	}
	if _, ok := t.ready(name, stage); !ok {
		return toolCommand{}, fmt.Errorf("installation did not produce a runnable %s", name)
	}
	// A second workspace may finish the same install concurrently. Only publish
	// complete directories; never remove another process's valid installation.
	if err := os.Rename(stage, destination); err != nil {
		if command, ok := t.ready(name, destination); ok {
			return command, nil
		}
		return toolCommand{}, err
	}
	command, _ := t.ready(name, destination)
	fmt.Fprintf(t.log, "PHP: %s ready at %s\n", name, destination)
	return command, nil
}

func (t *toolManager) install(ctx context.Context, name, stage string) error {
	switch name {
	case "intelephense", "phptools":
		pkg := "intelephense"
		if name == "phptools" {
			if _, err := phpToolsPath(runtime.GOOS, runtime.GOARCH); err != nil {
				return err
			}
			pkg = "devsense-php-ls"
		}
		// Invoke npm's JS entry with node, including on Windows where npm.cmd
		// cannot be executed directly without a command shell.
		npm, err := exec.LookPath("npm")
		if err != nil {
			return fmt.Errorf("install Node.js and npm on the workspace machine: %w", err)
		}
		npm, err = filepath.EvalSymlinks(npm)
		if err != nil {
			return err
		}
		if runtime.GOOS == "windows" {
			npm = filepath.Join(filepath.Dir(npm), "node_modules", "npm", "bin", "npm-cli.js")
		}
		cmd := exec.CommandContext(ctx, "node", npm, "install", "--prefix", stage, "--no-audit", "--no-fund", "--no-package-lock", pkg+"@latest")
		hideWindow(cmd)
		cmd.Dir, cmd.WaitDelay = stage, time.Second
		cmd.Stdout, cmd.Stderr = t.log, t.log
		return cmd.Run()
	case "phpactor":
		url, err := t.releaseAsset(ctx, "phpactor/phpactor", "phpactor.phar")
		if err != nil {
			return err
		}
		return t.download(ctx, url, filepath.Join(stage, "phpactor.phar"))
	case "phpantom", "xdebug":
		repo, asset := "xdebug/vscode-php-debug", ""
		if name == "phpantom" {
			repo = "PHPantom-dev/phpantom_lsp"
			var err error
			asset, err = phpantomAsset(runtime.GOOS, runtime.GOARCH)
			if err != nil {
				return err
			}
		}
		url, err := t.releaseAsset(ctx, repo, asset)
		if err != nil {
			return err
		}
		archive := filepath.Join(stage, "download")
		if err := t.download(ctx, url, archive); err != nil {
			return err
		}
		if err := unpack(archive, stage, name, runtime.GOOS); err != nil {
			return err
		}
		return os.Remove(archive)
	}
	return fmt.Errorf("unknown PHP tool %q", name)
}

func (t *toolManager) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "pencode-php")
	response, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, response.Status)
	}
	return response, nil
}

func (t *toolManager) releaseAsset(ctx context.Context, repo, asset string) (string, error) {
	response, err := t.get(ctx, "https://api.github.com/repos/"+repo+"/releases/latest")
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var release struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&release); err != nil {
		return "", err
	}
	if asset == "" {
		version := release.Tag
		if len(version) > 0 && version[0] == 'v' {
			version = version[1:]
		}
		asset = "php-debug-" + version + ".vsix"
	}
	for _, entry := range release.Assets {
		if entry.Name == asset && entry.URL != "" {
			return entry.URL, nil
		}
	}
	return "", fmt.Errorf("%s release has no asset %q", repo, asset)
}

func (t *toolManager) download(ctx context.Context, url, destination string) error {
	response, err := t.get(ctx, url)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	file, err := os.Create(destination)
	if err != nil {
		return err
	}
	n, err := io.Copy(file, io.LimitReader(response.Body, (128<<20)+1))
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if n == 0 || n > 128<<20 {
		return fmt.Errorf("empty or oversized download")
	}
	return closeErr
}
