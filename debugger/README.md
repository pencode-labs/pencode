# Debugger

Pencode implements a DAP client in `dap/`, session state in `debugger/`, and
debugger views in `workbench/`. Adapters and debug terminals run on the workspace
machine through CS v7, including SSH workspaces. The GUI never opens a local
debugger for a remote project.

## Go quick start

Enable the Go extension to detect/install gopls and Delve automatically. Without
the extension, install `dlv` in the workspace server’s PATH. Select **Debug** in the
status bar and click **添加启动器** to open `.pencode/debug.json`. Add a Go launch
configuration using the examples below, save it, and refresh the debug configurations.
Choose the configured package or its tests, set a breakpoint by clicking the
left edge of the gutter or pressing F9, and press F5.

| Action | Shortcut |
| --- | --- |
| Start / continue | F5 |
| Stop / detach | Shift+F5 |
| Toggle breakpoint at caret | F9 |
| Step over | F10 |
| Step in | F11 while paused |
| Step out | Shift+F11 while paused |

Use the continue button to pause a running program. Select a thread or frame to
inspect its scopes. Expand variables on demand, or enter an expression in Debug
Console and press Enter. Program terminals requested by an adapter appear in the
existing Terminal panel; console output and adapter logs appear in Debug Console
and the `Debugger` Output channel.

## Project configuration

Launchers come from the project's `.pencode/debug.json` array. Without configured
launchers, the selector shows **暂无启动器** and starting debug is disabled; no language-specific
defaults are inserted. **添加启动器** opens the file in the editor, creating an empty
`[]` array if needed. Add entries to the array and use **刷新调试配置** after saving.
An empty array is also valid. `configuration` is the adapter's launch/attach
payload, and `${workspaceFolder}` is expanded recursively in its string values.

```json
[
  {
    "label": "Go server",
    "adapter": "Delve",
    "request": "launch",
    "configuration": {
      "mode": "debug",
      "program": "${workspaceFolder}/cmd/server",
      "cwd": "${workspaceFolder}",
      "args": ["--port", "8080"],
      "env": {"APP_MODE": "development"}
    }
  },
  {
    "label": "Go package tests",
    "adapter": "Delve",
    "request": "launch",
    "configuration": {
      "mode": "test",
      "program": "${workspaceFolder}/internal/service",
      "args": ["-test.run", "TestService"]
    }
  }
]
```

`Delve` launches `dlv dap --listen=127.0.0.1:0` and reads the assigned address;
`command` can override the `dlv` executable, and `args` adds adapter arguments.
`stdio` requires `command` and optionally `args` for other DAP adapters.
`tcp` requires an `address` such as `127.0.0.1:5678` and connects from the workspace
machine to an existing adapter. `request` accepts `launch` or `attach`; stopping
an attach session sends `disconnect` with `terminateDebuggee: false`.

Extensions can also contribute named stdio adapters. Set `adapter` to the
contributed ID, such as `Xdebug` from the [PHP extension](../extensions/php/README.md),
and omit `command`/`args` to use its launcher. An explicit `command` overrides the
launcher. Missing providers and duplicate adapter IDs report an error at startup.

The first version supports one active session, source breakpoints, threads,
the first 100 stack frames, lazy variable expansion, expression evaluation, and
integrated terminals launched directly from argv. Breakpoints remain in the
project model when a tab closes and follow edits and undo/redo. They are not yet
persisted across application restarts. Source navigation currently covers files
inside the session's project. The Go extension manages tool installation and
contributes the Delve launcher.
Watch expressions, conditional breakpoint UI, and memory/disassembly views are
not included.

## Validation

```sh
go test ./dap ./debugger ./terminal ./client ./server
PENCODE_TEST_DLV=/path/to/dlv go test ./debugger -run TestDelve
PENCODE_TEST_DLV=/path/to/dlv go test ./workbench -run '^TestDebug'
```

The real-Delve tests exercise the CS boundary, breakpoints, variable values,
stepping, evaluation, and cleanup. They skip when Delve is unavailable.
