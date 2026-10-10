package main

import (
	"strings"
	"syscall"
	"testing"
)

// TestMemoryCompressionHidden 确认只隐藏 Memory Compression。
func TestMemoryCompressionHidden(t *testing.T) {
	procs, err := listProcesses()
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) == 0 {
		t.Fatal("no processes")
	}
	for _, p := range procs {
		if strings.EqualFold(p.Name, "Memory Compression") {
			t.Fatalf("Memory Compression (pid %d) is listed; it should be hidden", p.PID)
		}
	}
	t.Logf("%d processes listed", len(procs))
}

// TestOtherKernelObjectsVisible 是这次修正的关键回归测试。
//
// 我先前用「名字不以 .exe 结尾」当判据，连带把 System、Secure System、
// Registry 也滤掉了。用户贴的任务管理器截图证明这三个是**显示**的
// （Secure System 188.9 MB、Registry 4.9 MB、System 0.1 MB）。
// 所以它们必须留在列表里。
func TestOtherKernelObjectsVisible(t *testing.T) {
	procs, err := listProcesses()
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, p := range procs {
		have[strings.ToLower(p.Name)] = true
	}
	// 这三个人任务管理器里有，我们也必须有。
	for _, want := range []string{"system", "secure system", "registry"} {
		if !have[want] {
			t.Errorf("%q is hidden but Task Manager displays it", want)
		}
	}
}

// TestRealProcessesStillVisible 确认没有误伤真实进程。
func TestRealProcessesStillVisible(t *testing.T) {
	procs, err := listProcesses()
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, p := range procs {
		have[strings.ToLower(p.Name)] = true
	}
	for _, want := range []string{"svchost.exe", "lsass.exe", "explorer.exe"} {
		if !have[want] {
			t.Errorf("real process %q is missing", want)
		}
	}
	if len(procs) < 100 {
		t.Errorf("only %d processes listed; the filter is too aggressive", len(procs))
	}
}

// TestIsHiddenProcess 直接测判据，不依赖机器上跑着什么。
func TestIsHiddenProcess(t *testing.T) {
	cases := map[string]bool{
		"Memory Compression":  true,
		"memory compression":  true,
		"MEMORY COMPRESSION":  true,
		" Memory Compression": true, // 首尾空白要容忍
		// 这三个任务管理器会显示，不能藏。
		"System":        false,
		"Secure System": false,
		"Registry":      false,
		// 普通进程。
		"svchost.exe":  false,
		"lsass.exe":    false,
		"chrome.exe":   false,
		"explorer.exe": false,
	}
	for name, want := range cases {
		u := syscall.StringToUTF16(name)
		if got := isHiddenProcess(u); got != want {
			t.Errorf("isHiddenProcess(%q) = %v, want %v", name, got, want)
		}
	}
}
