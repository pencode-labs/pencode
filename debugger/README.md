# Debugger

Pencode implements a DAP client in `dap/`, session state in `debugger/`, and
debugger views in `workbench/`. Adapters and debug terminals run on the workspace
machine through CS, including SSH workspaces. The GUI never opens a local
debugger for a remote project.

## Go quick start

Install Delve in **Settings → Debuggers (DAP) → Go** or put `dlv` on the workspace host’s PATH. Add a Delve configuration to
`.pencode/debug.json` using the example below. Save any changes to refresh the debug configurations automatically.
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

## Adapter settings

Open **Settings → Debuggers (DAP)** on the launcher or in the workspace.

| Language | Adapter | Workspace host setup |
| --- | --- | --- |
| Go | Delve | Install from Settings with Go, or put `dlv` on PATH. |
| PHP | Xdebug | Install from Settings with Node.js and enable Xdebug in the target PHP runtime. |
| Python | debugpy | Install from Settings with Python 3 and pip, or put `debugpy-adapter` on PATH. |
| Dart CLI | Dart Debug Adapter | Put the Dart SDK's `dart` executable on PATH. The adapter runs as `dart debug_adapter`. |
| C# (.NET) | NetCoreDbg | Install `netcoredbg` and the .NET runtime on the workspace host. |
| C, C++, Objective-C, Objective-C++, Rust, Zig, Swift | LLDB DAP | Put `lldb-dap` on PATH or set its executable path in Settings. For Swift, use the adapter from the matching Swift toolchain. |

Expand the adapter row to set its executable path. The PHP adapter path points to `phpDebug.js`.
A project launcher's explicit `command` takes priority, followed by the path in
Settings, then the managed copy. With none configured, Pencode uses the
registered extension launcher or detects the adapter on the workspace host.
For Dart and NetCoreDbg, setting the executable path in Settings supplies the
built-in adapter arguments automatically. A project that specifies its own
`command` also supplies its `args`, such as `["debug_adapter"]` for `dart`.

## Project configuration

Launchers come from the project's `.pencode/debug.json` array. Without configured
launchers, the selector shows **暂无启动器** and starting debug is disabled; no language-specific
defaults are inserted. **添加启动器** offers Go project and test launchers, a PHP file and an Xdebug listener, Python and Dart files, a .NET assembly, a native executable, and remote attach templates for Go and generic DAP servers. Loaded extensions can contribute additional templates.
Selecting one appends its configuration to the JSON file and opens it for editing,
without starting a debug session. Existing entries and workspace placeholders are
preserved; save unsaved config edits before adding another template. **手动编辑配置…**
opens the file directly, creating an empty `[]` array if needed. Saving refreshes the launcher list;
use **刷新调试配置** for changes made outside the editor.
An empty array is also valid. `configuration` is the adapter's launch/attach
payload, and `${workspaceFolder}` is expanded recursively in its string values.
`${file}` and `${fileDirname}` resolve to the selected editor file on the workspace
machine when debugging starts; launchers using them require an open file.

The Python template runs the selected file. Set `configuration.python` to the project interpreter if the program uses a virtual environment. For LLDB, build the executable with debug information and set `configuration.program` to its path on the workspace host. [debugpy settings](https://github.com/microsoft/debugpy/wiki/Debug-configuration-settings) · [LLDB setup](https://lldb.llvm.org/use/lldbdap.html)

The Dart template runs the selected CLI file from the workspace root. For C#, run `dotnet build -c Debug` and set `configuration.program` to the resulting application DLL. Keep the matching PDB alongside it for source breakpoints. [Dart SDK debugger](https://github.com/dart-lang/sdk/blob/main/third_party/pkg/dap/tool/README.md) · [NetCoreDbg](https://github.com/Samsung/netcoredbg)

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

For a locally launched adapter, `Delve` runs `dlv dap --listen=127.0.0.1:0` and reads the assigned address.
`command` can override the `dlv` executable, and `args` adds adapter arguments.
`stdio` requires `command` and optionally accepts `args` for other DAP adapters.

Extensions register optional JSON templates with `peb.DebugAdapter.Templates`.

Extensions can also contribute named stdio adapters. Set `adapter` to the
contributed ID
and omit `command`/`args` to use its launcher. An explicit `command` overrides the
launcher. Missing providers and duplicate adapter IDs report an error at startup.

### Remote debugging

Choose **添加启动器 → Go: 远程附加** to attach to a Go program already running under headless Delve. For example, if Delve is listening on port 2345 on the workspace host, use this `.pencode/debug.json` and press F5.

```json
[
  {
    "label": "Go: 远程附加",
    "adapter": "Delve",
    "request": "attach",
    "host": "127.0.0.1",
    "port": 2345,
    "configuration": {
      "mode": "remote"
    }
  }
]
```

The workspace host opens the TCP connection. In an SSH workspace, `127.0.0.1` refers to the SSH host. Set `host` to a hostname, IPv4 address, or unbracketed IPv6 address, or omit it to use `127.0.0.1`. Set `port` to an integer from 1 to 65535. You can also specify a single `address` such as `[::1]:2345` in place of both fields.

Use **DAP: 远程附加** for another TCP adapter. Set `adapter` to its ID if it expects a specific DAP `adapterID`. Put the adapter's launch or attach arguments in `configuration`, including any source path mappings. For **PHP: 监听 Xdebug**, the adapter runs on the workspace host and listens for the PHP runtime on `configuration.port` (default template value 9003). Stopping an attach session sends `disconnect` with `terminateDebuggee: false`.

## Session features

The first version supports one active session, source breakpoints, threads,
the first 100 stack frames, lazy variable expansion, expression evaluation, and
integrated terminals launched directly from argv. Breakpoints remain in the
project model when a tab closes and follow edits and undo/redo. They are not yet
persisted across application restarts. Source navigation currently covers files
inside the session's project.
Watch expressions, conditional breakpoint UI, and memory/disassembly views are
not included.

## Validation

```sh
go test ./dap ./debugger ./terminal ./client ./server
PENCODE_TEST_DLV=/path/to/dlv go test ./debugger -run TestDelve
PENCODE_TEST_DLV=/path/to/dlv go test ./workbench -run '^TestDebug'
PENCODE_TEST_PHP_DEBUG_JS=/path/to/phpDebug.js PENCODE_TEST_PHP_XDEBUG=/path/to/xdebug.so go test ./debugger -run TestPHPWithBuiltinXdebug
go test ./debugger -run TestBuiltinStdioDebuggers
```

The real-Delve tests exercise the CS boundary, breakpoints, variable values,
stepping, evaluation, and cleanup. They skip when Delve is unavailable.
The stdio adapter test runs debugpy, LLDB, Dart and NetCoreDbg when available. LLDB's test uses `clang` and NetCoreDbg's test uses the .NET SDK. Set `PENCODE_TEST_INSTALL_DEBUGPY=1` to test pip installation and launch in a temporary directory.
