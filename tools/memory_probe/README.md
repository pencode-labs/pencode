# 按需内存诊断

该工具用 Go overlay 注入按需诊断代码及 HTTP 服务。开发构建现已默认提供[持续性能记录](../../docs/profiling.zh-CN.md)；普通构建不包含本工具的 HTTP 服务或强制 GC。需要隔离按需实验时可设置 `PENCODE_DEBUG=0`，关闭持续采样。

在 Pencode 仓库的物理路径执行（macOS 的 `/tmp` 是 `/private/tmp` 的符号链接，Go 构建路径需与 overlay 一致）：

```sh
cd -P /path/to/pencode
python3 tools/memory_probe.py --output dist/memory-probe
go build -tags onix_metal,onix_release -trimpath \
  -overlay dist/memory-probe/overlay.json -o dist/pencode-probe .
```

Linux 可省略 `-tags`。诊断构建只用于开发，不用于发布。

运行时设置：

- `PENCODE_MEMORY_DIRECTORY`：新的诊断输出目录，必填。GUI 历史、设置和服务端用户资源均隔离到这里。
- `PENCODE_MEMORY_RESOURCES`：与源码匹配的发布资源目录，包含 `catalog.bin` 和 `base.dat`。使用 `onix_release` 时必填。
- `PENCODE_MEMORY_EXTENSIONS`：测试扩展目录。建议复制所需的已安装扩展，在副本中替换待测扩展二进制。

GUI 和内置服务端分别写入 `<role>-<pid>.endpoint.json`，其中包含随机本地端口和访问令牌。请求需设置 `Authorization: Bearer <token>`。

- `/snapshot?name=<label>&mode=natural`：导出运行时统计和采样堆，不主动触发 GC。
- `/snapshot?name=<label>&mode=gc`：执行一次 GC 后导出。
- `/snapshot?name=<label>&mode=trim`：执行一次 `debug.FreeOSMemory()` 后导出，仅用于诊断对照。
- `/stacks`：导出 goroutine 调用栈。
- GUI 的 `/ui?op=state`：在应用线程读取文档、扩展视图、原始图片、纹理和渲染缓冲统计。
- GUI 的 `/ui?op=project&path=<absolute-path>`：打开指定项目。
- GUI 的 `/ui?op=open&path=<relative-path>`：打开项目文件。
- GUI 的 `/ui?op=preview&path=<editor-id>`：选择打开方式；可用 ID 在 state 的 editors 中。
- GUI 的 `/ui?op=close&path=<relative-path>`：走正常文档关闭流程。
- GUI 的 `/ui?op=action&path=<action-id>`：执行 state 中列出的操作。
- GUI 的 `/ui?op=clear-ui&path=<extension-id>`：关闭指定扩展的视图。
- GUI 的 `/ui?op=quit`：退出诊断实例。

`state` 请求本身会唤醒事件循环。自然空闲占用应在停止发送 UI 请求后，从外部取样；不要把密集轮询下的数字当作空闲基线。

统计为真实 MemStats；heap 是采样数据，两者口径不同。分析时同时记录各进程的 macOS physical footprint，不把 `HeapAlloc`、虚拟地址空间或全部共享映射大小当作应用总内存。

```sh
go tool pprof -top -inuse_space dist/pencode-probe /path/to/gui-123-stage.heap
```
