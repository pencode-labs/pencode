# 性能优化记录（2026-10-28）

本文记录第一轮结果。后续的大文件尾部快照复用与 PEB v12 区域补丁见 [第二轮记录](snapshot-and-ui-transport.zh-CN.md)；下文“剩余成本”描述第一轮结束时的状态。

基于当前未提交工作区及本地依赖 `../onix`，优化搜索、缓存文件切换、大文件编辑、聊天模板更新、LSP／高亮改动范围计算。保留原有工作区修改和主题外观。

## 本机测量

Linux amd64，i5-11500，Go 1.27.3，单包依次执行，`-benchtime=500ms`。优化前来自本轮审计；数字为单轮观测，不是统计置信区间。工作台基准不含原生窗口／GPU 呈现；LSP 不含网络和语言服务器处理。

| 场景 | 优化前 | 优化后 | 优化后 B/op；allocs/op |
|---|---:|---:|---:|
| 1 MiB 密集文字，忽略大小写，保留 5000 个结果 | 0.993 ms | 0.071 ms | 0；0 |
| 同上，忽略大小写＋全词 | 120.6 ms | 0.139 ms | 0；0 |
| 两个缓存文件切换往返 | 9.05 ms | 3.46 ms | 4,342；45 |
| 100 文件目录下切换往返 | 34.0 ms | 12.53 ms | 15,348；135 |
| 原生缓冲单字符编辑＋一帧，64 KiB | 0.659 ms | 0.468 ms | 66,219；15 |
| 同上，512 KiB | 2.76 ms | 1.41 ms | 525,313；19 |
| 同上，2 MiB | 10.03–11.17 ms | 5.05 ms | 2,098,913；22 |
| 聊天模板更新，500 条简短消息 | 2.21 ms | 1.08 ms | 516,352；8,713 |
| LSP 增量范围计算，2 MiB 文档尾部插入 | 9.66 ms | 0.147 ms | 0；0 |
| 空闲工作台 | 4.31 µs | 3.86 µs | 0；0 |

密集搜索输入是重复的 `a `，查询 `a`；全词优化前会收集约 52 万候选，因此这个收益不能外推到普通代码搜索。聊天基准采用相同的内容结构和 CSS，优化后增加稳定消息区域，仅计量 `setView`，不包含 HTML 生成、传输、挂载、布局和绘制。

## 实现

1. `textsearch/search.go`：普通忽略大小写查询使用 Unicode SimpleFold 扫描，复用调用方结果缓冲；`FindRange` 保留文档绝对偏移和全局非重叠匹配语义。选区查找不再存储全文匹配；目录搜索分别计算总数和有界显示结果。正则继续使用完整输入，以保留锚点、捕获组和零宽匹配语义。
2. `workbench/refresh.go`：同项目、同树结构、同标签结构的普通缓存文件切换只更新编辑器标题、旧／新树行、标签和状态绑定。结构、项目、编辑器类型或动作注册变化时回退完整刷新。Onix `PatchBindings` 支持显式更新 Keyed 插槽，保留未指定插槽。
3. `workbench/editor.go` 和 Onix `scene/render2d.go`、`ui/system.go`：光标位置复用正文缓冲的索引；行号映射只保留可见视觉行。软换行续行不重复编号；IME 临时文本使用独立计算，不写入正文缓冲。断点点击使用索引定位源行。
4. Codex 消息和进度使用稳定区域引用；宿主复用外层模板、未变化区域的解析树和编译模板。验证仍检查跨区域引用唯一性，并继承区域父节点的隐藏状态。区域自身不再遍历整个区域列表，避免消息数增加导致平方级挂载循环。保留输入框身份和 CSS 作用域。
5. 新增 `textchange.Between`，按块比较共同前后缀，保留 UTF-8 和 CRLF 边界；文档／语法消费者与 LSP 共用。LSP `PositionAt` 先统计换行，再仅解码目标行，不再逐 rune 解码整个前缀。

这些改动需要同时保留 `../onix` 的文本索引接口和 Keyed 绑定补丁，不能只移交 pencode 仓库中的修改。

## 验证

- `textsearch`、`textchange`、`lsp`、`client`、`server` 包测试通过。
- Codex 扩展模块测试通过。
- Onix 定向测试通过：`TextCaret`、`HTMLComponent.*Patch`、`Textarea`；未运行 Onix 全仓测试。
- 新增工作台测试覆盖缓存切换与树节点保留、连续切换、树结构变动回退、聊天模板／历史区域／输入框保留、重复引用拒绝、继承隐藏状态，以及可见行号／软换行／IME 缓冲隔离。
- 工作台较大范围测试排除了此前对照中已出现的 `TestPencodeEditorDiagnosticsFromUnopenedProblem`、`TestPencodeTreeClickPublishesWithoutMouseMove`、`TestCodexStatusBarOpensWorkspaceWhenCLIUnavailable` 和缺字体的 `TestPencodeFontExtensionRestoresSelectionAndUnloads`。其余测试中仅 `TestPencodeIconOnlyThemeChangeUpdatesTexture` 因缺少 `../../../syntax/resources/themes/monokai.json` 失败；本次没有宣称工作台全套测试通过。

复现性能基准：

```sh
go test -p 1 ./textsearch ./lsp ./workbench -run '^$' \
  -bench '^(BenchmarkSearchDense|BenchmarkIncrementalTail|BenchmarkPencode(CachedFileSwitch|TreeFileSwitch|OpenFileIdle)|BenchmarkNativeEditFrame|BenchmarkExtensionStreamingUpdate)$' \
  -benchmem -benchtime=500ms
```

原始本轮日志：`/tmp/pencode-perf-final-bench.log`、`/tmp/pencode-perf-final-tests.log`。临时文件可能被系统清理，表中结果与仓库内基准可独立保留。

## 剩余成本

- 2 MiB 编辑仍生成约 2 MiB 不可变字符串快照，尚未改造字符串消费接口。
- 文件切换仍可能触发 Onix 对无关控件的几何同步；组件保留不等于零布局成本。
- 聊天仍生成、传输并遍历完整视图；本次没有实现消息增量协议或新增虚拟列表。区域模板保留也不代表所有区域组件都跳过渲染。
- 正则精确总数统计仍会收集全部匹配；过滤大量不合格候选时，有界候选预算仍可能扩展到全文。
- LSP／语法改动范围仍进行字节比较，尚未实现跨队列版本合并的编辑日志和完整 UTF-16 行索引。
