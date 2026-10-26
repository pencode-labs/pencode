# Pencode

使用 Go 编写的代码编辑器，基于 Onix 构建桌面界面，通过统一后端支持本地与 SSH 远程工作区。

[English](README.md) · **简体中文**

Pencode 将代码编辑、语言工具、终端、Git 和原生进程扩展整合到同一个工作台。客户端／服务端架构让界面留在本机，文件操作和开发工具则在项目所在的设备上执行。

**项目状态：早期开发。** Pencode 正从 Onix 仓库迁移为独立项目。源码已包含下述功能，并使用 Onix 公开的 HTML API。构建前请先阅读[开发环境](#开发环境)。

[功能](#功能) · [开发环境](#开发环境) · [架构](#架构) · [扩展](#扩展) · [参与贡献](#参与贡献)

## 功能

| 领域 | 已实现能力 |
| --- | --- |
| 代码编辑 | 标签页、撤销／重做、查找替换、快速打开、软换行和 piece tree 文本缓冲 |
| 语言工具 | 增量语法高亮，以及 LSP 补全、诊断、导航、重命名、格式化和代码操作，具体取决于语言服务器能力 |
| 内置终端 | 多 shell 会话，Linux/macOS PTY、Windows ConPTY，Unicode、真彩色和滚屏历史 |
| 调试 | DAP 会话、Go／Delve 快速启动、断点、单步、调用栈、变量和表达式求值，支持本地与 SSH 工作区 |
| Git | 暂存、提交、分支与历史视图、文件比较和三方合并编辑器 |
| 远程开发 | 使用系统 OpenSSH 连接工作区，文件操作、终端、语言服务器和扩展在工作区所在设备运行 |
| 个性化 | 系统字体、VS Code JSON/JSONC 格式颜色主题、文件图标主题和语法 capture 映射 |
| 扩展 | 通过可执行程序提供自定义编辑器、语言服务、字体、主题、图标、按钮、弹窗、菜单和面板 |

扩展管理器目前支持从本地目录安装，尚无线上扩展源。Pencode 不运行 VS Code 扩展；主题文件兼容范围仅限于 Pencode 已实现的字段。

## 开发环境

### 环境要求与当前限制

- Go **1.27.0 或更新版本**。
- 在本仓库旁准备名为 `onix` 的 Onix 源码目录。当前 `go.mod` 使用 `replace github.com/diiyw/onix => ../onix`。
- Onix 桌面构建所需的平台原生开发库，具体见 Onix 源码中的 `docs/desktop-builds.md`。
- Git 功能需要系统 `git`，远程连接需要 OpenSSH `ssh`。Go 扩展会自动安装所需工具；其他语言服务器由对应扩展决定。
- GUI 所在设备需安装可用字体，Pencode 不随程序打包字体。

预期目录布局：

```text
workspace/
├── onix/
└── pencode/
```

准备好 Onix 后，在其同级目录克隆 Pencode 并整理依赖：

```sh
git clone --recurse-submodules https://github.com/pencode-labs/pencode.git
cd pencode
go mod tidy
```

请使用提供 `github.com/diiyw/onix/html/{css,dom,layout,style}` 公开包的 Onix 源码版本。工作台及其测试已使用这些 API，可以作为独立模块构建：

```sh
go build ./...
```

编译安装 `pencode-server` 并重新安装仓库内的扩展后启动 Pencode：

```sh
make run
```

该命令先编译服务端并安装到 `~/.pencode/pencode-server`（Windows 为 `%USERPROFILE%/.pencode/pencode-server.exe`）。随后在每个已检出的扩展目录中构建，使用各自维护的 `go.mod` 和 `go.sum`，并禁用 Go workspace。扩展安装到本机的 `~/.pencode/extensions/<扩展 ID>/`。每个扩展构建成功后，会先删除同 ID 的旧安装目录，再安装新程序、资源和清单。Windows 自动使用 `.exe` 可执行文件并调整安装后的清单入口；Linux 和 macOS 使用原生可执行文件名。源码中的清单保持不变。

可运行 `make install-extensions` 只安装扩展而不启动应用，或使用 `make install-extensions EXTENSIONS_DIR="/path/to/extensions"` 将扩展打包到其他目录。远程工作区的扩展需要在远端安装。

可运行 `make install-server` 仅编译安装本机服务端。离线 SSH 引导所需的服务端可在仓库根目录运行 `go run ./cmd/pencode-build-servers` 构建，默认生成 Linux/macOS 的 amd64、arm64 程序，位于 `resources/servers/<os>-<arch>/pencode-server`。当前仓库适合源码开发，尚不是可直接安装的桌面发行版。

### 验证独立包

在 Pencode 仓库根目录执行：

```sh
go test -p 1 ./protocol ./peb ./lsp ./transport ./terminal
```

这些检查只覆盖对应包，不能代表完整应用已经通过构建或各平台运行验证。执行 `go test -p 1 ./...` 可覆盖主模块中的工作台及其他所有包。扩展使用独立模块，需单独测试：

```sh
git submodule foreach 'go test ./...'
```

## 架构

```text
桌面工作台（Onix）
        │
        │ CS v7：本地子进程管道或 SSH stdin/stdout
        ▼
Pencode 服务端
        ├── 文件、文档、索引与语法解析
        ├── Git 与终端进程
        ├── 语言服务器（LSP）
        └── 扩展进程（peb v8）
```

GUI 管理交互文本缓冲、撤销历史、布局与渲染；服务端管理文件系统、索引、语法解析、Git、PTY、语言服务器和扩展进程。本地会话启动子服务进程，SSH 会话通过加密字节流使用同一协议，无需开放服务端端口。

远程工作时，设置与扩展属于工作区所在设备，系统字体来自 GUI 所在设备。重连会保留 GUI 中的未保存文本并检查外部变更，不会自动重放保存、Git 写入或终端输入。

| 目录 | 职责 |
| --- | --- |
| `workbench/` | 编辑器界面、文档交互、主题与面板 |
| `client/`、`protocol/` | 类型化客户端服务与 CS 通信协议 |
| `server/` | 工作区服务与进程管理 |
| `transport/`、`remote/`、`connection/` | 本地传输、SSH 与连接生命周期 |
| `terminal/`、`lsp/`、`peb/` | 终端模拟器、LSP 支持和扩展协议／SDK |
| `extensions/` | 扩展宿主集成与示例扩展子模块 |
| `resources/` | 应用配置、场景、界面资源与图标 |
| `cmd/` | 独立服务端与服务端产物构建助手 |

## 扩展

扩展是独立可执行程序，通过 stdin/stdout 上的二进制协议 **peb v8** 与服务端通信，日志写入 stderr。扩展以服务端用户权限运行，远程工作区的扩展也在远端执行。

扩展安装在服务端的 `~/.pencode/extensions/<扩展 ID>/`，包含可执行文件和 `resources/extension.json` 清单。可通过扩展管理器的本地目录安装流程注册扩展。

以下示例均为 Git 子模块，各自在 `pencode-labs` 下的公开仓库维护，采用 MIT 协议：

- [Go 语言工具](https://github.com/pencode-labs/go)：提供 Go 语言和调试支持，缺少 `gopls`、Delve 时自动安装。
- [Markdown 预览](https://github.com/pencode-labs/markdown-preview)：只读预览，支持与宿主源码编辑器双栏显示。
- [Material 文件图标](https://github.com/pencode-labs/material-icons)：文件图标主题。
- [扩展 UI](https://github.com/pencode-labs/ui-demo)：工作台按钮、弹窗、菜单、浮窗与面板。

已有检出可运行 `git submodule update --init --recursive` 初始化子模块。每个扩展都有独立的 Go 模块与构建说明。例如，在主仓库根目录构建 Go 语言工具：

```sh
(cd extensions/go && go build -o bin/go .)
```

当前扩展协议为 peb v8（CS v7），旧扩展需要使用当前 SDK 重新构建。

## 文档

| 文档 | 内容 | 语言 |
| --- | --- | --- |
| [CS 协议](protocol/README.md) | 帧格式、请求、预算与生命周期 | 中文 |
| [扩展协议与 SDK](peb/README.md) | 扩展贡献、编辑器、按钮与 UI | 中文 |
| [LSP 支持](lsp/README.md) | 语言服务实现与开发说明 | 中文 |
| [调试系统](debugger/README.md) | Go 快速启动、项目调试配置与架构 | English |
| [历史实现说明](docs/implementation-notes.zh-CN.md) | 编辑器行为、主题、SSH、设计决策和迁移前的验证记录 | 中文 |

历史说明保留了原 Onix 仓库布局下的命令。当前目录结构与构建限制以本首页为准。欢迎协助翻译深入的技术指南。

## 参与贡献

欢迎提交 Issue 和 Pull Request。适合入手的方向包括：完成独立模块迁移、完善构建与平台文档、修复编辑行为、补充测试，以及翻译技术指南。

报告问题时，请提供操作系统与架构、Go 版本、Pencode 和 Onix 的提交版本、复现步骤及 Output 面板中的相关诊断。SSH 问题请补充远端系统，以及本地是否也能复现。提交日志前请移除凭据与私有项目数据。

保持修改范围聚焦，执行涉及包的检查，并在 PR 中说明验证结果和限制。较大的协议或架构变更，请先通过 Issue 讨论。

## 许可证与致谢

Pencode 使用 [MIT 许可证](LICENSE)。

项目基于 Onix、gotreesitter 等 Go 库构建。随仓库分发的第三方资源保留各自许可证，包括 [Codicons](resources/icons/CODICONS-LICENSE.txt)、[Lucide](resources/icons/LUCIDE-LICENSE.txt) 和 [Material 文件图标](extensions/material-icons/resources/LICENSE.txt)。再分发这些资源时，请保留对应声明。
