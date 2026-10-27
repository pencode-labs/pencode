# Pencode CS v7

GUI 和无头服务通过有序双向字节流通信。本地传输是子进程管道，远端传输是 OpenSSH 加密的 stdin/stdout；不监听或转发服务端端口，协议与具体传输无关。所有整数采用小端编码；不传递 Go 指针或渲染对象。旧版本不兼容。

每帧头部固定 24 字节：

| 偏移 | 长度 | 内容 |
| --- | --- | --- |
| 0 | 4 | `pcs\0` |
| 4 | 2 | 协议版本，当前 7 |
| 6 | 2 | Kind，见 protocol.go |
| 8 | 8 | 非零 request ID |
| 16 | 4 | 保留的 stream ID，当前必须为 0 |
| 20 | 4 | payload 字节数，最大 64 KiB |

payload 中的字符串为 `u32 长度 + 原始字节`，数组为 `u32 元素数 + 元素`，字段按序排列，不允许尾随字节。Reader 返回的 payload 借用其缓冲区，仅在下一次 Read 之前有效。大请求由同一 request ID 的若干 Data 帧和最终请求帧组成，大响应由 Data 帧和最终 Reply 帧组成，组装总量上限 64 MiB。客户端最多保留 64 个未完成请求，上传及未完成响应分别限制为 128 MiB 逻辑 payload；已取消但仍由写协程持有的上传继续计费，上传超限返回 Busy，响应超限关闭连接。不同请求的分块可以交错；客户端在大上传块之间轮转调度普通请求，取消优先。服务端未完成的上传合计上限 128 MiB。

v7 使用 peb v8 注册格式，增加扩展调试适配器和文档语言覆盖；保留调试适配器与调试终端服务，GUI、server 与扩展需同时更新。文本搜索匹配按 `start:u32, end:u32, line:u32, column:u32, previewStart:u32, preview:string` 编码。`previewStart` 是预览在原文中的 UTF-8 字节偏移，供客户端准确高亮截断预览中的命中。

首个请求必须是空 Hello，服务端返回空 Reply。之后支持：

| 请求 | payload | Reply |
| --- | --- | --- |
| WorkspaceOpen | path:string | id:u64, path:string, name:string |
| WorkspaceClose | id:u64 | 空 |
| DirectoryRead | workspace:u64, path:string | count:u32, {name:string, mode:u32}[] |
| DocumentOpen | workspace:u64, path:string, text:u32 | id:u64, version:u64, content:string |
| DocumentChange | id:u64, baseVersion:u64, start:u32, end:u32, text:string | version:u64 |
| DocumentSave | id:u64, version:u64 | 空 |
| DocumentClose | id:u64 | 空 |
| DocumentMove | id:u64, workspace:u64, path:string | 空 |
| ResourceCreate | workspace:u64, path:string, directory:u32 | 空 |
| ResourceRemove | workspace:u64, path:string | 空 |
| ResourceRename | workspace:u64, from:string, to:string | 空 |
| ResourceTransfer | source:u64, target:u64, from:string, directory:string, flags:u32 | destination:string |

text/directory 只接受 0/1；ResourceTransfer flags 的 bit 0 表示移动，bit 1 表示碰撞时生成副本名称。DocumentMove 在文件系统移动完成后重定位打开的文档，保留其未保存缓冲和磁盘基线。二进制文件打开只校验普通文件，返回零文档 ID 和空内容。

路径相对于工作区，采用 `/`，禁止绝对路径、`..`、反斜杠和 NUL；DirectoryRead 额外允许 `.`。工作区根目录由服务端规范化并通过 os.Root 持有；WorkspaceOpen 的 `~` 和 `~/...` 在服务端展开，绝不使用 GUI 的 home。文档初始版本为 1，每个有效 Change 递增一次；范围为 UTF-8 字节边界。每份 GUI 增量作为一个 DocumentChange 发送，可包含多个 Data 帧，只在完整请求到达后原子应用并确认一次版本；合并 GUI 编辑时不得跳过版本确认。文档预算为单份小于 64 MiB、每连接文本总量 128 MiB、128 个文档及 32 个工作区；这些是文本预算，不等于进程 RSS 上限。

失败使用 Failure 帧：`code:u32 + message:string`。错误码为 Invalid、NotFound、Conflict、VersionMismatch、Busy、IO。协议头错误会关闭连接。文档与文件资源操作按完整请求的接收顺序执行，Service 长任务最多并发 48 个，任务持有的请求 payload 合计不超过 128 MiB。`Cancel` 使用被取消请求的 ID 和空 payload，中断对应任务并丢弃未完成上传；客户端超时后发送取消，忽略迟到响应。独立响应写协程在 64 KiB 分块间轮转发送，避免大下载阻塞接收请求或独占响应链路；队列及发送中最多 64 个响应、缓冲容量合计最多 128 MiB，超限对生产者施加背压。取消会丢弃已排队响应的剩余分块，正在发送的一帧允许完成。此预算不包含服务构建结果、文档存储和缓冲池的额外内存。已经执行的文件修改不能回滚，不允许自动重放。当前语法解析在一次解析完成后检查取消，工作区关闭撤销派生的 Git 根并回收相关流与索引。连接关闭释放所有连接资源，协议不保留可跨连接使用的会话或句柄。连接管理器只能重新打开工作区和文档，检查磁盘基线后恢复 GUI 内存中的编辑；禁止重放保存、资源修改、Git 写入或终端输入。

## Service

`Service` payload 为 `operation:u32 + root:string + operation payload`。根目录只接受本连接打开的工作区或通过 Git 查询得到的仓库根；基于句柄的流、索引和解析操作的 root 为空。DirectoryBrowse 的 root 为空，用于选择项目；directory 接受服务端路径与 `~`，返回规范化路径、上级路径和按名称排序的子目录（含指向目录的符号链接），不创建工作区或改变已有文档。进程运行、路径规范化、PATH 查找均在服务端执行。响应仍使用 Reply/Data/Failure。

ConfigurationRead 与 SettingsSave 的 root 为空，分别读取和原子保存服务端用户配置目录下的 `pencode/resources`。读取返回目录路径及相对文件名和字节内容，包含 `settings.json`、`highlights.json` 与 `themes/` 下的 JSON/JSONC 文件；总量限制 16 MiB、4096 个文件。保存只接受 JSON 对象并替换 `settings.json`，不接受文件名或目录参数。GUI 在打开远程项目时使用这份配置，系统字体仍由 GUI 主机提供。

WorkspaceWatch 首次调用安装根目录的递归文件事件监听，并返回两个变化位用于初始快照；后续调用阻塞到有事件或请求被取消。目录树与 Git 变化分别按事件时间限频，每秒最多通知一次，窗口内的密集事件允许丢弃，不使用定时器补发。跳过 `node_modules`、`dist` 以及 Git 的对象、日志、hooks 目录和锁文件；工作区关闭或连接断开时释放监听。客户端仅在事件到来后刷新，已有扫描与 Git 任务期间的变化合并成一次待刷新状态。

| operation | 参数 | Reply |
| --- | --- | --- |
| DirectoryBrowse | directory:string（root 为空） | path:string, parent:string, count:u32, {name:string}[]（只含子目录） |
| ConfigurationRead | 空 | directory:string, count:u32, {path:string, data:bytes}[] |
| SettingsSave | data:bytes（JSON object） | 空 |
| GitCommand | args:string[] | output:bytes |
| GitRepositories | 空 | count:u32, {root:string}[]（项目及其子目录内的仓库） |
| WorkspaceWatch | 空（root 为工作区目录） | changes:u32（位 0：目录树；位 1：Git 状态） |
| FileRead | path:string, links:u32, limit:u32 | content:bytes |
| FileSave | path:string, diskBaseline:string, result:string | 空 |
| DiffPatch | left:bytes, right:bytes | unifiedPatch:bytes |
| ProcessOpen / TerminalOpen | cols:u32, rows:u32, command:string, args:string[] | stream:u64, stderrStream:u64 |
| DebugAdapterOpen | adapter:string（Delve / stdio / tcp）, command:string, address:string, args:string[] | stream:u64, stderrStream:u64 |
| DebugTerminalOpen | cwd:string, args:string[], count:u32, {key:string, present:u32, [value:string]}[] | pid:u32, stream:u64, stderrStream:u64（零） |
| StreamRead | stream:u64 | eof:u32, data:bytes |
| StreamWrite | stream:u64, data:bytes | 空 |
| PortList | 空（root 为空） | count:u32, {port:u32, host:string, pid:u32, process:string}[] |
| PortConnect | port:u32, host:string（root 为空，远端 TCP 目标） | stream:u64, stderrStream:u64（零） |
| StreamCloseWrite | stream:u64（TCP 流） | 空；关闭写方向，保留读取响应 |
| StreamResize | stream:u64, cols:u32, rows:u32 | 空 |
| StreamClose / StreamWait | stream:u64 | 空；Wait 非零退出码为 Failure |
| IndexOpen | 空 | index:u64 |
| IndexNext | index:u64, offset:u32 | done:u32, warning:string, count:u32, {path:string, name:string, depth:u32, directory:u32}[] |
| IndexClose | index:u64 | 空 |
| PathSearch | index:u64, query:string, preferred:string[], rules:{pattern:string, suffix:u32}[] | paths:string[] |
| SyntaxAnalyze | parser:u64, baseVersion:u64, filename:string, start:u32, end:u32, insert:string | version:u64, language:string, captures（见下） |
| SyntaxRelease | parser:u64, trim:u32 | 空 |
| ExtensionList | 空 | directory:string, count:u32, ExtensionInfo[] |
| ExtensionLoad | id:string | handle:u64, ExtensionInfo, contributions:bytes, diagnostics:string |
| ExtensionBegin | 空 | upload:u64 |
| ExtensionWrite | upload:u64, path:string, offset:u64, mode:u32, final:u32, data:bytes | 空；data 最大 32 KiB |
| ExtensionPrepare | upload:u64 | handle:u64, ExtensionInfo, contributions:bytes, diagnostics:string |
| ExtensionResources | handle:u64, limit:u32, count:u32, path:string[] | count:u32, {path:string, data:bytes}[]；返回请求路径的有序前缀 |
| ExtensionResource | handle:u64, path:string, limit:u32 | data:bytes |
| ExtensionEnable | id:string, enabled:u32 | enabled 时返回待提交注册；禁用时返回 ExtensionInfo |
| ExtensionCommit | handle:u64 | ExtensionInfo |
| ExtensionAbort | handle:u64 | 空 |
| ExtensionRemove | id:string | 空 |
| ExtensionProcess | handle:u64 | stream:u64, stderrStream:u64 |
| ExtensionLanguageProcess | handle:u64, contributionID:string | stream:u64, stderrStream:u64 |

所有布尔值只取 0/1。`FileRead links=1` 返回 Git 工作树中符号链接自身的文本，并把缺失文件视为空；普通源码读取跟随工作区内的链接，通过 os.Root 拒绝越界。`FileSave` 检查磁盘原文，写临时文件并替换。TerminalOpen 不使用 command/args，stderrStream 为零，因为 PTY 合并输出。

每条流有八个 32 KiB 服务端缓冲，输出窗口最多 256 KiB，按需接收并施加背压；StreamRead 等待首批数据后，立即合并已有数据返回（最多 256 KiB），不等待填满窗口；StreamWrite 最大 256 KiB，客户端串行发送完整 Write 内的批次，保持并发写入的消息顺序；每连接最多 16 条流（普通进程占 stdout、stderr 两条，PTY 占一条）。流关闭会终止并回收进程；正常连接退出先让服务端清理资源，再由传输层执行超时终止。标准流保持 LSP 原始 JSON-RPC，工作台收到的双向请求语义保持不变。

端口转发复用连接内的流传输，每个 TCP 连接占一条流；创建 TCP 流时总流预算为 32 条，创建进程时仍为 16 条。每个转发最多并发 16 个 TCP 连接，工作区最多 128 项转发。PortConnect 接受合法主机名或 IP 地址，使用服务端 DNS 与网络环境连接目标；主机和端口直接传给 TCP dialer，不经过 shell。本地默认监听 `127.0.0.1`，用户可以为单个端口切换为监听所有网络接口。

PortList 在 Linux 读取 `/proc/net/tcp{,6}` 并关联 `/proc/<pid>/fd`、`cmdline`，在 macOS 使用 `lsof`；返回当前用户监听端口的地址、PID 与进程信息。Ports 面板始终启用基于进程的自动转发，每 3 秒刷新，跳过 SSH 端口和无法确定所属进程的监听。远端停止监听时回收自动转发，手动停止的端口会被抑制，直至检测到监听消失。无变化的前台轮询不分配内存或重建行。

Ports 面板按 VS Code Remote SSH 的端口表格交互组织：行内添加端口号或 `host:port`，显示标签、转发地址、协议、运行进程、可见性和来源。工具栏的 ＋ 按钮用于添加转发；协议列点击切换 HTTP/HTTPS，可见性列点击切换本机/局域网；行内按钮提供复制地址、浏览器打开、更改本地端口、设置标签和停止转发，工具栏提供刷新。新建或断线重连时优先使用指定本地端口，占用时自动分配其他端口；更改本地端口先创建新监听，再释放旧监听，目标端口被占用时提示失败并保留原转发。输入支持 Enter 确认、Escape 取消；选中行支持上下导航、F2 修改标签、Delete 停止转发。

转发状态仅保留在当前会话内。临时断线重连保留当前会话的端口、实际本地端口、标签、协议与可见性；重新打开工作区时重新检测监听端口。连接断开、切换工作区或退出时关闭本地监听及活跃 TCP 连接。Ports 表头固定，只有端口列表纵向滚动。

索引每页最多 256 项，每工作区最多 262144 项，目录深度最多 256；不递归符号链接，跳过 `.git`、`dist`、`node_modules`。同根的新索引替换旧扫描。PathSearch 按历史顺序优先、随后按树顺序返回最多 128 个可打开文件，支持不区分大小写的子串和自定义编辑器规则；扫描期间可以查询已索引部分。

解析会话由客户端分配 ID，每连接最多 8 个，初始 baseVersion 为 0。增量范围采用 UTF-8 字节边界；结果只包含语言和 capture，不含颜色、字体、HTML、ECS 或 GPU 数据。captures 编码为 `prefix:u32, removed:u32, inserted:u32 + {start:u32, end:u32, captureID:u32, [name:string]}[inserted]`。保留上次结果的前 prefix 项，将之后的 removed 项替换为传回的 inserted 项，并将剩余后缀的起止位置加上本次源码字节长度变化。服务端比较解析后的真实 capture，允许多行注释等语法变化扩大替换范围；初次返回完整列表。名称表在解析会话内保留，captureID 从零递增；ID 首次出现时紧跟名称，后续复用 ID。释放会话后清空 capture 和名称表。本地按主题和 capture 映射着色，主题切换不发送解析请求。

`ExtensionInfo` 按顺序编码为 `id:string, name:string, version:string, publisher:string, description:string, entry:string, args:string[], disabled:u32, path:string, problem:string`。路径属于服务端文件系统；GUI 仅用于展示，不据此读取或执行。contributions 使用现有 peb v8 编码，CS 与 peb 的帧版本独立。

扩展安装是单连接事务：同时最多两个暂存包；文件路径必须是有效相对路径，拒绝符号链接和特殊文件，单包最多 256 MiB / 4096 项。Prepare 在暂存目录执行 peb 注册；GUI 完成字体、主题和图标验证后调用 Commit 发布，失败调用 Abort 清理。禁用、卸载和连接关闭撤销资源句柄并回收扩展进程，未提交的暂存包随连接释放。禁用扩展的 Load 返回零句柄，不执行扩展或占用注册预算。

资源单份最大 32 MiB，一个扩展最多缓存 64 MiB，连接内扩展缓存合计最多 128 MiB。ExtensionResources 的 limit 是每份资源的字节限制，最多请求 4096 条不重复路径；返回请求的有序前缀，响应目标大小为 16 MiB，单份超过此值时单独返回。客户端继续请求剩余路径直到取完。GUI 缓存已验证的图片；每视图最多 64 张，单张 4 MiB、总计 8 MiB / 4 MP，每边不超过 2048 像素。自定义编辑器 SVG 按原始尺寸栅格化，避免图标采样倍率扩大图片预算。

ExtensionLanguageProcess 的 root 必须是本连接工作区，可执行程序中的相对路径由服务端基于已发布的扩展目录解析，cwd 为工作区。action 进程允许空 root，生命周期属于连接。
