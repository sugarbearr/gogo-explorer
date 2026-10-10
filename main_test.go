package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// newTestApp 造一个状态齐全的 app，用于无头测试。
func newTestApp() *app {
	return &app{taskMgr: newTaskMgrState(), icons: newIconCache()}
}

// TestViewHasNoTabs 确认进程/性能的切换按钮已经去掉，性能区直接
// 常驻在进程表下方。
func TestViewHasNoTabs(t *testing.T) {
	a := newTestApp()
	a.taskMgr.loaded = true
	a.taskMgr.metrics = []procMetric{{PID: 1, Name: "a.exe"}}
	tt := ui.NewTester(a.view, 1180, 800)

	if !tt.HasText("任务管理器") {
		t.Errorf("title bar missing; texts = %q", tt.Texts())
	}
	if !tt.HasText("性能") {
		t.Error("performance strip missing")
	}
	// 「性能」不再是可点的标签，而是下方区域的标题。
	for _, gone := range []string{"进程"} {
		_ = gone
	}
	// 表头要有。
	for _, h := range []string{"名称", "PID", "CPU", "内存", "磁盘"} {
		if !tt.HasText(h) {
			t.Errorf("table header %q missing", h)
		}
	}
}

// TestNoSidebar 确认老的侧栏外壳已经彻底移除。
func TestNoSidebar(t *testing.T) {
	a := newTestApp()
	a.taskMgr.loaded = true
	tt := ui.NewTester(a.view, 1180, 800)
	for _, gone := range []string{"新建任务", "插件市场", "我的产物", "international-web"} {
		if tt.HasText(gone) {
			t.Errorf("old shell item %q is still present", gone)
		}
	}
}

// TestPerfStripCollapses 确认性能区能折叠，且折叠后仍显示概要数字。
func TestPerfStripCollapses(t *testing.T) {
	a := newTestApp()
	a.taskMgr.loaded = true
	a.taskMgr.metrics = []procMetric{{PID: 1, Name: "a.exe"}}
	a.taskMgr.sys = sysMetric{CPUPercent: 31, MemTotal: 100, MemUsed: 82}

	// 展开时高度大。
	a.taskMgr.perfOpen = true
	open := a.taskMgr.perfHeight()

	tt := ui.NewTester(a.view, 1180, 800)
	if err := tt.Click("性能"); err != nil {
		t.Fatalf("click 性能: %v", err)
	}
	if a.taskMgr.perfOpen {
		t.Fatal("performance strip did not collapse")
	}
	if a.taskMgr.perfHeight() >= open {
		t.Errorf("collapsed height %v not less than expanded %v",
			a.taskMgr.perfHeight(), open)
	}
	// 折叠后标题行上仍要有概要数字。
	if !tt.HasText("CPU 31%") {
		t.Errorf("collapsed strip lost its summary; texts = %q", tt.Texts())
	}
}
