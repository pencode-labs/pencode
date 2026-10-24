# Pencode LSP

此包实现独立于传输的 LSP JSON-RPC 客户端，生产环境通过远程 stdio 流通信；扩展通过 [peb.LanguageServer](../peb/lsp.go) 声明服务，宿主管理生命周期。协议参考 [LSP 3.17](https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/)。

## 扩展接入

一个扩展可以贡献多个服务。每个服务提供 ID、展示名称、languageId、文件名/后缀 selector、启动程序和参数，以及可选的 initializationOptions / configuration。示例见 [Go 扩展](../extensions/go/main.go) 和 [peb 协议](../peb/README.md#语言服务扩展)。服务可使用服务端系统 PATH 内的程序，或扩展内附带的可执行文件。宿主不会自动安装语言服务器。

服务按需启动，一个项目根目录和服务贡献共享一个进程。扩展禁用、移除、项目关闭和最终视图销毁都会停止服务。初始化最多 10 秒、普通请求最多 30 秒、退出宽限 1 秒；请求取消不关闭服务。失败会显示在工作台状态中，可通过禁用再启用扩展重新启动。

## 协议接口

- `Reader` / `Writer`：Content-Length 分帧，处理拆包、短写、UTF-8 编码和大小限制；缓存后的分帧零分配。
- `Conn.Call` / `Conn.Notify`：任意标准或自定义方法；保留 raw JSON、字符串/数字服务端 ID、结果联合类型和结构化错误。没有固定方法白名单。
- `Conn.BeginCall` / `PendingCall.Wait`：拆开发送与响应等待。`BeginCall` 返回前已将请求放入有序发送队列，调度器可继续发送文档通知；每次成功调用后必须执行一次 `Wait`，包括取消的请求，以清理等待状态。
- `Request[P, R]`：泛型请求接口，按扩展实际使用的参数、结果类型解码。
- `Handler`：服务端请求与通知；响应读循环独立于处理器，处理器可以发起嵌套请求。未知请求返回 MethodNotFound，未知通知无需响应。
- 请求 context 取消发送 `$/cancelRequest`；服务端取消会取消相应 handler context。连接断开释放所有等待请求并关闭进程管道。
- `Client`：initialize / initialized、shutdown / exit、真实服务能力、UTF-16/UTF-8/UTF-32 编码协商、配置查询/更新、workspaceFolders、动态能力注册/注销，以及 workDoneProgress/create。初始化 context 只约束初始化。
- `SyncDocument` / `CloseDocument` / `WillSave` / `DidSaveText`：依服务端 textDocumentSync 发送打开、完整/增量修改、关闭和保存。版本单调递增，增量修改保持 UTF-8 rune 和 CRLF 边界。
- `ApplyTextEdits`：先检查所有范围和重叠，再一次生成结果；位置转换只正向扫描文档一次。同位置插入保持顺序。
- `DecodeSemanticTokens` / `ApplySemanticDelta`：检查相对位置、token 类型、字符边界和增量编辑。

```go
stream, err := backend.OpenProcess(ctx, projectPath, "my-language-server")
// 检查 err；进程由 pencode-server 持有。
client, err := lsp.Connect(ctx, lsp.Options{
    Root: projectPath,
    Handler: handleServerMessage,
}, stream)
// 检查 err；应用退出时用独立、有期限的 context 调用 client.Shutdown。
result, err := lsp.Request[lsp.TextDocumentPositionParams, json.RawMessage](
    ctx, client.Conn, "textDocument/hover", params,
)
```

初始化 processId 为 null，避免把 GUI 的本地 PID 传给另一台机器上的语言服务；进程生命周期由服务端管道控制。文件 URI 使用服务端路径，POSIX 绝对路径和 Windows 盘符路径的编码、解码不依赖 GUI 所在系统；解码统一返回 `/` 分隔路径。SSH 重连后原 LSP 进程和句柄失效，按需创建新进程并同步当前 GUI 文档。

`Options.Capabilities` 可以递归扩展初始化能力。调用方自行定义高级或自定义请求的参数/结果类型，处理对应服务端回调；包括 notebook、inlineValue、linkedEditingRange、onTypeFormatting、文件操作和自定义命令等。`$/progress` 中的 work done 和 partial result 通知会原样交给 Handler。能力声明必须与调用方实际处理的功能一致。

## 原生工作台

工作台按服务端声明选择 provider，后台通信不执行渲染或直接修改文档。结果通过有界队列回到 UI 线程。支持的操作包含补全与 resolve、悬浮信息、参数提示、定义/声明/类型/实现/引用跳转、文档/工作区符号、重命名、文档/选区格式化、代码操作及 resolve/executeCommand、Code Lens、调用/类型层级、选择范围、文档高亮位置与诊断。

push 诊断按 URI 和版本保存，文档修改清除旧诊断；也可手动请求 pull 诊断。语义 token 自动请求 full / delta / range，增量解码后与现有语法颜色合并；改变主题重建颜色，不重复请求服务；仅保留当前原生文档的语义绘制缓存，最多 65536 个 token。空闲轮询和缓存后的语义颜色读取不分配。

多文件 WorkspaceEdit 在所有文档通过版本、范围校验后才发布；未打开的文件成为未保存标签，所有编辑保留撤销和未保存提示，不直接写磁盘。资源 create/rename/delete 不在原生 workspaceEdit 能力声明中，会明确返回失败。保存传递实际保存快照；服务注册文件监听后，扩展可以用通用 `Conn` API 实现其声明的文件通知。

内嵌提示、折叠区域、文档链接目前通过结果面板展示协议数据。工作台未提供 notebook 编辑、代码折叠布局、snippet 占位符交互、外部 URL 打开、逐字符格式化、文件系统监听和资源文件操作，也不会宣称这些能力。原生补全仅声明纯文本插入，语义颜色使用已有主题 scope；不解析 semanticTokenColors。扩展自定义编辑器可以使用此协议包补充其所需界面和方法。

## 验证

在仓库根目录运行串行定向检查，不启动编辑器截图：

```sh
go test -p 1 ./lsp ./peb ./extensions/... ./workbench .
go vet -p 1 ./lsp ./peb ./extensions/... ./workbench .
git submodule foreach 'GOWORK=off go test ./... && GOWORK=off go vet ./...'
```

测试包含独立假服务进程、双向请求、初始化期间的配置查询、取消和断开、Unicode/CRLF 随机增量修改、事务编辑、过期结果、卸载回收及零分配检查。若 PATH 有 `gopls`，还会运行真实服务的格式化、定义跳转和工作台生命周期测试；无 gopls 时只跳过这两项。
