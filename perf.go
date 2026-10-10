// 性能采样：每进程的 CPU、内存、磁盘，以及整机的 CPU、内存、磁盘、网络。
//
// 全部走 Win32 已文档化的接口，不引入新依赖。取舍依据见
// docs/plans/2026-10-09-taskmanager-design.md：
//   - 网络只有整机口径（GetIfTable）。每进程网络需要 ETW，
//     GetPerTcpConnectionEStats 返回的值不可信，故不做。
//   - GPU 不做：参照的界面里没有这一列。
package main

import (
	"runtime"
	"sort"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	modkernel32 = syscall.NewLazyDLL("kernel32.dll")
	modpsapi    = syscall.NewLazyDLL("psapi.dll")
	modiphlpapi = syscall.NewLazyDLL("iphlpapi.dll")

	procGetProcessTimes            = modkernel32.NewProc("GetProcessTimes")
	procGetProcessIoCounters       = modkernel32.NewProc("GetProcessIoCounters")
	procOpenProcess                = modkernel32.NewProc("OpenProcess")
	procGetSystemTimes             = modkernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx       = modkernel32.NewProc("GlobalMemoryStatusEx")
	procGetDiskFreeSpaceEx         = modkernel32.NewProc("GetDiskFreeSpaceExW")
	procGetProcessMemoryInfo       = modpsapi.NewProc("GetProcessMemoryInfo")
	procGetIfTable                 = modiphlpapi.NewProc("GetIfTable")
	procQueryFullProcessImageNameW = modkernel32.NewProc("QueryFullProcessImageNameW")
)

// sampleInterval 是任务管理器的刷新间隔。采样全部进程约 4ms，
// 1 秒一次的开销可以忽略。
const sampleInterval = time.Second

const (
	processQueryLimitedInformation = 0x1000
	errorInsufficientBuffer        = 122
)

// procCounters 是一个进程的累计计数。CPU 与磁盘要两次采样求差，
// 所以保存原始计数而不是速率。
type procCounters struct {
	cpuTicks uint64
	read     uint64
	write    uint64
}

// procMetric 是一行进程的瞬时指标。
type procMetric struct {
	PID  int
	PPID int
	Name string
	// Exe 是可执行文件全路径，用于取图标。受保护进程为空。
	Exe string
	// CPU 是占整机容量的百分比，与任务管理器口径一致：把所有进程的 CPU
	// 加起来约等于整机使用率。注意这不是「单核百分比」。
	CPU  float64
	Mem  uint64  // 工作集字节数
	Disk float64 // 每秒读写字数
}

// sysMetric 是整机指标。
type sysMetric struct {
	CPUPercent float64
	MemTotal   uint64
	MemUsed    uint64
	DiskFree   uint64
	DiskTotal  uint64
	NetRx      float64 // 每秒接收字节
	NetTx      float64 // 每秒发送字节
}

type processMemoryCounters struct {
	Cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// filetimeTicks 把 FILETIME 折成 100ns 计数。
//
// 不能走 time.Duration(ft.Nanoseconds())：两个 64 位量相加会溢出，
// 得到负数（实测 -4.8e9 秒）。
func filetimeTicks(ft syscall.Filetime) uint64 {
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
}

// readCounters 取一个进程的累计 CPU 与磁盘计数，以及当前工作集。
// 受保护进程打不开句柄，返回 false，调用方跳过即可。
func readCounters(pid uint32) (procCounters, uint64, bool) {
	h, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if h == 0 {
		return procCounters{}, 0, false
	}
	defer syscall.CloseHandle(syscall.Handle(h))

	var creation, exit, kernel, user syscall.Filetime
	r, _, _ := procGetProcessTimes.Call(h,
		uintptr(unsafe.Pointer(&creation)), uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if r == 0 {
		return procCounters{}, 0, false
	}
	c := procCounters{cpuTicks: filetimeTicks(kernel) + filetimeTicks(user)}

	var pmc processMemoryCounters
	pmc.Cb = uint32(unsafe.Sizeof(pmc))
	procGetProcessMemoryInfo.Call(h, uintptr(unsafe.Pointer(&pmc)), uintptr(pmc.Cb))

	var io ioCounters
	if r, _, _ := procGetProcessIoCounters.Call(h, uintptr(unsafe.Pointer(&io))); r != 0 {
		c.read, c.write = io.ReadTransferCount, io.WriteTransferCount
	}
	return c, uint64(pmc.WorkingSetSize), true
}

// systemCPUTicks 返回空闲与总 CPU 计数。
func systemCPUTicks() (idle, total uint64) {
	var i, k, u syscall.Filetime
	procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&i)), uintptr(unsafe.Pointer(&k)), uintptr(unsafe.Pointer(&u)))
	return filetimeTicks(i), filetimeTicks(k) + filetimeTicks(u)
}

// systemMemory 返回物理内存总量与已用量。
func systemMemory() (total, used uint64) {
	var ms memoryStatusEx
	ms.Length = uint32(unsafe.Sizeof(ms))
	if r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms))); r == 0 {
		return 0, 0
	}
	return ms.TotalPhys, ms.TotalPhys - ms.AvailPhys
}

// systemDisk 返回系统盘的可用与总容量。
func systemDisk() (free, total uint64) {
	root := "C:" + "\\"
	if d, ok := syscall.Getenv("SystemDrive"); ok && d != "" {
		root = d + "\\"
	}
	p, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return 0, 0
	}
	var f, t, tf uint64
	if r, _, _ := procGetDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&f)), uintptr(unsafe.Pointer(&t)), uintptr(unsafe.Pointer(&tf))); r == 0 {
		return 0, 0
	}
	return f, t
}

// MIB_IFROW 与 MIB_IFTABLE，用于整机网络吞吐。
//
// 这里用 v1 的 GetIfTable 而不是 v2 的 GetIfTable2：v2 的 MIB_IF_ROW2
// 结构体较大且布局易错（实测 OperStatus 全为 0），v1 结构简单且实测正确。
type mibIfRow struct {
	Name            [256]uint16
	Index           uint32
	Type            uint32
	Mtu             uint32
	Speed           uint32
	PhysAddrLen     uint32
	PhysAddr        [8]byte
	AdminStatus     uint32
	OperStatus      uint32
	LastChange      uint32
	InOctets        uint32
	InUcastPkts     uint32
	InNucastPkts    uint32
	InDiscards      uint32
	InErrors        uint32
	InUnknownProtos uint32
	OutOctets       uint32
	OutUcastPkts    uint32
	OutNucastPkts   uint32
	OutDiscards     uint32
	OutErrors       uint32
	OutQLen         uint32
	DescrLen        uint32
	Descr           [256]byte
}

// networkOctets 返回所有非回环网卡的累计收发字节。
func networkOctets() (rx, tx uint64) {
	var size uint32
	r, _, _ := procGetIfTable.Call(0, uintptr(unsafe.Pointer(&size)), 0)
	if r != errorInsufficientBuffer || size == 0 {
		return 0, 0
	}
	buf := make([]byte, size)
	r, _, _ = procGetIfTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0)
	if r != 0 {
		return 0, 0
	}
	num := *(*uint32)(unsafe.Pointer(&buf[0]))
	rowSize := unsafe.Sizeof(mibIfRow{})
	for i := uint32(0); i < num; i++ {
		off := uintptr(4) + uintptr(i)*rowSize
		if off+rowSize > uintptr(len(buf)) {
			break
		}
		row := (*mibIfRow)(unsafe.Pointer(&buf[off]))
		if row.Type == 24 { // 回环
			continue
		}
		rx += uint64(row.InOctets)
		tx += uint64(row.OutOctets)
	}
	return rx, tx
}

// sampler 保存上一次的累计值，用来把计数差分成速率。
type sampler struct {
	mu sync.Mutex

	prevProcs map[int]procCounters
	prevAt    time.Time

	prevIdle, prevTotal uint64
	prevCPUAt           time.Time

	prevRx, prevTx uint64
	prevNetAt      time.Time

	numCPU int

	// mem 读每个进程的专用工作集（任务管理器口径）。
	// 不用 PMC.WorkingSetSize 求和：那会把共享页重复计入，实测虚高 1.6 倍。
	mem *memSampler
}

// numCPU 返回逻辑处理器个数，用于把进程 CPU 折算成整机占比。
func numCPU() int { return runtime.NumCPU() }

// sleep 暂停一段时长。抽出来是为了让测试能替换掉真实等待。
func (a *app) sleep(d time.Duration) { time.Sleep(d) }

func newSampler() *sampler {
	return &sampler{prevProcs: map[int]procCounters{}, numCPU: runtime.NumCPU(), mem: newMemSampler()}
}

// sample 采一次样，返回进程指标与整机指标。
//
// 首次调用没有前值可减，进程 CPU 与磁盘为 0（内存仍然有效）；
// 整机 CPU 与网络同理。这比伪造一个数字诚实。
func (s *sampler) sample() ([]procMetric, sysMetric) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	procs, err := listProcesses()
	if err != nil {
		return nil, sysMetric{}
	}

	// ---- 进程 ----
	// 内存走 PDH 的专用工作集，与任务管理器同源。
	privateWS := s.mem.sample()

	metrics := make([]procMetric, 0, len(procs))
	cur := make(map[int]procCounters, len(procs))
	havePrev := !s.prevAt.IsZero()
	elapsed := now.Sub(s.prevAt).Seconds()

	for _, p := range procs {
		exe := processExePath(uint32(p.PID))
		mem := privateWS[uint32(p.PID)]
		c, _, ok := readCounters(uint32(p.PID))
		if !ok {
			// 打不开的进程仍然列出来，只是没有 CPU 指标——任务管理器也这么做。
			// 内存可能仍然读得到（PDH 不经过进程句柄）。
			metrics = append(metrics, procMetric{
				PID: p.PID, PPID: p.PPID, Name: p.Name, Exe: exe, Mem: mem,
			})
			continue
		}
		cur[p.PID] = c
		m := procMetric{PID: p.PID, PPID: p.PPID, Name: p.Name, Exe: exe, Mem: mem}
		if havePrev && elapsed > 0 {
			if prev, ok := s.prevProcs[p.PID]; ok {
				dcpu := float64(c.cpuTicks-prev.cpuTicks) / 1e7 // 秒
				// 除以核数，得到占整机容量的比例，与任务管理器一致。
				m.CPU = dcpu / elapsed / float64(s.numCPU) * 100
				m.Disk = float64((c.read-prev.read)+(c.write-prev.write)) / elapsed
			}
		}
		metrics = append(metrics, m)
	}
	s.prevProcs, s.prevAt = cur, now

	sort.Slice(metrics, func(i, j int) bool {
		if metrics[i].CPU != metrics[j].CPU {
			return metrics[i].CPU > metrics[j].CPU
		}
		return metrics[i].Mem > metrics[j].Mem
	})

	// ---- 整机 ----
	var sys sysMetric
	idle, total := systemCPUTicks()
	if !s.prevCPUAt.IsZero() {
		di, dt := idle-s.prevIdle, total-s.prevTotal
		if dt > 0 {
			sys.CPUPercent = float64(dt-di) / float64(dt) * 100
		}
	}
	s.prevIdle, s.prevTotal, s.prevCPUAt = idle, total, now

	sys.MemTotal, sys.MemUsed = systemMemory()
	sys.DiskFree, sys.DiskTotal = systemDisk()

	rx, tx := networkOctets()
	if !s.prevNetAt.IsZero() {
		dt := now.Sub(s.prevNetAt).Seconds()
		if dt > 0 {
			sys.NetRx = float64(rx-s.prevRx) / dt
			sys.NetTx = float64(tx-s.prevTx) / dt
		}
	}
	s.prevRx, s.prevTx, s.prevNetAt = rx, tx, now

	return metrics, sys
}
