> 历史实现说明 / Historical implementation notes
>
> 本文保留项目从 Onix 仓库迁出前的设计、API 示例和验证记录。
> 构建与验证命令已按当前独立仓库布局调整，默认从 Pencode 仓库根目录执行；
> Onix 引擎测试单独在相邻的 Onix 仓库运行。性能与验证结论仅对应当时的实现。
> 当前项目介绍、环境要求和已知限制见 [中文首页](../README.zh-CN.md)
> 与 [English README](../README.md)。协议细节请以对应包文档和源码为准。

# Pencode

## 客户端／服务端迁移

第六阶段已完成远程文件操作异步化和定向验证。本地与 SSH 共用 CS 链路。本地 GUI 自动启动当前可执行文件的 `--pencode-server` 无头模式；远端通过系统 OpenSSH 安装并启动独立 `pencode-server`，二者均通过二进制 stdin/stdout 连接；索引、Git、PTY、LSP、解析器以及扩展宿主均由 server 持有。无头入口在窗口和工作台初始化之前运行。独立程序可直接构建：

```sh
go build -p 1 -o /tmp/pencode-server ./cmd/pencode-server
```

| 模块 | 职责 |
| --- | --- |
| `workbench` | 本地文本缓冲、撤销、光标、VT 屏幕、主题着色、布局与渲染 |
| `client` | 类型化服务、文档增量邮箱、远程进程流、索引与解析代理 |
| `protocol` | [CS v3](../protocol/README.md) 帧、分块、请求 ID、取消与预算 |
| `transport` | 拥有并回收子进程管道；正常关闭先发送 EOF，超时才终止 |
| `remote` | OpenSSH 平台探测、摘要验证、原子安装与加密标准流 |
| `connection`、`workbench/connection.go` | 环境选择、连接状态、重试与只读恢复；GUI 保留未保存内容 |
| `server/server.go` | 有序文档操作与有界后台任务调度 |
| `server/extensions.go`、`server/extensionhost` | 扩展发现、暂存安装、注册探测、资源缓存、action/editor/LSP 进程管理 |
| `server/workspace` | 工作区、版本化文档、保存冲突检查及文件操作 |
| `server/index.go` | 递归索引、分页文件树和文件名搜索，包括历史排序与扩展文件规则 |
| `server/git.go`、`server/files.go` | Git 命令、差异计算、源码预览读取、合并结果保存 |
| `server/process.go`、`server/stream.go` | LSP stdio、独立 stderr、PTY、尺寸调整、退出码与进程回收 |
| `server/syntax.go` | 增量语法树和与渲染无关的 capture 区间 |

独立服务不依赖窗口、ECS、HTML 或 GPU。stdout 只传协议，服务诊断写 stderr；语言服务和扩展 stderr 使用独立的有界流传回 Output。LSP 的协商、通知和双向请求继续使用原有协议库，其可执行文件查找与进程运行发生在服务端。扩展安装目录、注册与扩展内相对命令也由 server 解析。

通信使用单条可替换字节流：文档编辑和解析更新发送 UTF-8 字节增量，文件树每页最多 256 项；语法 capture 返回变化区间并复用未变化的前后缀，名称在解析会话内去重，本地换主题不请求服务端。扩展资源按贡献路径去重、批量预取，响应按约 16 MiB 分页，连接内缓存合计最多 128 MiB；常规图标包的数千图标可合并成一次 RPC。GUI 只在用户选择本地扩展源时读取源目录，文件按 32 KiB 分块上传到 server 暂存区，注册探测和 GUI 资源校验通过后发布。大消息分帧发送并轮转让出写入机会，取消优先发送。PTY、LSP 和扩展进程按需接收，服务端每条流保留最多 256 KiB 输出窗口，输出施加背压，不丢弃 VT 或 JSON 字节。长任务与文档操作分开调度，终端等待输出不阻塞保存。索引查询、源码预览和悬浮提示异步提交，过期结果由 UI 拒绝。

本地文本缓冲和撤销栈继续由 GUI 持有，输入不等待 IPC；每个文档只保留最新待发送快照。保存等待增量确认，检查磁盘摘要后通过临时文件替换，失败保留未保存状态。打开、保存、重载、资源变更与 LSP 工作区编辑由单一有界后台队列串行执行，UI 只接收结果并校验项目、文件身份及编辑版本。保存提交不可变内容快照；保存期间继续输入的内容留在编辑器并保持 Dirty，关闭确认等保存结果后再继续。重载期间发生的新编辑不会被覆盖，旧文档句柄在后台退役，后续保存仍检查原磁盘基线。过期打开结果不会抢回焦点或替换预览文件；资源变更与文档移动在同一工作队列保持次序。快速打开查询文件名；当前文档的查找替换保留在本地。文件树在 GUI 中保留路径元数据和展开行索引，仅挂载视口及预取范围内的行组件。编辑、语义高亮和终端输出只更新对应绑定，结构变化执行完整渲染；diff 行使用紧凑状态并交换复用后台重算缓冲，括号、链接和输入法预编辑绘制按版本缓存。

服务端句柄属于连接，断线后释放工作区、索引、解析树、PTY、LSP 与扩展进程。连接管理器重新打开工作区与文档，恢复 GUI 的未保存文本和撤销历史；不自动重放写操作。取消是尽力中断，已完成的修改不能回滚；语法解析器在当前解析结束后检查取消。文件任务队列最多保留 32 个用户任务，UI 每帧最多接收 4 个结果；有序工作队列还串行化工作区释放，避免关闭后立即重开时旧清理任务关闭新会话。语言服务器的工作区编辑要等待读取并在前台检查所有目标仍匹配后才整体提交，失败或过期结果不会部分改写缓冲区。

输入提交、稳定空闲轮询、预热后的帧编解码与本地重着色有零分配验证；RPC、快照、索引存储、解析及扩容仍会分配。服务有显式文本、索引、并发与流预算，这些不等于进程 RSS 上限。

定向验证，不启动编辑器或截图测试：

```sh
go test -p 1 ./protocol ./client ./server ./server/extensionhost ./server/workspace ./extensions ./transport ./remote ./connection ./lsp ./terminal
(cd ../onix && go test -p 1 ./syntax)
go test -p 1 ./workbench -skip '^TestSourceControlPencodeMergeEditor$'
go test -race -p 1 ./protocol ./client ./server ./server/extensionhost ./server/workspace ./extensions ./transport ./terminal
```

`TestSourceControlPencodeMergeEditor` 在迁移前的基线也存在“未显示合并警告”的失败，单独保留跟踪。

## 查找与替换

打开文本文件后，`Ctrl+F` 打开查找，`Ctrl+H` 打开替换（macOS 使用 `Cmd+F` / `Cmd+Option+F`）。查找栏支持大小写、全词、正则、选区范围、匹配高亮与循环导航；替换支持保留大小写、单次替换和全部替换，均进入文档撤销历史。查找输入框中 Enter / Shift+Enter 跳转下一处 / 上一处，替换输入框中 Enter 替换当前匹配，Ctrl/Cmd+Enter 全部替换；Esc 关闭并返回编辑器，F3 / Shift+F3 导航。Alt+C / W / R / P 切换大小写、全词、正则、保留大小写。

文件树右键的 **Find in Folder...** 打开所属项目和所选目录范围的文本搜索弹窗，文件节点使用父目录。`Ctrl/Cmd+Shift+F` / `Ctrl/Cmd+Shift+H` 在当前项目根目录搜索 / 替换。弹窗支持搜索模式与替换模式切换、结果预览、方向键选择、分页和 Enter 打开匹配位置。搜索优先使用已打开文件的未保存内容，在后台通过同一服务协议执行，本地和远端采用相同流程。

弹窗最多展示前 2000 个匹配，但全部替换覆盖完整搜索结果。已打开文档保留为可撤销的未保存更改，未打开文件写入磁盘；替换前校验搜索摘要，写入时再次检查磁盘内容，前台应用前检查文件身份和缓冲区版本。批量操作按文件报告失败，已完成的文件不会因其他文件失败而回滚。搜索跳过 `.git`、`node_modules`、`dist` 子目录、符号链接、二进制内容和超过 2 MiB 的未打开文件，并显示无法读取、非文本和过大文件的跳过数量。单次搜索最多匹配 65536 个文件，超限提示缩小范围。

正则使用 Go 的 RE2 语法，不支持 JavaScript 的环视与模式反向引用；错误直接显示在查找栏或弹窗。替换支持 `$1`、`${name}`、`$&`、`$$` 与正则模式下的 `\n` / `\r` / `\t`。

## 弱网与传输预算

文档每次增量只提交一个版本化请求，大段粘贴通过 64 KiB Data 帧传输，收到一次确认后才推进版本；保存另有一次确认。以 1 MiB 插入为例，由原先 32 次修改加一次保存，减少为一次修改加一次保存。接收完整修改请求前不会应用部分文本，上传取消不会留下半段粘贴；已经执行的修改仍不能撤销或重放。

服务端使用独立写协程，在大响应分块间轮转发送其他响应，接收协程可以继续处理文档及资源请求。取消会丢弃已排队响应的后续分块，正在发送的一帧允许完成。排队及发送中的响应合计最多 64 个，缓冲容量合计最多 128 MiB；超出时对生产者施加背压，断线会解除等待。小响应缓冲复用，大于 1 MiB 的缓冲用后释放。服务执行时正在构建的结果、文档存储及池缓存另有开销，此预算不代表整个进程的内存上限。

进程 stdout、stderr 与 PTY 每条流保留最多 256 KiB 输出窗口，单次读取合并已经就绪的数据；首批字节到达即返回，不为填满窗口而延迟。写入同样按最多 256 KiB 分批，并按完整 Write 串行，避免并发 LSP 消息的分块交错。仍只有一个未完成读取，空闲 shell 不会持续轮询或占用发送带宽。不同流通过原有 request ID 复用同一链路。

客户端上传和未完成响应分别限制为 128 MiB 逻辑 payload。已取消、但写协程尚未释放的上传仍计入预算，避免慢连接上重复超时堆积副本；上传超限返回 Busy，响应组装超限关闭连接。文本快照及编码过程仍有分配，输入提交、预热流读取和稳定空闲路径有零分配验证。

可复现的无窗口弱网测试和基准：

```sh
go test -p 1 ./client -run 'Test(WeakNetwork|SlowDocument|CancelledDownload|InterruptedLarge|ProcessConcurrent)' -count=1 -v
go test -p 1 ./client -run '^$' -bench '^BenchmarkWeakNetworkDocumentSave$' -benchtime=3x
```

测试链路分别模拟双向传播延迟与限速，允许多个帧同时在途，使用有界网络队列，并将字节流拆成最多 997 字节的片段。覆盖 UTF-8/CRLF、大段编辑、批量和单字节进程输出、下载与文件操作交错、取消下载、上传中断及并发写入顺序，不依赖公网环境。

Linux amd64 / i5-11500 的一次参考结果：50 ms RTT、每方向 4 MiB/s 时，1,080,000 字节的编辑加保存约 0.38 秒（2 次 RPC），2,359,296 字节的进程输出约 1.2 秒（10 次 RPC）。1 MiB 编辑加保存、每方向 8 MiB/s 的三次基准平均如下；模拟器和存储开销均计入时间，不等同于真实 SSH 的吞吐保证。

| RTT | 每次编辑加保存 | 吞吐 |
| --- | --- | --- |
| 0 ms | 138 ms | 7.62 MB/s |
| 50 ms | 246 ms | 4.26 MB/s |
| 200 ms | 548 ms | 1.91 MB/s |

## SSH 远程开发

标题栏的「远程管理」按钮打开连接面板，保存 SSH Host 别名或 `user@host`、可选端口、密钥文件和项目目录。连接管理独立维护每台设备的 SSH 会话，连接、退出和重连其他设备不影响当前项目，连接时不自动打开目录。点击连接行的文件夹按钮先打开项目选择窗口，可进入子目录、返回上级或输入路径；确认“打开此目录”后才载入项目，取消保留当前工作区。配置目录仅作为选择窗口的起始位置，留空时从 `~` 浏览，也支持 `~/project`，由服务端展开。打开远程项目后，编辑器设置、用户主题和高亮映射从远端读取并保存到远端；只连接 SSH 时继续使用当前工作区配置，切回本地项目时恢复本机配置。系统字体目录仍取 GUI 主机，远端扩展提供的字体、主题和图标注册照常加载。打开其他设备的项目之前仍需关闭当前项目；已连接的 SSH 会话继续保持。当前项目所在设备断线时保留项目和编辑内容。

使用系统 `ssh`，沿用 SSH config、agent、IdentityFile 和 ProxyJump。需要密码或密钥口令时，通过 OpenSSH askpass 弹窗输入。密码仅交给本次认证，提交或取消后清空，不写入配置、文件、命令行或环境变量；断线重连时重新询问，不自动将解锁的密钥加入 agent。已有密钥或 agent 可免密码认证。首次连接会弹窗显示主机和指纹，点击“信任并连接”后由 OpenSSH 写入 `known_hosts`；取消不记录信任。已知主机密钥发生变化时拒绝连接，不自动覆盖。服务协议直接走加密的 SSH 标准流，不开放服务端端口，不需要端口转发；远端 shell 初始化不能向 stdout 打印欢迎语或调试信息。

连接时依次检查远端 PATH 中的 `pencode-server`、`pencode`，以及安装目录中的 `pencode-server`（默认 `~/.pencode/pencode-server`），使用首个可执行程序直接启动；`pencode` 通过 `pencode --pencode-server` 启动无头服务。复用已安装程序时无需准备本地服务端文件，也不会上传或重复安装。

均不可用时才自动安装。远端支持 Linux/macOS 的 amd64、arm64，需要 POSIX shell、`uname`、`mktemp`、`head`、`wc`、`tr`、`sha256sum`（Linux）或 `shasum`（macOS）。先在仓库根目录准备对应平台的独立程序，远端无需 Go 工具链：

```sh
go run -p 1 ./cmd/pencode-build-servers
```

默认生成 `resources/servers/<os>-<arch>/pencode-server`。发布时将整个 `resources/servers` 放在 GUI 可执行文件旁；开发运行可显式指定目录：

```sh
go build -p 1 -o /tmp/pencode .
/tmp/pencode --ssh devbox --remote-path '~/workspace/app' \
  --server-directory /absolute/pencode/resources/servers
```

也可以用 `--server-binary /absolute/pencode-server` 指定与远端平台一致的单个本地程序。同平台时，GUI 旁的独立 `pencode-server` 也可作为备选。上传前验证独立服务的 Go 构建信息与平台；不能用 GUI 可执行文件代替独立服务。

其他选项：`--ssh-port`、`--ssh-identity`、`--ssh-config`、`--remote-server-directory`。最后一项必须是远端绝对路径；服务端默认安装到远端 `~/.pencode/pencode-server`，指定目录时使用 `<指定目录>/pencode-server`，检测与安装使用同一路径。已有可执行程序直接复用；不可用时流式上传到暂存目录，核对 SHA-256 后原子替换。升级时直接替换该文件。扩展仍安装到服务端用户的扩展目录，扩展、语言服务和 shell 均在远端执行。

连接和恢复在后台进行，一轮最长 2 分钟；SSH 建连超时 10 秒，保活间隔 15 秒、连续 3 次无响应断开。检测到断线后，一轮最多尝试连接 3 次，失败间隔为 1、2 秒；连接刚恢复又在 30 秒内中断时，最多启动 3 轮自动恢复，之后需手动重连。稳定运行 30 秒后重置连续中断计数。SSH 诊断进入 Output 的 `SSH` 信息源。

恢复只重建读句柄和服务端内存中的当前文档，不执行保存、Git 写入或重发终端输入。如果远端磁盘内容等于上次保存的原文，可继续编辑和显式保存；如果等于当前缓冲，认定该内容已保存；其他变化保留 GUI 文本并提示冲突，阻止覆盖，需比较或重载后保存。终端保留已显示的屏幕与历史，旧进程结束；新终端由用户创建，LSP 与扩展进程按需重启。连接配置保存在系统用户配置目录的 `pencode/resources/connection.json`，只保存主机与路径等配置，不保存密钥内容或密码。

真实 SSH 集成测试使用临时密钥、独立 known_hosts 和回环 sshd，不修改用户 SSH 配置：

```sh
go build -p 1 -o /tmp/pencode-server ./cmd/pencode-server
PENCODE_SSH_TEST_SERVER=/tmp/pencode-server \
  go test -p 1 ./remote -run '^TestSSHIntegration$' -count=1 -v
go test -race -p 1 ./workbench -run '^TestConnection' -count=1
```

该测试需要安装 `ssh`、`sshd` 和 `ssh-keygen`，覆盖首次主机指纹确认与拒绝、主机密钥变更拒绝、加密密钥的弹窗认证、重连重新询问、单次会话安装、固定路径复用、远端保存、进程双流和 PTY shell；恢复测试覆盖未保存编辑、撤销与终端屏幕保留、外部冲突、取消及连续掉线限制。跨平台构建不代替对应系统的人工验收。

## 文本引擎

普通文件、Git 差异编辑和合并结果使用 `text/piecetree` 中的 AVL piece tree。树节点引用只读原文和追加缓冲区，维护子树字节数、Unicode rune 数和 LF 换行数；每片最多 1024 字节，按 rune 边界切分。插入只复制新增文本，删除回收节点，连续输入合并相邻片段。字节/rune 换算和行列定位通过树索引完成，远距离移动编辑位置无需搬运中间文本。CRLF 和末尾换行保持原样，输入文本仍沿用原有的换行规范化规则。

每份文档拥有独立的 `input.TextBuffer`，原生 `TextField.Buffer` 直接绑定该文档。输入批次同时维护修改范围，工作台据此记录增量历史和重映射高亮，无需扫描整份文本寻找差异。撤销/重做修改树中的局部范围，继续遵守 128 条、合计 4 MiB 的历史预算；磁盘重载清空历史和旧树。追加缓冲区及被删除的原文占用超过文档预算时压缩回收，历史记录独立拥有其文本，不受压缩影响。

渲染、语法解析、LSP 和扩展协议目前仍消费不可变字符串快照。每个修改版本最多生成一份快照，重复读取复用缓存；外部传入的完整快照直接复用。节点与追加缓冲区预留容量内的局部修改、索引查询和缓存读取保持零分配，生成新字符串快照、历史副本及扩容仍会分配。

定向验证：`go test -p 1 ./workbench`；Onix 引擎测试：`(cd ../onix && go test -p 1 ./text/piecetree ./input ./ui)`。核心远距离编辑基准：`(cd ../onix && go test -p 1 ./text/piecetree -run '^$' -bench '^BenchmarkTreeDistantEdit$' -benchtime=1000x)`。

## 并发执行模型

工作台采用前台单一状态所有者与后台 worker 分离的设计。项目、标签页、编辑历史、HTML 和 ECS 只由 UI 线程修改；后台接收不可变快照，结果通过有界通道或邮箱回到前台，并唤醒按需渲染。后台任务不直接操作编辑器实体。

- **语法高亮**：每个视图按需启动一个长期复用的协程，持有服务端解析会话，接收 capture 后在本地着色并计数。待处理请求和待接收结果各只保留最新一份，两个结果缓冲区循环复用。连续输入保留 50 ms 防抖和即时颜色重映射；文件身份、路径、内容、主题、capture 定义与任务版本全部匹配后才提交结果。切换标签或场景使旧任务失效，缓存命中直接复用颜色；空闲维护也交给同一个 worker，销毁视图时取消并等待协程退出。
- **LSP**：每个服务保持一个有序发送调度器，文档通知与请求按发送次序进入协议连接；请求发出后，最多 8 个协程并发等待响应。额外请求进入固定的 64 项等待队列，文档通知继续发送，队列溢出显式返回繁忙错误。排队请求发送前检查文档版本，返回结果仍由 UI 校验，关闭服务会取消并回收等待协程。
- **目录与扩展**：目录在服务端扫描，GUI 继续使用两个可回收批次缓冲区，前台每帧限量接收；扩展通信、安装与原生命令沿用各自后台执行流程。

快照提交、无结果轮询、高亮缓存命中和稳定空闲路径保持零分配；解析、内容存储和缓存扩容仍按实际数据分配。文件任务有界，但编辑器仍保留 GUI 本地文本快照与撤销记录，服务端也持有打开文档内容；文件任务串行化会让慢请求延迟后续文件操作，取消后已在远端完成的文件变更无法回滚。UI 布局与渲染遵循引擎的线程归属。

状态栏左侧的透明按钮 `Problems` 显示工作区诊断总数，点击后在底部展开问题列表；列表包含错误、警告和提示，点击一项定位到对应文件和位置。`Output` 打开输出面板，可按信息源切换、筛选和清空输出。`Source Control` 打开 Git 版本控制面板。这些内容与 Terminal 共用底部面板，可通过状态栏按钮切换；点击关闭按钮或按 Esc 收起面板。

输出源包含 `pencode`、各扩展和语言服务。扩展向 stderr 写入的启动与运行日志会自动归入对应扩展，LSP 的 stderr 和日志通知归入对应语言服务；stdout 用于协议通信。每个信息源保留最近 64 KiB 输出。宿主也可以通过 `session.Output.Channel("信息源名称")` 获得线程安全的 `io.Writer`，注册自定义输出源。

## 内置终端

状态栏 **Terminal** 或 `Ctrl+反引号` 打开底部终端，首次打开在当前项目目录启动系统 shell。`Ctrl+Shift+反引号` 或工具栏新建图标创建独立会话，点击标签切换会话，标签上的关闭按钮终止对应会话；shell 中输入 `exit` 后自动关闭标签，最后一个终端关闭时自动收起面板。右键菜单提供复制、粘贴和清屏，`Ctrl+Shift+K` 清空显示与历史。拖动底部面板上边缘调整高度；收起或切换 Output/Problems 保留正在运行的会话，关闭所属项目或退出时回收进程。

终端模拟器和平台后端均位于 `terminal/`，仅使用 Go 标准库，没有新增外部包。Linux/macOS 直接打开 PTY、设置控制终端与窗口尺寸；Windows 直接调用 ConPTY 和进程 Job API，需要 Windows 10 1809 或更新版本。Unix 使用 `$SHELL`，默认 `/bin/sh`；Windows 优先启动已安装的 `pwsh.exe`，否则使用 `%COMSPEC%` / `cmd.exe`。

支持流式 UTF-8、中文宽字符与组合字符、光标与擦除指令、滚动区域、插入/删除、16/256 色及真彩色、反色、DEC 线框字符、备用屏幕、应用方向键、鼠标报告、括号粘贴、终端标题和常用状态/默认颜色查询。方向键、Tab、功能键、Ctrl+C 等直接编码并写入终端，命令编辑、历史和补全由 shell 提供。终端获得焦点时 Esc 发送给终端程序，使用面板关闭按钮或切换快捷键收起。

鼠标拖动选择文字，`Ctrl+Shift+C` / `Ctrl+Shift+V` 复制粘贴，macOS 也可用 `Cmd+C` / `Cmd+V`。滚轮与 `Shift+PageUp/PageDown` 查看最近 2000 行历史；程序启用鼠标报告时转发鼠标事件，按住 Shift 可使用本地选择与滚屏。备用屏幕中未启用鼠标报告时，滚轮转发方向键。

每个会话使用有界输入/输出队列，输出满时施加背压，不丢弃终端字节；后台只处理原生进程和字节队列，屏幕与原生 UI 均由前台更新。滚屏历史采用环形缓冲区，视图只生成当前可见网格，文本颜色与背景批次复用；屏幕解析及视图缓存的稳定路径有零分配测试。输出快照、缓冲区扩容、创建和调整屏幕尺寸仍会分配。

定向验证：`go test -p 1 ./terminal ./workbench .`。终端包还可单独运行 `go test -race ./terminal`。PTY 实际交互和无窗口原生控件测试覆盖中文输入、复制、尺寸变化、Ctrl+C、会话切换及进程回收；不启动编辑器截图测试。Windows/macOS 的交叉编译不代替对应系统的人工交互验收。

## Git 版本控制

底部状态栏的 **Source Control** 在与 Output 共用的底部面板中打开当前项目所属仓库，保留上方正在编辑的文件。左侧按目录展示 Merge Changes、Staged Changes 和 Changes；文件旁的加号、减号 SVG 按钮暂存或取消暂存，也可以批量操作。输入提交说明后点击 Commit Staged Changes，只提交索引内容。未解决的冲突会弹出提示，阻止暂存或提交。后台状态刷新不会锁住比较编辑器或阻止关闭项目；只有未保存的比较/合并编辑内容和正在执行的 Git 写入操作会阻止关闭。

右侧 Source Control Graph 展示提交与分支连接，点击提交列出该次修改的文件。右键提交或点击 `…` 可以复制哈希、检出提交、新建分支/标签、合并、cherry-pick、revert，以及 soft/mixed reset。点击工具栏分支名，会弹出按本地和远程分组的分支菜单；选择后直接切换，远程分支优先复用已有跟踪分支，否则创建本地跟踪分支。菜单支持方向键、Enter、Esc 和点击外部关闭。右键本地或远程分支可选择删除，确认后删除对应分支；当前检出的本地分支不可删除，未合并的本地分支由 Git 拒绝删除。工具栏还提供 fetch、pull（仅 fast-forward）、push 和取消合并；这些操作中改变工作区的操作先显示确认框。

点击变动或历史文件会在现有标签栏新建比较标签：Changes 比较索引与磁盘，Staged Changes 比较 HEAD 与索引，历史比较父提交与所选提交；合并提交按第一个父提交比较。左右两栏对齐行号，以红/绿色显示删除和新增，并同步垂直滚动。历史、暂存和工作区比较使用不同标签，可以随时切回普通文件。

冲突文件打开本地 / 合并结果 / 传入三栏，中间可直接编辑，也能逐个接受本地、传入或双方内容。完成后点击 Save & Stage（或保存快捷键）写回文件并暂存；仍有冲突标记时拒绝保存。结果支持撤销/重做，未保存时关闭标签会确认，关闭项目或窗口会提示先处理合并内容。保存还会检查磁盘是否被外部修改，避免覆盖其他工具的修改。

每个项目独立保存 Git 视图和比较标签。Git 命令、读取和差异计算在后台执行，每个项目同时只运行一项任务，结果通过有界通道返回 UI；关闭项目会取消任务。历史首次加载 32 条，滚动接近底部时异步追加下一批，也可点击 Load more；分页沿用分支连线状态，并固定本轮历史的引用，避免新提交造成重复。打开 Git 视图时每 5 秒刷新，也可手动 Refresh；引用未变时保留已加载历史。Git 列表和差异只渲染可见行附近的窗口，稳定空闲轮询与控件同步保持零分配。图标使用 SVG，滚动条沿用 Output 的主题细滚动条。文本比较限制为每侧 2 MiB、100,000 行；二进制文件显示说明。使用系统 `git` 及已配置的身份和远程认证，不弹出终端认证提示。

定向验证：`go test ./workbench -run '^TestSourceControl' -count=1`。交互测试使用无窗口的 HTML/原生控件事件，不启动编辑器或截图；视觉验收由人工进行。

面板收起后保留 DOM 和原生控件，未变化的文件树、历史和工具栏复用已有视图，避免反复绑定事件和同步整页。布局引擎分别缓存 Flex 的 auto basis 与最终尺寸，并复用定位前的测量结果；嵌套 Flex 无需改为其他布局。可在真实仓库上运行无窗口基准：

```sh
ONIX_PENCODE_PROFILE_PROJECT="$PWD" \
ONIX_PENCODE_PROFILE_FILE=workbench/source_workspace.go \
go test ./workbench -run '^$' \
  -bench '^BenchmarkPencodeSourceControl$' -benchtime=10x
```

`Idle` 测量空闲帧，`Redraw` 测量数据不变的刷新帧；`Toggle` 和 `OutputToggle` 各测量一次关闭后重新打开的完整往返，不包含 Git 命令等待时间。

## 扩展

Pencode 通过 stdin/stdout 上的 **peb（Pencode Extension Binary）** 协议加载本机扩展。支持字体、文件图标主题、编辑器和工作台颜色主题，以及可按文件类型选择的 HTML/CSS 编辑工具、标准 stdio LSP 语言服务和可实时更新的 action 按钮。二进制帧、字段编码和 Go SDK 说明见 [peb v7 协议](../peb/README.md)。

点击右上角设置按钮左侧的扩展图标打开「扩展市场」。目前使用本地扩展：可以搜索已安装列表、查看名称/版本/发布者/描述，点击「从本地目录安装」选择扩展目录，或在详情中启用、禁用和卸载扩展。操作在后台执行，完成后即时加载或移除对应主题、文件图标和编辑工具；禁用或卸载当前主题时自动回到内置外观。禁用保留扩展文件，启用前会验证加载，失败时保持禁用状态。

扩展统一安装在 server 用户的 `~/.pencode/extensions/<扩展 ID>/`，与 GUI 的编辑器设置分开。也可以手动将扩展复制到服务端的这个目录，再打开扩展市场点击「刷新」；新增扩展会立即注册，已删除扩展会移除。每个扩展包含可执行程序及 `resources/extension.json`：

```text
my-extension/
  bin/my-extension
  resources/
    extension.json
    icons/code.svg
    themes/ink.json
```

```json
{
  "id": "my-extension",
  "name": "My Extension",
  "version": "1.0.0",
  "publisher": "Pencode",
  "description": "编辑器颜色主题与文件图标主题。",
  "entry": "bin/my-extension",
  "args": [],
  "disabled": false
}
```

`id` 必须与安装目录名相同；`entry` 相对于扩展目录，`args` 直接传给程序。`name`、`version`、`publisher`、`description` 是可选展示信息。扩展市场的启用/禁用操作会保存到已安装扩展的 `resources/extension.json` 的 `disabled` 字段，重启后保留；也可以手动修改此字段后刷新。扩展以 server 当前用户权限运行，请只安装信任的扩展；安装目录属于 server 用户。

扩展源由 GUI 文件夹选择器指定，按 32 KiB 分块上传到 server 暂存目录；注册探测和 GUI 资源校验通过后再发布，保留源目录。符号链接、特殊文件、无效协议和损坏资源会使安装失败；已安装的同名扩展不会被覆盖。损坏扩展仍显示在列表中，可直接卸载。

仓库提供 [Markdown 编辑示例](../extensions/markdown-preview/main.go) 与 [Material Design 图标示例](../extensions/material-icons/main.go)，分别展示自定义编辑工具和文件图标主题，构建方法见下文对应章节。选择保存在用户配置目录的 `pencode/resources/settings.json` 的 `theme` 与 `iconTheme` 字段；异步注册完成后会恢复上次选择。

## LSP 语言服务

扩展通过 `peb.Contributions.LanguageServers` 注册语言服务，宿主负责按项目启动进程、能力协商、文档同步、诊断和退出。代码区右键菜单提供补全、悬浮信息、参数提示、定义/引用跳转、重命名、格式化、代码操作、符号和层级等入口，不支持的操作会禁用；语义 token 自动覆盖代码前景色。

构建安装 [Go/gopls 示例](../extensions/go/main.go)：

```sh
# 先确保 gopls 已安装且在 PATH 中
pencode_extension_dir="$HOME/.pencode/extensions/go"
mkdir -p "$pencode_extension_dir/bin"
cp -R extensions/go/resources "$pencode_extension_dir/"
(cd extensions/go && GOWORK=off go build -p 1 -o "$pencode_extension_dir/bin/go" .)
```

在扩展市场刷新后打开 Go 文件即可激活。输入标识符时自动弹出补全，语言服务器声明的触发字符（例如 `.`、`(`、`,`）会触发对应的补全或参数提示。补全支持方向键选择、Enter/Tab 接受和分页；普通鼠标悬停显示符号说明，Ctrl 悬停显示可用导航链接下划线。Ctrl+左键在变量、方法等定义位置查找引用，在符号使用位置转到定义；`Shift+F12` 始终查找引用。补全、悬停、参数提示、引用和命令结果均在代码区域的浮层显示，Esc 关闭。鼠标命中使用实际文字排版，支持缩放、Tab、中文和折行，移动光标、修改文档或切换文件会取消过期请求。

快捷键：`Ctrl+Space` 补全、`Ctrl+Shift+Space` 参数提示、`F12` 定义、`Shift+F12` 引用、`Ctrl+F12` 实现、`F2` 重命名、`Alt+Shift+F` 格式化、`Ctrl+.` 代码操作。选择结果按钮应用补全或跳转；重命名在输入框输入新名称后确认。编辑结果使用现有撤销和保存流程。

协议 SDK、能力范围和验证方式见 [LSP 开发文档](../lsp/README.md)。peb 已升级为 v7，已有扩展需要重新构建。

## 字体

不打包字体。启动时读取系统已安装的字体列表，在「设置 → 外观 → 代码字体 / 界面字体」中分别选择。列表按字体家族去重，支持 TTF、OTF、TTC、OTC；字体按需加载，普通体与粗体缓存由会话共享。新安装的字体在重启后显示。

macOS / iOS 优先 Menlo，其次 Monaco、Courier New；Windows / Linux 等平台优先 Courier New，其次 Menlo、Monaco。上述字体均未安装时使用其他可用的系统等宽字体。代码与界面默认使用同一字体，选择按字体家族名保存到 `resources/settings.json`，已卸载的字体会回退到当前平台默认字体。

所选字体缺少字形时，文本系统按需查找支持该字符的系统字体，并尽量匹配字重和斜体。例如选择 Consolas 后，英文仍使用 Consolas，中文自动使用已安装的中文字体。测量、光标、选区与灰度/LCD 绘制共用字形回退结果；首次遇到缺字时读取候选字体的字符映射，仅加载命中的字体，后续复用缓存（包括未找到字形的结果）。行高和基线继续由主字体决定。

扩展仍可通过 `peb.Contributions.Fonts` 注册字体，异步加载完成后恢复选择；禁用或卸载当前字体扩展时回退到系统默认字体并释放缓存。注册示例见 [字体扩展 API](../peb/README.md#字体)。

## 自定义文件编辑工具

代码区右上角的浮动图标组提供匹配文件类型的打开方式；只有源码时隐藏，透明度为 30%，悬停或聚焦时为 100%。点击图标替换代码区，路径栏的双栏按钮可同时显示源码与扩展视图。扩展通过 `Contributions.Actions` 注册 `Location: peb.OpenWith`，并提供文件关联、图标和视图处理函数。扩展可以注册默认关联，自行渲染 HTML/CSS 并接收控件事件；修改接入宿主的未保存提示、保存、撤销和重做。每个文件保留独立会话，扩展通信在后台执行。二进制文件目前支持扩展按路径读取查看，暂不支持写回。当前协议为 peb v7，旧扩展需要重新构建。

[Markdown 示例](../extensions/markdown-preview/main.go) 提供只读 Markdown 预览。在仓库根目录构建到本地扩展目录后，打开扩展市场刷新，打开 `.md` 文件并点击「Markdown 预览」图标：

```sh
pencode_extension_dir="$HOME/.pencode/extensions/markdown-preview"
mkdir -p "$pencode_extension_dir/bin"
cp -R extensions/markdown-preview/resources "$pencode_extension_dir/"
(cd extensions/markdown-preview && GOWORK=off go build -o "$pencode_extension_dir/bin/markdown-preview" .)
```

源码编辑由宿主管理，双栏模式下修改源码会自动同步预览。扩展开发与事件协议见 [自定义文件编辑工具 API](../peb/README.md#自定义文件编辑工具)。

## Material Design 图标扩展

[extensions/material-icons](../extensions/material-icons) 是使用 peb Go SDK 实现的文件图标扩展。`resources/rules.psv` 在构建时嵌入 Go 程序，注册 `Material Design Icons` 文件图标主题，图标为 `resources/icons/` 下的 SVG。规则表由 [tools/build_material_icons.py](../tools/build_material_icons.py) 从上游 [vscode-material-icon-theme](https://github.com/material-extensions/vscode-material-icon-theme) 的 `dist/material-icons.json` 生成，覆盖文件扩展名、完整文件名与目录名，共 7970 条规则、852 个图标。在仓库根目录构建并安装：

```sh
pencode_extension_dir="$HOME/.pencode/extensions/material-icons"
mkdir -p "$pencode_extension_dir/bin"
cp -R extensions/material-icons/resources "$pencode_extension_dir/"
(cd extensions/material-icons && GOWORK=off go build -o "$pencode_extension_dir/bin/material-icons" .)
```

重新生成规则表和图标（需要 Node.js，脚本在临时目录中克隆上游仓库）：

```sh
python3 tools/build_material_icons.py
```

脚本默认更新 `extensions/material-icons/resources/`，把每个 SVG 的根元素规范化为 `width="16" height="16"` 并保留原 viewBox，因为 peb 要求图标尺寸在 1..256 之间。更新规则后需重新构建扩展。Go SDK 在 Initialize 中校验宿主 `capabilities` 包含文件图标，注册完成后等待 Shutdown 退出。Windows 上构建 `bin/material-icons.exe` 并相应修改 manifest 的 `entry`。

扩展在后台注册，完成后退出，资源保留在宿主缓存中。未配置文件图标规则时回退内置图标；加载失败显示在扩展详情或设置面板中，不影响内置主题。修改已加载扩展的资源后仍需重启；新增、删除和禁用可以通过刷新生效。目前没有线上市场源。

## 代码行宽与换行

在「设置 → 外观 → 代码行宽度」中按编辑区域可用宽度的百分比设置换行位置：范围 20%–100%，每次调整 5%，默认 100%。`resources/settings.json` 的 `editorWidth` 字段保存百分比数值；窗口大小、侧栏和界面缩放变化时，换行位置与参考线一起更新。

代码按单词、标识符和标点边界换行，保留空格、制表符及原始换行；单词和标识符不会从中间切开。超长 token 保持完整，可通过水平滚动查看。软换行不会增加源文件行号。

## 代码高亮与主题

Pencode 和引擎的 Markdown 代码块共用 `syntax` 包，使用 [gotreesitter](https://github.com/odvcencio/gotreesitter) 的语言注册表、语法树和高亮查询。语言按文件名识别，Markdown 围栏也接受注册表中的语言名称和别名。未知语言保持纯文本；不完整的代码仍使用语法树中可解析的部分。

编辑器保留当前文件的语法树，在 50 ms 编辑防抖后增量解析。输入和中文预编辑期间沿用并移动已有颜色区间，坐标使用 UTF-8 字节偏移。切换主题只给缓存的查询结果重新着色。未变化文件的解析、着色和编辑器同步高亮路径复用缓存；解析修改后的文本仍有 gotreesitter 自身的分配，不能视为整条编辑链路零分配。单次解析设置了 50 ms 时间限制，超限时保留部分语法树。

## 选择主题

在设置的「外观 → 颜色主题」中选择 Monokai、Dark+ 或 Light+。选择保存在用户配置目录的 `pencode/resources/settings.json` 的 `theme` 字段。主题统一控制启动页、标题栏、目录树、标签栏、状态栏、查找/替换、快速打开、设置和关闭确认面板，以及代码颜色、背景、光标、选区、行号和宽度参考线。按钮和文件图标也随主题着色。

切换即时生效，保留打开的文件、选区和语法树；启动页和工作区共享当前主题。工作台样式和图标地址在加载主题时生成并缓存，未变化主题的同步路径零分配。

普通操作与文件图标使用 `icon.foreground`；主按钮图标使用 `button.foreground`；选中文件图标使用 `list.activeSelectionForeground`；标签关闭图标和未保存圆点使用 `tab.activeForeground` / `tab.inactiveForeground`。同一个 SVG 按颜色缓存纹理，切换主题或选中状态时更新已有图标，不需要分别维护深浅色 SVG。

工作台操作与内置文件图标统一使用 [Microsoft Codicons](https://github.com/microsoft/vscode-codicons)，保留上游 16×16 图形和 `currentColor`。关闭图标统一按原生 14px 栅格化并显示，其余图标显示为 16px，减少缩放带来的模糊。`resources/icons/` 与 `resources/workbench/assets/icons/` 中的对应 SVG 保持一致；来源映射和上游版本记录在各目录的 `CODICONS-LICENSE.txt`。应用标志和扩展提供的文件图标由各自的品牌或主题决定。

构建打包会保留含 `currentColor` 的 SVG 原文，供运行时按主题着色。它们不能提前栅格化或做纹理压缩，否则默认黑色会固化在资源包中。旧版本构建的资源包需要重新构建；构建缓存版本已更新，会自动丢弃旧结果。

内置配置位于仓库的 `syntax/resources/themes/`；capture 到 TextMate scope 的默认映射位于 `syntax/resources/highlights.json`。这些文件随 `syntax` 包嵌入程序。

## 添加自定义主题

把 VS Code JSON/JSONC 颜色主题放入用户配置目录的 `pencode/resources/themes/`，重启后会出现在主题选择器中。例如 Linux 默认目录为 `~/.config/pencode/resources/themes/`，也遵循 `XDG_CONFIG_HOME`。

主题采用 [VS Code 的 colors 与 tokenColors 格式](https://code.visualstudio.com/api/language-extensions/syntax-highlight-guide#theming)。例如 `resources/themes/custom.json`：

```json
{
  "$schema": "vscode://schemas/color-theme",
  "name": "My Theme",
  "type": "dark",
  "colors": {
    "editor.background": "#181818",
    "editor.foreground": "#d4d4d4",
    "editorCursor.foreground": "#ffffff",
    "editorGutter.background": "#181818",
    "editorLineNumber.foreground": "#858585",
    "editor.selectionBackground": "#264f78",
    "titleBar.activeBackground": "#202020",
    "sideBar.background": "#252526",
    "tab.activeBackground": "#181818",
    "tab.activeBorder": "#007acc",
    "statusBar.background": "#007acc",
    "statusBar.foreground": "#ffffff",
    "icon.foreground": "#c5c5c5"
  },
  "tokenColors": [
    { "scope": "comment", "settings": { "foreground": "#6a9955" } },
    { "scope": ["keyword", "storage"], "settings": { "foreground": "#c586c0" } },
    { "scope": "entity.name.function, support.function", "settings": { "foreground": "#dcdcaa" } },
    { "scope": "source.go entity.name.type", "settings": { "foreground": "#4ec9b0" } }
  ]
}
```

支持注释、尾随逗号、相对 `include`、十六进制颜色（RGB/RGBA，短格式或完整格式）、scope 字符串或数组、逗号分隔选择器、父 scope 选择器和 ` - ` 排除选择器。更具体的 scope 优先，同等具体程度下后面的规则优先；颜色和字体样式分别继承。循环 include 和无效颜色会报告错误。

界面颜色使用 [VS Code Theme Color](https://code.visualstudio.com/api/references/theme-color) 的命名，在同一个 `colors` 对象内配置：

| 区域 | 主要颜色键 |
| --- | --- |
| 通用、窗口与图标 | `foreground`、`descriptionForeground`、`focusBorder`、`window.activeBorder`、`icon.foreground` |
| 标题栏、目录树 | `titleBar.activeBackground/activeForeground`、`sideBar.background/foreground`、`sideBarSectionHeader.*`、`list.*` |
| 标签、状态栏 | `editorGroupHeader.tabsBackground`、`tab.*`、`statusBar.background/foreground` |
| 按钮与输入 | `button.*`、`input.*`、`dropdown.*`、`toolbar.hoverBackground` |
| 设置与弹层 | `settings.headerForeground/headerBorder`、`settings.dropdown*`、`editorWidget.*`、`widget.border/shadow`、`quickInput.*`、`quickInputList.*` |
| 编辑区域与滚动条 | `editor.selectionBackground`、`editorRuler.foreground`、`editorGroup.border`、`scrollbarSlider.background` |

只需配置需要覆盖的颜色；其余颜色先继承对应的通用颜色，再使用 `type: "light"` 或 `type: "dark"` 的内置配色，省略 `type` 时使用深色默认值。显式透明颜色不会被默认值覆盖。内置 JSON 列出了当前工作台读取的完整颜色键；上表的 `*` 仅表示同一组键，不表示已实现 VS Code 的所有界面部件。

`tokenColors` 使用数组形式，不读取 `.tmTheme` 路径。原生编辑器的 `TextSpan2D` 当前只渲染 token 前景色；主题读取器接受 `fontStyle`，Markdown 的 HTML 高亮会输出相应样式。LSP 语义 token 在语法高亮之上覆盖前景色，按 token 类型映射到主题 scope；目前不读取 `semanticTokenColors`。没有接入 TextMate 的正则语法引擎，语法 scope 来自语法树 capture 映射，不能保证和 VS Code 的逐 token 输出完全相同。

## 调整 capture 映射

在用户配置目录的 `pencode/resources/highlights.json` 中覆盖需要调整的映射，其余条目继承内置定义。例如：

```json
{
  "captures": {
    "function.method": "entity.name.function.method",
    "variable.parameter": "variable.parameter"
  },
  "sourceScopes": {
    "go": "source.go",
    "javascript": "source.js"
  }
}
```

最长 capture 前缀优先，例如 `function.method.call` 继承 `function.method`。主题匹配时附加语言后缀，因此既能使用 `entity.name.function`，也能使用 `entity.name.function.go` 和 `source.go entity.name.function`。解析查询中的通用节点规则在上下文规则之前执行，避免通用 identifier 颜色覆盖函数、方法等更具体的分类。

## 项目加载

打开项目时先校验目录并切换工作区，目录递归扫描在后台执行，不会自动读取或打开文件；文件由用户点击或通过快速打开选择。目录结果通过两个复用缓冲区分批传回主线程，每批最多 256 项，每个项目每帧最多接收 4 批；目录加载期间可以展开目录、编辑文件和使用快速打开，快速打开结果随扫描更新。

重复打开已打开的项目直接复用工作区。刷新、关闭项目或退出时取消旧扫描，过期结果不会覆盖新目录或用户已打开的文件。无法读取的子目录会显示错误并继续扫描其他目录。扫描结果唤醒按需渲染，等待磁盘时不需要持续轮询；扫描完成后的检查零分配，目录数据和文件内容本身仍需要分配内存。

左侧目录树支持按住文件或文件夹拖动：拖到文件夹中移入该目录，拖到文件行时移入其所在目录，拖到项目标题或目录空白处时移回根目录，也支持跨项目移动。拖动目标会高亮，悬停折叠文件夹约 650 毫秒会自动展开，按 Esc 或在目录树外松开可取消。移动保留未保存内容、标签页和撤销记录；遇到同名资源会提示错误，不覆盖或自动改名。

## 验证

仅运行相关包；不启动编辑器和截图测试：

```sh
go test -p 1 ./lsp ./peb ./extensions/... ./workbench .
go vet -p 1 ./lsp ./peb ./extensions/... ./workbench .
git submodule foreach 'GOWORK=off go test ./... && GOWORK=off go vet ./...'
```

## Action 按钮

原生代码通过 `session.Actions.Register(actions.Definition{Action: ..., Run: ...})` 注册按钮；扩展通过 `peb.Contributions.Actions` 和 `peb.ServeExtension` 的 `Handlers.Action` 注册并处理点击。按钮可以放在状态栏、左侧文件树上下文菜单、右侧编辑区上下文菜单、标题栏右侧区域。

支持自定义文本、提示、SVG 图标、文本颜色、图标颜色和图标 CSS 样式，以及禁用、隐藏、移动和移除。扩展可通过 `ActionPublisher.Update` 主动实时创建或更新按钮，无需点击；`Remove` 删除按钮。原生注册表同样支持后台线程更新，并自动唤醒 UI。扩展退出、禁用或卸载会清理其按钮和进程。

具体字段、完整替换语义、点击上下文和原生/扩展开发示例见 [Action API](../peb/README.md#actions)。此功能将协议升级为 **peb v7**，已有扩展需要使用当前 SDK 重新构建。

## 扩展弹窗、菜单与 Panel

扩展通过 `ActionPublisher.Show(peb.UI{...})` 打开 `Dialog`、`Popup`、`ContextMenu` 或 `Panel`，通过 `CloseUI(id)` 关闭。`Popup` 支持类似扩展市场的非模态浮窗，使用 `UI.Placement` 指定工作区坐标、action 锚点偏移和宽高，也可居中显示；更新位置时可保留输入状态，`UI.CloseOnOutsideClick` 可设置点击背后的工作区是否关闭浮窗（默认保持打开），关闭事件的原因为 `outside`。`Handlers.UIEvent` 支持 HTML 控件的点击、输入、选择、焦点、提交与重置事件，以及打开、关闭和可见性变化事件。菜单支持 action 锚定、边界约束、方向键和外部点击关闭；模态弹窗支持焦点循环与 Esc，Panel 复用底部区域并支持调整高度。HTML/CSS、主题变量和图片资源沿用自定义编辑器机制。

完整 API 见 [扩展 UI 协议](../peb/README.md#弹窗上下文菜单与-panel)。可直接构建 [UI 示例扩展](../extensions/ui-demo/main.go)：

```sh
mkdir -p extensions/ui-demo/bin
(cd extensions/ui-demo && GOWORK=off go build -o bin/ui-demo .)
```

在扩展市场选择「从本地目录安装」，选择 `extensions/ui-demo`。安装后点击标题栏「UI 示例」，即可打开菜单、表单弹窗、自由定位浮窗和 Panel；浮窗中的按钮可切换工作区坐标与 action 锚点，并保留搜索输入；勾选「点击外部关闭」可实时切换关闭策略。当前协议为 **peb v7**，已有扩展需用当前 SDK 重新构建。
