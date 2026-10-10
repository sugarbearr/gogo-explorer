# 任务管理器设计

日期：2026-10-09
状态：实现中

## 目标

做一个现代风格（Fluent / macOS 取向）的任务管理器：进程列表 + 性能页。

## 可行性实测结论（全部亲手跑过）

### 每进程指标

| 指标 | 可行 | 依据 API | 实测 |
|---|---|---|---|
| CPU % | ✅ | `GetProcessTimes` 差分 | 燃烧 400ms 测得 0.39s，吻合 |
| 内存 | ✅ | `GetProcessMemoryInfo` (WorkingSetSize) | 526 进程 4.1ms |
| 磁盘 | ✅ | `GetProcessIoCounters` 读写差分 | 有量 |
| 网络 | ❌ | 需 ETW | `GetPerTcpConnectionEStats` 返回不可信值；`Network Interface` PDH 计数器在本机为空 |
| GPU | ⚠️ | PDH `GPU Engine(*)` | 能拿到 1107 个有效采样，但**展开后的路径已含计数器名**，不能再拼一次 |

**注意**：用户截图里的列是 CPU / 内存 / 磁盘 / 网络，没有 GPU 列。所以 GPU 不做。

### 系统级指标

| 指标 | 可行 | API |
|---|---|---|
| CPU % | ✅ | `GetSystemTimes`（实测 8.24%） |
| 内存 | ✅ | `GlobalMemoryStatusEx`（68.4 GB，用 83%） |
| 磁盘 | ✅ | `GetDiskFreeSpaceEx`（C: 46/322 GB） |
| 网络 | ✅ | `GetIfTable` v1（实测 RX 288 KB/s，TX 55 KB/s） |

**踩过的坑**：
- `MIB_IF_ROW2`（GetIfTable2）我手写的结构体布局不对，`OperStatus` 全 0 → 改用 v1 的 `GetIfTable`，结构简单且实测正确。
- `Network Interface(*)` 这个 PDH 计数器集在本机返回 0 个有效值，不能用。
- 展开后的 PDH 路径形如 `\\DEV\GPU Engine(pid_123_...)\Utilization Percentage`，**已含计数器名**。

### CPU% 口径

任务管理器的进程 CPU 列求和 ≈ 系统总 CPU，即**占整机容量**的百分比。
所以 `cpu% = Δ进程CPU时间 / Δ墙钟 / NumCPU * 100`（28 核）。
不除以 NumCPU 会得到"单核百分比"，求和可达 158%，与任务管理器不符。

### 性能

- 采样全部 526 进程：**4.1ms**（含 OpenProcess）
- → 1 秒刷新一次，开销可忽略

## 架构

```
perf.go       采样层：每进程 CPU/内存/磁盘 + 系统级 CPU/内存/磁盘/网络
taskmgr.go    任务管理器视图：进程页 + 性能页
```

复用 `procs.go` 的进程枚举与 `post()` 的异步回主线程机制。

## 设计语言（Fluent / macOS 取向）

- 8px 栅格，留白充足，层级靠留白和细边框而非重色块
- 圆角 8–12px；边框 `#0000001a` 级别
- 数据用色彩编码：CPU 蓝、内存紫、磁盘橙（对齐任务管理器的色彩语义）
- 次要信息 `TextMuted`，主信息正常色
- 进程按名字分组，显示「名字 (N)」——对齐任务管理器的合并行为
- 列头可点击排序
- 状态用颜色圆点，不用重底色
- 空态/加载态/错误态都要有

## 验证

- 单测：CPU% 口径、采样差分、首次采样无 CPU%、系统级指标合理范围
- `ui.NewTester`：标签切换、排序、选中
- 无头渲染比对布局
- `go tool mygo vet`、`CGO_ENABLED=0 go test ./...`
