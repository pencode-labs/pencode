# Pencode

A code editor written in Go, with an Onix-powered desktop interface and a shared backend for local, SSH, and WebSocket workspaces.

**English** · [简体中文](README.zh-CN.md)

Pencode brings editing, language tools, a terminal, Git, and native process extensions into one workbench. Its client/server design keeps the interface on your machine while file operations and development tools run where your project lives.

**Status: early development.** Pencode is being extracted from the Onix repository into a standalone project. The source includes the features below and uses Onix's public HTML APIs. Read [development setup](#development-setup) before building.

UI languages: change **Settings → Appearance → Display language** for Chinese, English, Japanese, Korean, German, French or Spanish. Extensions can add JSON language packs; see the [language pack guide](docs/localization.zh-CN.md). Missing translations fall back to English.

[Features](#features) · [Development setup](#development-setup) · [Architecture](#architecture) · [Extensions](#extensions) · [Contributing](#contributing)

## Features

| Area | Implemented capabilities |
| --- | --- |
| Editing | Tabs, undo/redo, find and replace, quick file opening, soft wrapping, and a piece-tree text buffer |
| Language tools | Incremental syntax highlighting; LSP completion, diagnostics, navigation, rename, formatting, and code actions, depending on the language server |
| Terminal | Multiple shell sessions, Linux/macOS PTY, Windows ConPTY, Unicode, true color, and scrollback |
| Debugger | DAP sessions, Go/Delve quick start, breakpoints, stepping, call stacks, variables, and expression evaluation in local and SSH workspaces |
| Git | Staging, commits, branch and history views, file comparisons, and a three-way merge editor |
| Remote work | System OpenSSH or token-authenticated WebSocket connections, with file operations, terminals, language servers, and extensions running on the workspace host |
| Customization | System fonts, VS Code JSON/JSONC color themes, file icon themes, and syntax capture mappings |
| Extensions | Executable extensions for custom editors, language servers, fonts, themes, icons, actions, dialogs, menus, and panels |

The extension manager currently installs from local directories; there is no online catalog. Pencode does not run VS Code extensions. Theme compatibility is limited to the fields implemented by Pencode.

At startup, the editor, standalone server, and extension installer import manual HTTP/HTTPS system proxies from Windows Internet settings (including machine policy), macOS network settings, or Linux GNOME settings. Extensions and tools inherit them through environment variables. Existing `HTTP_PROXY`, `HTTPS_PROXY`, or `ALL_PROXY` variables (including lowercase forms) take precedence, and explicit `NO_PROXY` settings are preserved. Restart the relevant process after changing system proxies. PAC/WPAD scripts are not evaluated; child processes interpret bypass rules according to their own `NO_PROXY` support.

File changes use native FSEvents on macOS, recursive `ReadDirectoryChangesW` on Windows, and inotify on Linux. A unified file service distributes changes to the editor and extensions, with bounded history and explicit resynchronization after overflow.

## Development setup

### Requirements and current limitations

- Go **1.27.0 or newer**.
- An Onix checkout next to this repository, named `onix`. The current `go.mod` uses `replace github.com/onix-engine/onix => ../onix`.
- The native development libraries required by Onix for desktop builds; see `docs/desktop-builds.md` in the Onix checkout.
- System `git` for source control and OpenSSH `ssh` for SSH connections. The Go extension installs its tools automatically; other language servers depend on their extensions.
- Text fonts installed on the GUI host. Pencode bundles Symbols Nerd Font Mono as a fallback for shell prompt icons.
- Image paste on Linux requires `wl-paste` (from `wl-clipboard`) on Wayland or `xclip` on X11. Windows and macOS use native clipboard APIs.

Expected layout:

```text
workspace/
├── onix/
└── pencode/
```

With Onix in place, clone Pencode beside it and resolve dependencies:

```sh
git clone --recurse-submodules git@github.com:pencode-labs/pencode.git
cd pencode
go mod tidy
```

Use an Onix checkout that provides the public `github.com/onix-engine/onix/html/{css,dom,layout,style}` packages. The workbench and its tests use these APIs and can build as an independent module:

```sh
go build ./...
```

To compile and install `pencode-server` and reinstall the bundled extensions before starting Pencode, run:

```sh
make run
```

This builds and installs `pencode-server` into `~/.pencode/pencode-server` (`%USERPROFILE%/.pencode/pencode-server.exe` on Windows). It builds each checked-out extension in its own Go module, using its own `go.mod` and `go.sum` with Go workspaces disabled. Extensions are installed in `~/.pencode/extensions/<extension-id>/` on the local host. After each successful build, the previous installation with that ID is deleted and replaced, including its resources and manifest. Windows executables and installed manifest entries use `.exe`; Linux and macOS use the native executable name. Source manifests are unchanged.

Use `make install-extensions` to install without launching the app, or `make install-extensions EXTENSIONS_DIR="/path/to/extensions"` to stage packages in another directory. Remote workspaces need their extensions installed on the remote host.

Use `make install-server` to compile and install only the local server. For offline SSH bootstrap, run `go run ./cmd/pencode-build-servers` from the repository root to build Linux/macOS amd64 and arm64 servers into `resources/servers/<os>-<arch>/pencode-server`.

Build a native desktop installer for the current host with `make release VERSION=v0.1.0` (default version: `dev`; override the output directory with `DIST_DIR`). This uses Onix's release pipeline to compile module plans and resources into `catalog.bin` and `.dat` packages, then builds with `onix_release`. Outputs are `dist/pencode-<version>-windows-<arch>.exe`, `dist/pencode-<version>-darwin-<arch>.pkg`, and `dist/pencode-<version>-linux-<arch>.deb`, each with a `.sha256` sidecar. Windows uses Inno Setup 7 (`ISCC` can point to the compiler), shows the installation directory page, defaults to `%LOCALAPPDATA%\Programs\Pencode`, and registers shortcuts, an uninstaller and an Open with entry for supported text and source files. File associations are registered for the current user and removed on uninstall; existing default apps are preserved. macOS uses `productbuild` to install `Pencode.app` into `/Applications`. Debian/Ubuntu use `dpkg-deb` to install into `/opt/pencode`, with a `pencode` command and application menu entry. The desktop executable is named `Pencode` (`Pencode.exe` on Windows). All installers include the license, icon notices and both READMEs. Extensions are installed separately; offline SSH server binaries are not included.

Launch with `pencode [--] <file-or-directory> ...` to open local files or folders. Each file opens as an editable, pinned tab using its parent directory as the workspace. Multiple files in the same directory share a workspace. Local paths cannot be combined with remote connection options. You can also drop files or folders onto the launcher or editor; existing unsaved tabs are retained. Dropping local files into a remote session opens a separate local window. Native installers register Pencode in Windows, macOS and Linux Open With menus without changing existing default apps. Finder document events work both when launching Pencode and while it is running.

Pushing a `v*` tag triggers [the release workflow](.github/workflows/release.yml), which builds Linux, Windows and macOS for both amd64 and arm64, verifies checksums, and uploads six native installers, six standalone servers and `SHA256SUMS` to the GitHub Release. Server assets are named `pencode-server-<version>-<os>-<arch>` (with `.exe` on Windows), are built with `CGO_ENABLED=0`, and can be downloaded independently for remote hosts. Set the repository Actions variable `ONIX_REF` to the compatible Onix commit or tag before releasing; it defaults to `main`. Manual workflow runs and release-related changes on `main` build downloadable artifacts without publishing a release. Builds need the native libraries described by Onix; Windows uses MinGW-w64 (LLVM MinGW also supports arm64). macOS bundles receive ad-hoc signatures; Developer ID signing and notarization are not configured.

Settings → About provides a single button that changes from Check for updates to Download update, then Install and restart as the update progresses. Updates resolve GitHub tags to published releases, select the native installer for the current OS/architecture, and verify SHA-256 before staging it in a temporary directory. Restart follows the unsaved-file confirmation flow. After the editor exits, a helper runs the native installer and restarts Pencode; cancellation or installer failure also attempts to restart the installed editor. Windows updates keep the existing installation path. macOS opens Installer; Debian/Ubuntu use `pkexec` and `apt-get` with system authorization. Native installers manage package registration and uninstall metadata. Stable installations skip prereleases. Update detection requires an installed package containing `pencode-version`; existing archive distributions need one manual installation to switch to this flow.

For a direct WebSocket connection, start the server on the remote host:

```sh
pencode-server --listen 0.0.0.0:8080 --token 'your-secret-token'
```

In **Remote management → Add**, select **WebSocket**, enter `ws://server-address:8080/ws` and the same token (the display name is optional). Save, connect from the connection menu, and choose a remote project directory. Profiles and tokens are stored in the local user configuration directory under `pencode/resources/connection.json` with `0600` permissions. The client also supports `wss://` endpoints served by a TLS reverse proxy.

`--listen` requires a nonempty token, checked through the HTTP `Authorization: Bearer` header. Without `--listen`, the server uses stdio. `ws://` sends traffic in plaintext; use a TLS reverse proxy with `wss://` across public networks.

### Verify individual packages

From the Pencode repository root:

```sh
go test -p 1 ./protocol ./peb ./lsp ./transport ./terminal
```

These checks cover individual packages, not full application or platform validation. Run `go test -p 1 ./...` to include the workbench and all other packages in the main module. Test the independent extension modules separately:

```sh
git submodule foreach 'go test ./...'
```

Workbench rendering tests use resources in `workbench/testdata/` and do not require
the Codex submodule. To also build and run the actual Codex extension in the host:

```sh
git submodule update --init extensions/codex
go test -p 1 -tags codex_integration ./workbench
```

## Architecture

```text
Desktop workbench (Onix)
        │
        │ CS v9: local process pipes, SSH stdin/stdout, or WebSocket
        ▼
Pencode server
        ├── Files, documents, indexing, and syntax parsing
        ├── Git and terminal processes
        ├── Language servers (LSP)
        └── Extension processes (peb v13)
```

The GUI owns interactive text buffers, undo history, layout, and rendering. The server owns filesystem access, indexing, syntax parsing, Git, PTYs, language servers, and extension processes. Local sessions start a child server; SSH sessions use the same protocol over an encrypted stream without opening a server port. WebSocket sessions use the same protocol at the token-authenticated `/ws` endpoint.

For remote workspaces, settings and extensions belong to the workspace host; system fonts come from the GUI host. Reconnection preserves unsaved GUI text and checks for external changes. It does not automatically replay saves, Git writes, or terminal input.

| Directory | Responsibility |
| --- | --- |
| `workbench/` | Editor interface, document interaction, themes, and panels |
| `client/`, `protocol/` | Typed client services and CS wire protocol |
| `server/` | Workspace services and process ownership |
| `transport/`, `remote/`, `connection/` | Local transport, SSH, WebSocket, and connection lifecycle |
| `terminal/`, `lsp/`, `peb/` | Terminal emulator, LSP support, and extension protocol/SDK |
| `extensions/` | Extension host integration and example extension submodules |
| `resources/` | Application configuration, scenes, UI assets, and icons |
| `cmd/` | Standalone server and server artifact build helper |

## Extensions

Extensions are native executables or JavaScript bundles compiled from TypeScript, communicating with the server through **peb v18**, a binary protocol over stdin/stdout. Logs go to stderr. They run with the server user's permissions, including on remote workspace hosts.

Extensions live under `~/.pencode/extensions/<extension-id>/` on the server host and include an executable and a `resources/extension.json` manifest. Use the extension manager's local-directory installation flow to register one.

Bundled extensions include the following; see each extension for its license and build instructions:

- [Codex Agent](https://github.com/pencode-labs/codex): editor chat, streaming replies, approvals, and project conversations through the Codex CLI.
- [Docker](https://github.com/pencode-labs/docker): container lists, start/stop/restart controls, and automatically refreshed logs from the title bar.
- [Go language tools](https://github.com/pencode-labs/go): provides Go language and debug support, automatically installing `gopls` and Delve when missing.
- [Office Viewer](extensions/office-viewer/README.md): a TypeScript extension for local read-only Word `.docx`, Excel `.xlsx`, and PowerPoint `.pptx` previews.
- [Markdown preview](https://github.com/pencode-labs/markdown-preview): read-only preview with native source editing and optional split layout.
- [Material file icons](https://github.com/pencode-labs/material-icons): a file icon theme.
- [PHP](https://github.com/pencode-labs/php): PHP/Blade language servers with automatic installation and an Xdebug adapter, ported from the Zed PHP extension.
- [Extension UI](https://github.com/pencode-labs/ui-demo): actions, dialogs, menus, popups, and panels.

For an existing checkout, initialize the submodules with `git submodule update --init --recursive`. Go extensions have independent Go modules; TypeScript extensions have independent npm packages. Each directory includes build instructions. For example, build Go language tools from the repository root with:

```sh
(cd extensions/go && go build -o bin/go .)
```

The current extension protocol is peb v18 (CS v11). Older extensions must be rebuilt with the current SDK.

The workspace host recognizes `.js`, `.mjs`, and `.cjs` entries and launches them with Node.js from its PATH, falling back to Bun. Runtime loading never invokes a package manager; compile TypeScript and bundle dependencies first. See the [TypeScript SDK](peb/ts/README.md). `make install-extensions` uses npm (or Bun) with the lockfile, runs the package build, and installs its output directory, resources and package.json. The package metadata preserves ESM/CJS behavior for `.js`; JavaScript entries keep the same name on every platform.

## Documentation

| Document | Contents | Language |
| --- | --- | --- |
| [CS protocol](protocol/README.md) | Framing, requests, limits, and lifecycle | Chinese |
| [Extension protocol and SDK](peb/README.md) | Contributions, editors, actions, and UI | Chinese |
| [LSP support](lsp/README.md) | Language server implementation and development | Chinese |
| [Debugger](debugger/README.md) | Go quick start, debug configurations, and architecture | English |
| [Historical implementation notes](docs/implementation-notes.zh-CN.md) | Editor behavior, themes, SSH, design decisions, and earlier verification records | Chinese |

The historical notes retain commands from the former Onix monorepo layout. Use this README for the current layout and build limitations. English translations of the technical guides are welcome.

## Contributing

Issues and pull requests are welcome. Useful starting points include completing the standalone-module migration, improving build and platform documentation, fixing editor behavior, extending tests, and translating technical guides.

For bug reports, include your OS and architecture, Go version, Pencode and Onix revisions, reproduction steps, and relevant Output panel diagnostics. For SSH issues, include the remote OS and whether the issue also occurs locally. Remove credentials and private project data from logs.

Keep changes focused, run checks for the packages you touch, and describe what you verified and any limitations. Discuss substantial protocol or architecture changes in an issue before implementing them.

## License and acknowledgments

Pencode is licensed under the [MIT License](LICENSE).

Pencode builds on Onix, [textmate-go](https://github.com/eugenioenko/textmate-go), and other Go libraries. TextMate grammar notices are embedded in Onix’s syntax resources. Bundled third-party assets retain their own licenses, including [Codicons](resources/icons/CODICONS-LICENSE.txt), [Lucide](resources/icons/LUCIDE-LICENSE.txt), [Symbols Nerd Font Mono](workbench/fonts/NERD-FONTS-LICENSE.txt), and [Material file icons](extensions/material-icons/resources/LICENSE.txt). Preserve the corresponding notices when redistributing those assets.
