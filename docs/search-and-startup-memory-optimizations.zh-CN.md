# 正则搜索与启动内存优化

2026-10-29，接续[文档与终端内存回收优化](memory-retention-optimizations.zh-CN.md)。本阶段修改位于 Pencode；验证使用当前本地 `../onix`。

## 正则搜索

搜索现在逐个读取匹配，不再先创建完整的二维捕获组列表，也不再为补足过滤后的结果反复从头收集。总数仍然精确，只保存调用方请求的结果数量。

扫描保留原文位置语义：对包含行首、绝对起点、词边界等左侧断言的表达式，后续查找带入前一个 rune，通过锚定且非贪婪的前缀搜索原表达式。捕获组偏移恢复为全文偏移；零宽匹配的推进和相邻空匹配抑制遵循标准库行为。没有依赖标准库内部接口或使用 unsafe。

纯文本形式的正则（包括转义文字和忽略大小写的文字）复用普通搜索路径，保留 `$0` 替换和 Unicode SimpleFold 语义。包含 U+FFFD 的表达式继续使用正则匹配，以正确识别无效 UTF-8 字节。

测试输入为 1 MiB 的重复 `a `，精确统计 524288 个匹配，只保存 5000 条结果。Linux amd64、Go 1.27.3、i5-11500，预热后使用 `-benchmem -benchtime=300ms` 测量。下表是每次搜索的累计分配，不是峰值或 RSS。

| 查询 | 优化前 B/op | 优化后 B/op | 优化前 → 后 allocs/op |
| --- | ---: | ---: | ---: |
| `a` | 73,386,714 | 0 | 524,360 → 0 |
| `(a)` | 81,782,348 | 16,830,904 | 524,374 → 524,333 |
| `\ba\b` | 73,396,624 | 16,826,368 | 524,381 → 524,326 |

一般正则仍受标准库单次匹配接口的分配限制；本轮主要消除了全量结果容器及其扩容、重复收集。没有实现新的正则执行引擎。表中时间未用于收益承诺，因为测量期间存在其他编译和测试负载。

## 工作区与图标

- 启动时只安装启动器，工作区在第一次进入时安装，之后继续保留并复用。首次进入承担原来预加载的工作。
- 扩展注册只保留图标资源。某个图标首次使用某种主题前景色时缓存对应 URI；后续相同查找零分配。主题切换、扩展卸载和重新导入均有覆盖。
- 内置主题的 CSS 与基础 IDE 图标仍按原方式准备，本轮消除的是扩展图标的全组合预展开。
- 三处模块加载调用迁移到当前 Onix 的 `onix.LoadModules[module.FullContent]`。最终验证期间，本地 Onix 又将 ECS 组件、资源和消息接口迁移为泛型方法，Pencode 的生产代码与测试同步改用新方法，没有增加兼容层。主题图标测试的资源路径也更新为当前仓库布局。

以下对照每个场景、每个版本各运行三个独立进程，GC 后读取存活堆。启动场景执行真实模块安装但不创建原生窗口；图标场景独立构建包含三个内置主题的模型，导入 1000 个资源并实际使用其中 20 个图标的一种前景色。

| 场景 | 优化前 | 优化后 |
| --- | ---: | ---: |
| 无项目启动的堆增量 | 18.753–18.754 MiB | 12.750–12.751 MiB |
| 安装的视图数 | 2 | 1 |
| 导入扩展图标后的堆增量 | 2.124–2.131 MiB | 0.099 MiB |
| 缓存的扩展图标变体 | 12000 | 20 |

启动堆减少约 6 MiB，图标测试减少约 2 MiB。它们对应不同的隔离场景，不能相加当作完整应用 RSS 的减少量；没有测量 GPU、原生窗口、LSP 或外部扩展进程。

## 验证与复现

- `textsearch`、`client`、`server` 包测试和竞态检查通过。客户端搜索覆盖截断后保留精确计数。
- 正则逐项对照标准库，覆盖捕获组、命名捕获、选区、数量限制、Unicode、无效 UTF-8、大小写、CRLF、行首尾、绝对起止、词边界及零宽匹配。最终 30 秒 fuzz 完成约 112 万次执行并通过。
- 工作台主题、图标、首次进入和保留场景测试通过。首次按需进入与预加载两种路径都验证了文件、编辑器、字体、主题、未保存文本和语法 worker 的后续复用。
- 工作台定向竞态检查通过，覆盖图标按需缓存、主题切换、卸载、重新导入、图标纹理更新和保留场景。
- ECS 接口迁移后重新通过普通构建、上述工作台定向竞态检查，以及输入菜单、查找替换、诊断装饰、忙碌动画和终端绘制定向测试。
- 空闲工作台基准仍为 0 B/op、0 allocs/op；普通文字搜索和纯文字正则的热路径也保持零分配。
- 普通 `go build` 通过。没有运行完整工作台测试套件或原生窗口视觉验收。
- 额外检查的 `TestPencodeEditorDiagnosticsFromUnopenedProblem` 失败：诊断装饰未进入最终渲染结果。当前版本与本阶段优化前的基线测试二进制都能复现，未在本轮内存优化中修改其渲染逻辑；失败日志为临时目录中的 `ecs-migration-functional.log` 和 `diagnostic-render-baseline-failure.log`。

```sh
go test -p 1 ./textsearch ./client ./server
go test ./textsearch -run '^$' -fuzz '^FuzzRegexpIteration$' -fuzztime=30s -parallel=2
go test ./textsearch -run '^$' -bench '^(BenchmarkSearchDense|BenchmarkRegexpCountBounded)$' -benchmem -benchtime=300ms
go test ./workbench -run 'Test(Pencode.*(Theme|Icon)|ExtensionUIReusesIDEIconTextures|ExtensionIconsCacheOnlyUsedThemeVariants|RetainedWorkspace)'
PENCODE_LAUNCHER_MEMORY=on-demand go test ./workbench -run '^TestPencodeLauncherMemoryProfile$' -v -count=1
PENCODE_LAUNCHER_MEMORY=preload go test ./workbench -run '^TestPencodeLauncherMemoryProfile$' -v -count=1
PENCODE_ICON_MEMORY=1 go test ./workbench -run '^TestPencodeExtensionIconMemoryProfile$' -v -count=1
```

启动测量应分别启动进程，避免共享全局缓存影响结果。基线通过 Go overlay 只替换本轮修改前的搜索、图标和启动文件；模块加载接口迁移同时保留于两侧。原始 overlay、对照二进制和日志位于 `/tmp/pencode-memory-stage2.nXojEW/`，临时目录可能被系统清理。
