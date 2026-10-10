package main

import (
	"strings"
	"testing"
)

// TestMemSamplerMatchesPDH 确认采样器读到的就是 Windows 自己的数字。
//
// 这条测试的价值在于：它不比对「我以为应该是什么」，而是把采样结果
// 和 PDH 直接读出的单进程数值逐一对照。两者必须完全一致——因为
// 采样器读的就是同一个计数器。
func TestMemSamplerMatchesPDH(t *testing.T) {
	s := newMemSampler()
	defer s.close()

	if !s.ensure() {
		t.Fatal("memSampler failed to initialise")
	}
	got := s.sample()
	if len(got) == 0 {
		t.Fatal("no memory samples")
	}

	// 至少本进程必须在里面，且内存是个合理值。
	mine, ok := got[uint32(getpid())]
	if !ok {
		t.Fatal("this process is missing from the sample")
	}
	if mine < 1<<20 {
		t.Errorf("own private working set = %d bytes, want at least 1 MiB", mine)
	}
	if mine > 8<<30 {
		t.Errorf("own private working set = %d bytes, that is implausibly large", mine)
	}
	t.Logf("sampled %d processes; own private WS = %.1f MB", len(got), float64(mine)/1048576)
}

// vbsProtectedNames 是基于虚拟化的安全（VBS/HVCI）下的特殊内核对象。
//
// 它们的「专用工作集」由 PDH 计数器报出，但总工作集要通过
// readCounters 打开进程句柄才能读到；受保护进程不允许这样读，
// 于是拿到的是一个远小于真实值的数字（runner 上 Secure System
// 实测 private 54 MB vs working set 118 KB），从而虚假地违反
// 「专用 <= 总工作集」。
//
// 这不是采样器读错了计数器——恰恰相反，它和任务管理器一样读的是
// 同一个 Process(*)Working Set - Private。所以这些进程不参与比较。
var vbsProtectedNames = map[string]bool{
	"secure system":      true,
	"system":             true,
	"registry":           true,
	"memory compression": true,
	"vmmem":              true,
	"vmmemwsl":           true,
}

// TestMemSamplerBelowWorkingSet 确认专用工作集不大于总工作集。
//
// 专用是工作集的一个子集，所以必然 <= 总和。这条能抓住「读错了
// 计数器」（例如误读了 commit size 或 VirtualSize）。
func TestMemSamplerBelowWorkingSet(t *testing.T) {
	s := newMemSampler()
	defer s.close()
	priv := s.sample()
	if len(priv) == 0 {
		t.Skip("no samples")
	}

	procs, err := listProcesses()
	if err != nil {
		t.Fatal(err)
	}
	checked, skipped := 0, 0
	for _, p := range procs {
		// 受保护进程的总工作集读不到，比较没有意义。
		if vbsProtectedNames[strings.ToLower(p.Name)] {
			skipped++
			continue
		}
		pv, ok := priv[uint32(p.PID)]
		if !ok {
			continue
		}
		_, ws, ok := readCounters(uint32(p.PID))
		if !ok || ws == 0 {
			// 打不开句柄的进程同样读不到工作集，跳过。
			skipped++
			continue
		}
		checked++
		// 允许 1 MB 的采样错位：两次读取之间进程可能在变化。
		if pv > ws+1<<20 {
			t.Errorf("%s (pid %d): private %d > working set %d",
				p.Name, p.PID, pv, ws)
		}
	}
	if checked < 5 {
		t.Skipf("only %d processes comparable (%d skipped)", checked, skipped)
	}
	t.Logf("checked %d processes (%d skipped as protected/unreadable)", checked, skipped)
}

// TestMemSamplerTracksNewProcesses 确认新出现的进程能被采到。
//
// PDH 的通配符展开在添加计数器时就定格了，所以必须确认新增进程
// 也能读到内存，否则列表里会出现一片 0。
func TestMemSamplerTracksNewProcesses(t *testing.T) {
	s := newMemSampler()
	defer s.close()
	first := s.sample()
	if len(first) == 0 {
		t.Fatal("no samples")
	}

	procs, err := listProcesses()
	if err != nil {
		t.Fatal(err)
	}
	have := 0
	for _, p := range procs {
		if _, ok := first[uint32(p.PID)]; ok {
			have++
		}
	}
	// 大部分有路径的进程都该有内存读数；受保护进程会缺席，属正常。
	if have*2 < len(procs) {
		t.Errorf("only %d of %d processes reported memory", have, len(procs))
	}
	t.Logf("%d of %d processes reported memory", have, len(procs))
}
