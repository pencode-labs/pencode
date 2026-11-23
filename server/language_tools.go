package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pencode-labs/pencode/configuration"
	"github.com/pencode-labs/pencode/internal/languagetool"
	"github.com/pencode-labs/pencode/protocol"
)

func (s *services) languageTools(ctx context.Context, d *protocol.Decoder, e *protocol.Encoder) error {
	action, id, n := d.String(), d.String(), d.U32()
	if n > uint32(len(languagetool.Tools)) {
		return protocol.ErrProtocol
	}
	paths := make(map[string]string, n)
	for range n {
		key, path := d.String(), d.String()
		if languagetool.Find(key) == nil || len(path) > 32768 {
			return protocol.ErrProtocol
		}
		paths[key] = path
	}
	if err := d.Done(); err != nil {
		return err
	}
	directory := s.configurationDirectory
	if directory == "" {
		var err error
		directory, err = configuration.Directory()
		if err != nil {
			return err
		}
	}
	root := filepath.Join(directory, "language-servers")
	if action != "" || id != "" {
		tool := languagetool.Find(id)
		if tool == nil {
			return protocol.ErrProtocol
		}
		if err := manageLanguageTool(ctx, filepath.Join(root, id), tool, action); err != nil {
			return err
		}
	}
	e.U32(uint32(len(languagetool.Tools)))
	for i := range languagetool.Tools {
		tool := &languagetool.Tools[i]
		info := detectLanguageTool(filepath.Join(root, tool.ID), tool, paths[tool.ID])
		e.String(tool.ID)
		e.String(info.Command)
		e.String(info.Entry)
		e.String(info.Problem)
		if info.Managed {
			e.U32(1)
		} else {
			e.U32(0)
		}
	}
	return nil
}

func languageToolBuild(directory string) string {
	data, err := os.ReadFile(filepath.Join(directory, "current"))
	name := string(data)
	if err != nil || !strings.HasPrefix(name, "build-") || filepath.Base(name) != name {
		return ""
	}
	return filepath.Join(directory, name)
}

func detectLanguageTool(directory string, tool *languagetool.Tool, override string) languagetool.Installation {
	build := languageToolBuild(directory)
	info := languagetool.Installation{Managed: build != ""}
	var err error
	switch {
	case override != "":
		info.Command, info.Entry, err = systemLanguageTool(tool, override)
	case build != "":
		info.Command, info.Entry, err = installedLanguageTool(build, tool)
	default:
		info.Command, info.Entry, err = systemLanguageTool(tool, tool.ID)
		if errors.Is(err, exec.ErrNotFound) && info.Entry == "" {
			return info
		}
	}
	if err != nil {
		info.Command = ""
		info.Problem = err.Error()
	}
	return info
}

func systemLanguageTool(tool *languagetool.Tool, name string) (string, string, error) {
	if !filepath.IsAbs(name) && filepath.Base(name) != name {
		return "", "", fmt.Errorf("expected an absolute executable path: %s", name)
	}
	command, err := exec.LookPath(name)
	if err != nil {
		return "", "", err
	}
	if runtime.GOOS == "windows" && strings.EqualFold(filepath.Ext(command), ".cmd") && tool.Manager == "npm" {
		return installedLanguageTool(filepath.Dir(command), tool)
	}
	return command, "", nil
}

func installedLanguageTool(build string, tool *languagetool.Tool) (command, entry string, err error) {
	switch tool.Manager {
	case "npm":
		pkg := filepath.Join(build, "node_modules", tool.Package)
		data, err := os.ReadFile(filepath.Join(pkg, "package.json"))
		if err != nil {
			return "", "", err
		}
		var manifest struct{ Bin map[string]string }
		if err = json.Unmarshal(data, &manifest); err != nil {
			return "", "", err
		}
		script := manifest.Bin[tool.ID]
		if !filepath.IsLocal(script) {
			return "", "", fmt.Errorf("%s has no executable %s", tool.Package, tool.ID)
		}
		entry, command = filepath.Join(pkg, script), "node"
	case "composer":
		entry, command = filepath.Join(build, "vendor", "bin", tool.ID), "php"
	default:
		command = filepath.Join(build, "bin", tool.ID)
	}
	if entry != "" {
		if stat, err := os.Stat(entry); err != nil || !stat.Mode().IsRegular() {
			return "", "", fmt.Errorf("missing language server: %s", entry)
		}
	}
	command, err = exec.LookPath(command)
	return command, entry, err
}

// Each installation has its own immutable directory. Publish its name only
// after validating the entry point, so a failed update leaves the old one live.
func manageLanguageTool(ctx context.Context, directory string, tool *languagetool.Tool, action string) error {
	marker := filepath.Join(directory, "current")
	old := languageToolBuild(directory)
	if action == "uninstall" {
		if old == "" {
			return nil
		}
		if err := os.Remove(marker); err != nil {
			return err
		}
		return os.RemoveAll(old)
	}
	if action != "install" && action != "update" {
		return protocol.ErrProtocol
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	build, err := os.MkdirTemp(directory, "build-")
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			os.RemoveAll(build)
		}
	}()
	if err = installLanguageTool(ctx, build, tool); err != nil {
		return err
	}
	if _, _, err = installedLanguageTool(build, tool); err != nil {
		return err
	}
	f, err := os.CreateTemp(directory, ".current-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(filepath.Base(build))
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), marker); err != nil {
		return err
	}
	published = true
	if old != "" {
		_ = os.RemoveAll(old)
	}
	return nil
}

func installLanguageTool(ctx context.Context, build string, tool *languagetool.Tool) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	var cmd *exec.Cmd
	switch tool.Manager {
	case "go":
		cmd = exec.CommandContext(ctx, "go", "install", tool.Package+"@latest")
		cmd.Env = append(os.Environ(), "GOBIN="+filepath.Join(build, "bin"), "GOWORK=off")
	case "npm":
		args := []string{"install", "--prefix", build, "--no-audit", "--no-fund", "--no-package-lock", tool.Package + "@latest"}
		if tool.ID == "typescript-language-server" {
			// This server requires the JavaScript tsserver shipped through TypeScript 6.
			args = append(args, "typescript@6")
		}
		if runtime.GOOS == "windows" {
			npm, err := exec.LookPath("npm.cmd")
			if err != nil {
				return err
			}
			cmd = exec.CommandContext(ctx, "node", append([]string{filepath.Join(filepath.Dir(npm), "node_modules", "npm", "bin", "npm-cli.js")}, args...)...)
		} else {
			cmd = exec.CommandContext(ctx, "npm", args...)
		}
	case "composer":
		args := []string{"require", "--no-interaction", "--no-plugins", "--no-scripts", tool.Package}
		if runtime.GOOS == "windows" {
			composer, err := exec.LookPath("composer")
			if err != nil {
				return err
			}
			cmd = exec.CommandContext(ctx, "php", append([]string{filepath.Join(filepath.Dir(composer), "composer.phar")}, args...)...)
		} else {
			cmd = exec.CommandContext(ctx, "composer", args...)
		}
	case "cargo":
		cmd = exec.CommandContext(ctx, "cargo", "install", "--locked", "--root", build, tool.Package)
	default:
		return protocol.ErrProtocol
	}
	cmd.Dir = build
	cmd.WaitDelay = time.Second
	hideServiceWindow(cmd)
	var logs limitedOutput
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Run(); err != nil {
		const errorTail = 4096
		detail := logs.data[max(0, len(logs.data)-errorTail):]
		return fmt.Errorf("%s: %w %s", tool.Manager, err, strings.TrimSpace(string(detail)))
	}
	return nil
}
