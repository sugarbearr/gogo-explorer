// 每核 CPU 利用率。
//
// 走 PDH 的 \\Processor(*)\% Processor Time：每个逻辑处理器一个实例。
// 这条路不需要管理员权限（每进程网络需要的 ETW 需要，故不做，
// 见 docs/plans/2026-10-09-percpu-network-findings.md）。
package main

import (
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

var modpdh = syscall.NewLazyDLL("pdh.dll")

var (
	procPdhOpenQuery  = modpdh.NewProc("PdhOpenQueryW")
	procPdhAddEnglish = modpdh.NewProc("PdhAddEnglishCounterW")
	procPdhCollect    = modpdh.NewProc("PdhCollectQueryData")
	procPdhGetValue   = modpdh.NewProc("PdhGetFormattedCounterValue")
	procPdhExpand     = modpdh.NewProc("PdhExpandWildCardPathW")
	procPdhCloseQuery = modpdh.NewProc("PdhCloseQuery")
)

const (
	pdhFmtDouble   = 0x00000200
	pdhMoreData    = 0x800007D2
	pdhStatusValid = 0x00000000
)

// fmtCounterValue 对应 PDH_FMT_COUNTERVALUE。
//
// 这个结构体有个必须留意的对齐问题：CStatus 是 4 字节，但它后面的
// union 里最大成员是 double，需要 8 字节对齐，所以 union 落在偏移 8
// 而不是 4。少写这 4 字节填充的话，读到的 CStatus 是垃圾值
// （实测 0x95774853 之类），每个核都会被误判为无效。
type fmtCounterValue struct {
	CStatus uint32
	_       uint32
	Value   float64
}

// percpuSampler 持有每核计数器的查询句柄。
//
// 句柄只在建立时拿一次，之后每次采样只是 collect + 读值，开销很小。
type percpuSampler struct {
	mu      sync.Mutex
	query   uintptr
	handles []uintptr
	names   []string
	failed  bool
}

func newPerCPUSampler() *percpuSampler { return &percpuSampler{} }

// expandPath 展开一个带通配符的 PDH 路径。
func expandPath(path string) []string {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil
	}
	var size uint32
	r, _, _ := procPdhExpand.Call(0, uintptr(unsafe.Pointer(p)), 0,
		uintptr(unsafe.Pointer(&size)), 0)
	if uint32(r) != pdhMoreData || size == 0 {
		return nil
	}
	buf := make([]uint16, size)
	r, _, _ = procPdhExpand.Call(0, uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0)
	if r != 0 {
		return nil
	}
	// 双空字符结尾的多字符串。
	var out []string
	start := 0
	for i, c := range buf {
		if c == 0 {
			if i == start {
				break
			}
			out = append(out, syscall.UTF16ToString(buf[start:i]))
			start = i + 1
		}
	}
	return out
}

// ensure 建立查询与每核计数器。失败后不再重试，避免每帧都白跑一遍。
func (s *percpuSampler) ensure() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return false
	}
	if s.query != 0 {
		return true
	}

	// 展开后的路径已经含计数器名，可以直接用。
	paths := expandPath("\\Processor(*)\\% Processor Time")
	var per []string
	for _, p := range paths {
		// 跳过 _Total：那是整机合计，不是单个核。
		if strings.Contains(p, "(_Total)") {
			continue
		}
		per = append(per, p)
	}
	if len(per) == 0 {
		s.failed = true
		return false
	}

	var q uintptr
	if r, _, _ := procPdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&q))); r != 0 || q == 0 {
		s.failed = true
		return false
	}
	for _, p := range per {
		up, err := syscall.UTF16PtrFromString(p)
		if err != nil {
			continue
		}
		var h uintptr
		if r, _, _ := procPdhAddEnglish.Call(q, uintptr(unsafe.Pointer(up)), 0,
			uintptr(unsafe.Pointer(&h))); r == 0 {
			s.handles = append(s.handles, h)
			s.names = append(s.names, p)
		}
	}
	if len(s.handles) == 0 {
		procPdhCloseQuery.Call(q)
		s.failed = true
		return false
	}
	s.query = q
	return true
}

// sample 返回每个逻辑处理器的利用率，按核序排列。
//
// PDH 的速率类计数器需要两次采集：第一次建立基线，所以首帧返回
// 全 0（而不是编造数字）。
func (s *percpuSampler) sample() []float64 {
	if !s.ensure() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if r, _, _ := procPdhCollect.Call(s.query); r != 0 {
		return nil
	}
	out := make([]float64, 0, len(s.handles))
	for _, h := range s.handles {
		var v fmtCounterValue
		r, _, _ := procPdhGetValue.Call(h, pdhFmtDouble, 0,
			uintptr(unsafe.Pointer(&v)))
		if r != 0 || v.CStatus != pdhStatusValid {
			// 某个核读不到就给 0，整张图不至于因为一个坏点全黑。
			out = append(out, 0)
			continue
		}
		out = append(out, v.Value)
	}
	return out
}

// close 释放 PDH 查询句柄。
func (s *percpuSampler) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.query != 0 {
		procPdhCloseQuery.Call(s.query)
		s.query = 0
	}
}
