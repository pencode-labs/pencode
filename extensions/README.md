# Extension releases

Each extension is an independent repository and publishes its own GitHub Release
when a version tag such as `v0.1.0` is pushed. Workflows test before publishing and
build Linux (`linux`), macOS (`darwin`) and Windows (`windows`), each for `amd64`
and `arm64`. Tags with a prerelease suffix create GitHub prereleases.

The fixed asset format is `<extension-id>-<tag>-<os>-<arch>.zip`, plus one
`SHA256SUMS` per release. ZIPs contain exactly one top-level `<extension-id>/`
directory with `resources/extension.json`, the entry point, README, license and
resource notices. Manifest versions follow the tag without `v`. Go entries live
in `bin/` and use `.exe` on Windows; Office Viewer includes its bundled JavaScript
in `dist/` and `package.json`, with no development dependencies.

Download for the **workspace server's** OS and architecture and extract directly
into `~/.pencode/extensions/` (`%USERPROFILE%/.pencode/extensions/` on Windows).
For example:

```sh
sha256sum --check --ignore-missing SHA256SUMS
unzip docker-v0.1.0-linux-amd64.zip -d ~/.pencode/extensions/
```

This creates `~/.pencode/extensions/docker/resources/extension.json` and
`~/.pencode/extensions/docker/bin/docker`. Preserve executable permissions when
extracting on Linux/macOS. Restart Pencode to discover the extension. You can also
use the extension manager to install the extracted `docker/` directory. Install
on the remote server for a remote workspace.

| Extension | Downloads | Runtime requirements |
| --- | --- | --- |
| Codex Agent | [Releases](https://github.com/pencode-labs/codex/releases) | Codex CLI and its configured account |
| Docker | [Releases](https://github.com/pencode-labs/docker/releases) | Docker CLI and an accessible Docker daemon |
| Go | [Releases](https://github.com/pencode-labs/go/releases) | Go toolchain; gopls and Delve install automatically |
| Markdown Preview | [Releases](https://github.com/pencode-labs/markdown-preview/releases) | No additional runtime |
| Material Icons | [Releases](https://github.com/pencode-labs/material-icons/releases) | No additional runtime |
| Office Viewer | [Releases](https://github.com/pencode-labs/office-viewer/releases) | Node.js 22+ or Bun |
| PHP | [Releases](https://github.com/pencode-labs/php/releases) | Selected language server/debugger prerequisites; see its README |
| UI Demo | [Releases](https://github.com/pencode-labs/ui-demo/releases) | No additional runtime |

Release packages target Pencode's PEB v20 SDK (Pencode `v0.1.0-rc.5`). Go modules
pin the published SDK dependency; Office Viewer's workflow checks out the matching
TypeScript SDK commit before `npm ci`. Its six platform assets use the same
portable bundled JavaScript. Using a downloaded Go extension does not require a
Go toolchain unless the extension itself manages Go tools.
