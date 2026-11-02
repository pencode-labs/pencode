# 版本控制左右差异面板内存分析

> 后续已替换为 TextMate 逐行词法高亮，首次打开成本的最新结果见 [实施报告](/home/zhuoh/workspace/pencode/docs/textmate-line-highlighting.zh-CN.md)。本文保留替换前的诊断与第一轮优化记录。

2026-11-02，实测项目 `/home/zhuoh/workspace/onix`。**已复现明显内存上涨，主要来源是后台语法解析的高分配量，以及重复点击变更文件时重新解析左右全文。** GUI 的布局和缓存也会增长，但本次最大的增量发生在 `--pencode-server` 进程。

首轮诊断未复现持续无界增长，也未发生 OOM；发现了关闭最后一个差异标签后仍保留该标签引用的问题。本文前半部分记录优化前基线；后续实现和对照结果见文末。

## 单文件复现

用新进程打开 Onix，展开版本控制，再打开 `render/opengles.go`。该文件为 **77,447 字节（75.63 KiB）、2036 行**，左右文本合计 154,436 字节。没有先在普通编辑器打开它，避免普通文件高亮缓存影响结果。约 1.92 秒后两侧高亮均完成。

以下单位均为 MiB。RSS 是进程驻留内存；HeapAlloc 是 Go 堆中尚未回收的对象字节数，未经 GC 时包含垃圾。两者不能混用。

| 状态 | GUI RSS | 服务端 RSS | GUI HeapAlloc | 服务端 HeapAlloc |
| --- | ---: | ---: | ---: | ---: |
| 已展开版本控制，未打开文件，自然状态 | 233.59 | 40.22 | 46.82 | 7.14 |
| 同一基线，执行一次 GC | 233.67 | 40.24 | 35.99 | 4.91 |
| 左右差异及高亮完成 | 265.34 | 254.13 | 54.05 | 252.02 |
| 再空闲 10 秒，未强制回收 | 265.65 | 255.12 | 55.04 | 253.01 |
| 空闲后执行一次 GC | 264.42 | 250.91 | 47.97 | 78.22 |
| 再执行 `debug.FreeOSMemory()`，包含另一次 GC | 252.15 | 52.29 | 47.64 | 8.47 |
| 关闭差异，等待并 GC／归还闲页 | 248.14 | 52.28 | 42.83 | 8.47 |

这轮自然状态下，GUI 增加约 **32 MiB RSS**，服务端增加约 **214 MiB RSS**。不能把两个 RSS 相加当作独占物理内存，因为共享映射会重复计算。相同自然采样的 GUI＋服务端 PSS 从约 216.78 增至 460.42 MiB；PSS 是共享页按比例分摊的口径，未包含扩展子进程。

另一次同文件对照明确区分 GC 和归还页面：

| 服务端状态 | HeapAlloc | RSS |
| --- | ---: | ---: |
| 解析完成并空闲 | 181.13 | 234.28 |
| 第一次 GC | 78.26 | 221.77 |
| 第二次 GC，尚未主动归还闲页 | 8.49 | 203.73 |
| `FreeOSMemory()` 后 | 8.50 | 52.62 |

因此存在三层成本：解析期间的大量分配、缓存／垃圾等待 GC、回收后的空闲堆页等待归还操作系统。**释放解析对象不等于 RSS 立即下降。** 强制回收仅用于归因，不是建议在每次点击后执行的产品方案。

[单文件左右差异截图](/home/zhuoh/workspace/pencode/dist/diff-memory-20261102/fresh-opengles.png)

## 重复点击会重新付出完整成本

在已有标签、磁盘内容不变的情况下，通过 X11 原生鼠标事件，连续三轮点击版本控制列表中同一条 `render/opengles.go`。每轮等待 6 秒，再取快照；另有后台约 200 ms 间隔的采样。

| 轮次 | 服务端累计分配增量 | 服务端采样 RSS 峰值 | GUI 累计分配增量 | GUI 一次 GC 后堆 |
| --- | ---: | ---: | ---: | ---: |
| 1 | 361.04 | 242.97 | 14.13 | 56.85 |
| 2 | 360.89 | 284.98 | 13.63 | 56.56 |
| 3 | 360.73 | 286.60 | 14.13 | 56.78 |

这是每轮累计分配，不是净常驻增长。数字包含诊断端点、堆快照和背景刷新产生的少量分配；不应将两位小数理解为纯解析的精确成本。此次重复测试在已打开其他差异标签的探索进程中完成，因此 GUI 基线高于前面的新进程单文件测试。

作为对照，在两个已高亮标签之间切换三轮，服务端累计分配只增加约 2.62 MiB（包含测量开销），没有再次出现数百 MiB 解析分配。GUI 仍发生布局和绑定分配，不能把整个标签切换称为零分配。

直接原因在 [source_actions.go](/home/zhuoh/workspace/pencode/workbench/source_actions.go:210)：`sc-open:` 只复用存在未保存编辑的标签；干净标签仍调用 `openDiff`。结果返回后，[source_workspace.go](/home/zhuoh/workspace/pencode/workbench/source_workspace.go:372) 用新标签替换旧标签，只拷贝滚动位置，已有左右高亮随旧标签丢失。顶部的 `sc-tab:` 则只激活原标签，保留高亮。

## 分配来源

`opengles.go` 的服务端分配差分采样中，`gotreesitter.(*Parser).parseInternal` 累计约 **353.85 MiB，占该段采样约 92%**。主要 flat 分配是：

| 函数／缓冲 | 采样分配量 |
| --- | ---: |
| `nodeArena.allocNodeSlow` | 88 MiB |
| `gssScratch.allocNodeSlow` | 75.36 MiB |
| `transientParentScratch.allocParent` | 56 MiB |
| `Parser.parseInternal` 自身 | 24.55 MiB |
| `nodeArena.allocRawShapeChildren` | 16 MiB |
| `nodeArena.ensureExactNodeCapacity` | 15.33 MiB |

累计调用栈包含子调用，不能再与 flat 值相加。上述差分来自探索进程首次打开该 Go 文件的区间，与重复点击表是不同区间。

左右高亮由 [source_highlight.go](/home/zhuoh/workspace/pencode/workbench/source_highlight.go:40) 顺序提交给单一工作线程。左侧快照在处理前后释放解析器，见 [highlight_worker.go](/home/zhuoh/workspace/pencode/workbench/highlight_worker.go:88)；因此本场景左右两侧需要独立解析，不能从当前代码推断为两棵 AST 永久并存。普通文件的绘制缓存只有在匹配的文件已打开且缓存有效时才能命中，本次 `m.files` 为空。

现有代码已有两秒空闲释放，见 [pencode.go](/home/zhuoh/workspace/pencode/workbench/pencode.go:1436)。服务端的释放操作删除语法会话，并调用 `DrainArenaPools`。但 gotreesitter v0.56.0 另有 `parserScratchPool`（`sync.Pool`），保存 GSS、临时父节点等 scratch；`DrainArenaPools` 只处理 full/incremental arena 池。结合连续两次 GC 的对照，scratch 缓存生命周期是短期保留的重要来源。回收后主要剩下语言表、协议和工作区状态，大块 parser scratch 不再出现在存活堆前列。

GUI 的增量分散在 HTML 布局、CSS、ECS 容量、文字绘制和差异缓存中。行已经虚拟化，但 [source_editor.go](/home/zhuoh/workspace/pencode/workbench/source_editor.go:92) 固定构建最多 **160 行／侧**，当前屏幕每侧实际可见约 22 行，仍有缩小预取窗口的空间。没有证据将本次服务端的数百 MiB 增量归因于 GPU 或整文件 HTML 渲染。

## 关闭后存在有界的残留引用

代码检查发现 `pencodeSourceControl.gutterTab` 保存 `*sourceTab`，只在存在活动标签时更新；关闭最后一个标签没有清空它。相关位置为 [source_workspace.go](/home/zhuoh/workspace/pencode/workbench/source_workspace.go:849) 和 [source_actions.go](/home/zhuoh/workspace/pencode/workbench/source_actions.go:387)。`bindSourceDiffControls` 在无活动标签时只清空 `diffPaneTab` 和 `inlinePaint`，`diffEditors` 中的文本／绘制状态也需检查。

因此标签数组为空后，最后一个标签的源码、行对齐和高亮仍可能沿这些界面缓存引用存活。关闭后堆采样也仍有 `sourceDiffRow` 和高亮 span 的存活分配。这属于应修正的文档生命周期问题，但目前指向的是最后一次使用的数据，并不能据此称为每次点击都永久累积。

单文件关闭并回收后 GUI 堆约 42.83 MiB，比面板基线 35.99 MiB 高约 6.84 MiB；其中还包括已经扩容的 ECS、布局和渲染缓存。**没有隔离实验支持把这 6.84 MiB 全部算作 `gutterTab` 可回收收益。** 三轮重复点击后的 GUI 堆基本稳定，本次未做小时级泄漏测试。

## 建议优化顺序

| 优先级 | 建议 | 依据与验证重点 |
| --- | --- | --- |
| P0 | 内容和比较基线未变化时，复用已有干净差异标签及两侧 paint | 最直接避免约 361 MiB／次的重复工作。必须用工作区／索引变化版本使缓存失效，覆盖外部修改、暂存／取消暂存、分支切换和主题变化；不能只按路径永久复用。未变化的重复点击热路径应尽量零分配。 |
| P1 | 给解析 scratch 的保留量设置总字节预算，支持安全的空闲 trim | 当前 arena 释放未覆盖所有 scratch 池。保留普通文件所需的小容量，超大缓冲在归还时裁剪；保持顺序复用，避免每个输入事件都清空池或触发 GC。需要在依赖层提供并发安全的策略。 |
| P1 | 关闭最后一个差异时，清理 `gutterTab` 和文档相关引用 | 释放源码、行数据和 spans 的所有权；可以保留不引用文档、且有容量上限的通用 scratch。验证关闭后可回收，及重新打开、滚动、编辑的正确性。 |
| P1（后续专项） | 降低首次左右全文高亮的解析放大 | 用该真实 76 KiB Go 文件做 parser 分配基准，定位 GSS／临时节点增长。评估有字节预算的不可变 paint 缓存；如果尝试左右增量复用，需要验证 AST arena 不跨标签滞留，不能直接取消现有快照释放保护。 |
| P2 | 将固定 160 行窗口改为视口行数加适度预取，并复用行节点 | 主要改善 GUI 的布局、CSS 和节点成本；保持当前主题、同步滚动、行号和块操作，收益需单独测量。它不会消除服务端解析峰值。 |

如需限制极端文件的解析峰值，应设计明确的预算和退化行为，并保留失败状态。依赖已经有解析内存预算，默认上限较宽；简单调低可能导致高亮失败，不能当作无行为变化的修复。仅按文件大小阈值也不够，本次显著放大的文件只有 76 KiB。

首轮建议先实施 P0 和关闭引用清理，再测相同三轮点击；随后再改 parser 池和首次解析成本。以上数值均为优化前诊断基线，后续实测见文末。

## 测量边界与原始材料

- Linux amd64、Go 1.27.3，原生 Pencode 窗口，Xvfb 1600×1000，应用 1440×900，默认 OpenGL／Mesa 环境。GUI 原生开销不能直接推广到用户的其他 GPU 或 Windows 后端。
- Pencode 提交 `9b9f9ba06fad1748eee35531ab8581fe8feb649e`；Onix 提交 `80d04a5f46b6e8b1e927cc6a6e39efd4b64fa7fc`，包含原有 41 个变更／新增文件，测量前后已有 tracked diff 内容相同。没有保存、暂存或修改被测文件。
- 在独立 X 显示中用原生鼠标打开版本控制、打开差异并重复点击。批量文件调查和新进程单文件复测通过主线程诊断队列调用同一生产 `sc-open:` 处理函数；两者都运行真实窗口、原生控件和渲染循环。没有启动 Onix 引擎编辑器。
- Go overlay 只加入诊断端点和主线程状态查询，未改变 diff／解析算法；GUI 和自身启动的服务端分别采样。HTTP 仅监听本机，堆采样间隔 64 KiB。200 ms 定时采样可能漏掉更短峰值，`TotalAlloc` 含诊断开销。
- 稳态总量以 `runtime.MemStats` 和 `/proc` 为准。堆 profile 有采样误差和 GC 统计滞后，只用于来源归因，不把其各项强行凑成精确总量。
- 实际调查了 Go、C、头文件共 8 个差异，包括 `backend.go`、`vulkan_native.c`、`opengles.go`、`directx_native.c` 等；不是所有文件都出现同等放大。
- 诊断构建、两轮真实窗口复现、重复点击、标签切换、关闭和 GC 对照均完成。没有改业务代码，没有运行全量测试。诊断实例及本次 Xvfb 已清理。

材料保存在 [dist/diff-memory-20261102](/home/zhuoh/workspace/pencode/dist/diff-memory-20261102)（Git 忽略）：

- `fresh-{panel,ready,idle,closed}.json`：新进程单文件数据；`fresh-opengles-ui.json` 确认左右高亮完成。
- `repeat-{1,2,3}-{before,after}.json`：同一变更文件重复点击；`tab-switch-*.json` 为已有标签切换对照。
- `gc-control.json`：两次普通 GC 与归还闲页的分离对照。
- `exploration-monitor.jsonl`、`monitor.jsonl`：连续采样；`*.heap` 为分阶段 Go 堆快照。
- `opengles-alloc-top.txt`、`server-settled-top.txt`：解析分配与回收后存活对象来源。
- `setup_probe.py`、`overlay.json`、`*.go.txt`、`ui.py`、`measure.py`：诊断构建和操作／测量源码。
- `onix-before.diff`、`onix-before-status.txt`、`input-files.json`：输入状态及实测文件哈希。

示例复查命令：

```sh
go tool pprof -top -alloc_space \
  -base dist/diff-memory-20261102/survey-render_gl_api_h-server.heap \
  dist/diff-memory-20261102/pencode-probe \
  dist/diff-memory-20261102/survey-render_opengles_go-server.heap
```

## 后续已实施：差异标签复用和关闭清理

同日完成第一轮优化，仅修改 Pencode。Onix 和 gotreesitter 保持原状。

- 后台仍重新读取索引／HEAD 和工作区文本；返回结果与现有干净标签的两侧内容、保存基线和磁盘基线完全相同时，保留原标签及其高亮、行数据和编辑器状态。没有依赖可能相同的 Git 状态字母判断内容是否变化，也没有新增一份全局源码缓存。
- 关闭标签立即清空指向该标签的 `gutterTab`、`diffPaneTab`、行内标记和原生编辑器文本状态；退出双栏比较时也清理这些引用，合并标签关闭时清理相应编辑器状态。
- 内容改变时仍使用新结果；已有未保存编辑继续受到保护。主题／高亮定义的有效性仍由原有高亮键检查。

### 优化前后实测

使用首轮保留的原始诊断二进制作为 before，以当前代码构建 after。两者依次在独立进程中运行，使用相同 Onix 文件、窗口、渲染后端和诊断采样代码。每次重复打开后等待 6 秒，再取累计分配增量；每轮结束执行两次 GC，为下一轮建立可比较的对象状态。分配差值包含这段时间内的后台刷新和诊断采样，**不代表纯 diff 算法的精确分配**。

最初一轮对照与竞态检查并行运行，其结果已单独存档；下面只使用随后没有并发测试任务的完整复测。每个版本各三次重复打开，数值为三次均值：

| 指标 | 优化前 | 优化后 | 变化 |
| --- | ---: | ---: | ---: |
| 服务端每次重复打开的累计分配 | 358.53 MiB | **1.58 MiB** | **减少约 99.6%** |
| GUI 每次重复打开的累计分配 | 10.49 MiB | **1.92 MiB** | **减少约 81.7%** |
| 重复打开至高亮就绪的观测时间 | 2.28 秒 | **约 0.023 秒** | 原高亮直接复用 |
| 关闭标签、两次 GC 后 GUI 堆 | 42.68 MiB | 41.62 MiB | 单轮总量，不能全部归因为关闭清理 |

观测时间通过主线程诊断接口触发生产 `sc-open:` 路径，并约每 20 ms 检查完成状态，属于短时端到端观测，不是精确延迟基准。原生窗口和两侧绘制正常运行；版本控制面板由原生鼠标事件展开。

逐次服务端分配为：before **358.39 / 358.39 / 358.80 MiB**，after **1.52 / 1.60 / 1.61 MiB**。优化后第二、三次重复打开期间，服务端采样 RSS 峰值约 **44.7 / 44.6 MiB**；对应 before 为 **261.5 / 278.8 MiB**。优化后第一轮仍约 217.7 MiB，这是首次解析后尚未归还的空闲页面，不能称为该次点击重新产生了同量存活对象。

后台读取和差异对齐仍有分配，整个重复打开操作尚非零分配；新增的“接纳未变化结果并复用标签”路径已通过零分配断言。

### 首次打开仍需后续处理

首次打开耗时约 1.83 → 1.77 秒；服务端在高亮完成时 RSS 约 253.8 → 246.3 MiB。这个差异受到 GC 和运行时采样影响，**没有证据支持本轮显著改善首次解析峰值**。首次打开后、两次 GC 的服务端堆仍约 8.1／8.5 MiB；解析期间的分配放大和 scratch 池预算仍是后续专项。

关闭后 GUI 堆没有完全回到面板基线。两个进程的启动堆也有差异，不将表中约 1 MiB 的关闭后总量差异宣称为稳定独立收益。关闭清理的正确性由回归测试直接断言文档引用已移除；通用 ECS／布局／字体缓存继续保留。

### 验证和材料

通过以下检查：

```sh
go test ./workbench -run '^Test(Source|PencodeIdleParser|PencodeSyntaxWorker)' -count=1 -timeout=180s

go test -race ./workbench \
  -run '^Test(SourceDiff(Reopen|CloseDrops|UnchangedResult)|SourceControlPencodeInteraction|SourceDiffNativeEditing|SourceDiffSnapshots|PencodeIdleParser)' \
  -count=1 -timeout=180s

git diff --check
```

新增回归覆盖：未变化比较保留左右高亮和滚动状态、Git 状态字母未变的外部文本修改、索引变化、HEAD／暂存内容变化、未保存差异编辑、关闭后文档引用清理，以及复用结果的零分配。既有源码比较、编辑／保存、合并、主题、高亮、滚动及相关竞态检查通过。诊断应用构建和原生窗口 A/B 测量完成，相关进程已退出。

新材料位于 [dist/diff-memory-optimization-20261102](/home/zhuoh/workspace/pencode/dist/diff-memory-optimization-20261102)：

- `before.json`、`after.json`、`summary.json`：最终对照数据与汇总。
- `before-trace.json`、`after-trace.json`：连续内存采样。
- `compare.py`、`overlay.json`、`*.go.txt`：测量脚本及诊断代码。
- `manifest.json`：两个诊断二进制与输入文件的 SHA256；确认 Onix 既有 tracked diff 保持一致。
- `with-race-load/`：与竞态检查并行运行的探索数据，未用于最终收益表。

本轮保留约束：没有改主题外观，没有缩减高亮内容，没有新增强制 GC 或工作集裁剪逻辑，也没有修改用户的 Onix 文件。
