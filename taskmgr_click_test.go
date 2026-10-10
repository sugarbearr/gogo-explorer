package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestClickDetailAtBottomOfList 复现真机上的现象：列表滚动后，
// 点名次靠后的行是否还能打开详情。行号用的是「拍平后的下标」，
// 所以这里构造一个长列表来覆盖。
func TestClickDetailAtBottomOfList(t *testing.T) {
	a := newTestApp()
	a.taskMgr.loaded = true
	// 40 个单进程分组，最后一个是 explorer.exe。
	for i := 0; i < 39; i++ {
		a.taskMgr.metrics = append(a.taskMgr.metrics,
			procMetric{PID: 1000 + i, Name: "app" + itoa(i) + ".exe", CPU: 1})
	}
	// 默认按内存降序，所以给前面的行更小的内存，explorer.exe 仍然排在最后。
	for i := range a.taskMgr.metrics {
		a.taskMgr.metrics[i].Mem = uint64(1000 + i)
	}
	a.taskMgr.metrics = append(a.taskMgr.metrics,
		procMetric{PID: 46256, Name: "explorer.exe", CPU: 9, Mem: 9000})
	a.taskMgr.sys = sysMetric{MemTotal: 100, MemUsed: 50}

	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("explorer.exe"); err != nil {
		t.Fatalf("click: %v", err)
	}
	if a.taskMgr.detailPID != 46256 {
		t.Fatalf("detailPID = %d, want 46256", a.taskMgr.detailPID)
	}
	if !tt.HasText("PID 46256") {
		t.Errorf("detail panel not rendered; texts = %q", tt.Texts())
	}
}

// TestClickDifferentRowsOpensEach 覆盖连续点不同行：每点一行，
// 详情都要跟着换成那一行指向的进程。
//
// 表格会按 CPU 每秒重排，行号所指的进程会变，所以这条路径要单独守住。
func TestClickDifferentRowsOpensEach(t *testing.T) {
	a := newTestApp()
	a.taskMgr.loaded = true
	a.taskMgr.metrics = []procMetric{
		{PID: 111, Name: "aaa.exe", CPU: 5, Mem: 100},
		{PID: 222, Name: "bbb.exe", CPU: 3, Mem: 50},
		{PID: 333, Name: "ccc.exe", CPU: 1, Mem: 10},
	}
	a.taskMgr.sys = sysMetric{MemTotal: 100, MemUsed: 50}
	tt := ui.NewTester(a.view, 1180, 800)

	for _, c := range []struct {
		name string
		pid  int
	}{{"aaa.exe", 111}, {"bbb.exe", 222}, {"ccc.exe", 333}, {"aaa.exe", 111}} {
		if err := tt.Click(c.name); err != nil {
			t.Fatalf("click %s: %v", c.name, err)
		}
		if a.taskMgr.detailPID != c.pid {
			t.Fatalf("after clicking %s: detailPID = %d, want %d",
				c.name, a.taskMgr.detailPID, c.pid)
		}
	}
}

// TestClickMultiProcessGroupExpands 确认点多进程的分组是展开而不是开详情，
// 展开后再点具体子进程才开详情。
func TestClickMultiProcessGroupExpands(t *testing.T) {
	a := newTestApp()
	a.taskMgr.loaded = true
	a.taskMgr.metrics = []procMetric{
		{PID: 1, Name: "chrome.exe", CPU: 9, Mem: 200},
		{PID: 2, Name: "chrome.exe", CPU: 4, Mem: 100},
	}
	a.taskMgr.sys = sysMetric{MemTotal: 100, MemUsed: 50}
	tt := ui.NewTester(a.view, 1180, 800)

	if err := tt.Click("chrome.exe (2)"); err != nil {
		t.Fatalf("click group: %v", err)
	}
	if !a.taskMgr.expanded["chrome.exe"] {
		t.Fatal("group did not expand on click")
	}
	if a.taskMgr.detailPID != 0 {
		t.Errorf("detailPID = %d after clicking the group, want 0", a.taskMgr.detailPID)
	}
}
