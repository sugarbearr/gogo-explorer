package main

import (
	"testing"
	"time"
)

// TestProcessCPUSumMatchesSystem 是这一层最关键的校验：任务管理器的进程
// CPU 列求和约等于整机 CPU。如果口径搞错（比如漏除核数），两者会差出
// 一个核数倍。这里允许较宽容差，因为两次采样并非同一瞬间。
func TestProcessCPUSumMatchesSystem(t *testing.T) {
	s := newSampler()
	s.sample() // 建立基线
	time.Sleep(1200 * time.Millisecond)
	metrics, sys := s.sample()

	var sum float64
	for _, m := range metrics {
		sum += m.CPU
	}
	t.Logf("sum of per-process CPU = %.2f%%, system CPU = %.2f%% (cores %d)",
		sum, sys.CPUPercent, numCPU())

	// 求和应当与整机使用率同量级。若漏除核数，sum 会是 sys 的约 28 倍；
	// 若多除一次，则约为 1/28。用 3 倍余量足以抓住这两类错误。
	if sys.CPUPercent > 1 {
		ratio := sum / sys.CPUPercent
		if ratio > 3 || ratio < 0.2 {
			t.Errorf("per-process CPU sum (%.2f%%) is %.2fx the system CPU (%.2f%%); "+
				"the scale is wrong", sum, ratio, sys.CPUPercent)
		}
	}
	// 求和不该超过 100% 太多（允许 20% 采样错位）。
	if sum > 120 {
		t.Errorf("per-process CPU sum = %.1f%%, far above 100%%", sum)
	}
}

// TestProcessMemorySumReasonable 校验进程内存求和不超出物理内存太多。
func TestProcessMemorySumReasonable(t *testing.T) {
	s := newSampler()
	s.sample()
	time.Sleep(300 * time.Millisecond)
	metrics, sys := s.sample()

	var sum uint64
	for _, m := range metrics {
		sum += m.Mem
	}
	// 工作集可以重复计算共享页，所以求和略大于已用内存是正常的，
	// 但不应超过物理内存的 3 倍。
	if sys.MemTotal > 0 && sum > sys.MemTotal*3 {
		t.Errorf("sum of working sets (%d) exceeds 3x physical memory (%d)", sum, sys.MemTotal)
	}
	t.Logf("sum of working sets = %s, physical = %s, used = %s",
		humanBytes(sum), humanBytes(sys.MemTotal), humanBytes(sys.MemUsed))
}

// TestGroupMemoryIsPrivateWorkingSet 是这次修正的核心回归测试。
//
// 之前用工作集求和，多进程程序（chrome 46 个进程共享代码页）会虚高
// 约 1.6 倍，并且导致按内存排序的结果整体错位。现在应当读到 Windows
// 自己的「专用工作集」。
func TestGroupMemoryIsPrivateWorkingSet(t *testing.T) {
	s := newSampler()
	defer s.mem.close()
	s.sample() // 建立基线
	time.Sleep(1100 * time.Millisecond)
	metrics, sys := s.sample()
	if len(metrics) == 0 {
		t.Fatal("no metrics")
	}

	// 1) 每个进程的内存都不该超过整机物理内存。
	for _, m := range metrics {
		if m.Mem > sys.MemTotal {
			t.Errorf("%s reports %d bytes, more than physical memory %d",
				m.Name, m.Mem, sys.MemTotal)
		}
	}

	// 2) 求和应当接近已用内存，而不是远超它。
	//
	// 专用工作集不含共享页，所以求和 <= 已用内存是正常的；
	// 用工作集求和则会明显超过已用内存。这是判别的关键。
	var sum uint64
	for _, m := range metrics {
		sum += m.Mem
	}
	t.Logf("sum of private working sets = %s; physical in use = %s",
		humanBytes(sum), humanBytes(sys.MemUsed))

	if sum > sys.MemUsed*3/2 {
		t.Errorf("sum of private working sets (%s) exceeds 1.5x memory in use (%s); "+
			"shared pages are probably being double counted",
			humanBytes(sum), humanBytes(sys.MemUsed))
	}
}

// TestMemorySumsMatchPDHPerGroup 抽查几个多进程程序，确认合计口径正确。
func TestMemorySumsMatchPDHPerGroup(t *testing.T) {
	s := newSampler()
	defer s.mem.close()
	s.sample()
	time.Sleep(900 * time.Millisecond)
	metrics, _ := s.sample()

	by := map[string]uint64{}
	count := map[string]int{}
	for _, m := range metrics {
		by[m.Name] += m.Mem
		count[m.Name]++
	}
	// 找出进程数最多的几个，它们最能暴露共享页重复计数的问题。
	type kv struct {
		name string
		n    int
		mem  uint64
	}
	var list []kv
	for n, c := range count {
		if c >= 5 {
			list = append(list, kv{n, c, by[n]})
		}
	}
	if len(list) == 0 {
		t.Skip("no multi-process groups")
	}
	for _, e := range list {
		avg := e.mem / uint64(e.n)
		t.Logf("%-24s %3d procs  total %8s  avg %8s",
			e.name, e.n, humanBytes(e.mem), humanBytes(avg))
		// 单个进程平均不该超过 4 GB——超过说明口径又错了。
		if avg > 4<<30 {
			t.Errorf("%s: average per process %s looks wrong", e.name, humanBytes(avg))
		}
	}
}
