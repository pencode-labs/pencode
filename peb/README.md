# peb v15 — Pencode Extension Binary

peb 是 Pencode 和 server 上扩展进程之间的二进制协议。宿主写扩展的 stdin，读取其 stdout；日志只写 stderr。不使用 JSON 消息、换行分隔或 shell 命令。支持 `Fonts`（字体）、`FileIcons`（文件图标）、`EditorThemes`（颜色主题）、`CustomEditors`（HTML/CSS 文件编辑工具）、`LanguageServers`（语言服务）、`Actions`（工作台按钮）、`ExtensionUI`（弹窗、菜单与 Panel）、`DebugAdapters`（调试适配器）和 `ExtensionSettings`（扩展配置）。

server 负责扩展目录、安装、注册探测和进程；GUI 通过 CS 连接代理 peb 流，并在后台解析字体、主题、图标与编辑器图片。已验证资源进入本地内存缓存，渲染时不读取服务端文件。

字体、主题与图标保持一次静态注册；编辑工具在用户打开对应文件时按需启动独立会话；action 扩展在注册校验后启动一个工作台会话，支持主动推送更新。每个打开的文件拥有自己的扩展进程，标签切换保留会话，关闭文件、切换工具、卸载扩展或退出时取消并回收进程。注册、进程 I/O 和退出不阻塞 UI。此版本将帧版本更新为 **15**，为通知增加严重级别；宿主与扩展需用当前 SDK 重新构建。

## 扩展配置

扩展通过 `Contributions.Settings` 注册配置项。在设置左侧展开“扩展”，仅列出已启用、加载成功且声明了配置项的扩展；点击名称后，右侧显示对应表单。界面沿用 IDE 的输入框、下拉框、复选框和主题颜色。文本在提交更改时保存，布尔和下拉选项立即保存；支持恢复当前扩展的默认设置。

```go
contributions := peb.Contributions{
    Settings: []peb.Setting{
        {ID: "name", Label: "名称", Description: "扩展显示名称",
            Type: peb.SettingString, Default: json.RawMessage(`"Pencode"`)},
        {ID: "enabled", Label: "启用", Type: peb.SettingBoolean,
            Default: json.RawMessage(`true`)},
        {ID: "scale", Label: "比例", Type: peb.SettingNumber,
            Default: json.RawMessage(`1.5`)},
        {ID: "limit", Label: "数量", Type: peb.SettingInteger,
            Default: json.RawMessage(`10`)},
        {ID: "mode", Label: "模式", Type: peb.SettingString,
            Default: json.RawMessage(`"auto"`),
            Options: []peb.SettingOption{{Value: "auto", Label: "自动"}, {Value: "manual", Label: "手动"}}},
    },
}
handlers := peb.Handlers{
    Settings: func(values peb.SettingValues) error {
        name, err := peb.DecodeSetting[string](values, "name")
        if err != nil { return err }
        // 保存 name 供后续 action 或编辑器请求使用。
        _ = name
        return nil
    },
}
err := peb.ServeExtension(os.Stdin, os.Stdout, contributions, handlers)
```

`SettingString=0`、`SettingBoolean=1`、`SettingNumber=2`、`SettingInteger=3`；整数为有符号 64 位整数，数字必须可表示为有限的 float64。`Default` 必填且必须匹配类型。`Options` 仅用于字符串，提供选项时默认值和用户值必须在选项中。每个扩展最多 256 项，每项最多 256 个选项，ID 在扩展内部唯一，不允许 `/`、`\` 或 NUL；Label 必填，Description 可选。重复 ID、重复选项、未知类型和非法默认值会拒绝注册。

宿主在 `Register` 成功后、`ActionActivate` 或首次 `EditorRequest` 前，向声明配置的扩展发送一次 `SettingsRequest`；修改或恢复默认时，向正在运行的 action 和文件编辑器进程发送新的完整快照。新启动的进程直接获得最新配置。`Handlers.Settings` 与其他处理函数串行执行，收到的 map 与 JSON 数据归扩展所有，可以保留；初次回调时 action publisher 尚未激活。回调返回错误时回复 `Failure`，运行期错误通过现有扩展诊断通路报告。注册探测只读取声明，不调用此回调。

值按扩展 ID 隔离，写入工作区宿主的用户 `settings.json`：

```json
{"extensions":{"ui-demo":{"name":"Pencode"}}}
```

远程工作区沿用服务端配置保存通路。未显式设置或不再符合声明的值使用默认值；停用、卸载扩展会移除设置导航，但保留已保存值，以便重新启用或安装。未识别的扩展和配置键也会保留。配置快照仅包含当前声明的键。等待发送的快照合并为最新一次，空闲轮询与已缓存配置查询不分配内存。

配置在二进制帧中编码，JSON 只用于单个值，以保留其类型：

```text
Setting {
  id: string
  label: string
  description: string
  type: u32
  default: string              // JSON scalar
  options: array<SettingOption>
}
SettingOption { value: string, label: string }
SettingsRequest {
  values: array<SettingValue>
}
SettingValue { id: string, value: string } // value 为 JSON scalar
SettingsResponse {}                         // 空 payload
```

同一快照不能重复 ID，且必须恰好覆盖声明的配置项。所有字符串、数组和 payload 仍受 PEB 的通用大小上限约束。此变更将 PEB 升级为 v10，现有扩展需要用当前 SDK 重新构建。

## 帧

所有整数都是无符号小端整数。每帧由固定 16 字节头部和 payload 组成；流允许任意拆包、粘包。读取方必须精确读取指定长度。

| 偏移 | 长度 | 字段                                 |
| ---- | ---- | ------------------------------------ |
| 0    | 4    | magic：`70 65 62 00`（`peb\0`）      |
| 4    | 2    | version：`14`                         |
| 6    | 2    | kind：见下表                         |
| 8    | 4    | request ID                           |
| 12   | 4    | payload 字节数，不含头部，最大 4 MiB |

| kind | 名称           | 方向        | request ID                                          |
| ---- | -------------- | ----------- | --------------------------------------------------- |
| 1    | Initialize     | 宿主 → 扩展 | 非零，当前宿主使用 `1`                              |
| 2    | Register       | 扩展 → 宿主 | 原样返回 Initialize 的 ID                           |
| 3    | Shutdown       | 宿主 → 扩展 | 必须为 `0`，payload 必须为空                        |
| 4    | Failure        | 扩展 → 宿主 | 原样返回请求 ID                                     |
| 5    | EditorRequest  | 宿主 → 扩展 | 非零，当前会话内递增                                |
| 6    | EditorResponse | 扩展 → 宿主 | 原样返回 EditorRequest 的 ID                        |
| 7    | ActionActivate | 宿主 → 扩展 | `0`，payload 为空，仅发送一次                       |
| 8    | ActionInvoke   | 宿主 → 扩展 | 非零，当前 action 会话内递增                        |
| 9    | ActionResponse | 扩展 → 宿主 | 原样返回 ActionInvoke 的 ID，payload 为空           |
| 10   | ActionUpdate   | 扩展 → 宿主 | `0`，无需等待点击即可推送                           |
| 11   | UIUpdate       | 扩展 → 宿主 | `0`，打开、更新或关闭扩展界面                       |
| 12   | UIRequest      | 宿主 → 扩展 | 与 action 共用递增的非零 ID，携带控件或生命周期事件 |
| 13   | UIResponse     | 扩展 → 宿主 | 原样返回 UIRequest 的 ID，payload 为空              |
| 14   | SettingsRequest | 宿主 → 扩展 | 非零，完整配置快照，与所在会话的请求共用 ID 序列 |
| 15   | SettingsResponse | 扩展 → 宿主 | 原样返回 SettingsRequest 的 ID，payload 为空 |

版本不符、未知 kind、非法 ID、超大帧、截断数据和非法 payload 均终止本次注册。扩展进程启动、握手与退出总计最多 5 秒；失败或关闭会话会关闭管道、终止并回收子进程。GUI 使用最多 4 个资源加载 worker，server 当前串行执行扩展注册与安装控制操作；最多发现 64 个已安装扩展。stderr 持续读取，最多保留 4096 字节诊断。

## Payload 编码

`string` = `u32 字节数` + UTF-8 字节，无终止符；最大 64 KiB。`array<T>` = `u32 元素数` + 按序排列的元素，单个数组最多 16384 项。结构体依次编码字段，不写字段名，不填充、不对齐。payload 不允许尾随字节。

```text
Initialize {
  host: string                  // 当前为 "pencode"
  capabilities: u32             // FileIcons=1, EditorThemes=2, CustomEditors=4, Fonts=8, LanguageServers=16, Actions=32, ExtensionUI=64, DebugAdapters=128, ExtensionSettings=256，当前为 511
}

Register {
  themes: array<Theme>
  iconThemes: array<IconTheme>
  fonts: array<Font>
  languageServers: array<LanguageServer>
  actions: array<Action>
  actionService: u32             // boolean；允许初始按钮列表为空，之后主动注册
  debugAdapters: array<DebugAdapter>
  settings: array<Setting>
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
  templates: text              // 可选的 debug.json 数组；每项 adapter 必须与 id 相同
}

Failure {
  message: string
}
```

字体、主题和图标主题的 ID 分别唯一，只允许小写字母、数字、`.`、`-`，长度为 1–128 字节，不能为 `.` 或 `..`。label 必须非空。宿主将它们注册为 `extensions/<扩展 ID>/<贡献 ID>`，各类贡献各自使用独立的选择器。扩展应只返回宿主声明支持的能力。

| match | Go 常量       | pattern                       | 匹配对象      |
| ----- | ------------- | ----------------------------- | ------------- |
| 0     | DefaultFile   | 空字符串                      | 默认文件      |
| 1     | FileName      | 完整文件名，例如 `go.mod`     | 文件 basename |
| 2     | FileSuffix    | 带前导点，例如 `.go`、`.d.ts` | 文件名后缀    |
| 3     | DefaultFolder | 空字符串                      | 默认目录      |
| 4     | FolderName    | 完整目录名，例如 `src`        | 目录 basename |

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
- 按钮的 `title` 会由宿主显示为 IDE 风格的悬浮提示；未设置 `title` 时使用 `aria-label`。图标按钮应提供简短的功能说明。Action 按钮使用 `Tooltip`，留空时使用 `Text`。
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

原生代码和扩展使用相同的 `peb.Action` 描述按钮。位置为 `StatusBar=0`（状态栏）、`LeftContextMenu=1`（左侧文件树右键菜单）、`RightContextMenu=2`（右侧编辑区右键菜单）、`TitleBarRight=3`（标题栏右侧）、`OpenWith=4`（代码区右上角打开方式图标）和 `Command=5`（无按钮命令）。按钮按注册顺序排列；更新位置后移动到对应区域。禁用或隐藏的按钮不会执行；禁用、卸载扩展及进程异常退出时清理其所有 action。

```text
Action {
  id, text, tooltip, icon, color, iconColor, iconStyle, shortcut: string
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

`Shortcut` 声明单个组合键，例如 `Primary+Shift+B`、`Ctrl+Space`、`F6`。`Primary` 在 macOS 表示 Command，在其他平台表示 Ctrl；也可显式使用 `Ctrl`、`Super`、`Shift`、`Alt`。修饰键不能重复，必须在按键之前；名称区分大小写，不支持多段 chord。按键支持 `A`–`Z`、`0`–`9`、`F1`–`F12`、标点（等号、减号、逗号、句点、斜杠、分号、方括号、反斜杠、单引号、反引号），以及 `Escape`、`Tab`、`Enter`、`Space`、`Backspace`、`Delete`、`Insert`、`Home`、`End`、`PageUp`、`PageDown`、`Left`、`Right`、`Up`、`Down`。`ParseShortcut` 可用于校验和无分配解析。非法组合键会拒绝编码或注册。

`Command` 只注册可调用命令，不占按钮区域；隐藏、禁用或已移除的 action 不响应快捷键。文件树／编辑区菜单 action 的快捷键只在对应菜单打开时生效；`OpenWith` 只对匹配当前文件的工具生效，并切换打开方式。工作台内置快捷键优先，扩展之间相同组合键按注册顺序执行第一个可用 action。快捷键沿用 `Handlers.Action` 回调和当前文件／选区上下文；`OpenWith` 仍走编辑工具切换流程。

所有已加载、未隐藏的 action 自动显示在“设置 → 快捷键”，用户可在当前方案覆盖扩展默认组合键，即使扩展未声明 `Shortcut` 也可绑定。清除用户绑定后该方案不再触发该 action，恢复方案默认时重新使用扩展声明。运行期间 `Update` 可以修改或清空 `Shortcut`，用户覆盖优先。

快捷键页面提供“默认”及用户创建的方案 selector。通过名称输入框复制当前方案、新建后修改、重命名或删除；每个方案独立保存，切换立即生效。命令覆盖工作台、编辑器剪贴板和缩进、语言服务、调试、搜索、文件树菜单、终端、端口、源代码管理以及菜单／列表导航；录制时 Backspace 清除，Esc 取消。相同上下文内检查冲突，互斥上下文允许复用组合键。原生文本输入和 PTY 内容输入仍由对应输入层处理。

宿主 `settings.json` 中 `keymap` 保存当前方案名（默认 `default`），`bindings` 保存当前方案映射，`keymaps` 保存其他方案的映射。每条映射包含 `key` 和可选的 `primary`、`control`、`super`、`shift`、`alt`；零值映射表示禁用。扩展覆盖键为完整 action ID `extensions/<扩展 ID>/<action ID>`。未加载扩展的覆盖也会保留；本地及远端设置使用相同保存通路。普通按键匹配与未变化的 action 快照复用缓存，热路径不分配内存。

`ActionPublisher.Update` 对未注册 ID 创建按钮，对已有 ID **完整替换**描述；空字段清除旧值，因此更新时应保留仍需要的字段。`Remove(id)` 移除按钮。每个扩展最多 256 个 action，更新复用已读取的图标资源并遵循资源大小限制。宿主每个会话最多排队 64 次 action/UI 事件，每次只执行一个请求；握手和点击请求超时均为 5 秒，空闲连接没有超时。UI 不等待扩展 I/O。

扩展使用 `ServeExtension`，提供 `Handlers.Action`、`Handlers.UIEvent` 或 `ActivateActions` 即自动注册 `ActionService`。`ActivateActions` 仅在 action 工作台进程启动时执行一次，静态校验和文件编辑进程不会调用它。可以在其中启动后台 goroutine，使用提供的 context 停止任务；publisher 支持并发调用。action 和 editor handler 在其所属进程内顺序执行，多个进程之间不共享内存。

```go
action := peb.Action{
    ID: "build", Text: "构建", Tooltip: "构建当前项目",
    Icon: "resources/icons/build.svg", Location: peb.TitleBarRight,
    Shortcut: "Primary+Shift+B",
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

## 弹窗、上下文菜单、Panel 与通知

`ActionPublisher.Show(peb.UI{...})` 打开或更新扩展界面，`CloseUI(id)` 关闭；`Handlers.UIEvent` 接收控件和生命周期事件。必须注册 `UIEvent`，否则 `Show` 返回 `ErrProtocol`。这些界面复用 action 的常驻进程，I/O、图片读取和资源校验在后台执行。

| Kind               | 行为                                                                                                  |
| ------------------ | ----------------------------------------------------------------------------------------------------- |
| `peb.Dialog`       | 居中的模态弹窗；包含标题、关闭按钮、焦点循环及 Esc 关闭                                               |
| `peb.Notification` | 右下角堆叠通知；包含关闭按钮，不抢焦点，可设置自动关闭及自定义内容与操作                              |
| `peb.Popup`        | 类似扩展市场的非模态浮窗；支持坐标、锚点、宽高与关闭按钮；可操作背后的工作区，焦点在浮窗内时 Esc 关闭 |
| `peb.ContextMenu`  | 锚定 action 的菜单；自动限制在工作区内，支持方向键、Tab、Esc 和点击外部关闭                           |
| `peb.Panel`        | 使用可调整高度的底部工作台面板；支持工具栏关闭、Esc 关闭及切换可见性                                  |

`UI.ID` 属于扩展自身命名空间，遵循 action 本地 ID 规则。`Title` 必须非空，用作界面的可访问名称。`Anchor` 是本扩展的 action ID，打开时留空采用触发 action；文件树和编辑器菜单中的 action 也可作为锚点。也可用 `ui:<surface ID>#<data-ref>` 锚定本扩展另一界面的可聚焦控件（该界面须已打开）。首次打开必须提供 `View`，以后 `View:nil` 保留现有视图和输入状态；非空 `View` 替换 HTML/CSS 和事件绑定。其余描述字段完整替换，同一 ID 的 `Kind` 在关闭前不能改变。

浮层使用 `UI.Placement` 设置位置和大小，单位为工作区 CSS 像素（随工作区缩放）：

| 字段                         | 含义                                                            |
| ---------------------------- | --------------------------------------------------------------- |
| `Mode: peb.PositionDefault`  | Dialog/Popup 居中，ContextMenu 锚定 action；X/Y 是偏移          |
| `Mode: peb.PositionAbsolute` | X/Y 是相对工作区左上角的坐标                                    |
| `Mode: peb.PositionAnchor`   | X/Y 是相对 Anchor action 左下角的偏移；锚点不可用时采用触发位置 |
| `Mode: peb.PositionAnchorAbove` | 在锚点左上方显示，Y 相对菜单底边；上方空间不足时改在下方显示 |
| `Width`, `Height`            | 浮层宽高；0 使用默认大小；负数及非有限数值无效                  |

打开后可发送相同 ID、`View:nil` 更新位置和大小，保留输入、选区和滚动。坐标可为负数，最终位置和大小受工作区边界约束；窗口变化后会重新按请求的位置定位，锚定浮层跟随 action。`Panel` 使用底部布局，`Notification` 使用右下角通知布局，两者 Placement 必须为零值。Popup 的 Tab 使用原生焦点遍历；焦点移到工作区后不会截获工作区快捷键。

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

`Severity` 选择通知风格：`peb.NotificationInfo`（默认普通通知）、`peb.NotificationWarning`（警告）、`peb.NotificationError`（错误）。工作台使用当前主题的通知配色、左侧色条及对应图标区分；错误使用 alert 语义。该字段仅适用于 Notification，每次更新需传入当前级别，省略恢复普通通知。

通知使用相同的 `View` 和 `UIEvent`，支持自定义 HTML/CSS、表单与多个操作按钮。`AutoCloseMS` 仅适用于 Notification：0（默认）持续显示，正数指定自动关闭的毫秒数，关闭时发送 `Reason="timeout"`。更新同一 ID 会重置计时；传 0 取消自动关闭。通知始终可以点击关闭按钮或调用 `CloseUI` 关闭；焦点在通知控件内时支持 Esc，编辑器中的 Esc 不会关闭通知。自动关闭和关闭按钮不会移走其他控件的焦点。

在扩展 UI 中添加 `<button data-ref="logs" data-pencode-action="show-output" type="button">查看日志</button>`，工作台会直接打开 Output 并选中当前扩展的 `Extension: <id>` 通道，清空日志过滤条件。此按钮无需声明 `Events`，点击由工作台处理，不发送 `UIEvent`；禁用和隐藏按钮不触发操作。

```go
err := publisher.Show(peb.UI{
    ID: "build-result", Title: "构建完成", Kind: peb.Notification,
    Severity: peb.NotificationInfo, // 警告和错误分别使用 NotificationWarning / NotificationError。
    AutoCloseMS: 5000, // 省略则一直显示，等待手动关闭或扩展调用 CloseUI。
    View: &peb.EditorView{
        HTML: `<p>构建成功，可以查看结果。</p><button data-ref="open" type="button">查看结果</button>`,
        Events: []peb.EditorBinding{{Ref: "open", Kind: "click"}},
    },
})
// 在 Handlers.UIEvent 中处理自定义操作；是否关闭由扩展决定。
// if e.ID == "build-result" && e.Event.Kind == "click" && e.Event.Ref == "open" {
//     return publisher.CloseUI(e.ID)
// }
```

工作台最多保留 32 个扩展界面（包含通知），同时展示一个弹窗、浮窗或菜单、一个底部 Panel，以及多条堆叠通知。通知独立显示，打开通知不会替换浮层、关闭其他界面或移动焦点。打开新的浮层会关闭原来的浮层，关闭原因为 `replace`。底部面板切换保留扩展视图，返回后继续接收事件。禁用、卸载、重新加载扩展或进程异常退出会移除所属界面，退出后的进程不再接收生命周期事件。

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

输入控件的 `input` 事件携带以 Unicode 字符计数的 `Anchor` 和 `Focus`。设置 `data-pencode-completion="true"` 后，宿主优先将无修饰键的 ↑/↓、Enter/Tab 和 Esc 转为 `completion` 事件，`Ref` 为输入控件的 ref，`Data` 分别为 `previous`、`next`、`accept` 和 `dismiss`，并携带最新文本和选区；无需额外订阅此事件。Shift+Enter 和输入法确认保持原生行为。扩展负责候选的筛选、绘制和应用；插入或替换文本后，可用 `data-pencode-caret="字符位置:修订号"` 恢复光标，每次编辑使用新的修订号。动态增加候选或附件时，应为输入控件及表单指定固定 ID，以保留焦点。

生命周期事件无需显式订阅：

| `e.Event.Kind`     | 内容                                                                             |
| ------------------ | -------------------------------------------------------------------------------- |
| `open`             | 新界面已被宿主接受；同一 ID 的更新不会重复触发                                   |
| `close`            | `e.Reason` 为 `extension`、`button`、`escape`、`outside`、`replace` 或 `timeout` |
| `visibilitychange` | `e.Event.Value` 为 `visible` 或 `hidden`，用于 Panel 切换                        |
| `projectchange`    | 活动项目变化时通知 EditorTab，`e.Context.ProjectPath` 为新项目目录；关闭最后一个项目时为空 |

每个事件附带 `e.ID` 和打开界面时的 `e.Context`（项目、文件路径、目录标记及选区）。EditorTab 收到 `projectchange` 后更新上下文的项目目录，并清空文件路径、目录标记及选区。SDK 在 `Action` 和 `UIEvent` handler 中自动继承当前上下文；后台任务应保存并显式传入 `UI.Context`，避免使用后来触发的 action 上下文。异步控件事件无法同步取消浏览器默认行为，当前不支持 JavaScript 或任意键盘、指针事件订阅。

UI 和 action 请求共享串行队列及 5 秒请求超时，handler 内可以继续推送界面更新；不要在 handler 中等待后续用户事件。空闲轮询和复用缓冲区的编码保持零分配；创建或替换视图、解码字符串与加载资源有必要的分配。

`ActionPublisher.Show` 自动比较带 `data-pencode-region` 的视图。外层结构、CSS 和事件绑定相同时，只传内容变化的区域；新增／删除区域或结构变化发送完整视图。`ViewVersion` 标识结果版本，补丁的 `Base` 必须等于接收端版本，且结果版本等于 `Base+1`。区域替换的是内部 HTML；空字符串清空区域，未列出的区域保持不变，嵌套区域由最外层区域一起更新。`View` 与 `Patch` 互斥，补丁不能与 `Close` 同时使用。

宿主使用每个 action 会话独立的 `peb.ViewReceiver` 在 I/O worker 重建原始视图，然后沿用资源处理和 UI 校验。版本不匹配时请求内部 `resync` 事件；SDK 自动重发最新完整视图，不交给扩展业务回调。关闭界面释放差分基线，进程重连后首帧为完整视图。发送失败不会推进基线，重复或乱序补丁不应用。每个会话最多保留 32 个界面基线，重建结果仍受完整帧大小限制。

调用方提交的视图字符串必须保持不可变。此优化减少传输量；扩展仍生成完整视图，宿主仍重建并校验完整视图。新增 HTML tokenizer 依赖后，仓库内独立扩展模块的 `go.mod`／`go.sum` 已同步；所有扩展需使用当前 SDK 重建。

UI payload 的字段顺序如下。`ActionContext` 与 `ActionInvoke` 编码一致，`EditorEvent` 与 `EditorRequest` 的事件部分一致，bool 使用严格的 u32 0/1。

```text
UIUpdate {
  id, title, anchor: string
  kind: u32                     // Dialog=0, ContextMenu=1, Panel=2, Popup=3, Notification=4, EditorTab=5
  close: bool
  placementMode: u32            // Default=0, Absolute=1, Anchor=2, AnchorAbove=3
  x, y, width, height: f32      // IEEE 754，小端
  closeOnOutsideClick: bool    // Popup 点击外部是否关闭，默认 false
  autoCloseMS: u32             // Notification 自动关闭毫秒数，0 持续显示
  severity: u32               // Notification: Info=0, Warning=1, Error=2
  busy: bool                  // EditorTab 关闭图标切换为处理动画
  context: ActionContext
  hasView: bool
  // hasView=true: html: text, css: text, events: array<{ref: string, kind: string}>
  viewVersion: u32
  hasPatch: bool
  // hasPatch=true: base: u32, regions: array<{ref: string, html: text}>
}
UIRequest {
  id, reason: string
  context: ActionContext
  event: EditorEvent
}
UIResponse {}                   // 成功应答；错误使用 Failure
```

可构建的完整示例见 [UI Demo](../extensions/ui-demo/main.go)，包含 action 菜单、表单弹窗、自由定位浮窗、点击外部关闭选项、输入与提交事件、Panel 更新、通知自定义操作、自动关闭和关闭事件。

协议版本 15 为 UIUpdate 增加 `severity:u32`；宿主与扩展需要使用相同版本的 PEB 重新构建。运行 `make install-extensions` 会通过临时 Go workspace 使用当前仓库的 SDK 构建所有扩展，扩展源目录的模块文件保持不变。

## 调试工具

`Contributions.DebugAdapters` 为 workspace server 提供调试启动命令。Go 扩展注册 `Delve`，以自身的 `--tool dlv` 启动器按需安装工具。显式配置的 command 优先；同一 adapter ID 有多个提供者时返回冲突。禁用、卸载和断开连接会取消启动并回收对应适配器进程。

`DebugAdapter.Templates` 是可选的 `.pencode/debug.json` 数组文本，每项须为有效配置，且 `adapter` 与该适配器 ID 一致。工作台的“添加启动器”从已加载扩展展示模板，选中后将单项追加写入项目文件并打开编辑；注册和添加模板不会启动调试。模板中的 `${workspaceFolder}` 原样保留到启动时解析。Go 扩展提供项目运行和包测试模板，PHP 扩展提供调试当前文件和监听 Xdebug 模板。

`DocumentSelector` 在 match、pattern 后增加 `languageId:string`，为空则继承 LanguageServer.LanguageID，允许同一 gopls 会话分别用 go、go.mod、go.work 打开文档。

### 独立编辑器标签页

`peb.EditorTab` 为会话、工具结果等不对应文件的视图创建编辑器标签页，`Title` 同时作为标签标题。同一扩展内的 `ID` 标识独立标签，`Placement` 必须为零值。事件继续通过 `UIEvent` 发送。

设置 `Busy:true` 时，关闭图标替换为主题色的点阵动画，后台标签同样显示动画，点击仍发送关闭事件。任务结束后更新为 `Busy:false` 恢复关闭图标。每次更新需传入当前忙碌状态；扩展可在关闭事件中中断任务并退出会话。

```go
publisher.Show(peb.UI{
    ID: "conversation-123", Title: "实现搜索功能", Kind: peb.EditorTab,
    View: &peb.EditorView{HTML: `<article><p>聊天记录</p></article>`},
})
```

首次创建自动选中。带 `View` 的更新替换记录并更新标题，保留当前标签选择；更新后台会话不会抢走正在编辑的文件。发送相同 ID、`View:nil` 则主动选中已有标签。切换时发送 `visibilitychange`，关闭标签时发送 `close`；扩展卸载会清理其标签。标签页关闭不要求扩展终止后台任务，扩展可自行管理会话持久化和任务生命周期。

扩展 UI 的 CSS 支持与自定义编辑器相同的 `var(--vscode-...)` 主题变量，随 IDE 主题变更重新解析。
