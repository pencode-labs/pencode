# TextMate 逐行词法高亮实施报告

2026-11-02。已将 Pencode / Onix 的高亮后端从 gotreesitter 替换为固定版本的纯 Go TextMate 引擎 `github.com/eugenioenko/textmate-go v0.1.0`。普通编辑器、左右差异、合并结果、搜索预览和 Markdown 代码块沿用同一个高亮接口。两边 go.mod / go.sum 已移除 gotreesitter；旧语法树、查询重排、arena 清理和 trim 接口已删除。

当前依赖已升级为 textmate-go v0.2.2，间接依赖 regexp2/v2 同步升级为 v2.8.2。下方性能表保留首次实施时 v0.1.0 的实测结果，不代表 v0.2.2 的重新测量。

## 实现

- [Onix Document](/home/zhuoh/workspace/onix/syntax/document.go) 保存行起点、token 起点和行末词法状态。已知字节编辑直接定位首个受影响行，向后着色；到达未改后缀且输入状态与旧状态一致时复用余下 token 和状态。插入、删除行与跨行语法变化采用相同路径。
- 两组行/token 缓冲区交换使用。缓存 token 保留完整 TextMate scope 栈，并保留用于括号匹配的粗类别。增量计算不保留被替换的语法树或旧词法状态；切换文档时清理缓冲区中的旧引用。
- grammar 输出的 rune 偏移单次扫描转换为 UTF-8 字节偏移；源文本中的 CRLF、中文和 emoji 不规范化、不截断。
- [主题 Painter](/home/zhuoh/workspace/onix/syntax/document.go) 按完整 scope 栈匹配现有 VS Code 主题；显式的类别颜色覆盖继续生效。换主题只重着色。未变文本与主题的 Update + Tokens 路径为零分配。
- 活动文档在两秒空闲后保留轻量行状态，避免下一次输入重新扫描全文。只读差异快照在产出绘制结果后关闭语法会话；关闭和切换视图沿用现有缓存回收。
- [grammar 缓存](/home/zhuoh/workspace/onix/syntax/grammar.go) 最多保留 4 个编译后的根 grammar；活动文档独立持有引用，淘汰缓存不会使其失效。依赖库提供 124 种内置语言，另保留显式 vimdoc 的本地 TextMate grammar；普通 .txt 仍为纯文本。依赖库自身按需加载的原始 grammar 缓存另行存在，不计入这 4 个编译项。
- 单行超过 20,000 字节时跳过该行的词法规则，保留传入状态；缩短后重新着色。该行为是性能降级边界，超长行内若包含状态结束符，后续行的着色可能延后恢复。

[CS 协议](/home/zhuoh/workspace/pencode/protocol/README.md) 升级至 **v8**：首次出现的 token 名称 ID 同时传输类别与空格分隔的完整 scope 栈，随后复用 ID。后缀复用同时比较范围、类别和 scopes；主题不经服务端传输。SyntaxRelease 删除 trim 字段，GUI 与本地／SSH 服务端需要同步重建。扩展协议仍为 peb v13。

## 同文件对照

同一 `render/opengles.go` 的 HEAD / 工作区文本合计 154,436 字节。工作区版本为 77,447 字节、2037 个逻辑行（包含末尾空行），最长行 659 字节，因此对照没有触发长行跳过。

每种后端运行三个新进程，串行处理两个文档快照，再对工作区版本进行 20 次交替插入／删除一个空格的编辑。客户端与服务端通过同进程 net.Pipe 连接，包含协议编解码和客户端主题匹配。快照之间关闭会话；测量段没有强制 GC 或归还内存。取三轮中位数；各轮每次编辑的值是 20 次编辑的平均值。原始数据在 [wire-runs.json](/home/zhuoh/workspace/pencode/dist/textmate-memory-20261102/wire-runs.json)，汇总在 [wire-summary.json](/home/zhuoh/workspace/pencode/dist/textmate-memory-20261102/wire-summary.json)。

| 指标 | gotreesitter | TextMate | 降幅 |
| --- | ---: | ---: | ---: |
| 首次左右快照累计分配 | 270.27 MiB | 48.43 MiB | 82.1% |
| 首次快照阶段进程峰值 RSS | 198.04 MiB | 27.55 MiB | 86.1% |
| 首次左右快照耗时 | 1464.6 ms | 285.5 ms | 80.5% |
| 每次小编辑累计分配 | 116.69 MiB | 245.6 KiB | 99.8% |
| 每次小编辑耗时 | 807.41 ms | 1.01 ms | 99.9% |

RSS 是整个测试进程的 Linux VmHWM，采样位置在首次左右快照完成后、连续编辑前；累计分配是 runtime.MemStats.TotalAlloc 的差值。**这不是 GUI 窗口的总内存或完整交互耗时**，也不与之前原生窗口的服务端 RSS 直接混算。测试保留了更细的 TextMate tokens（两侧合计 34,398 项，旧后端 13,164 项）；不是以跳过整个文档换取结果。两种方案的词法类别与着色细节可能不同，不能视为逐 token 等价。

基准运行中没有并行 race 测试或其他本任务的性能测试。早期探索样本归档在 exploratory 目录，不用于上表。可复现程序与对照脚本位于 [测量目录](/home/zhuoh/workspace/pencode/dist/textmate-memory-20261102)。环境为 Go 1.27.3、Linux amd64、Intel i5-11500，默认编译选项。

## 验证

- Onix syntax 包测试通过，涵盖 Go / JS / TS / TSX / Python / Rust / C / C++ / HTML / CSS / JSON / YAML / Shell、语言切换、纯文本回退、UTF-8 / CRLF、主题与自定义覆盖。
- 新增跨行注释、Go 原始字符串、Python 三引号、JS 模板插值、Shell heredoc、Markdown 围栏、HTML 嵌入脚本的增量／全量结果一致性测试。2000 行文档的局部数字修改只重新着色 1 行。
- 增量随机编辑 fuzz 20 秒，通过 10,576 组输入；每步编辑都与全新文档的完整 tokenization 比较。
- server / client / protocol 包完整测试通过；高亮、差异、括号匹配、快照生命周期的 workbench 定向测试及相关 race 测试通过；Onix syntax race 测试通过。
- Markdown 代码块相关测试通过。GUI 和独立 pencode-server 均构建成功。未启动编辑器做截图验收。
- workbench 全包测试存在 TestPencodeIconsRasterizeThemeColorsAndReuseTextures、TestPortsPanelInlineInteractions 两项失败；Onix ui/markdown 全包存在 TestMarkdownFractionRendersHorizontalBar 失败。用 overlay 恢复旧后端后，这三项均原样失败，属于现有工作区基线，不由本次替换引起。

## 边界

使用的是 TextMate 的 Go 移植和其 grammar 集合；正则由 Go 版引擎翻译执行，并非直接运行微软的 JS + Oniguruma WASM。依赖库记录不支持的正则和执行诊断，因此不同 grammar 仍需按具体语言验证。支持的语言集合改为该版本的内置集合，未知语言继续显示纯文本。

行状态收敛能减少重新执行词法规则的范围；协议当前仍使用扁平 token 列表，需要复制／移动未改后缀并比较传输差量。大文件一次编辑的整条路径尚未达到 O(修改行数) 或零分配。没有引入 LSP semantic tokens，本次实现负责词法高亮；不再提供语法树 API。

依赖与 grammar 的原始许可证声明已嵌入 [TEXTMATE-NOTICES.txt](/home/zhuoh/workspace/onix/syntax/resources/TEXTMATE-NOTICES.txt)。实现同时修改 `/home/zhuoh/workspace/pencode` 和本地替换依赖 `/home/zhuoh/workspace/onix`；交付时需要一并包含两边改动。
