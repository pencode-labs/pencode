# Office Viewer 大表格内存排查

2026-11-09。对象：`paoyou@100.104.96.111` 上的 `~/workspace/xiuxing/xiuxing-config/M怪物宝箱表.monster_box.xlsx`。

结论：主要成本在全量 HTML 表格进入 Onix 后产生的逐单元格布局状态、测量样式副本和裁剪实体。插件补齐空白列进一步放大节点数量。Pencode 的搜索标识、模板和 DOM 副本有额外成本，但不是最大项。

四轮优化及后续二维虚拟滚动已落地到 Pencode、`../onix` 和 office-viewer，实施内容和各轮复测结果见文末。前面的表格记录初始排查及隔离实验。最初的候选补丁、原始文件副本、诊断源码、日志和堆快照保存在被忽略的 `dist/office-memory-20261109/` 目录。

## 测量范围

- SSH 检查了原文件、已安装插件的 JavaScript/CSS、Pencode 二进制构建信息；安装插件具有与本地相同的补齐 26 列、固定表格宽度和逐格 `overflow:hidden` 行为。
- 原机为 macOS arm64、16 GiB 内存。检查时没有正在运行的 Pencode，未取得用户看到 2 GB 时的原进程快照。
- 在原机运行当前代码构建的 Onix 无窗口渲染探针；另外在 Linux amd64 上用 Pencode 现有测试夹具执行真实的 `setView → component → layout → render` 链路。
- 本地 Pencode 基于 `1590e9a`、Onix 基于 `9e88d1c`、office-viewer 基于 `f54b8dd`，包含开始排查时已有的工作区修改。已安装 Pencode 使用 Go 1.27.1，实验使用 Go 1.27.3；安装包没有可用于核对的 VCS revision。
- 下文区分 GC 后的 Go 存活堆、累计分配量和进程峰值 RSS。实验没有实际窗口、Metal 呈现或完整用户工作区，不能把实验数字当作原 GUI 进程的最终内存，也没有精确复现原版 GUI 的 2 GB 峰值。

## 文件如何被放大

文件磁盘大小约 447 KiB，ZIP 中所有条目解压后合计约 3.31 MiB。没有发现异常巨大的解压体积。

| 工作表 | 行数 | 实际范围列数 | XML 单元格数 | 当前生成的 HTML 单元格数，含行列标题 |
| --- | ---: | ---: | ---: | ---: |
| Sheet1 | 1,548 | 6 | 3,108 | 41,823 |
| baseAwards | 3,477 | 4 | 13,908 | 93,906 |
| adBonusAwards | 11,150 | 4 | 44,600 | 完整生成将有 301,077，当前提前报大小限制 |
| drawing | 2,790 | 4 | 11,160 | 75,357 |
| guaranteeDrawing | 2,442 | 4 | 9,768 | 65,961 |

`extensions/office-viewer/src/workbook.ts:123` 使用 `Math.max(26, this.columnCount)`，并为每行追加空 `<td>`。第一张表仅 G–Z 这 20 列就增加 30,960 个空格。前六列内也有大量稀疏空格。

当前 1.8 MiB 限制检查 HTML 字节/字符串长度，无法代表最终节点成本：第一张表约 575 KiB HTML，已经生成四万多个单元格；第二张约 1.37 MiB HTML，生成九万多个。第三张表还会因补齐空列触发错误，无法预览。

插件的顺序打开五张表实验中，含 TypeScript 加载器的 Node 进程 RSS 最高约 196 MiB；这不是发布插件的独立基准，但足以说明本次测得的 GB 级开销主要发生在后续渲染链路。

## 首要问题：Onix 每格复制完整计算样式

位置：`../onix/html/layout/table.go:274`，`layoutTableCell`。

每个单元格第一次布局都会分配 `new(style.ComputedStyle)`，修改其中的 margin、width、box-sizing 后长期挂在 `Box.tableMeasureStyle` 上。`ComputedStyle` 实测为 **5,544 字节**，在当前 Go 分配器中进入更大的尺寸档；相同样式、相同列宽的空格仍各有一份。CSS cascade 已经做了样式共享，这一步又把共享结果复制成逐格对象。

第一张表单独执行 DOM、样式、布局：

| 阶段 | GC 后 Go 存活堆 |
| --- | ---: |
| DOM | 9.77 MiB |
| 计算样式 | 11.05 MiB |
| 布局完成 | 298.60 MiB |

堆采样中 `layoutTableCell` 的 flat 存活量约 **245.94 MiB，占 79%**。这是采样估计，不能与精确 `MemStats` 数字逐项等同。

候选改法：同一 table 内，按原样式指针和实际内容宽度共享测量样式；缓存挂在 table scratch 上，重排时更新共享值，清除本轮未使用的键。保留文本片段持有的样式生命周期，不能直接把一个临时栈变量复用于所有不同宽度的单元格。

隔离补丁验证了跨高度修改的增量布局与全量布局一致，现有 warm table reflow 测试仍为零分配。落地时补充了不同列宽、原地样式修改、单格隔离及删行后缓存回收的回归覆盖。

## 第二个问题：离屏及空单元格仍创建裁剪实体

位置：

- `extensions/office-viewer/resources/viewer.css`：`.sheet th, .sheet td` 统一设置 `overflow:hidden`。
- `../onix/ui/html/partial.go:494`：`htmlBoxNeedsOverflowClip` 根据 overflow 和盒子尺寸判断，未排除空内容或离屏格。
- `../onix/ui/html/surface.go:3322`：`applyHTMLOverflowClips` 遍历全树，为满足条件的格创建实体及路由。
- `../onix/ui/html/paint_cull.go`：绘制窗口只限制部分绘制对象，并不让完整 DOM、布局、逐盒渲染状态和 clip 实体变成按需创建。

原 Mac 的探针中，第二张表只有 **381 个文字实体**，却有 **93,919 个裁剪实体**。已有 paint culling 不等于表格虚拟化。

插件侧的低风险候选规则是 `.office-content .sheet td:empty { overflow:visible; }`：这里空格没有文字或子节点，不需要裁剪。保留有内容的格原有裁剪，避免长文本跨列。该规则也保留了空白网格、所有行列和单元格尺寸。

引擎侧可以进一步让没有需要裁剪的后代绘制内容的盒子不创建 clip，或让离屏 clip 随绘制窗口挂载。后者需要同时处理嵌套滚动、输入、文本选择和离屏搜索定位，不能只按当前屏幕坐标跳过创建。

## 对照结果

Linux Pencode 无窗口完整链路，第一张表。每个方案独立进程，工作台基线约 43 MiB；为控制测试机峰值使用 `GOMEMLIMIT=700MiB`，该值不是产品修改。

| 方案 | GC 后存活堆 | 进程峰值 RSS |
| --- | ---: | ---: |
| 当前实现，26 列全量 HTML | 527.24 MiB | 723.82 MiB |
| 仅取消空格裁剪 | 453.40 MiB | 530.33 MiB |
| 仅共享测量样式 | 282.27 MiB | 520.18 MiB |
| 共享测量样式 + 取消空格裁剪 | **208.45 MiB** | **292.67 MiB** |
| 仅保留范围内的 6 列，当前引擎 | 178.62 MiB | 261.54 MiB |
| 仅取前 100 行，保留 26 列，当前引擎 | 76.78 MiB | 136.57 MiB |

两项组合使存活堆减少约 **60.5%**，仍保留全部行、26 列空白网格和所有数据。共享样式采用保留缓存的候选版本，组合实验及 warm reflow 检查均通过。

6 列和 100 行两项仅用于量化节点规模成本：没有把它们应用到插件，也不代表已实现可用的虚拟滚动。直接截断行或删除空列会改变体验。

原 Mac，Onix 无窗口渲染第二张表，未设置 Go 内存上限：

| 指标 | 当前实现 | 共享测量样式 + 取消空格裁剪 |
| --- | ---: | ---: |
| GC 前 Go 堆 | 1,134.24 MiB | 272.98 MiB |
| GC 后 Go 存活堆 | 895.62 MiB | **240.15 MiB** |
| 峰值 RSS | 1,205.44 MiB | **352.02 MiB** |
| 裁剪实体 | 93,919 | **17,425** |
| 文字实体 | 381 | 381 |

原 Mac 第一张表当前实现的存活堆约 400 MiB，峰值 RSS 约 567 MiB。该探针只含 Onix HTML 渲染，不能与 Linux Pencode 工作台数值直接相减或合计。每项为单次对照，时间数据不作为稳定性能结论。

## Pencode 自身的优化空间

`workbench/custom_editor.go:341` 的 `setView` 链路包含以下阶段：

1. `workbench/find_rendered.go:37` 的 `renderedFindTemplate` 为每个元素添加长 ID，空 td 也包含在内，生成新的 HTML 字符串。
2. `validateExtensionView` 解析一份 DOM，selection 和 find 保留它。
3. `tooltipTemplate` 再次扫描 HTML；`htmlui.DefineScoped` 解析模板 DOM。
4. Onix 挂载组件时克隆模板，并维护布局、渲染和查询索引。

第一张表在 Pencode `setView` 完成但尚未渲染时，存活堆已从约 **43 MiB 增加至 92 MiB**。完整链路堆采样也显示 DOM 属性、克隆节点和节点分配合计为数十 MiB。

推荐把搜索/选择元数据和 tooltip 标识在一次遍历中生成，并设计明确所有权的已解析模板接口，复用校验得到的不可变 DOM。只给实际需要边界查询的文本容器、选择区域、滚动祖先和交互元素补 ID。修改时保留表格复制的 tab/newline 分隔，以及未进入绘制窗口的搜索结果定位。

XML 部分优先级更低：`Archive` 当前一次性解压所有支持的条目；`SheetData` 仍保留 XML 节点和嵌套 Map。后续可做按工作表解压、紧凑单元格记录及格式预计算，减少切表时解析/格式化的临时分配。本文件只有约 3.31 MiB 解压内容，先改这些无法解决主要的渲染放大。

## 建议实施顺序

1. 先落地 table 测量样式共享和空格免裁剪。隔离实验已证明收益，外观和全表搜索语义可以保留。
2. 做真正的二维表格窗口化：常驻数据、稀疏行列索引和全文搜索索引；DOM/布局/实体只维护视口与缓冲区。空白扩展区域按需绘制，滚动范围、列宽、行高保留。当前扩展事件绑定不包含 scroll，需一并设计宿主的视口数据与事件契约，避免用翻页或截断替代原有连续滚动。
3. 合并 Pencode 模板处理，减少重复 DOM 和无用 ID。
4. 再优化插件 XML 数据结构、按需解压和切表分配。

不要先提高 1.8 MiB HTML 限制；这会让第三张表产生约三十万个单元格。也不建议用强制 GC 掩盖问题，当前最大的几项都是有效引用持有的对象。

## 验证与复查

通过：

- 原机实际工作簿的五张表解析/输出规模检查。
- `go test ./workbench -run '^TestOfficeViewerSavedColumnLayout$' -count=1`。
- 候选 overlay 下 Onix `go test ./html/layout -run 'Test.*Table' -count=1`，含增量布局一致性及零分配检查。
- 候选 overlay 下 Pencode `TestOfficeViewerSavedColumnLayout`、`TestOfficeViewerSelectionCopyAndSearch`、`TestOfficeViewerClippedSelectionCopiesOnlySelectedText`。
- 候选 overlay 下 `fieldalignment ./html/layout` 无诊断。

没有运行 `go test ./...`。未验证完整 macOS GUI、Metal/GPU 内存、长时间切表、候选样式缓存的全部失效场景。

本地诊断目录：`dist/office-memory-20261109/`。原机临时诊断目录：`/tmp/pencode-office-memory-20261109/`，没有替换安装包或修改原工作簿。

主要文件：`inspect.mts`、`probe/main.go`、`renderprobe/main.go`、`workbench_probe_test.go.txt`、`overlay.json`、`shared-overlay.json`、`shared-table-style.patch`、`workbench-*.txt`、`render-first.txt`、`render-second.txt`、`render-second-combined.txt` 和 `*.heap`。

复查示例（从 Pencode 仓库根目录运行）：

```sh
go tool pprof -top -nodecount=20 -inuse_space \
  dist/office-memory-20261109/workbench-probe \
  dist/office-memory-20261109/sheet-0.html.render.heap

go test ./workbench -run '^TestOfficeViewer' -count=1
```

## 第一轮已实施

生产改动分布在三个仓库，交付时需要一起保留：

- **Onix**：`html/layout/table.go` 按原计算样式和内容宽度共享测量样式，每次表格重排刷新仍在使用的值并移除过期键；空表不创建测量缓存。`html/layout/box.go` 移除了逐单元格的 `tableMeasureStyle` 字段。
- **office-viewer**：`resources/viewer.css` 对空 td 设置 `overflow:visible`，有内容的单元格继续裁剪，保留全量行列、空白网格和已有主题样式。
- **Pencode**：`workbench/custom_editor.go` 对已经由搜索/选择处理补齐 ID 的模板直接构建组件，跳过无实际修改的 tooltip 扫描及整份 HTML 复制。

空格裁剪生命周期测试还发现 Onix 会把零长度文本节点视为非空，导致单元格从有内容变为空后仍保留裁剪实体。`html/css/selector.go` 已修正，保留空白字符属于内容的原有行为，符合 [Selectors Level 3 的 :empty 定义](https://www.w3.org/TR/selectors-3/#empty-pseudo)。新增回归覆盖从空到长文本、清空、再次填入和再次清空，验证裁剪实体随之创建/释放且单元格尺寸保持一致。

最终生产代码复测，未使用替换引擎源码的 overlay；Pencode 探针只通过 overlay 注入诊断测试：

| 场景与指标 | 初始基线 | 最终代码 |
| --- | ---: | ---: |
| Linux，第一张表 `setView` 后存活堆 | 92.06 MiB | 88.78 MiB |
| Linux，第一张表渲染后存活堆 | 527.24 MiB | **205.14 MiB，减少约 61.1%** |
| Linux，第一张表累计分配量 | 879.12 MiB | 300.38 MiB |
| Linux，第一张表峰值 RSS | 723.82 MiB | 288.13 MiB |
| 原 Mac，第二张表渲染后存活堆 | 895.62 MiB | **240.19 MiB** |
| 原 Mac，第二张表峰值 RSS | 1,205.44 MiB | **349.98 MiB，减少约 71.0%** |
| 原 Mac，第二张表裁剪实体 | 93,919 | 17,425 |

测试和构建覆盖：

- Onix `html/css`、`html/layout` 包测试，包含新增的测量样式共享、不同宽度隔离、原地修改、删行回收和既有热重排零分配测试。
- Onix `ui/html` 的 Table、Overflow、PaintCull、StructureClip、Text、Empty 相关测试。
- Pencode 的 Office Viewer、Custom Editor、Rendered Find 和 Tooltip Template 相关测试，包含复制、跨屏搜索及空格裁剪生命周期。
- office-viewer `npm run build` 与全部 9 项测试。
- Pencode 主程序 `go build`，产物为 `dist/office-memory-20261109/optimized/pencode`。
- `fieldalignment ./html/css ./html/layout` 和 Pencode `fieldalignment ./workbench`，无诊断。

最终诊断日志、探针和 Linux 构建产物在 `dist/office-memory-20261109/optimized/`。macOS 侧仅运行临时探针，尚未替换 `/Applications/Pencode.app` 或已安装插件。

本轮完成测量样式、空格裁剪和重复模板扫描优化。二维表格窗口化、减少完整 DOM 副本及按需 XML 解压仍未实施；第三张表仍受原有 1.8 MiB 预览限制。


## 第二轮已实施：共享解析树与减少空节点 ID

本轮修改 Pencode 和 Onix，沿用第一轮 office-viewer CSS：

- **Onix**：增加 `htmlui.NewDocumentTemplate`，接收已解析文档的所有权，提取内嵌样式、验证组件引用并保存编译结果。`Define` 复用此结果；挂载时仍为每个实例克隆，避免组件渲染修改搜索/选择持有的只读树。文档模板的 CSS scope 对 DOM 内容与 CSS 计算稳定哈希，属性 map 的迭代顺序不影响结果。
- **Pencode**：`setView` 只解析一次，验证、搜索、选择和组件模板共享同一份只读树；在验证遍历中直接补充 ID/tabindex，移除全量 HTML token 扫描、字符串扩写及组件的重复解析。由三份 DOM 降为两份：一份只读模板/索引树与一份挂载的可变树。
- **ID**：保留作者提供的 ID、引用、文本容器、所有可能承载其滚动偏移的祖先及交互控件；空叶节点（尤其补齐列的空 td）不再生成 ID。自动生成的 ID 同样经过前缀和重复检查。空单元格本身和全量网格仍保留，复制时继续输出空格对应的制表符。

以下为 Linux amd64 完整 Pencode 链路测量，使用同一批实际工作簿生成的 HTML 和第一轮 CSS。每次独立进程、同一 `GOMEMLIMIT=700MiB`；第二张表的第一轮数据使用留存的第一轮探针二进制在本轮补测。`setView` 和渲染堆均为 GC 后存活量，包含工作台基础内存（第一张约 43.1 MiB，第二张约 43.9 MiB）。累计分配量自进程启动计算。RSS 包含运行时保留页和临时峰值，不等于存活堆。

| 工作表与指标 | 第一轮 | 第二轮 | 相对第一轮下降 |
| --- | ---: | ---: | ---: |
| Sheet1：`setView` 后堆 | 88.78 MiB | **52.53 MiB** | 40.8% |
| Sheet1：渲染后堆 | 205.14 MiB | **167.44 MiB** | 18.4% |
| Sheet1：累计分配量 | 300.38 MiB | **241.23 MiB** | 19.7% |
| Sheet1：峰值 RSS | 288.13 MiB | **244.44 MiB** | 15.2% |
| baseAwards：`setView` 后堆 | 144.48 MiB | **66.70 MiB** | 53.8% |
| baseAwards：渲染后堆 | 423.07 MiB | **342.66 MiB** | 19.0% |
| baseAwards：累计分配量 | 675.30 MiB | **544.78 MiB** | 19.3% |
| baseAwards：峰值 RSS | 570.37 MiB | **495.75 MiB** | 13.1% |

第一张表相对最初实现，渲染后存活堆从 527.24 MiB 降至 167.44 MiB，累计下降约 **68.2%**。本轮没有在原 Mac 上复测完整 GUI，不能将 Linux 数字直接作为原机应用内存。

验证已通过：

- Onix `go test ./ui/html -count=1`，包含新增的解析文档复用、模板只读性、不同实例隔离、内嵌 CSS、属性顺序无关的 scope 和无效文档/重复引用校验。
- Pencode 的 Office Viewer、Custom Editor、Rendered Find、Tooltip Template 和 Extension 相关测试。新增空单元格的 ID/作者 ID/引用保留与生成 ID 冲突检查；选择复制用例覆盖空格制表符，跨屏查找目标位于 25 个空列之后。既有热搜索和选择同步零分配断言继续通过。
- `fieldalignment ./ui/html`（Onix）与 `fieldalignment ./workbench`（Pencode），无诊断。
- Pencode 主程序构建通过，产物为 `dist/office-memory-20261109/round2/pencode`。

本轮日志、探针、堆快照和 Linux 构建位于 `dist/office-memory-20261109/round2/`，堆快照按惯例保存在对应 HTML 文件旁（`.round2.*.heap`）。Onix 的改动必须与 Pencode 一起交付；尚未替换 Mac 应用或已安装插件。

剩余主要成本是完整表格布局盒、逐盒渲染状态、完整可变 DOM 和 scope class 属性。二维窗口化与按需 XML 解压仍未实施，第三张表仍受原有 1.8 MiB 预览限制。


## 第三轮已实施：共享 scope 属性与压缩渲染快照

本轮生产改动集中在 Onix，沿用第二轮 Pencode 和第一轮 office-viewer 代码。

**组件 scope 属性共享。** 原来每个无属性的空 td 在挂载时都会创建一张只含 `class=onix-scope-...` 的 map。现在在同一次组件子树准备过程中，共享这张不可变属性表。DOM 新增 `ShareAttributes`、`SetAttr` 和 `RemoveAttr`；对共享节点首次编辑属性时复制一份，之后继续复用其私有 map。HTML Node 的设置、删除属性和显隐接口统一走这些方法，保留同值写入和缺失属性删除不分配的行为。已有作者属性仍单独克隆。

共享所有权由 DOM 节点记录。Parser Reset 会丢弃共享属性引用，而不是清空或将共享 map 留给下次解析，避免影响共享者以及后续节点意外串用存储。新增回归同时覆盖共享源、兄弟节点、组件实例和只读模板隔离；千节点 scope 初始化不再逐节点分配 map，热准备及已分离节点的属性更新仍为零分配。

**渲染样式快照压缩。** `htmlBoxRendering` 不再给每个布局盒保存 `overflow-x`、`overflow-y` 和 `visibility` 的完整通用 `style.Value`（每个值还含字符串、长度、百分比、数值和颜色载荷）。两个 overflow 保存为 uint8 模式，visibility 保存为可见性 bool。保留 hidden/clip/auto/scroll 的区别以及与当前可变计算样式独立的历史快照，继续用于滚动路由、局部重绘和裁剪失效判断。字段按 `fieldalignment` 调整位置。

同一 Linux amd64 无窗口完整 Pencode 链路、同一 HTML/CSS、同一 `GOMEMLIMIT=700MiB`，每项独立进程：

| 工作表与指标 | 第二轮 | 第三轮 | 相对第二轮下降 |
| --- | ---: | ---: | ---: |
| Sheet1：渲染后存活堆 | 167.44 MiB | **148.11 MiB** | 11.5% |
| Sheet1：累计分配量 | 241.23 MiB | **222.06 MiB** | 7.9% |
| Sheet1：峰值 RSS | 244.44 MiB | **218.49 MiB** | 10.6% |
| baseAwards：渲染后存活堆 | 342.66 MiB | **301.39 MiB** | 12.0% |
| baseAwards：累计分配量 | 544.78 MiB | **503.75 MiB** | 7.5% |
| baseAwards：峰值 RSS | 495.75 MiB | **472.20 MiB** | 4.8% |

工作台基线分别约 43.12 MiB / 43.92 MiB。节点新增属性所有权标记，使挂载前 `setView` 堆略增：第一张从 52.53 到 53.04 MiB，第二张从 66.70 到 67.71 MiB；完整渲染后的净收益已包含该成本。GC 后对象数第一张从 442,232 减至 367,994，第二张从 1,017,562 减至 864,609。第一张表相对最初实现的渲染堆从 527.24 MiB 降至 148.11 MiB，累计下降约 **71.9%**。

验证通过：

- Onix `go test ./html/dom ./ui/html -count=1`，包含新增的共享属性首次写入/删除隔离、无变化操作和后续更新零分配、Parser 重用、组件实例隔离及千节点 scope 分配检查；既有 overflow 模式切换与 fresh render 对照、visibility、局部更新、滚动 culling、组件动态更新等测试继续通过。
- Onix `go test ./html/css ./html/style ./html/layout -count=1`。
- Pencode Office Viewer、Custom Editor、Rendered Find、Tooltip Template、Extension 和 Pencode Extension 测试，包含空格复制、跨屏定位、空单元格裁剪生命周期和热搜索/选择零分配断言。
- `fieldalignment ./html/dom ./ui/html`，无诊断；Git diff 空白检查通过。
- Pencode 主程序构建通过，产物为 `dist/office-memory-20261109/round3/pencode`。

日志、诊断二进制和 Linux 主程序位于 `dist/office-memory-20261109/round3/`；堆快照位于对应 HTML 文件旁（`.round3.*.heap`）。本轮未替换原 Mac 应用或安装插件，以上数字仍不能代表其实际 GUI 峰值。

本轮保留全量网格、布局、搜索和选择。剩余主要成本仍是完整布局盒和每盒的渲染状态；二维窗口化、按需 XML 解压以及第三张表的 1.8 MiB 预览限制尚未处理。


## 第四轮已实施：按需保存布局与渲染辅助状态

本轮生产改动集中在 Onix，保留前三轮 Pencode 和 office-viewer 改动。

- **列表标记缓存**：`Box` 的列表序号、格式化文本、样式键和有效性状态移入按需创建的 `listMarkerCache`，只有 list-item 使用。普通表格单元格不再预留这些字段。列表重排仍会使序号改变的子项失效；缓存继续避免热重排反复格式化标记字符串。
- **根视口**：文档视口矩形改为仅根布局盒拥有的存储，后代不再各留一份始终为零的矩形。根存储在后续尺寸变化中复用。读取接口由 `box.Viewport` 字段改为 `box.Viewport()` 方法，仍返回原来的矩形值；视口写入由 Build / Cache.Update 维护，几何哈希使用新接口。Pencode 及 Onix 内部调用已检查并通过构建。
- **输入上下文**：后代共享从父节点继承的控件、焦点归属和控件矩形；只有改变归属的节点保存自有输入上下文。自有存储保留供后续局部更新复用，避免反复分配；既有上下文所有者优先刷新的顺序继续生效。
- **效果边界**：普通盒子的绘制边界直接取 border box，只有阴影扩展或合成组需要的额外边界才创建 `htmlBoxEffects`。曾使用效果的节点保留其辅助存储用于后续更新，其他单元格不分配。

64 位诊断中，单个 `layout.Box` 原始结构大小从 **856 降至 760 字节**，`htmlBoxRendering` 从 **512 降至 424 字节**。实际 Go 堆分配还受尺寸档位影响，下面的完整链路数字才是最终净收益。

新增的阴影、滤镜、普通绘制循环对照还发现：普通背景 surface 在写入器完成后才附加 InputTarget，切换成复用的合成图时，该输入组件没有被可选组件追踪并移除。已将背景输入目标及普通/合成 surface 标记纳入写入器管理，清理残留命中区域和类型标记。新增回归验证效果切换后的绘制和输入状态与全新渲染一致。

同一 Linux amd64 无窗口完整 Pencode 链路、同一工作簿 HTML/CSS、同一 `GOMEMLIMIT=700MiB`，每项独立进程：

| 工作表与指标 | 第三轮 | 第四轮 | 相对第三轮下降 |
| --- | ---: | ---: | ---: |
| Sheet1：渲染后存活堆 | 148.11 MiB | **140.14 MiB** | 5.4% |
| Sheet1：累计分配量 | 222.06 MiB | **214.12 MiB** | 3.6% |
| Sheet1：峰值 RSS | 218.49 MiB | **215.44 MiB** | 1.4% |
| baseAwards：渲染后存活堆 | 301.39 MiB | **283.19 MiB** | 6.0% |
| baseAwards：累计分配量 | 503.75 MiB | **485.84 MiB** | 3.6% |
| baseAwards：峰值 RSS | 472.20 MiB | **436.52 MiB** | 7.6% |

工作台基线分别约 42.94 / 43.90 MiB，`setView` 后存活堆约 52.95 / 67.67 MiB。表中渲染堆包含工作台基础内存，累计分配量自进程启动计算。第一张表相对最初实现从 527.24 MiB 降至 140.14 MiB，累计下降约 **73.4%**。

验证通过：

- Onix `go test ./html/layout ./ui/html -count=1`。新增列表 start/value/reversed 和标记样式变化与全量布局对照、普通盒子不持有列表缓存、根视口尺寸变化及几何哈希一致性、焦点归属切换与兄弟隔离、效果缓存生命周期及 surface 组件清理测试。
- 既有列表标记热重排、保留布局热 resize、组件/作用域和其他热路径零分配测试继续通过；既有嵌套控件、裁剪、透明组、阴影 culling、局部结构更新和滚动定位回归继续通过。
- Pencode Office Viewer、Custom Editor、Rendered Find、Tooltip Template、Extension 和 Pencode Extension 相关测试，包含复制、跨屏查找与选择同步。
- `fieldalignment ./html/layout ./ui/html`，无诊断；Git diff 空白检查通过。
- Pencode 主程序构建通过：`dist/office-memory-20261109/round4/pencode`。

日志、结构大小诊断和 Linux 构建位于 `dist/office-memory-20261109/round4/`，堆快照位于对应 HTML 文件旁（`.round4.*.heap`）。完整链路测量仍只通过 overlay 注入诊断测试，没有替换生产源码。结构大小诊断使用单独的 overlay，仅用于比较记录载荷大小。

本轮未部署到原 Mac，数字不代表实际 GUI 峰值。全量网格仍完整保留；二维窗口化、按需 XML 解压以及第三张表的 1.8 MiB 预览限制尚未处理。

## 二维虚拟滚动已实施（2026-11-10）

本次将 Excel 从全量 HTML 表格改为稀疏数据驱动的二维虚拟网格。工作表仍完整读取 XML 并格式化数据；DOM、布局盒和渲染实体仅为当前可见区域及缓冲区创建，不再为全部行列预留节点。

- **插件入口**：`SheetData.render()` 通过空容器的 `data-pencode-grid` 属性传递 HTML 转义后的 JSON。数据包含行列范围、默认尺寸、尺寸覆盖、去重样式及按行列排序的非空单元格元组。不会补齐空白单元格；忽略可预览范围之外或等于默认值的尺寸覆盖。此工作簿第一至第三张表的视图分别约 80.6 / 368.3 / 1195.8 KiB，第三张不再因为补齐到 26 列而触发 1.8 MiB 限制。
- **原生窗口**：Pencode 在编译模板前取走数据载荷，模板只保留空网格容器。行列几何由默认尺寸及稀疏尺寸覆盖计算，无需为 Excel 的最大行列范围建立稠密数组。窗口上下缓冲 8 行、左右缓冲 2 列；滚动尚在缓冲范围内时不更新节点。跨窗口后使用稳定单元格键和有界环形缓存复用重叠节点及属性，卸载旧区域。外层 custom editor 统一承担横向和纵向原生滚动，包含滚动条拖动。
- **全文搜索**：索引由完整稀疏单元格文本生成，不依赖已挂载 DOM。命中离屏数据后，根据真实行列几何更新滚动位置，再挂载目标窗口并匹配文字片段、绘制高亮。行列标题不进入数据搜索索引。
- **选择与复制**：选择端点使用完整数据的文本偏移，原单元格卸载后仍保留选区。复制时才按实际单元格位置还原制表符和换行，支持完整文本、跨空白列和离屏数据；全选按已用数据列对齐，不复制行列标题及补齐网格的末尾空白。样式/主题更新保留偏移，切换工作表或替换数据时清理旧选择和数据引用。
- **显示行为**：保存的列宽、行高、水平/垂直对齐和换行设置继续生效。行高为确定的已保存尺寸（至少容纳一行文字），换行内容超出该高度时裁剪；全文仍可被搜索和全选复制。

同一 Linux amd64 无窗口完整 Pencode 链路、同一原始工作簿，使用原先的测试夹具及 `GOMEMLIMIT=700MiB`，各表独立进程；以下是初次渲染并 GC 后的存活堆，包含工作台基础内存：

| 工作表 | 第四轮 | 二维虚拟滚动 | 相对第四轮下降 |
| --- | ---: | ---: | ---: |
| Sheet1 | 140.14 MiB | **49.63 MiB** | 64.6% |
| baseAwards | 283.19 MiB | **57.86 MiB** | 79.6% |
| adBonusAwards | 原先超过 HTML 限制 | **60.71 MiB，正常渲染** | — |

首表相对最初 527.24 MiB 的存活堆累计下降约 **90.6%**。本轮工作台基线约 42.6–43.6 MiB；初次渲染时网格实际挂载 369 / 779 / 779 个单元格及标题节点，而非 41,823 / 93,906 / 301,077 个。挂载量随视口和缓冲范围变化。

额外执行 21 个覆盖表格各处的纵向位置跳转，每次推进两帧，最后 GC 后存活堆分别为 **54.92 / 60.89 / 64.03 MiB**；最后窗口挂载 387 / 817 / 817 个节点。样式解析、ECS 和虚拟窗口缓存预热会增加少量存活内存。此滚动压力过程的峰值 RSS 约 175 / 191 / 199 MiB，工作量多于第四轮仅初次渲染的 RSS 探针，不作同负载对比。

验证通过：

- 新增真实应用夹具回归：二维视口限制、原生滚动位置跳转、远处搜索及高亮、重复文本对应的真实偏移、旧区域卸载、真实指针跨空白列拖选、离屏选区复制、全选、保存尺寸与换行、缩放、样式刷新和工作表替换。
- 最大 Excel 稀疏范围 `XFD1048576` 的几何和存储验证，以及非法元组、范围、样式、尺寸覆盖验证；全选数据列对齐和 Unicode 部分复制验证。
- 稳定窗口、查找与选择同步的零分配检查；既有 Office、Custom Editor、Rendered Find、Extension Selection、Input Context 等相关回归。
- 插件 `npm run build`、`npm test`；Go 宿主的 Office Viewer 注册及 JavaScript 测试、PEB 相关测试。
- `fieldalignment ./workbench` 和两仓库 diff 空白检查；Pencode 主程序构建。

PEB 的二进制消息格式不变，新增声明式 `data-pencode-grid` 视图能力，格式说明在 `peb/editor.go`。此功能需要同时更新 Pencode 主程序与 office-viewer 插件。Linux 构建、视图、诊断源码、日志和堆快照位于 `dist/office-memory-20261109/virtual/`；overlay 仅注入诊断测试，不替换生产代码。

本次未替换原 Mac 上的主程序或插件，未取得实际 GUI 峰值。XML 仍完整解析，文件、XML、解压总量和稀疏视图数据的大小限制继续保留；本轮没有实现按需 XML 解压。


## 2026-11-10：按需解析和分块传输

已移除插件的整页约 1.8 MiB 检查，同时保留 PEB 单个视图和消息预算。ZIP 仅索引中央目录，成员按需读取、解压和校验；工作表及共享字符串保留源范围索引，按可视行列解析单元格。宿主通过 `grid` 内部事件请求窗口，64 KiB 的 UTF-8 数据块以 base64 传输，完成后原子合并；切换工作表时以 source/request 标识丢弃旧响应。查找、全选和复制按需补齐完整稀疏索引，原生 DOM 仍保持二维虚拟化。稳定窗口与请求等待路径的零分配测试通过。

Word 和 PowerPoint 使用平衡 HTML 的有限分段缓存，大表格和单个超长段落均可继续浏览；这两类预览的查找、全选限于当前分段。原有 32 MiB 原文件、64 MiB 总解压、单项 16 MiB、XML 8 MiB 等归档预算仍保留；宿主完整表格索引的单次组装预算为 128 MiB。

对本地 `monster_box.xlsx` 的插件端检查结果（不含 Go 宿主或原生渲染堆）：

| 工作表 | 行数 | 初始 HTML | 首个 49 行 × 26 列窗口及元数据 | GC 后 Node 存活堆 |
| --- | ---: | ---: | ---: | ---: |
| 1 | 1548 | 1096 B | 1926 B | 9 MiB |
| 2 | 3477 | 1095 B | 2999 B | 10 MiB |
| 3 | 11150 | 1096 B | 3014 B | 11 MiB |

每个窗口均只需一个数据块。合成测试另覆盖大于 1.8 MiB 的完整稀疏索引、超大共享字符串、真实 Node PEB 进程、文档向前/向后分段、UTF-8 跨块边界、切表旧响应、主题重编译、加载中的复制与搜索。
