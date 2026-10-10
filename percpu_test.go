package main

import (
	"runtime"
	"testing"
	"time"
)

// TestPerCPUCount 确认核数与 NumCPU 一致——每个逻辑处理器一个实例。
func TestPerCPUCount(t *testing.T) {
	s := newPerCPUSampler()
	defer s.close()

	// 第一次调用只是建立基线。
	s.sample()
	time.Sleep(700 * time.Millisecond)
	cores := s.sample()

	if len(cores) == 0 {
		t.Fatal("no per-core samples; PDH setup failed")
	}
	if len(cores) != runtime.NumCPU() {
		t.Errorf("got %d cores, want %d (runtime.NumCPU)", len(cores), runtime.NumCPU())
	}
}

// TestPerCPURange 确认每核利用率落在 0..100。
//
// 这条测试专门守 PDH_FMT_COUNTERVALUE 的对齐问题：少写 4 字节填充时
// CStatus 读到垃圾值，每个核都会被判为无效而返回 0——那样这里会看到
// 全是 0，测试就会失败。
func TestPerCPURange(t *testing.T) {
	s := newPerCPUSampler()
	defer s.close()
	s.sample() // 基线
	time.Sleep(900 * time.Millisecond)
	cores := s.sample()
	if len(cores) == 0 {
		t.Fatal("no samples")
	}

	// 至少有一个核要在动。全 0 说明读值失败（对齐问题）而不是机器空闲。
	busy := 0
	for _, v := range cores {
		if v < 0 || v > 100 {
			t.Errorf("core value %.2f out of range 0..100", v)
		}
		if v > 1 {
			busy++
		}
	}
	if busy == 0 {
		t.Errorf("every core read exactly 0%%; the counter values are not being read")
	}
	t.Logf("%d cores, %d above 1%%", len(cores), busy)
}

// TestPerCPUBusyCoreResponds 烧一个核，确认对应的核读数上升。
//
// 只要求「有核明显上升」，不指定是哪一个：调度器可以把 goroutine
// 放在任意核上跑，而且会被抢占。
func TestPerCPUBusyCoreResponds(t *testing.T) {
	s := newPerCPUSampler()
	defer s.close()
	s.sample()
	time.Sleep(600 * time.Millisecond)
	before := s.sample()
	if len(before) == 0 {
		t.Skip("no per-core samples")
	}

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
	time.Sleep(1200 * time.Millisecond)
	after := s.sample()
	close(stop)
	<-done

	if len(after) != len(before) {
		t.Fatalf("core count changed: %d then %d", len(before), len(after))
	}
	max := 0.0
	idx := -1
	for i, v := range after {
		if v > max {
			max, idx = v, i
		}
	}
	t.Logf("busiest core %d at %.1f%%", idx, max)
	if max < 5 {
		t.Errorf("busiest core is only %.1f%% while a core was pinned; sampling looks wrong", max)
	}
}

// TestPerCPUSamplerFailureIsSticky 确认建立失败后不再每帧重试。
func TestPerCPUSamplerFailureIsSticky(t *testing.T) {
	s := newPerCPUSampler()
	s.failed = true
	if got := s.sample(); got != nil {
		t.Errorf("sample returned %v after a recorded failure", got)
	}
	if !s.failed {
		t.Error("failed flag was cleared")
	}
}
