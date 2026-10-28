# PHP

PHP and Blade support for Pencode, ported from
[zed-extensions/php](https://github.com/zed-extensions/php), upstream commit
`6768398bea34ea3b2ad590ac8081eb6c1ce6bf29` (version 0.5.4).

The extension registers one language server per workspace, shared by `.php`,
`.phtml`, `.phpt` and `.blade.php` documents. Blade uses the `blade` LSP language
ID. Completion, diagnostics, navigation, references, rename, formatting and code
actions use Pencode's existing LSP client. Syntax highlighting uses Pencode's
built-in PHP grammar and the active editor theme.

## Language servers

Intelephense is enabled by default. Set `PENCODE_PHP_LANGUAGE_SERVER` on the
workspace machine before starting Pencode or its remote server, then reload the
extension to choose another server:

| Value | Installation when missing | Prerequisites |
| --- | --- | --- |
| `intelephense` | npm package `intelephense` | Node.js and npm |
| `phptools` | npm package `devsense-php-ls` and its native platform package | Node.js and npm for installation |
| `phpactor` | `phpactor.phar` from the latest Phpactor GitHub release | PHP compatible with the selected release |
| `phpantom` | Native binary from the latest PHPantom GitHub release | Supported release platform |

Existing `intelephense`, `phptools`, `phpactor` and `phpantom_lsp` executables in
PATH take priority. Managed installations live in
`<UserCacheDir>/pencode/php/<tool>`. Override the cache location with
`PENCODE_PHP_TOOLS_DIR`. Tools are installed on first use, with a ten-minute
deadline; registration never downloads tools. Finished installs are published
atomically and reused without network checks or automatic upgrades. Remove a
tool's cache directory while it is stopped to reinstall it. Tool errors and
installation logs appear in the language server's Output channel.

Optional workspace-host environment variables:

| Variable | Meaning |
| --- | --- |
| `PENCODE_PHP_SERVER_COMMAND` | Custom language server executable, preferably an absolute path; bypasses installation |
| `PENCODE_PHP_SERVER_ARGS` | JSON string array; appended to the managed/default arguments, or used as the complete arguments for a custom command |
| `PENCODE_PHP_CONFIGURATION` | LSP workspace configuration JSON, e.g. `{"intelephense":{"files":{"maxSize":2000000}}}` |
| `PENCODE_PHP_INITIALIZATION_OPTIONS` | LSP initialization options JSON |
| `PENCODE_PHP_XDEBUG_PATH` | Absolute path to an existing `extension/out/phpDebug.js`; bypasses adapter download |

PhpTools and PHPantom native downloads support Linux, macOS and Windows on
amd64/arm64 when the upstream release provides the corresponding asset.
Third-party language server licensing remains governed by each provider.

## Xdebug

The extension contributes the `Xdebug` stdio DAP adapter. Node.js is required on
the workspace machine. On first launch it downloads the latest
`xdebug/vscode-php-debug` VSIX from GitHub, extracts the adapter and its bundled
dependencies, and runs `extension/out/phpDebug.js` through Node.js. Xdebug itself
must be installed and enabled in the PHP runtime separately.

Add this entry to the workspace's `.pencode/debug.json`, refresh the debugger
configuration, and start it before running PHP with Xdebug enabled:

```json
[
  {
    "label": "PHP: listen for Xdebug",
    "adapter": "Xdebug",
    "request": "launch",
    "configuration": {
      "port": 9003,
      "cwd": "${workspaceFolder}"
    }
  }
]
```

Set `pathMappings` in `configuration` when PHP runs in a container or another
machine. The adapter and managed tools always run on the workspace server,
including SSH workspaces. Adapter installation logs use the Debugger Output
channel. An explicit debugger `command`/`args` overrides the contributed launcher.

## Build and install

Requires Go 1.27.0 or newer and the current Pencode peb v10 SDK.

```sh
go build -o bin/php .
go test -race ./...
```

Choose **Install from local directory** in Pencode and select this directory.
On Windows build `bin/php.exe` and change the manifest entry accordingly. The
repository's `make install-extensions` builds and installs this extension along
with the others, adjusting executable suffixes automatically.

## Porting notes and license

This port replaces Zed's Rust/WASM API with Pencode's Go SDK and process
launchers. The server package names, launch arguments, platform asset names,
file suffixes and Xdebug adapter are adapted from upstream. Pencode does not
currently expose extension hooks for Zed's completion label styling, PHPDoc
injections, bracket/indent/outline/text-object queries, tasks or debug schema;
those editor-specific resources are not registered by this port. PHP files
without a recognized suffix cannot be selected by shebang through PEB v10.

Apache-2.0; see [LICENSE](LICENSE). Upstream copyright 2022–2025 Zed Industries,
Inc. The Go implementation and Pencode integration are modifications made in
2026 by Pencode Labs.
