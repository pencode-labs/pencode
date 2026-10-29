// Adapted from zed-extensions/php (Apache-2.0) for Pencode's PEB protocol.
// Tool installation runs only in launchers, never during registration.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pencode-labs/pencode/peb"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) >= 3 && os.Args[1] == "--tool" {
		if err := ownProcesses(); err != nil {
			return err
		}
		tools, err := newTools()
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		command, err := tools.ensure(ctx, os.Args[2])
		cancel()
		if err != nil {
			return err
		}
		return launch(command.path, append(command.args, os.Args[3:]...))
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	contributions, err := phpContributions(filepath.Base(self))
	if err != nil {
		return err
	}
	return peb.ServeExtension(os.Stdin, os.Stdout, contributions, peb.Handlers{})
}

func phpContributions(executable string) (peb.Contributions, error) {
	server := os.Getenv("PENCODE_PHP_LANGUAGE_SERVER")
	if server == "" {
		server = "intelephense"
	}
	label := ""
	switch server {
	case "intelephense":
		label = "Intelephense"
	case "phptools":
		label = "PhpTools"
	case "phpactor":
		label = "Phpactor"
	case "phpantom":
		label = "PHPantom"
	default:
		return peb.Contributions{}, fmt.Errorf("unknown PHP language server %q", server)
	}
	command := "bin/" + executable
	ls := peb.LanguageServer{
		ID: server, Label: "PHP (" + label + ")", LanguageID: "php",
		Command: command, Args: []string{"--tool", server},
		// Match Blade first, since .blade.php also ends with .php.
		Selectors: []peb.DocumentSelector{
			{Match: peb.FileSuffix, Pattern: ".blade.php", LanguageID: "blade"},
			{Match: peb.FileSuffix, Pattern: ".php"},
			{Match: peb.FileSuffix, Pattern: ".phtml"},
			{Match: peb.FileSuffix, Pattern: ".phpt"},
		},
		Configuration:         os.Getenv("PENCODE_PHP_CONFIGURATION"),
		InitializationOptions: os.Getenv("PENCODE_PHP_INITIALIZATION_OPTIONS"),
	}
	if override := os.Getenv("PENCODE_PHP_SERVER_COMMAND"); override != "" {
		ls.Command, ls.Args = override, nil
	}
	if args := os.Getenv("PENCODE_PHP_SERVER_ARGS"); args != "" {
		var extra []string
		if err := json.Unmarshal([]byte(args), &extra); err != nil {
			return peb.Contributions{}, fmt.Errorf("PENCODE_PHP_SERVER_ARGS must be a JSON string array: %w", err)
		}
		ls.Args = append(ls.Args, extra...)
	}
	for _, options := range []string{ls.Configuration, ls.InitializationOptions} {
		if options != "" && !json.Valid([]byte(options)) {
			return peb.Contributions{}, fmt.Errorf("PHP configuration and initialization options must be valid JSON")
		}
	}
	return peb.Contributions{
		LanguageServers: []peb.LanguageServer{ls},
		DebugAdapters:   []peb.DebugAdapter{{ID: "Xdebug", Label: "PHP (Xdebug)", Command: command, Args: []string{"--tool", "xdebug"}, Templates: `[{"label":"PHP: 当前文件","adapter":"Xdebug","request":"launch","configuration":{"program":"${file}","cwd":"${fileDirname}","hostname":"127.0.0.1","port":0,"stopOnEntry":true,"runtimeArgs":["-dxdebug.start_with_request=yes"],"env":{"XDEBUG_MODE":"debug","XDEBUG_CONFIG":"client_host=127.0.0.1 client_port=${port}"}}},{"label":"PHP: 监听 Xdebug","adapter":"Xdebug","request":"launch","configuration":{"port":9003,"cwd":"${workspaceFolder}"}}]`}},
	}, nil
}
