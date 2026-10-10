# 内存口径修正：改用 Windows 自己的「Working Set - Private」

日期：2026-10-09

## 问题

用户对照 Windows 任务管理器发现对不上：

| | chrome.exe 合计 |
|---|---|
| Windows 任务管理器 | **6,343.2 MB** |
| 本程序（改前） | **9.7 GB** |

## 根因

改前用的是 `GetProcessMemoryInfo` 的 `WorkingSetSize` 求和。
`WorkingSetSize` 是**工作集**：包含共享页，且每个进程各算一遍。
chrome 有 46 个进程，大量共享同一批代码页和资源，所以同一块内存
被重复计了十几次，求和严重虚高。

任务管理器用的是**专用工作集**（private working set）：只算进程独占的
驻留页，共享页不重复计入。

## 走过的弯路（都实测过）

我试了三种自算方案，**没有一种能对上**：

| 方案 | chrome 合计 | 比值 |
|---|---|---|
| `PMC.WorkingSetSize` 求和 | 10,323 MB | 1.63× |
| `PMC.PrivateUsage` 求和 | 8,105 MB | 1.28× |
| 自己遍历 `QueryWorkingSet` + `QueryWorkingSetEx` 分类共享/专用页 | 7,949 MB | 1.25× |
| **Windows PDH「Working Set - Private」** | **7,035 MB** | — |

差距的原因：**一个页在本进程看来是「专用」，但可能被多个进程同时映射**。
任务管理器的分类是跨进程的全局判断，单进程视角重算不出来。

### 顺带踩到的两个坑

**坑 1：`QueryWorkingSet` 的缓冲区不能开太大。**

缓冲区过大时它会把整个数组当成有效槽位填满，多出来的条目是垃圾：

```
maxPages = 1<<19 (2 MB 缓冲) -> 3203 页 = 12.51 MB   // 错
maxPages = 2048   (刚好够)   -> 1861 页 =  7.27 MB   // 对，与 PMC 一致
```

必须从小往大递增试探，用第一次成功的那个大小。

**坑 2：`QueryWorkingSetEx` 的入参结构体是 16 字节。**

```c
typedef struct _PSAPI_WORKING_SET_EX_INFORMATION {
    PVOID VirtualAddress;             // 调用方填
    PSAPI_WORKING_SET_EX_BLOCK VirtualAttributes;  // API 填回
} PSAPI_WORKING_SET_EX_INFORMATION;
```

只声明后 8 字节（属性）会让 API 把页地址读成属性偏移，读回来全是垃圾。

（另：`QueryWorkingSet` 需要 `PROCESS_QUERY_INFORMATION` + `PROCESS_VM_READ`，
只用 `QUERY_LIMITED_INFORMATION` 会被拒绝。）

## 决定

**直接读 PDH 的 `\Process(*)\Working Set - Private`。**

理由：这就是任务管理器用的同一个数据源，**按定义就对得上**，
不需要我去复刻 Windows 的跨进程分类算法。

### 性能

| 阶段 | 耗时 |
|---|---|
| `PdhExpandWildCardPath`（一次性） | 458 ms |
| 添加 550 个计数器（一次性） | 4 ms |
| 每次 `PdhCollectQueryData` | **7–12 ms** |

一次性的 458ms 放在首次采样（后台线程）里完全可以接受；
稳态每次只需 8ms，1 秒刷新毫无压力。

## 影响

这个 bug 不只是显示不准——任务管理器默认按内存降序排，
**排序结果也会错**：共享页多的程序被错误地排到前面。
修正后第一屏的顺序才是对的。
