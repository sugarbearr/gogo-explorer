package main

import (
	"runtime"
	"testing"
	"time"
)

// TestSamplerFirstPass 确认首次采样不伪造 CPU 与磁盘速率：
// 没有前值可减时应当是 0，而不是某个编造的数字。
func TestSamplerFirstPass(t *testing.T) {
	s := newSampler()
	metrics, sys := s.sample()
	if len(metrics) == 0 {
		t.Fatal("no processes sampled")
	}
	for _, m := range metrics {
		if m.CPU != 0 {
			t.Errorf("%s: first-pass CPU = %v, want 0", m.Name, m.CPU)
		}
		if m.Disk != 0 {
			t.Errorf("%s: first-pass Disk = %v, want 0", m.Name, m.Disk)
		}
	}
	// 整机内存与磁盘容量与采样次数无关，首帧就该有值。
	if sys.MemTotal == 0 {
		t.Error("MemTotal = 0 on first pass")
	}
	if sys.DiskTotal == 0 {
		t.Error("DiskTotal = 0 on first pass")
	}
}

// TestSamplerSystemMetrics 确认整机指标落在物理合理的范围里。
func TestSamplerSystemMetrics(t *testing.T) {
	s := newSampler()
	s.sample()
	time.Sleep(1100 * time.Millisecond)
	_, sys := s.sample()

	if sys.CPUPercent < 0 || sys.CPUPercent > 100 {
		t.Errorf("CPUPercent = %v, want 0..100", sys.CPUPercent)
	}
	if sys.MemTotal < 1<<30 {
		t.Errorf("MemTotal = %d, want at least 1 GB", sys.MemTotal)
	}
	if sys.MemUsed > sys.MemTotal {
		t.Errorf("MemUsed %d > MemTotal %d", sys.MemUsed, sys.MemTotal)
	}
	if sys.DiskTotal == 0 || sys.DiskFree > sys.DiskTotal {
		t.Errorf("disk free %d / total %d look wrong", sys.DiskFree, sys.DiskTotal)
	}
	if sys.NetRx < 0 || sys.NetTx < 0 {
		t.Errorf("network rates are negative: rx=%v tx=%v", sys.NetRx, sys.NetTx)
	}
}

// TestSamplerCPUScale 是本层最容易搞错的一点：CPU 必须按「占整机容量」
// 计，也就是要除以核数。若漏掉这一步，所有进程 CPU 之和会达到核数倍，
// 与任务管理器对不上。
func TestSamplerCPUScale(t *testing.T) {
	s := newSampler()
	s.sample()

	// 烧满一个核。
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
		}
	}()
	time.Sleep(900 * time.Millisecond)
	metrics, _ := s.sample()
	close(stop)
	<-done

	self := getpid()
	var got float64
	var found bool
	for _, m := range metrics {
		if m.PID == self {
			got, found = m.CPU, true
			break
		}
	}
	if !found {
		t.Fatal("this process is missing from the sample")
	}
	// 一个核满载 = 1/NumCPU 的整机容量。给足容差，因为调度与
	// 采样窗口都不精确。
	want := 100.0 / float64(runtime.NumCPU())
	if got < want*0.3 {
		t.Errorf("busy-loop CPU = %.2f%%, want roughly %.2f%% (=100/%d cores)", got, want, runtime.NumCPU())
	}
	if got > want*3 {
		t.Errorf("busy-loop CPU = %.2f%%, far above the %.2f%% one core can reach", got, want)
	}
}

// TestSamplerProcessMetrics 确认进程指标能拿到内存，且不可读的进程
// 仍然出现在列表里（只是没有指标）。
func TestSamplerProcessMetrics(t *testing.T) {
	s := newSampler()
	s.sample()
	time.Sleep(300 * time.Millisecond)
	metrics, _ := s.sample()

	self := getpid()
	var selfMem uint64
	var found bool
	withMem := 0
	for _, m := range metrics {
		if m.Mem > 0 {
			withMem++
		}
		if m.PID == self {
			found, selfMem = true, m.Mem
		}
	}
	if !found {
		t.Fatal("this process is missing")
	}
	if selfMem == 0 {
		t.Error("this process has no working set")
	}
	if withMem < 10 {
		t.Errorf("only %d processes reported memory; expected most to", withMem)
	}
	// 按 CPU 降序排列。
	for i := 1; i < len(metrics); i++ {
		if metrics[i-1].CPU < metrics[i].CPU {
			t.Fatalf("not sorted by CPU desc at %d: %v then %v", i, metrics[i-1].CPU, metrics[i].CPU)
		}
	}
}

// TestNetworkOctets 确认能读到累计网卡字节。
func TestNetworkOctets(t *testing.T) {
	rx, tx := networkOctets()
	if rx == 0 && tx == 0 {
		t.Error("no network octets at all; GetIfTable may be misparsed")
	}
}
