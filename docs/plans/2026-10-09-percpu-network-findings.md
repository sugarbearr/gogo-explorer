# 每进程网络（ETW）与每核 CPU

日期：2026-10-09

## 结论一：每进程网络做不到（需要管理员权限）

用户要求补上每进程网络。查证过程与证据：

### 1. Provider 确实存在

`TdhEnumerateProviders` 列出 1184 个 provider，其中包含：
```
Microsoft-Windows-Kernel-Network   {7DD42A49-5329-4832-8DFD-43D979153A88}
Microsoft-Windows-Kernel-Process
```
任务管理器的每进程网络就是消费这两个 provider 得来的。

### 2. 但实时会话需要提升权限

```
StartTrace r=5 err=Access is denied.
```
当前进程完整性级别 **Medium**（`S-1-16-8192`），不是管理员。

### 3. 这不是我的代码问题

用微软自己的 `logman`（底层同一套 API）验证：
```
> logman create trace MyGoTest -p Microsoft-Windows-Kernel-Network
Error:
Access is denied.
Try running this command as an administrator.
```
**官方工具同样被拒。** 所以这是 Windows 的硬性要求，不是实现缺陷。

### 4. 顺带踩到的坑

`ControlTrace(session=0, STOP)` 对一个**不存在**的会话会直接让进程
访问违例崩溃（`0xc0000005`），而 `QUERY` 会干净地返回错误码。
所以清理逻辑必须先 QUERY 再决定是否 STOP。

### 决定

**不做每进程网络。** 选项只有两个：
- 要求用户以管理员身份运行 —— 对一个任务管理器来说要求过高，
  而且会让整个程序常年跑在高权限下，不是好取舍。
- 不做 —— 诚实。

网络只保留整机口径（`GetIfTable`，无需权限）。

## 结论二：每核 CPU 可以做到（已打通）

用 PDH 的 `\Processor(*)\% Processor Time`，28 个核全部拿到有效值。

### 关键坑：`PDH_FMT_COUNTERVALUE` 的结构体对齐

```c
typedef struct _PDH_FMT_COUNTERVALUE {
    DWORD  CStatus;   // 4 字节
    union {           // double 需要 8 字节对齐 -> 偏移 8，不是 4
        LONG      longValue;
        double    doubleValue;
        ...
    };
} PDH_FMT_COUNTERVALUE;
```

我一开始把 double 放在偏移 4，于是 `CStatus` 读到的是垃圾值
（`0x95774853` 这种），所有核都判为无效。加上 4 字节填充后
（结构体 16 字节）全部正常。

正确写法：
```go
type fmtCounterValue struct {
    CStatus uint32
    _       uint32   // 让 Value 落在偏移 8
    Value   float64
}
```

## 实现

- `percpu.go`：PDH 每核 CPU 采样
- 性能区新增一张「CPU 每核」卡，画 28 个核的网格，按负载着色
