# 开发版本持续性能记录

开发构建（`version.Value=dev` 且未启用 `onix_release`）默认开启性能记录。桌面进程、内置本地服务端和独立服务端分别采样，在标准错误输出中打印各自目录；标准输出仍用于服务端协议。`--version`、更新辅助进程和 SSH askpass 不启动记录。

默认位置为 `~/.pencode/profiles/<UTC 时间>-<gui|server>-<PID>-<随机后缀>/`。Windows 使用用户主目录下的 `.pencode/profiles/`。每次运行创建独立目录，不覆盖上次记录。

```sh
# 自定义记录根目录，桌面端和本地服务端都继承此设置
PENCODE_PROFILE_DIR=/path/to/profiles make run

# 关闭；也可设置 PENCODE_DEBUG=1 显式开启发布构建的记录
PENCODE_DEBUG=0 make run
```

SSH 上的独立服务端使用远端环境变量及目录。外部扩展、语言服务器和终端子进程不包含在当前进程的 CPU/堆采样中。

| 文件 | 内容与留存 |
| --- | --- |
| `session.json` | 时间、角色、PID、版本、系统及架构 |
| `cpu-000000.pprof` | 连续 Go CPU 采样，每分钟封存一段，保留最近 60 段 |
| `heap-000001.pprof` | 每分钟的 Go 采样堆，包含 `inuse_space` 和累计 `alloc_space`，保留最近 60 份 |
| `resources-000001.pprof` | 与堆快照同一时刻的资源指标，保留最近 60 份 |
| `heap-baseline.pprof`、`resources-baseline.pprof` | 启动时的基线，始终保留 |
| `heap-final.pprof`、`resources-final.pprof` | 正常退出时的最终快照 |
| `metrics-000000.jsonl` | 每 5 秒记录时间、完整 Go MemStats、goroutine 数、进程 CPU 累计纳秒数、进程／系统内存、GPU 指标；每小时轮转，保留最近 24 份 |

CPU 文件在完成时才从 `.tmp` 重命名为 `.pprof`；其他 profile 也写完后再重命名。正常退出会等待最后一段 CPU 和最终快照写完；强制终止只能保证此前封存的文件可用。每次运行内部的文件数量有界，历史运行目录保留，需要自行归档或删除。目录权限为 `0700`，文件为 `0600`（Windows 使用系统权限规则）。启动记录失败时打印错误并继续运行；后台写入失败时停止采样并打印错误。

## 分析

保留与采样匹配的可执行文件和源码版本。以下 `RUN` 是某一个进程的记录目录：

```sh
RUN=/path/to/profiles/20261114T000000Z-gui-12345-example

# CPU 热点；多段可合并分析
go tool pprof -top /path/to/Pencode "$RUN"/cpu-*.pprof
go tool pprof -http=127.0.0.1:8081 /path/to/Pencode "$RUN"/cpu-000005.pprof

# 存活堆、累计分配及相对启动基线的增量
go tool pprof -top -inuse_space "$RUN"/heap-final.pprof
go tool pprof -top -alloc_space "$RUN"/heap-final.pprof
go tool pprof -top -inuse_space -base "$RUN"/heap-baseline.pprof "$RUN"/heap-final.pprof

# 资源快照是独立指标：通过 sample_index 选择，不能相加
go tool pprof -top -sample_index=process_resident "$RUN"/resources-final.pprof
go tool pprof -top -sample_index=gpu_instance_buffers "$RUN"/resources-final.pprof
```

CPU 和 heap 是 Go runtime 生成的标准二进制 pprof，包含可解析的调用栈；资源快照同样使用 pprof protobuf，但只包含实测或明确注明口径的指标，没有分配调用栈。`metrics-*.jsonl` 用于查看长期趋势；相邻 `process.cpu_time_ns` 的差值除以墙钟纳秒差，就是进程消耗的 CPU 核数。

## 指标口径

- Go 堆使用 runtime 默认分配采样率，不修改应用的 GC 参数。快照不强制 GC，因此存活堆采样可能滞后最近一次 GC；需要结合 MemStats 和多份快照判断。
- 进程内存：Linux 读取当前 RSS；macOS 读取当前 resident size 和 physical footprint；Windows 读取 working set 和 private usage。它们都只覆盖当前进程，不是整个进程树。Go heap、RSS、footprint 不能相加。
- 系统内存：Linux 使用 `MemTotal`／`MemAvailable`；macOS 使用物理总量及 free + inactive 页（近似可用量）；Windows 使用系统物理总量和可用量。它们是整台设备的统计，不是应用独占内存。
- Linux GPU：驱动支持标准 DRM fdinfo 时读取当前进程 `drm-resident-*` 区域总量，按设备和 client ID 去重。它可能同时包含独立显存和 GPU 可访问的系统内存，共享缓冲仍可能与其他进程重叠。未提供该接口时 `gpu_drm.available=false`，不会生成虚假的零值指标。
- 所有平台的渲染器指标：实例、staging、readback 缓冲的保留字节数，以及 native／Go scratch。`native_render_scratch` 是 CPU 内存，`go_render_scratch` 已包含在 Go heap 中。`texture_cache_logical` 是工作台图片缓存按尺寸和 mip 层计算的逻辑纹理大小，排除字体图集、其他纹理和驱动开销，不能当作实际 GPU 总显存。
- 渲染器信息只在应用线程每 5 秒至多读取一次。空闲时不唤醒 UI，`graphics.sampled_at` 保留实际读取时间；空闲裁剪或最小化期间的数据可能陈旧。macOS／Windows 当前没有驱动总显存统计；缺失指标不会伪装为已测量的零值。

默认 CPU 采样和低频快照会产生诊断开销。写文件及 profile 编码在后台执行，渲染线程的普通帧检查和指标交接保持零分配。没有开启 HTTP 调试端口。

平台接口参考：[Linux DRM fdinfo 统计规范](https://www.kernel.org/doc/html/latest/gpu/drm-usage-stats.html)、[Apple rusage_info 数据结构](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/resource.h)。
