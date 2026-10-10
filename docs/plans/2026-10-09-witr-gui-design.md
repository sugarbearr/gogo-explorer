# witr GUI 套壳设计

日期：2026-10-09
状态：待实现

## 目标

给 `witr`（Why is this running?）做一个原生 GUI，回答"这个进程为什么在跑"。
两种入口都要：**进程列表**（主视图）+ **搜索**（按名字/端口/PID 查询）。

## 关键约束（实测得出）

1. **不能当 Go 库用。** witr 的逻辑全在 `internal/`（`pipeline`/`proc`/`source`/`target`），
   Go 禁止跨模块 import internal；`pkg/` 只有 `model`，且只有 struct 没有导出函数。
   → 只能以 **CLI 子进程** 调用。Apache-2.0，需要时也可内联改造。
2. **witr 路径**：~~winget 装在 …，已在 PATH 里~~ **已改为内置**（2026-10-10）：
   `witr.exe` 放在 `resources/`，`mygo build` 会把它复制到可执行文件旁边（即安装目录），
   运行时按「同目录 → PATH」查找。开发机上 winget 那份（v0.3.3）仍可作为回退。
3. **延迟**：单次查询 215–345ms → 必须异步 + loading，不能阻塞 UI 线程。

## witr CLI 契约

```
witr [name...] [--pid N] [--port N] [--file P] [--container C]
     [--json] [--no-color] [--exact] [--tree] [--short] [--warnings] [--env] [--verbose]
```

### 退出码表（实测）

| 情况 | rc | stdout | 处理 |
|---|---|---|---|
| 成功 | 0 | 合法 JSON | 正常解析 |
| **名字匹配多个** | 4 | **非 JSON**，人类可读列表 | 单独解析，或改用 --pid |
| **受保护进程** | 1 | **合法 JSON**（数据完整） | 不能因 rc!=0 就丢弃 |
| 查不到 | 2 | 空 | 错误提示 |
| 没有该容器 | 5 | 空 | 错误提示 |

### 两个必须防的坑

- **不带 target 调用 `witr --json` 会挂住等输入**（压 30s 超时仍不返回）。
  GUI 绝不能空参调用；搜索框为空时走本地列表，不调 witr。
- **rc=4 时 stdout 不是 JSON**，直接 Unmarshal 会炸。

### JSON 结构（成功时）

```json
{
  "Target": {"Type":"pid","Value":".."},
  "ResolvedTarget": "DeepSeek Harness.exe",
  "Process": {"PID":..,"PPID":..,"Command":..,"Cmdline":..,"Exe":..,"StartedAt":..,
              "User":..,"CPUPercent":..,"MemoryRSS":..,"WorkingDir":..,
              "GitRepo":..,"GitBranch":..,"Service":..,"Health":..,"Env":[..],
              "Memory":{..},"IO":{..}},
  "Ancestry": [ {同上结构}, ... ],
  "Source": {"Type":"windows_service","Name":"Service Control Manager",...},
  "Warnings": null, "SocketInfo": null, "ResourceContext": null, "FileContext": null
}
```

`Ancestry` 就是"为什么在跑"的因果链（root → target）。

## Windows 现实预期

扫 22 个真实进程：`Source.Type` = init 4 / windows_service 4 / unknown 3，约一半 rc=1（数据仍可用）。
ancestry 多数 1–4 层。**能解析出 Service Control Manager 链，但不如 Linux 上戏剧化**，
UI 要对 unknown 有得体呈现，不能显得像坏了。

## 架构

```
main.go              窗口 + 视图（沿用现有自绘标题栏/侧栏风格）
witr.go              witr CLI 封装：执行、退出码分发、JSON 解析
procs.go             进程枚举（syscall.CreateToolhelp32Snapshot，stdlib，无新依赖）
```

### 数据层

- `runWitr(args ...string) (Result, error)`：统一执行、分发退出码、解析 JSON。
- `listProcesses() []Proc`：Toolhelp32 快照枚举，拿 PID/PPID/名字。
- 端口列：`netstat -ano`（36ms）解析 LISTENING → PID 映射，可选。

### UI

- 左侧沿用现有侧栏；新增「进程溯源」导航项。
- 主区左：进程列表（`ui.List` + `ListState.Selected`，虚拟化，支持 500+ 行）。
- 主区右：详情面板，展示 ResolvedTarget / Source / 因果链 / 关键指标 / 警告。
- 顶部搜索框：输入名字/端口/PID → 调 witr；空输入走本地列表。
- 后台 goroutine 加载 → `Window.Update` 回主线程更新状态。

### 状态

```go
type app struct {
    // ...现有字段
    procs   []Proc
    procSel int
    list    ui.ListState
    detail  *witr.Result
    loading bool
    err     string
    query   string
}
```

## 验证

- 单测：退出码分发（0/1/2/4/5 各自路径）、rc=4 非 JSON 不 panic、空查询不调 witr。
- `ui.NewTester` 无头测试：点列表行 → 详情更新；搜索框输入 → 触发加载。
- `go tool mygo vet .`、`CGO_ENABLED=0 go test ./...`。
- 无头渲染比对，确认列表/详情/空态/错误态布局正常。
