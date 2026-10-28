# 大文件快照与聊天传输优化

2026-10-28，接续第一轮性能优化。改动覆盖 pencode、本地 `../onix` 和独立扩展模块的依赖文件；没有提交 Git commit。

## 大文件快照

Onix piece tree 记录旧快照仍然有效的前缀。连续尾部输入通过只追加的 `strings.Builder` 延长快照，尾部删除返回旧快照的前缀切片，不再每次复制整个文件。预留空间最多 256 KiB；第一次追加及预留空间耗尽时仍需分配、复制。删除后重新输入不能覆盖旧字符串，按需建立新存储。

旧快照保持不可变，可以继续被保存任务、语法分析、LSP、渲染和撤销记录持有。外部快照替换、缓冲压缩及中段编辑会释放追加存储的所有权，回到独立快照路径，没有使用 unsafe 字符串或可变共享内存。

| 约 2 MiB 文档，原生编辑＋一帧 | 优化前 B/op | 优化后 B/op | 优化前 → 后时间 |
|---|---:|---:|---:|
| 连续尾部输入 | 2,108,198 | 3,105 | 6.19 → 4.92 ms |
| 连续尾部删除 | 2,101,826 | 3,890 | 5.99 → 5.01 ms |

表中是整个工作台帧的分配，包含历史和布局等成本。piece tree 本身预热后的追加、尾删快照测试为零分配。**非尾部修改仍分配完整字符串**：原有 2 MiB 原生替换基准仍约 2.1 MB/op。若要消除这项成本，需要把渲染和其他字符串消费者迁移到分块快照接口；本轮没有完成这一架构迁移。

## 聊天区域补丁

PEB 升级为 **v12**。`ActionPublisher.Show` 自动为同一 UI 保留差分基线：外层模板、CSS 和事件绑定相同，只发送内容变化的 `data-pencode-region`。单消息更新先计算字节改动范围，复用区域边界，只检查变化区域；新增／删除区域、结构变化、CSS／事件变化发送完整视图。

`ViewVersion` 标识结果版本，`ViewPatch.Base` 标识依赖版本。宿主在 action 会话 I/O worker 重建原始视图，再执行现有资源处理和 UI 校验。重建前后的原始基线与资源处理改写的视图分开保存，避免自动控件 ID、图片 URI 改写破坏下一次补丁。

发送成功后才推进基线。版本不匹配触发内部 `resync` 请求，SDK 自动重发最新完整视图；关闭界面清理基线，进程重连后从完整视图开始。重复引用、未知区域、区域逃逸、无效版本、截断消息及超限重建均被拒绝。每个会话最多保留 32 个界面基线，原有帧、文本和事件数量限制继续生效。

| 微基准场景 | 完整视图载荷 | 区域补丁载荷 | 发送端编码／差分耗时 |
|---|---:|---:|---:|
| 10 条历史，末尾单字符区域变化 | 3,216 B | 117 B | 0.32 → 1.22 µs |
| 100 条历史，同上 | 30,306 B | 117 B | 1.91 → 3.37 µs |
| 500 条历史，同上 | 151,106 B | 117 B | 9.42 → 14.18 µs |

载荷不含 16 字节帧头。补丁大小取决于变化区域的完整内容，并不总是 117 B；该例减少约 99.92% 的载荷。单区域差分在此基准中分配 432 B、5 个对象；不会为了节省传输而每次重新解析全部历史。差分增加少量发送端 CPU 工作，表中明确计入，未包含进程管道、网络或宿主渲染耗时。

**扩展仍生成完整 HTML，宿主仍重建完整 HTML**；本轮消除的是稳定结构下的完整视图传输，没有引入宿主 DOM 的直接增量协议或新的消息虚拟列表。

所有扩展需要使用 v12 SDK 重新构建。仓库中的 Codex、Go、Material Icons、Markdown Preview、UI Demo、PHP 模块已同步 HTML tokenizer 的 `go.mod`／`go.sum` 依赖。其中四个模块是 Git 子模块，交付时需一并保留子模块内的依赖修改；Onix 的快照实现同样不能遗漏。

## 验证与复现

- Onix `text/piecetree`、`input` 包测试通过；快照所有权、连续追加／删除、Unicode、批量编辑、外部替换及压缩的定向竞态检查通过。没有运行 Onix 全仓测试。
- PEB 测试与竞态检查通过，覆盖补丁编解码、版本错配、写入失败、重同步、元数据保留、区域清空、嵌套区域、偏移变化和大小边界。
- 扩展会话测试验证完整视图→区域补丁→重同步真实进程链路，以及资源处理后的基线正确性。`extensions` 包测试与竞态检查通过，但排除了环境缺字体文件的 `TestFontResourcesValidateAndShareFaces`。
- 工作台定向测试通过：保存进行中继续输入、撤销／重做、旧快照不可变、IME、历史、聊天补丁后的输入框／草稿／选区保留、区域和扩展 UI 流程。
- 六个独立扩展模块的测试通过，UI Demo 无测试但编译通过。未宣称工作台全仓测试通过。

测量环境为 Linux amd64、i5-11500、Go 1.27.3，`-benchtime=500ms`，单包依次测量；工作台预热三帧，不含原生窗口和 GPU 呈现。数字是单轮本机观测，时间受运行状态影响。大文件基线用 Go overlay 仅替换优化前的 piece tree 实现，工作台代码与场景相同。

```sh
go test -p 1 ./peb ./workbench -run '^$' \
  -bench '^(BenchmarkViewTransport|BenchmarkNativeTailEditFrame)$' \
  -benchmem -benchtime=500ms

go test ./peb
go test ./extensions -skip '^TestFontResourcesValidateAndShareFaces$'
go test ./workbench -run 'Test(WireRegionPatch|NativeTailSnapshots|StreamingRegion|ExtensionHistoryRegion|SplitExtensionRegions|PencodeHistory|FileIOSave|PencodeComposition|PencodeHighlightUndo)'
```

Onix 目录下只运行定向包：

```sh
go test ./text/piecetree ./input
go test -race ./text/piecetree ./input -run 'Test(Snapshots|WarmTail|TextBuffer)'
```

本轮原始基准日志及基线 overlay 位于 `/tmp/pencode-snapshot-perf/`，其中 `before.log`、`final.log` 对应上表；临时文件可能被系统清理。
