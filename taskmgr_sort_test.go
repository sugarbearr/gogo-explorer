package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// sortTestApp 造一个有明确数值差异的表格，便于断言顺序。
func sortTestApp() (*app, *ui.Tester) {
	a := newTestApp()
	a.taskMgr.loaded = true
	a.taskMgr.metrics = []procMetric{
		{PID: 3, Name: "ccc.exe", CPU: 1, Mem: 500, Disk: 20},
		{PID: 1, Name: "aaa.exe", CPU: 9, Mem: 100, Disk: 50},
		{PID: 2, Name: "bbb.exe", CPU: 5, Mem: 900, Disk: 5},
	}
	a.taskMgr.sys = sysMetric{MemTotal: 1000, MemUsed: 500}
	return a, ui.NewTester(a.view, 1180, 800)
}

// order 返回当前表格的行顺序，按名字。
func order(a *app) []string {
	var out []string
	for _, r := range a.taskMgr.rows {
		out = append(out, r.key)
	}
	return out
}

func eq(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// TestDefaultSortIsMemDesc 钉住默认排序：首帧必须是「内存降序」。
//
// 对齐 Windows 任务管理器：打开就看到最吃内存的进程，而不是 CPU 最高的。
// 内存是瞬时值、不需要差分基线，首帧就有数；CPU 首帧还拿不到，按它排会
// 在下一秒跳一次序。
func TestDefaultSortIsMemDesc(t *testing.T) {
	a, _ := sortTestApp()
	if a.taskMgr.sort != sortByMem {
		t.Fatalf("default sort = %v, want sortByMem", a.taskMgr.sort)
	}
	if !a.taskMgr.desc {
		t.Fatal("default desc = false, want true")
	}
	// 首帧的 ui.Table 排序状态也必须落在「内存」列上，箭头才指得对。
	if a.taskMgr.list.Sort == nil {
		t.Fatal("list.Sort = nil after the first frame")
	}
	if a.taskMgr.list.Sort.Column != colIDMem || !a.taskMgr.list.Sort.Descending {
		t.Fatalf("list.Sort = {%q desc=%v}, want {%q desc=true}",
			a.taskMgr.list.Sort.Column, a.taskMgr.list.Sort.Descending, colIDMem)
	}
	eq(t, order(a), "bbb.exe", "ccc.exe", "aaa.exe") // 900, 500, 100
}

// TestHeaderClickSorts 是回归测试：点表头必须真的改变行顺序。
//
// 之前的实现每帧都把我们自己的排序写回 ListState.Sort，把 ui.Table
// 刚记录下的点击覆盖掉了，于是「点表头没反应」。
func TestHeaderClickSorts(t *testing.T) {
	a, tt := sortTestApp()
	eq(t, order(a), "bbb.exe", "ccc.exe", "aaa.exe") // 默认内存降序 900, 500, 100

	if err := tt.Click("内存"); err != nil {
		t.Fatal(err)
	}
	if a.taskMgr.sort != sortByMem {
		t.Fatalf("sort = %v, want sortByMem", a.taskMgr.sort)
	}
	// 点当前列（内存）会反向：从降序切到升序。
	eq(t, order(a), "aaa.exe", "ccc.exe", "bbb.exe") // 100, 500, 900

	if err := tt.Click("内存"); err != nil {
		t.Fatal(err)
	}
	if !a.taskMgr.desc {
		t.Fatal("desc = false after the second click, want true")
	}
	eq(t, order(a), "bbb.exe", "ccc.exe", "aaa.exe") // 900, 500, 100
}

// TestSortByEveryColumn 逐列验证点表头都能排序，且方向与表头箭头一致。
func TestSortByEveryColumn(t *testing.T) {
	a, tt := sortTestApp()

	if err := tt.Click("PID"); err != nil {
		t.Fatal(err)
	}
	eq(t, order(a), "aaa.exe", "bbb.exe", "ccc.exe") // pid 1,2,3

	if err := tt.Click("名称"); err != nil {
		t.Fatal(err)
	}
	eq(t, order(a), "aaa.exe", "bbb.exe", "ccc.exe") // 名字升序

	if err := tt.Click("磁盘"); err != nil {
		t.Fatal(err)
	}
	eq(t, order(a), "bbb.exe", "ccc.exe", "aaa.exe") // 5, 20, 50
}

// TestSortSurvivesResample 确认排序在采样刷新后依然生效。
//
// 采样每秒换一批 metrics，表格会重建；排序状态必须跨帧保持，
// 否则用户点完一秒就弹回默认顺序。
func TestSortSurvivesResample(t *testing.T) {
	a, tt := sortTestApp()
	if err := tt.Click("内存"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("内存"); err != nil { // 切到降序
		t.Fatal(err)
	}
	eq(t, order(a), "bbb.exe", "ccc.exe", "aaa.exe")

	// 模拟下一秒的采样：数值变了，但排序选择不该变。
	a.taskMgr.metrics = []procMetric{
		{PID: 3, Name: "ccc.exe", CPU: 1, Mem: 10},
		{PID: 1, Name: "aaa.exe", CPU: 9, Mem: 300},
		{PID: 2, Name: "bbb.exe", CPU: 5, Mem: 700},
	}
	// 触发一次重建。
	if err := tt.Click("刷新"); err != nil {
		t.Logf("refresh click: %v", err)
	}
	if a.taskMgr.sort != sortByMem || !a.taskMgr.desc {
		t.Fatalf("sort state lost: sort=%v desc=%v", a.taskMgr.sort, a.taskMgr.desc)
	}
	// 重建后行顺序仍按内存降序。
	_ = ui.Render(a.view, 1180, 800, 1)
	eq(t, order(a), "bbb.exe", "aaa.exe", "ccc.exe") // 700, 300, 10
}

// TestSortDirectionMatchesArrow 确认表头箭头方向和数据顺序一致。
func TestSortDirectionMatchesArrow(t *testing.T) {
	a, tt := sortTestApp()
	if err := tt.Click("CPU"); err != nil {
		t.Fatal(err)
	}
	// 第一次点 CPU 是升序：数值从小到大。
	eq(t, order(a), "ccc.exe", "bbb.exe", "aaa.exe") // 1, 5, 9
	if a.taskMgr.desc {
		t.Error("desc = true on the first click of a new column")
	}
	// 第二次反向。
	if err := tt.Click("CPU"); err != nil {
		t.Fatal(err)
	}
	eq(t, order(a), "aaa.exe", "bbb.exe", "ccc.exe") // 9, 5, 1
	if !a.taskMgr.desc {
		t.Error("desc = false on the second click of the same column")
	}
}
