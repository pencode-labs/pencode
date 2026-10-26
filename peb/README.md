# peb v8 — Pencode Extension Binary

peb 是 Pencode 和 server 上扩展进程之间的二进制协议。宿主写扩展的 stdin，读取其 stdout；日志只写 stderr。不使用 JSON 消息、换行分隔或 shell 命令。支持 `Fonts`（字体）、`FileIcons`（文件图标）、`EditorThemes`（颜色主题）、`CustomEditors`（HTML/CSS 文件编辑工具）、`LanguageServers`（语言服务）、`Actions`（工作台按钮）、`ExtensionUI`（弹窗、菜单与 Panel）和 `DebugAdapters`（调试适配器）。

server 负责扩展目录、安装、注册探测和进程；GUI 通过 CS 连接代理 peb 流，并在后台解析字体、主题、图标与编辑器图片。已验证资源进入本地内存缓存，渲染时不读取服务端文件。

字体、主题与图标保持一次静态注册；编辑工具在用户打开对应文件时按需启动独立会话；action 扩展在注册校验后启动一个工作台会话，支持主动推送更新。每个打开的文件拥有自己的扩展进程，标签切换保留会话，关闭文件、切换工具、卸载扩展或退出时取消并回收进程。注册、进程 I/O 和退出不阻塞 UI。此版本将帧版本更新为 **8**，已有 Go 扩展需用当前 SDK 重新构建，不提供旧协议兼容分支。

## 帧

所有整数都是无符号小端整数。每帧由固定 16 字节头部和 payload 组成；流允许任意拆包、粘包。读取方必须精确读取指定长度。

| 偏移 | 长度 | 字段 |
| --- | --- | --- |
| 0 | 4 | magic：`70 65 62 00`（`peb\0`） |
| 4 | 2 | version：`8` |
| 6 | 2 | kind：见下表 |
| 8 | 4 | request ID |
| 12 | 4 | payload 字节数，不含头部，最大 4 MiB |

| kind | 名称 | 方向 | request ID |
| --- | --- | --- | --- |
| 1 | Initialize | 宿主 → 扩展 | 非零，当前宿主使用 `1` |
| 2 | Register | 扩展 → 宿主 | 原样返回 Initialize 的 ID |
| 3 | Shutdown | 宿主 → 扩展 | 必须为 `0`，payload 必须为空 |
| 4 | Failure | 扩展 → 宿主 | 原样返回请求 ID |
| 5 | EditorRequest | 宿主 → 扩展 | 非零，当前会话内递增 |
| 6 | EditorResponse | 扩展 → 宿主 | 原样返回 EditorRequest 的 ID |
| 7 | ActionActivate | 宿主 → 扩展 | `0`，payload 为空，仅发送一次 |
| 8 | ActionInvoke | 宿主 → 扩展 | 非零，当前 action 会话内递增 |
| 9 | ActionResponse | 扩展 → 宿主 | 原样返回 ActionInvoke 的 ID，payload 为空 |
| 10 | ActionUpdate | 扩展 → 宿主 | `0`，无需等待点击即可推送 |
| 11 | UIUpdate | 扩展 → 宿主 | `0`，打开、更新或关闭扩展界面 |
| 12 | UIRequest | 宿主 → 扩展 | 与 action 共用递增的非零 ID，携带控件或生命周期事件 |
| 13 | UIResponse | 扩展 → 宿主 | 原样返回 UIRequest 的 ID，payload 为空 |
| 14 | NotificationUpdate | 扩展 → 宿主 | `0`，显示、更新或关闭右下角通知 |

版本不符、未知 kind、非法 ID、超大帧、截断数据和非法 payload 均终止本次注册。扩展进程启动、握手与退出总计最多 5 秒；失败或关闭会话会关闭管道、终止并回收子进程。GUI 使用最多 4 个资源加载 worker，server 当前串行执行扩展注册与安装控制操作；最多发现 64 个已安装扩展。stderr 持续读取，最多保留 4096 字节诊断。

## Payload 编码

`string` = `u32 字节数` + UTF-8 字节，无终止符；最大 64 KiB。`array<T>` = `u32 元素数` + 按序排列的元素，单个数组最多 16384 项。结构体依次编码字段，不写字段名，不填充、不对齐。payload 不允许尾随字节。

```text
Initialize {
  host: string                  // 当前为 "pencode"
  capabilities: u32             // FileIcons=1, EditorThemes=2, CustomEditors=4, Fonts=8, LanguageServers=16, Actions=32, ExtensionUI=64, DebugAdapters=128，当前为 255
}

Register {
  themes: array<Theme>
  iconThemes: array<IconTheme>
  fonts: array<Font>
  languageServers: array<LanguageServer>
  actions: array<Action>
  actionService: u32             // boolean；允许初始按钮列表为空，之后主动注册
  debugAdapters: array<DebugAdapter>
}

Theme {
  id: string
  label: string                 // 主题选择器显示名称
  path: string                  // 相对扩展根目录，例如 resources/themes/ink.json
}

IconTheme {
  id: string
  label: string
  rules: array<IconRule>
}

IconRule {
  match: u32                    // 见下表
  pattern: string
  path: string                  // 相对扩展根目录，例如 resources/icons/code.svg
}

Font {
  id: string
  label: string
  path: string                  // resources/fonts/regular.ttf
  boldPath: string              // 可选粗体路径，无粗体时为空字符串
}

LanguageServer {
  id: string
  label: string
  languageId: string             // 例如 go / typescript / rust
  command: string               // PATH、绝对路径或相对扩展目录的可执行文件
  args: array<string>
  selectors: array<DocumentSelector>
  initializationOptions: text    // 空字符串或标准 JSON
  configuration: text            // 空字符串或标准 JSON
}

DebugAdapter {
  id: string                    // 例如 Delve，与调试配置的 adapter 对应
  label: string
  command: string               // PATH、绝对路径或相对扩展目录
  args: array<string>           // 启动前缀，位于适配器固定参数和用户参数之前
}

Failure {
  message: string
}

NotificationUpdate {
  id: string                    // 扩展内唯一；相同 ID 更新原通知
  title: string                 // 非关闭通知必填
  message: string               // 纯文本，非关闭通知必填
  action: string                // 可选的本地 action ID，不必注册工作台按钮
  actionText: string            // 与 action 同时填写
  error: u32                    // boolean；错误使用 alert 和主题错误颜色
  close: u32                    // boolean；按 ID 关闭通知
}
```

action 会话可通过 `publisher.Notify(peb.Notification{ID: "tools", Title: "Go", Message: "工具安装失败", Error: true, Action: "retry", ActionText: "重试"})` 发布宿主管理的通用通知，无需提供 HTML/CSS 或 `UIEvent`。按钮通过 `Handlers.Action` 接收 `ActionContext.ID="retry"`。通知不抢占焦点、不替换扩展弹窗，支持用户关闭；扩展卸载或会话结束时自动清理。最多保留 8 条通知，超出时移除最早的通知；更新已有通知不改变顺序。传入 `Notification{ID: "tools", Close: true}` 可主动关闭。

字体、主题和图标主题的 ID 分别唯一，只允许小写字母、数字、`.`、`-`，长度为 1–128 字节，不能为 `.` 或 `..`。label 必须非空。宿主将它们注册为 `extensions/<扩展 ID>/<贡献 ID>`，各类贡献各自使用独立的选择器。扩展应只返回宿主声明支持的能力。

| match | Go 常量 | pattern | 匹配对象 |
| --- | --- | --- | --- |
| 0 | DefaultFile | 空字符串 | 默认文件 |
| 1 | FileName | 完整文件名，例如 `go.mod` | 文件 basename |
| 2 | FileSuffix | 带前导点，例如 `.go`、`.d.ts` | 文件名后缀 |
| 3 | DefaultFolder | 空字符串 | 默认目录 |
| 4 | FolderName | 完整目录名，例如 `src` | 目录 basename |

匹配区分大小写。文件名精确匹配优先，再匹配最长后缀，再使用默认文件图标；目录名精确匹配优先于默认目录图标。同一类型和 pattern 重复时，最后一条规则生效。未命中且未声明默认规则时，使用 Pencode 内置图标。pattern 不能包含路径分隔符。

资源必须位于扩展包的 `resources/` 中。路径使用 `/`，禁止绝对路径、`..`、查询字符串及跳出扩展根目录的符号链接。主题和图标每份资源最多 1 MiB，字体每份最多 32 MiB，一个扩展累计最多读取 64 MiB；相同路径只读取一次。主题使用 Pencode 现有的 JSON/JSONC `colors` / `tokenColors` 格式，支持的 include 同样受资源路径与大小限制。

文件图标使用 SVG，原始尺寸为 1–256 像素；SVG 在注册时解析校验，原文缓存于宿主内存。`currentColor` 随颜色主题和文件选中状态着色，SVG 中显式颜色保留。纹理按资源和前景色缓存。图标规则编译为查找表，匹配时不分配内存，也不发起进程通信。

## 字体

扩展通过 `Contributions.Fonts` 提供字体，同一字体可用于代码和界面。`Path` 必填，`BoldPath` 可省略；使用引擎支持的 TTF 或 OTF 字体。资源在后台解析，损坏字体会使整次注册失败。同一路径只读取和解析一次，渲染阶段复用字形缓存。字体文件不通过 peb 帧传输，只传资源路径；CS 链路把字节批量传给 GUI，在后台解析并缓存。

```go
peb.Contributions{
    Fonts: []peb.Font{
        {ID: "mono", Label: "My Mono", Path: "resources/fonts/regular.ttf", BoldPath: "resources/fonts/bold.ttf"},
    },
}
```

字体 ID 在宿主中为 `extensions/<扩展 ID>/<字体 ID>`，保存在 `codeFont` / `uiFont` 设置中。尚未完成注册或加载失败时使用当前平台的系统默认等宽字体；注册完成后恢复选择。禁用、卸载时移除字体选项、回退系统默认字体并释放字形缓存。扩展包应随字体附上相应许可证。

## Go 扩展

使用 `github.com/pencode-labs/pencode/peb`：

```go
package main

import (
    "fmt"
    "os"

    "github.com/pencode-labs/pencode/peb"
)

func main() {
    err := peb.Serve(os.Stdin, os.Stdout, peb.Contributions{
        Themes: []peb.Theme{
            {ID: "ink", Label: "Ink", Path: "resources/themes/ink.json"},
        },
        IconThemes: []peb.IconTheme{
            {ID: "code", Label: "Code", Rules: []peb.IconRule{
                {Match: peb.FileSuffix, Pattern: ".go", Path: "resources/icons/code.svg"},
            }},
        },
    })
    if err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

`Reader` 复用帧缓冲区，`Frame.Payload` 只在下次 `Read` 前有效；各 `Decode*` 函数返回的字符串拥有独立存储。`Encode*` 函数接受可复用的目标切片。`Reader` / `Writer` 各自应由一个 goroutine 使用。

完整示例及安装步骤见 [Pencode README](../README.md#扩展)。


## 自定义文件编辑工具

使用 `peb.ServeEditors(stdin, stdout, contributions, handler)`，在 `Contributions.Actions` 注册类型为 `OpenWith` 的 action：

```go
peb.Action{
    ID: "markdown",
    Text: "Markdown 预览",
    Tooltip: "Markdown 预览",
    Icon: "resources/preview.svg",
    Location: peb.OpenWith,
    Default: false,
    Selectors: []peb.DocumentSelector{
        {Match: peb.FileSuffix, Pattern: ".md"},
        {Match: peb.FileName, Pattern: "README"},
    },
}
```

打开方式使用 action 的 ID、图标与提示校验规则，宿主 ID 为 `extensions/<扩展 ID>/<工具 ID>`，消息内 Editor 使用扩展本地 ID。selector 只允许 FileName、FileSuffix，必须非空；区分大小写。文本文件默认使用内置编辑器，`Default: true` 声明自动打开。精确文件名优先于后缀，较长后缀优先，相同优先级按宿主工具 ID 排序。二进制文件自动选择匹配工具。代码区右上角浮动显示匹配的打开方式图标，默认透明度为 30%，悬停或键盘聚焦时为 100%。只有内置代码编辑器时不显示图标组；存在扩展打开方式时显示代码按钮，点击可返回源码。点击扩展图标默认替换整个代码区；路径栏的双栏按钮可同时显示左侧源码与右侧扩展视图，再次点击保留当前扩展为单栏。选择和布局只保留在当前打开的文件中。

handler 接收 `EditorMessage`：Editor、文件绝对 Path、宿主当前 Content、Binary 和 Event。`open` 请求创建视图，`documentchange` 表示撤销、重做或从磁盘重新加载后同步文档。HTML 事件提供 Kind、data-ref、Value、Checked、Selected、Anchor、Focus、InputType 和 Data。宿主串行提交事件，每次均附最新文档快照；切换标签后回复仍应用到所属文件。与宿主新内容冲突的过期回复会丢弃并重新同步。

返回 `EditorReply{View: ..., Content: ...}`：

- `View == nil` 保留 DOM、光标与滚动；提供 View 时替换整个 HTML 片段和作用域 CSS。
- `Content == nil` 不修改文档；指向空字符串表示清空。修改由宿主统一管理 dirty、保存与撤销/重做。
- `View.Events` 显式订阅 `{Ref, Kind}`，支持 click、input、change、focus、blur、submit、reset。Ref 必须对应 HTML 的 data-ref。异步事件不能同步阻止默认行为，因此暂不开放 beforeinput 取消接口。
- HTML 使用 Onix 现有 HTML/CSS 渲染器。CSS 按组件隔离，元素 id 必须以 `pencode-extension-` 开头，避免占用工作台控件 ID。可继承工作台 CSS 使用的 `--vscode-*` 颜色变量，主题切换保留组件样式。
- `img src` 可使用 `resources/` 下的 SVG、PNG、JPEG 或 WebP 相对路径，也可使用本扩展的 `peb://<扩展 ID>/resources/...`。GUI 后台从 server 批量读取并校验，再改写成缓存的 `peb://` URI；每张最大 4 MiB、每边不超过 2048 像素、每视图合计 8 MiB / 64 项 / 4 MP。SVG 按原始尺寸栅格化。资源获取最长 5 秒，文件大小、尺寸和路径限制失败会终止当前视图。CSS 保持内联文本，不增加外部样式表或脚本执行能力。

初次回复必须提供 View。扩展在 server 进程中处理事件，当前没有 JavaScript、VS Code API、任意键盘/指针事件订阅或增量 DOM 消息 API。二进制文件只提供 server 路径 Path、Binary=true、空 Content，扩展可在远程环境中按路径读取；第一版不支持二进制内容写回，宿主文本保存不会触碰这些文件。

每个请求最多等待 5 秒，空闲会话没有超时；错误、超时和进程退出显示在编辑区域。每份文档最多排队 32 个事件；队列溢出会终止该会话并显示错误。保存与关闭会等待尚未处理完的事件，避免丢失最后输入。协议缓冲区与空闲轮询复用内存；进程通信、HTML 替换和修改内容仍有必要分配，不能视为整个编辑链路零分配。

编辑工具新增的 payload 按以下顺序编码。bool 使用 u32，严格只接受 0/1；text 为 u32 字节数 + UTF-8，最大 2 MiB；普通 string 仍为 64 KiB，单帧总大小仍为 4 MiB。

```text
DocumentSelector { match: u32, pattern: string, languageId: string }
EditorRequest {
  editor: string, path: string, content: text, binary: bool,
  kind: string, ref: string, value: text, inputType: string, data: text,
  checked: bool, selected: u32, anchor: u32, focus: u32
}
EditorResponse {
  hasView: bool,
  // hasView=true 时：html: text, css: text, events: array<{ref: string, kind: string}>
  hasContent: bool
  // hasContent=true 时：content: text
}
```

完整运行示例见 [Markdown 预览](../extensions/markdown-preview/main.go)。插件只提供只读预览，在 `open` 和 `documentchange` 时渲染最新宿主快照；源码编辑和双栏布局由宿主管理。

## 语言服务扩展

`Contributions.LanguageServers` 注册标准 stdio Language Server。注册扩展进程退出后，宿主在打开匹配文件时，为每个项目根目录和服务 ID 启动一个进程，多个文档共享它；禁用、卸载扩展、关闭项目或退出时执行 `shutdown` / `exit` 并回收进程。语言服务 stdout 只允许 LSP 的 `Content-Length` JSON-RPC 消息，和 peb 扩展进程的二进制 stdout 分开。

```go
err := peb.Serve(os.Stdin, os.Stdout, peb.Contributions{
    LanguageServers: []peb.LanguageServer{{
        ID: "gopls", Label: "Go", LanguageID: "go", Command: "gopls",
        Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".go"}},
        Configuration: `{"gopls":{"staticcheck":true}}`,
    }},
})
```

selector 使用与编辑工具相同的 `FileName` / `FileSuffix` 规则，不区分目录，同一服务的文件使用声明的 `LanguageID`。`Command` 不通过 shell 执行；带路径分隔符的相对路径基于扩展目录，服务工作目录和 `rootUri` 为项目根目录。`InitializationOptions` 原样传给 initialize；`Configuration` 用于 `workspace/configuration` 的按 section 查询和初始 `workspace/didChangeConfiguration` 通知。所有 JSON 文件仍放在扩展的 `resources/` 中；也可以像示例一样在程序内生成选项。

完整可构建示例见 [Go 扩展](../extensions/go/main.go)，安装和操作见 [Pencode LSP 开发文档](../lsp/README.md)。

## Actions

原生代码和扩展使用相同的 `peb.Action` 描述按钮。位置为 `StatusBar=0`（状态栏）、`LeftContextMenu=1`（左侧文件树右键菜单）、`RightContextMenu=2`（右侧编辑区右键菜单）、`TitleBarRight=3`（标题栏右侧）和 `OpenWith=4`（代码区右上角打开方式图标）。按钮按注册顺序排列；更新位置后移动到对应区域。禁用或隐藏的按钮不会执行；禁用、卸载扩展及进程异常退出时清理其所有 action。

```text
Action {
  id, text, tooltip, icon, color, iconColor, iconStyle: string
  location: u32
  disabled, hidden, default: u32 // boolean，只允许 0 或 1
  selectors: array<DocumentSelector> // OpenWith 必须提供文件名或后缀；其他类型为空
}
ActionInvoke {
  id, projectPath, path: string
  directory: u32                // boolean
  caret, anchor: u32             // Unicode rune offset
}
ActionUpdate {
  action: Action
  remove: u32                   // boolean
}
```

`text` 和 `tooltip` 至少有一个非空；图标按钮用 `tooltip` 提供可访问名称。扩展 action 的 ID 规则与其他贡献一致，宿主统一添加 `extensions/<扩展 ID>/` 前缀；点击回调中的 ID 是扩展本地 ID。`icon` 是 `resources/` 内的 SVG 路径，尺寸 1–256；运行期间可换成其他 SVG，读取和校验在后台完成。使用 `currentColor` 绘制 SVG 的可着色部分。`color` 控制文本颜色，`iconColor` 控制图标颜色，两者接受 `#RRGGBB` 或 `#RRGGBBAA`；留空跟随主题。`iconStyle` 是图标的内联 CSS 声明，例如 `opacity:.6;transform:rotate(90deg)`，最多 1024 字节。

`ActionPublisher.Update` 对未注册 ID 创建按钮，对已有 ID **完整替换**描述；空字段清除旧值，因此更新时应保留仍需要的字段。`Remove(id)` 移除按钮。每个扩展最多 256 个 action，更新复用已读取的图标资源并遵循资源大小限制。宿主每个会话最多排队 64 次 action/UI 事件，每次只执行一个请求；握手和点击请求超时均为 5 秒，空闲连接没有超时。UI 不等待扩展 I/O。

扩展使用 `ServeExtension`，提供 `Handlers.Action`、`Handlers.UIEvent` 或 `ActivateActions` 即自动注册 `ActionService`。`ActivateActions` 仅在 action 工作台进程启动时执行一次，静态校验和文件编辑进程不会调用它。可以在其中启动后台 goroutine，使用提供的 context 停止任务；publisher 支持并发调用。action 和 editor handler 在其所属进程内顺序执行，多个进程之间不共享内存。

```go
action := peb.Action{
    ID: "build", Text: "构建", Tooltip: "构建当前项目",
    Icon: "resources/icons/build.svg", Location: peb.TitleBarRight,
}
var buttons *peb.ActionPublisher
err := peb.ServeExtension(os.Stdin, os.Stdout,
    peb.Contributions{Actions: []peb.Action{action}},
    peb.Handlers{
        ActivateActions: func(ctx context.Context, p *peb.ActionPublisher) {
            buttons = p
            // 在这里启动需要主动更新按钮的后台任务，并监听 ctx.Done()。
        },
        Action: func(c peb.ActionContext) error {
            // 根据 c.ProjectPath、c.Path 和 c.Caret 执行扩展逻辑。
            action.Text = "构建完成"
            action.IconColor = "#73c991"
            return buttons.Update(action)
        },
    })
```

左侧菜单传入右键目标的绝对路径、所属项目和目录标记；右侧菜单传入打开菜单时的编辑器选区；状态栏与标题栏传入当前文件和选区。原生代码在共享 `workbench.Session` 上注册：

```go
session := workbench.NewSession()
action := peb.Action{ID: "core/build", Text: "构建", Location: peb.StatusBar}
err := session.Actions.Register(actions.Definition{
    Action: action,
    Run: func(c peb.ActionContext) error {
        // 原生回调在 UI 线程执行；耗时工作自行放到后台。
        return nil
    },
})
// 支持从后台 goroutine 更新；自动唤醒界面，保持原点击回调。
action.Text = "构建中"
err = session.Actions.Update(action)
session.Actions.Remove(action.ID)
// 按归属批量清理：session.Actions.RemovePrefix("core/")
```

`actions` 包路径为 `github.com/pencode-labs/pencode/actions`。原生注册 ID 在整个 registry 中唯一，重复注册返回错误；`Update` 更新已有按钮，`Remove` 删除按钮。未变化的注册表快照、UI 描述同步以及复用缓冲区后的编码路径保持零分配。

## 弹窗、上下文菜单与 Panel

`ActionPublisher.Show(peb.UI{...})` 打开或更新扩展界面，`CloseUI(id)` 关闭；`Handlers.UIEvent` 接收控件和生命周期事件。必须注册 `UIEvent`，否则 `Show` 返回 `ErrProtocol`。这些界面复用 action 的常驻进程，I/O、图片读取和资源校验在后台执行。

| Kind | 行为 |
| --- | --- |
| `peb.Dialog` | 居中的模态弹窗；包含标题、关闭按钮、焦点循环及 Esc 关闭 |
| `peb.Popup` | 类似扩展市场的非模态浮窗；支持坐标、锚点、宽高与关闭按钮；可操作背后的工作区，焦点在浮窗内时 Esc 关闭 |
| `peb.ContextMenu` | 锚定 action 的菜单；自动限制在工作区内，支持方向键、Tab、Esc 和点击外部关闭 |
| `peb.Panel` | 使用可调整高度的底部工作台面板；支持工具栏关闭、Esc 关闭及切换可见性 |

`UI.ID` 属于扩展自身命名空间，遵循 action 本地 ID 规则。`Title` 必须非空，用作界面的可访问名称。`Anchor` 是本扩展的 action ID，打开时留空采用触发 action；文件树和编辑器菜单中的 action 也可作为锚点。首次打开必须提供 `View`，以后 `View:nil` 保留现有视图和输入状态；非空 `View` 替换 HTML/CSS 和事件绑定。其余描述字段完整替换，同一 ID 的 `Kind` 在关闭前不能改变。

浮层使用 `UI.Placement` 设置位置和大小，单位为工作区 CSS 像素（随工作区缩放）：

| 字段 | 含义 |
| --- | --- |
| `Mode: peb.PositionDefault` | Dialog/Popup 居中，ContextMenu 锚定 action；X/Y 是偏移 |
| `Mode: peb.PositionAbsolute` | X/Y 是相对工作区左上角的坐标 |
| `Mode: peb.PositionAnchor` | X/Y 是相对 Anchor action 左下角的偏移；锚点不可用时采用触发位置 |
| `Width`, `Height` | 浮层宽高；0 使用默认大小；负数及非有限数值无效 |

打开后可发送相同 ID、`View:nil` 更新位置和大小，保留输入、选区和滚动。坐标可为负数，最终位置和大小受工作区边界约束；窗口变化后会重新按请求的位置定位，锚定浮层跟随 action。`Panel` 使用底部布局，Placement 必须为零值。Popup 的 Tab 使用原生焦点遍历；焦点移到工作区后不会截获工作区快捷键。

`UI.CloseOnOutsideClick` 控制 Popup 点击外部的行为：`false`（默认）保持打开，`true` 关闭并发送 `Event.Kind="close"`、`Reason="outside"` 的 UIEvent。鼠标按下或触摸按下时检测；即使焦点已经移到工作区，配置仍然生效。关闭保留底层点击和焦点。可用 `View:nil` 实时修改该配置；其余描述字段完整替换，后续更新需继续传入希望保留的值。此字段只影响 Popup；ContextMenu 始终点击外部关闭，Dialog/Panel 使用各自的关闭行为，关闭按钮、Esc 和 `CloseUI` 也保留原有行为。

```go
err := publisher.Show(peb.UI{
    ID: "market", Title: "扩展市场", Kind: peb.Popup,
    CloseOnOutsideClick: true,
    Placement: peb.UIPlacement{
        Mode: peb.PositionAbsolute, X: 80, Y: 100, Width: 640, Height: 480,
    },
    View: &peb.EditorView{HTML: `<p>扩展内容</p>`},
})
// 改为显示在 action 下方；保留现有视图。
err = publisher.Show(peb.UI{
    ID: "market", Title: "扩展市场", Kind: peb.Popup, Anchor: "tools",
    CloseOnOutsideClick: true,
    Placement: peb.UIPlacement{
        Mode: peb.PositionAnchor, X: -480, Y: 8, Width: 640, Height: 480,
    },
})
```

工作台最多保留 32 个扩展界面，同时展示一个弹窗、浮窗或菜单，以及一个底部 Panel。打开新的浮层会关闭原来的浮层，关闭原因为 `replace`。底部面板切换保留扩展视图，返回后继续接收事件。禁用、卸载、重新加载扩展或进程异常退出会移除所属界面，退出后的进程不再接收生命周期事件。

```go
var ui *peb.ActionPublisher
err := peb.ServeExtension(os.Stdin, os.Stdout,
    peb.Contributions{Actions: []peb.Action{{
        ID: "tools", Text: "工具", Location: peb.TitleBarRight,
    }}},
    peb.Handlers{
        ActivateActions: func(ctx context.Context, p *peb.ActionPublisher) { ui = p },
        Action: func(c peb.ActionContext) error {
            return ui.Show(peb.UI{
                ID: "tools-menu", Title: "工具菜单", Kind: peb.ContextMenu, Anchor: c.ID,
                View: &peb.EditorView{
                    HTML: `<button data-ref="open" type="button" role="menuitem">打开 Panel</button>`,
                    Events: []peb.EditorBinding{{Ref: "open", Kind: "click"}},
                },
            })
        },
        UIEvent: func(e peb.UIEvent) error {
            if e.ID == "tools-menu" && e.Event.Kind == "click" && e.Event.Ref == "open" {
                if err := ui.CloseUI(e.ID); err != nil { return err }
                return ui.Show(peb.UI{
                    ID: "result", Title: "工具结果", Kind: peb.Panel,
                    View: &peb.EditorView{HTML: `<p>操作完成</p>`},
                })
            }
            return nil
        },
    })
```

`View` 使用与自定义编辑器相同的 `EditorView`、作用域 CSS、主题变量和图片资源规则。通过 `Events` 订阅 `click`、`input`、`change`、`focus`、`blur`、`submit`、`reset`，Ref 对应 `data-ref`。HTML 控件和表单缺少 ID 时由宿主生成独立 ID；未命名控件也能参与键盘焦点导航。`@peb-focus-` 为宿主生成的 ref 前缀，请勿用于自定义 ref。自定义 ID 仍需以 `pencode-extension-` 开头，且应在所有视图间保持唯一。菜单项由扩展使用标准 `role="menuitem"` 等语义描述，表单可直接使用 `form` 和 `button type="submit"`。

生命周期事件无需显式订阅：

| `e.Event.Kind` | 内容 |
| --- | --- |
| `open` | 新界面已被宿主接受；同一 ID 的更新不会重复触发 |
| `close` | `e.Reason` 为 `extension`、`button`、`escape`、`outside` 或 `replace` |
| `visibilitychange` | `e.Event.Value` 为 `visible` 或 `hidden`，用于 Panel 切换 |

每个事件附带 `e.ID` 和打开界面时的 `e.Context`（项目、文件路径、目录标记及选区）。SDK 在 `Action` 和 `UIEvent` handler 中自动继承当前上下文；后台任务应保存并显式传入 `UI.Context`，避免使用后来触发的 action 上下文。已打开界面的上下文保持到关闭。异步控件事件无法同步取消浏览器默认行为，当前不支持 JavaScript 或任意键盘、指针事件订阅。

UI 和 action 请求共享串行队列及 5 秒请求超时，handler 内可以继续推送界面更新；不要在 handler 中等待后续用户事件。空闲轮询和复用缓冲区的编码保持零分配；创建或替换视图、解码字符串与加载资源有必要的分配。

UI payload 的字段顺序如下。`ActionContext` 与 `ActionInvoke` 编码一致，`EditorEvent` 与 `EditorRequest` 的事件部分一致，bool 使用严格的 u32 0/1。

```text
UIUpdate {
  id, title, anchor: string
  kind: u32                     // Dialog=0, ContextMenu=1, Panel=2, Popup=3
  close: bool
  placementMode: u32            // Default=0, Absolute=1, Anchor=2
  x, y, width, height: f32      // IEEE 754，小端
  closeOnOutsideClick: bool    // Popup 点击外部是否关闭，默认 false
  context: ActionContext
  hasView: bool
  // hasView=true: html: text, css: text, events: array<{ref: string, kind: string}>
}
UIRequest {
  id, reason: string
  context: ActionContext
  event: EditorEvent
}
UIResponse {}                   // 成功应答；错误使用 Failure
```

可构建的完整示例见 [UI Demo](../extensions/ui-demo/main.go)，包含 action 菜单、表单弹窗、自由定位浮窗、点击外部关闭选项、输入与提交事件、Panel 更新和关闭事件。

## 调试工具

`Contributions.DebugAdapters` 为 workspace server 提供调试启动命令。Go 扩展注册 `Delve`，以自身的 `--tool dlv` 启动器按需安装工具。显式配置的 command 优先；同一 adapter ID 有多个提供者时返回冲突。禁用、卸载和断开连接会取消启动并回收对应适配器进程。

`DocumentSelector` 在 match、pattern 后增加 `languageId:string`，为空则继承 LanguageServer.LanguageID，允许同一 gopls 会话分别用 go、go.mod、go.work 打开文档。
