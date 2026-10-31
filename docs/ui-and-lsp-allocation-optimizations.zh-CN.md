# 第二阶段：扩展 UI 与 LSP 分配优化

2026-10-30。接续流式保存、文件搜索与请求载荷的第一阶段优化，本轮处理扩展 UI 更新和 LSP 同步中的临时分配。

## 改动

- PEB 新增 `ViewRegionSplitter`。单区域修改复用已有边界和外层模板，只解析变化的区域；结构变化回到完整拆分。区域字符串保持不可变，返回的区域切片只借用到下一次成功拆分。
- 扩展 UI 的区域查找、引用及 ID 校验共用池化临时 map。区域数组在成功更新后交换所有权，旧数组才能归还；失败更新不改变已发布的视图。归还前清空 DOM、定义及字符串引用，超过 4096 项的临时存储不保留。
- LSP 文档通知用结构体和固定长度数组替代嵌套 map、动态切片。参数与 JSON-RPC 外层一次编码，消除中间完整 JSON 副本。
- LSP 编码缓冲由写入线程持有，完成写入、发生错误或关闭连接时归还。关闭时等待入队操作结束并清理队列；取消通知仍不等待服务端读取。超过 1 MiB 的编码缓冲不保留，读取缓冲也在开始下一次读取时释放超过 1 MiB 的存储。

## 基准

Linux amd64、i5-11500、Go 1.27.3。本轮修改前的文件保存在 `/tmp/pencode-phase2-baseline/`；基线 overlay 保留第一阶段改动和已有工作区修改。

下表是每次操作的累计分配，**不是 RSS 或 GC 后常驻堆大小**。UI 场景更新已有视图的一个区域，视图预先生成；LSP 场景通过 `net.Pipe` 运行完整文档同步发送路径，接收端丢弃字节，计时前预热 128 次并等待发送完成。

| 场景 | 优化前 B/op | 优化后 B/op | 分配次数：前 → 后 |
| --- | ---: | ---: | ---: |
| 500 条聊天历史，更新一个区域 | 538,560 | 7,984 | 8,674 → 37 |
| 64 KiB 文档，LSP 增量同步 | 2,272 | 267 | 28 → 3 |
| 64 KiB 文档，LSP 全量同步 | 166,002 | 718 | 24 → 2 |
| 2 MiB 文档，LSP 全量同步 | 5,037,044 | 2,810,732 | 28 → 6 |

池可能在 GC 后被清理，冷启动和并发队列增长仍会分配。2 MiB 的全量同步仍需生成并持有完整编码消息，超过池的保留上限；未声称此路径零分配。原生编辑器中段修改所需的完整字符串快照也仍存在，需要另行迁移到分块快照接口。

```sh
go test ./workbench -run '^$' -bench '^BenchmarkExtensionStreamingUpdate$' -benchmem -benchtime=100x
go test ./lsp -run '^$' -bench '^BenchmarkDocumentSync$' -benchmem -benchtime=1000x
go test -overlay /tmp/pencode-phase2-baseline/overlay.json ./lsp -run '^$' -bench '^BenchmarkDocumentSync$' -benchmem -benchtime=1000x
```

日志：`/tmp/pencode-phase2-ui-before.txt`、`/tmp/pencode-phase2-ui-after.txt`、`/tmp/pencode-phase2-lsp-before.txt`、`/tmp/pencode-phase2-lsp-after.txt`。临时文件可能被系统清理。

## 验证

- `go test ./lsp ./peb -race` 通过。覆盖通知字节的所有权、写入阻塞、并发关闭与队列清理、JSON 参数及错误、Unicode 增量范围、空文本保存语义，以及大响应读取缓冲释放。
- 区域拆分与完整解析的差分测试通过，覆盖空区域、嵌套、Unicode、原始文本元素、多区域变化、外层结构变化、失败后的恢复与旧字符串不变。
- 工作台的聊天区域、输入框保留、真实扩展补丁、链接、自定义编辑器和语言功能定向回归通过。较大范围的竞态检查中，`TestPencodeExtensionUISurfacesEventsAndCleanup` 在第 351 行等待 Enter 提交事件时出现一次超时；第二阶段修改前的 overlay 也复现了相同失败，修改后单项竞态复跑通过。没有将该间歇失败计作整组测试通过。
- `git diff --check` 通过。本轮没有修改 Onix，也没有启动原生 GUI 做视觉验证。
