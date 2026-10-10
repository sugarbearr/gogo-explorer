# 任务管理器 UI 现代化 — 设计契约 v1

日期：2026-10-10　状态：冻结（Lead 定稿，改前先问 Lead）

## 诊断：现在哪里不现代（从 build/verify/*.png 实测）

1. 表格行高 28px、无斑马纹、无行分隔，10 行数据挤在顶部一大片空白里，下面留白 300px 却不用。
2. CPU 列的迷你条只有 2px 高、颜色随时变（蓝/橙/红），像故障点而不是数据。
3. 表头是裸文本贴顶，没有分隔带、没有背景区，和内容糊在一起。
4. 详情面板是纯 Surface 方块 + 一条 1px 边框，没有圆角、没有阴影、没有分区卡片。
5. 性能卡片的 4 个数字字号一样（24px），主次不分；圆点色标很小很随意。
6. 底部性能区与表格之间只有一条线，没有视觉分层。
7. 搜索框是细描边白底，没焦点态、没 hover 态，像 2010 年的输入框。
8. 全局圆角 6px、间距 4px，偏紧；没有阴影层级概念。

## 设计令牌（唯一真相：新建 tokens.go，别人只读）

    const (
        rSm   float32 = 6   // 小圆角：chip、迷你条
        rMd   float32 = 8   // 中圆角：输入框、按钮
        rLg   float32 = 12  // 大圆角：卡片、面板
        rXl   float32 = 16  // 特大圆角：面板外框
    )
    const (
        sp1 float32 = 4;  sp2 float32 = 8;  sp3 float32 = 12
        sp4 float32 = 16; sp5 float32 = 20; sp6 float32 = 24
    )
    // 行高
    const rowH float32 = 34   // 表格数据行（原 28）
    // 字阶
    const (fsCaption=11; fsSmall=12; fsBody=13; fsTitle=14; fsMetric=26)

    颜色（浅色，固定不随系统）：
      bg        #ffffff   窗口底
      bgSubtle  #fafafa   表格斑马纹 / 次级区
      bgSunken  #f5f5f7   标题栏 / 表头带
      surface   #ffffff   卡片
      line      #ebebef   细分隔线（比原 Border #d9d9de 轻）
      lineStrong#dcdce3   控件描边
      text      #14141a
      muted     #6b6b76   （对比度对白底 5.3:1，达 AA）
      accent    #2563eb
      danger    #dc2626 / warning #d97706 / success #16a34a

    阴影：卡片用 Shadow(0,1,3,0, RGBA(0,0,0,0.06))，浮层/面板用 Shadow(0,8,24,-4, RGBA(0,0,0,0.10))
    强调色语义不变：CPU 蓝 #2563eb / 内存 紫 #7c3aed / 磁盘 橙 #ea580c / 网络 青 #0891b2

## 硬性规则

- 主题必须继续固定浅色：view() 里 `if c.Theme().Dark { c.SetTheme(theme) }` 保留。
- 所有颜色只能来自 tokens.go 或 t.*，禁止再在业务代码里写 ui.Hex（除 tokens.go）。
- 不引入新依赖。不新增图形库。曲线继续用 Box 拼。
- 不动采样、witr、icons、procs 逻辑；只改视图层。
- 表格必须继续用 ui.Table（自带列拖动/排序），不要手搓。
- 保留所有现有测试通过的语义：搜索框居中、无「刷新」按钮、无「个进程」文案、
  表头 5 列文案不变、点表头排序、点行开详情、点多进程组展开、性能区可折叠且
  折叠后仍有「CPU 31%」这类概要文案。

## 分工与写作用域（严格不重叠）

- Lead：tokens.go（新建）+ 合并、验收
- 外壳：shell.go（新建，从 taskmgr.go 搬 titlebar/iconButton 并现代化）
- 表格：table_ui.go（新建，processesView/table/cell/nameCell/cpuCell/图标列）
- 性能：perf_ui.go（新建，performanceStrip/coreGrid/statCard/sparkline）
- 详情：detail_ui.go（新建，detailPanel/detailFacts/traceSection + 空/错态）

每个切片只写自己的新文件；taskmgr.go 的对应旧实现由 Lead 在合并时删除。

## 后续修正：分组展开/收起的两个缺陷（2026-10-10，用户真机反馈）

用户实测报告：「只能展开无法折叠，另外双击也无法折叠」。

### 根因（两层，缺一不可）

1. **只写单向赋值**：`onRowChosen` 里只有 `st.expanded[name] = true`，
   没有取反分支，展开后自然收不回去。

2. **折叠寄生于表格的选择信号**（更隐蔽的一层）：`ui.Table` 的
   `Changed()` 只在**选中发生变化**时报告一次。第一次点击已经把该行选上，
   之后再点同一行，`Changed()` 不会再出声——所以哪怕把赋值改成取反，
   第二下点击也根本到不了那段代码。
   `Submitted()`（双击）能触发，但平台的双击窗口是 **500ms**
   （见 ui/input.go 的 `now.Sub(lp.at) < 500*time.Millisecond`），
   慢速点两下就丢了——这就是「双击也无法折叠」。

### 修法

不再依赖表格的选择信号来做折叠，改由我们自己持有的元素接住点击：

- 分组行的**名称格**整格可点（`.Cursor(Pointer)`），每次单击都切换；
- 行首的**箭头 chip** 也是独立点击目标，点箭头同样切换；
- 两条路径共用 `toggleGroup(name)`，展开状态只有 `st.expanded` 一份，
  不会各记一份互相覆盖；
- 单击/双击不再需要区分——每次点击都是切换，所以「双击」表现为
  展开再收起（净效果回到原状），符合直觉。

### 回归测试

新增 `taskmgr_expand_test.go`，钉住：

- `TestGroupClickTogglesEveryTime`：连点四次，严格交替 展开/收起/展开/收起
  （这条会直接抓住「只能展开」的回归）；
- `TestGroupChipClickToggles`：点箭头 chip 也能切换，且行数与状态始终一致；
- `TestGroupCollapseDoesNotOpenDetail`：收起分组不会误开详情面板；
- `TestChildRowOpensDetail`：展开后点子行仍然是开它自己的详情；
- `TestSingleProcessRowStillOpensDetail`：单进程行不受影响，仍是开详情。
