# 改造为独立任务管理器

日期：2026-10-09
状态：实现中

## 需求（用户原话）

> 把整个软件，打造成一个任务管理器，不要做什么侧边栏的做法了。
> 进程溯源和任务管理器结合起来，还要展示对应软件的图标，
> 任务管理器的表格也要做好。

## 关键决策

### 1. 去掉侧栏，改多标签

现有 IDE 风格外壳（自绘标题栏 + 左侧导航 + 主区）整体让位：
- 顶部：自绘标题栏（沿用，窗口拖动）+ 管理/性能 两个标签
- 主体：占满整窗
- 「进程溯源」不再是一个页面，而是**选中进程时的详情面板**：
  点任一行 → 右侧或下方展开 witr 的因果链

### 2. 图标（已实测打通）

`ExtractIconExW` 取大图标 → `DrawIconEx` 画进 32 位自上而下 DIB → `ui.NewBitmap`。

实测结果（4 个真实进程，全部成功）：
```
explorer.exe     opaque=841  colors=349
Trae.exe         opaque=992  colors=64
DeepSeek Harness opaque=988  colors=457
Qoder.exe        opaque=1007 colors=266
```
肉眼确认：文件夹图标、Trae、DeepSeek 鲸鱼、Qoder 都正确。

**踩过的坑**：
- `GetDIBits` 传 NULL 缓冲区探测尺寸会返回 0，必须用真实位图 + `GetObjectW`。
- 更稳的做法是根本不读原始位图：`CreateDIBSection` 建一个 32 位 DIB，
  用 `DrawIconEx(DI_NORMAL)` 把图标画上去，各格式统一处理。
- 受保护进程（pid 4）打不开句柄，取不到路径 → 用通用占位图标。
- **位图必须缓存**：MyGo 文档明确说 bitmap 会常驻 GPU，不能在 view 里新建。

### 3. 表格

现在用的是自绘 Row 拼列。用户要「表格也要做好」，改用 `ui.Table`：
- `ui.TableColumn` 带 Title/Width/MinWidth/Align/Sortable
- 列可拖动调宽、双击自适应
- `ListState.Sort` 原生排序
- 但图标是 column 0 的 cell 内容，`ui.Image` 可以放进去

## 架构

```
icons.go      图标提取与缓存
perf.go       采样（已完成）
taskmgr.go    重建：多标签外壳 + 表格 + 详情面板
witr.go       保留（详情面板的数据源）
procs.go      保留
main.go       瘦身：只留窗口与主题
```

## 验证

- 单测：图标提取（真实进程）、图标缓存命中、采样、表格排序/筛选
- 无头渲染：进程页、性能页、选中态详情
- 真机：PrintWindow 抓图确认
- `go tool mygo vet`、`CGO_ENABLED=0 go test ./...`
