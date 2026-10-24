# Pencode

A code editor written in Go, with an Onix-powered desktop interface and a shared backend for local and SSH workspaces.

**English** · [简体中文](README.zh-CN.md)

Pencode brings editing, language tools, a terminal, Git, and native process extensions into one workbench. Its client/server design keeps the interface on your machine while file operations and development tools run where your project lives.

**Status: early development.** Pencode is being extracted from the Onix repository into a standalone project. The source includes the features below and uses Onix's public HTML APIs. Read [development setup](#development-setup) before building.

[Features](#features) · [Development setup](#development-setup) · [Architecture](#architecture) · [Extensions](#extensions) · [Contributing](#contributing)

## Features

| Area | Implemented capabilities |
| --- | --- |
| Editing | Tabs, undo/redo, find and replace, quick file opening, soft wrapping, and a piece-tree text buffer |
| Language tools | Incremental syntax highlighting; LSP completion, diagnostics, navigation, rename, formatting, and code actions, depending on the language server |
| Terminal | Multiple shell sessions, Linux/macOS PTY, Windows ConPTY, Unicode, true color, and scrollback |
| Git | Staging, commits, branch and history views, file comparisons, and a three-way merge editor |
| Remote work | System OpenSSH connections, with file operations, terminals, language servers, and extensions running on the workspace host |
| Customization | System fonts, VS Code JSON/JSONC color themes, file icon themes, and syntax capture mappings |
| Extensions | Executable extensions for custom editors, language servers, fonts, themes, icons, actions, dialogs, menus, and panels |

The extension manager currently installs from local directories; there is no online catalog. Pencode does not run VS Code extensions. Theme compatibility is limited to the fields implemented by Pencode.

## Development setup

### Requirements and current limitations

- Go **1.27.0 or newer**.
- An Onix checkout next to this repository, named `onix`. The current `go.mod` uses `replace github.com/diiyw/onix => ../onix`.
- The native development libraries required by Onix for desktop builds; see `docs/desktop-builds.md` in the Onix checkout.
- System `git` for source control and OpenSSH `ssh` for remote connections. Install language servers separately on the workspace host.
- Fonts installed on the GUI host. Pencode does not bundle fonts.

Expected layout:

```text
workspace/
├── onix/
└── pencode/
```

With Onix in place, clone Pencode beside it and resolve dependencies:

```sh
git clone --recurse-submodules https://github.com/pencode-labs/pencode.git
cd pencode
go mod tidy
```

Use an Onix checkout that provides the public `github.com/diiyw/onix/html/{css,dom,layout,style}` packages. The workbench and its tests use these APIs and can build as an independent module:

```sh
go build ./...
```

To build and reinstall the bundled extensions before starting Pencode, run:

```sh
make run
```

This builds each checked-out extension in its own Go module, using its own `go.mod` and `go.sum` with Go workspaces disabled. Extensions are installed in `~/.pencode/extensions/<extension-id>/` on the local host. After each successful build, the previous installation with that ID is deleted and replaced, including its resources and manifest. Windows executables and installed manifest entries use `.exe`; Linux and macOS use the native executable name. Source manifests are unchanged.

Use `make install-extensions` to install without launching the app, or `make install-extensions EXTENSIONS_DIR="/path/to/extensions"` to stage packages in another directory. Remote workspaces need their extensions installed on the remote host.

The server build helper in `cmd/pencode-build-servers` also still uses the former `./projects/pencode/...` layout and needs migration before use here. This repository is currently a source checkout for development, rather than a ready-to-install desktop release.

### Verify individual packages

From the Pencode repository root:

```sh
go test -p 1 ./protocol ./peb ./lsp ./transport ./terminal
```

These checks cover individual packages, not full application or platform validation. Run `go test -p 1 ./...` to include the workbench and all other packages in the main module. Test the independent extension modules separately:

```sh
git submodule foreach 'go test ./...'
```

## Architecture

```text
Desktop workbench (Onix)
        │
        │ CS v3: local process pipes or SSH stdin/stdout
        ▼
Pencode server
        ├── Files, documents, indexing, and syntax parsing
        ├── Git and terminal processes
        ├── Language servers (LSP)
        └── Extension processes (peb v6)
```

The GUI owns interactive text buffers, undo history, layout, and rendering. The server owns filesystem access, indexing, syntax parsing, Git, PTYs, language servers, and extension processes. Local sessions start a child server; SSH sessions use the same protocol over an encrypted stream without opening a server port.

For remote workspaces, settings and extensions belong to the workspace host; system fonts come from the GUI host. Reconnection preserves unsaved GUI text and checks for external changes. It does not automatically replay saves, Git writes, or terminal input.

| Directory | Responsibility |
| --- | --- |
| `workbench/` | Editor interface, document interaction, themes, and panels |
| `client/`, `protocol/` | Typed client services and CS wire protocol |
| `server/` | Workspace services and process ownership |
| `transport/`, `remote/`, `connection/` | Local transport, SSH, and connection lifecycle |
| `terminal/`, `lsp/`, `peb/` | Terminal emulator, LSP support, and extension protocol/SDK |
| `extensions/` | Extension host integration and example extension submodules |
| `resources/` | Application configuration, scenes, UI assets, and icons |
| `cmd/` | Standalone server and server artifact build helper |

## Extensions

Extensions are executable programs communicating with the server through **peb v6**, a binary protocol over stdin/stdout. Logs go to stderr. They run with the server user's permissions, including on remote workspace hosts.

Extensions live under `~/.pencode/extensions/<extension-id>/` on the server host and include an executable and a `resources/extension.json` manifest. Use the extension manager's local-directory installation flow to register one.

The examples are Git submodules, each maintained in a public MIT-licensed repository under `pencode-labs`:

- [Go language tools](https://github.com/pencode-labs/go): registers `gopls`, which must be installed separately.
- [Markdown editor](https://github.com/pencode-labs/markdown-editor): custom source editing and preview.
- [Material file icons](https://github.com/pencode-labs/material-icons): a file icon theme.
- [Extension UI](https://github.com/pencode-labs/ui-demo): actions, dialogs, menus, popups, and panels.

For an existing checkout, initialize the submodules with `git submodule update --init --recursive`. Each extension has its own Go module and build instructions. For example, build Go language tools from the repository root with:

```sh
(cd extensions/go-lsp && go build -o bin/go-lsp .)
```

The current protocol is v6. Older extensions must be rebuilt with the current SDK.

## Documentation

| Document | Contents | Language |
| --- | --- | --- |
| [CS protocol](protocol/README.md) | Framing, requests, limits, and lifecycle | Chinese |
| [Extension protocol and SDK](peb/README.md) | Contributions, editors, actions, and UI | Chinese |
| [LSP support](lsp/README.md) | Language server implementation and development | Chinese |
| [Historical implementation notes](docs/implementation-notes.zh-CN.md) | Editor behavior, themes, SSH, design decisions, and earlier verification records | Chinese |

The historical notes retain commands from the former Onix monorepo layout. Use this README for the current layout and build limitations. English translations of the technical guides are welcome.

## Contributing

Issues and pull requests are welcome. Useful starting points include completing the standalone-module migration, improving build and platform documentation, fixing editor behavior, extending tests, and translating technical guides.

For bug reports, include your OS and architecture, Go version, Pencode and Onix revisions, reproduction steps, and relevant Output panel diagnostics. For SSH issues, include the remote OS and whether the issue also occurs locally. Remove credentials and private project data from logs.

Keep changes focused, run checks for the packages you touch, and describe what you verified and any limitations. Discuss substantial protocol or architecture changes in an issue before implementing them.

## License and acknowledgments

Pencode is licensed under the [MIT License](LICENSE).

Pencode builds on Onix, gotreesitter, and other Go libraries. Bundled third-party assets retain their own licenses, including [Codicons](resources/icons/CODICONS-LICENSE.txt), [Lucide](resources/icons/LUCIDE-LICENSE.txt), and [Material file icons](extensions/material-icons/resources/LICENSE.txt). Preserve the corresponding notices when redistributing those assets.
