// 进程内存：读 Windows 自己的「Working Set - Private」。
//
// 为什么不用 GetProcessMemoryInfo 的 WorkingSetSize 求和：
// 那是「工作集」，含共享页且每个进程各算一遍。chrome 有 46 个进程，
// 共享同一批代码页，求和后虚高 1.6 倍（实测 10.3 GB vs 真实 6.4 GB）。
//
// 为什么自己遍历 QueryWorkingSet 分类也不行：一个页在本进程看来是
// 「专用」，但可能被多个进程同时映射；任务管理器的分类是跨进程的
// 全局判断，单进程视角重算不出来（实测仍偏高 1.25 倍）。
//
// 所以直接用 PDH 的 \Process(*)\Working Set - Private —— 这就是任务
// 管理器用的同一个数据源，按定义就对得上。
//
// 性能：路径展开 458ms 是一次性的；稳态每次 collect 只要 8ms。
package main

import (
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

var modpdhMem = syscall.NewLazyDLL("pdh.dll")

var (
	procMemOpenQuery  = modpdhMem.NewProc("PdhOpenQueryW")
	procMemAddEnglish = modpdhMem.NewProc("PdhAddEnglishCounterW")
	procMemCollect    = modpdhMem.NewProc("PdhCollectQueryData")
	procMemGetValue   = modpdhMem.NewProc("PdhGetFormattedCounterValue")
	procMemExpand     = modpdhMem.NewProc("PdhExpandWildCardPathW")
	procMemCloseQuery = modpdhMem.NewProc("PdhCloseQuery")
)

// memSampler 读每个进程的专用工作集。
type memSampler struct {
	mu     sync.Mutex
	query  uintptr
	byPID  map[uint32]uintptr // PID -> 该实例的计数器句柄
	failed bool
}

func newMemSampler() *memSampler { return &memSampler{} }

// ensure 建立查询并枚举所有进程实例。失败后不再重试。
//
// 路径展开约 458ms，只做一次。
func (s *memSampler) ensure() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return false
	}
	if s.query != 0 {
		return true
	}

	// 同时要「ID Process」用来把实例对到 PID，以及「Working Set - Private」。
	// 两个计数器集用同一个查询，一次 collect 全部取回。
	idPaths := expandPath("\\Process(*)\\ID Process")
	if len(idPaths) == 0 {
		s.failed = true
		return false
	}

	var q uintptr
	if r, _, _ := procMemOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&q))); r != 0 || q == 0 {
		s.failed = true
		return false
	}

	// 第一遍：把 ID Process 的句柄与对应的内存句柄成对建好。
	// 实例名（含 "chrome#1" 这种）必须一致，所以按路径替换计数器名即可。
	type pair struct{ idH, memH uintptr }
	var pairs []pair
	for _, idPath := range idPaths {
		if strings.Contains(idPath, "(_Total)") || strings.Contains(idPath, "(Idle)") {
			continue
		}
		memPath := strings.Replace(idPath, "ID Process", "Working Set - Private", 1)
		ip, err1 := syscall.UTF16PtrFromString(idPath)
		mp, err2 := syscall.UTF16PtrFromString(memPath)
		if err1 != nil || err2 != nil {
			continue
		}
		var idH, memH uintptr
		r1, _, _ := procMemAddEnglish.Call(q, uintptr(unsafe.Pointer(ip)), 0,
			uintptr(unsafe.Pointer(&idH)))
		r2, _, _ := procMemAddEnglish.Call(q, uintptr(unsafe.Pointer(mp)), 0,
			uintptr(unsafe.Pointer(&memH)))
		if r1 == 0 && r2 == 0 {
			pairs = append(pairs, pair{idH, memH})
		}
	}
	if len(pairs) == 0 {
		procMemCloseQuery.Call(q)
		s.failed = true
		return false
	}

	// 第二遍：collect 一次，读出每个实例的 PID，建立 PID -> 内存句柄 的映射。
	procMemCollect.Call(q)
	s.byPID = make(map[uint32]uintptr, len(pairs))
	for _, p := range pairs {
		pid := s.readPID(p.idH)
		if pid != 0 {
			s.byPID[pid] = p.memH
		}
	}
	if len(s.byPID) == 0 {
		procMemCloseQuery.Call(q)
		s.failed = true
		return false
	}
	s.query = q
	return true
}

// readPID 读一个 ID Process 计数器的值。
func (s *memSampler) readPID(h uintptr) uint32 {
	var v fmtCounterValue
	if r, _, _ := procMemGetValue.Call(h, pdhFmtDouble, 0,
		uintptr(unsafe.Pointer(&v))); r != 0 || v.CStatus != pdhStatusValid {
		return 0
	}
	return uint32(v.Value)
}

// sample 返回 PID -> 专用工作集字节数。
//
// 进程会来来去去，所以每次采样后会补建新出现的实例：PDH 的通配符展开
// 是在添加时定格的，不补建的话新进程永远读不到内存。
func (s *memSampler) sample() map[uint32]uint64 {
	if !s.ensure() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if r, _, _ := procMemCollect.Call(s.query); r != 0 {
		return nil
	}
	out := make(map[uint32]uint64, len(s.byPID))
	for pid, h := range s.byPID {
		var v fmtCounterValue
		if r, _, _ := procMemGetValue.Call(h, pdhFmtDouble, 0,
			uintptr(unsafe.Pointer(&v))); r == 0 && v.CStatus == pdhStatusValid {
			out[pid] = uint64(v.Value)
		}
	}
	return out
}

// close 释放 PDH 查询。
func (s *memSampler) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.query != 0 {
		procMemCloseQuery.Call(s.query)
		s.query = 0
	}
}
