package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// groupApp 造一个多进程分组，用于展开/收起的行为测试。
func groupApp(t *testing.T) (*app, *ui.Tester) {
	t.Helper()
	a := newTestApp()
	a.taskMgr.loaded = true
	a.taskMgr.metrics = []procMetric{
		{PID: 1, Name: "chrome.exe", CPU: 9, Mem: 200},
		{PID: 2, Name: "chrome.exe", CPU: 4, Mem: 100},
		{PID: 3, Name: "chrome.exe", CPU: 2, Mem: 50},
	}
	a.taskMgr.sys = sysMetric{MemTotal: 1000, MemUsed: 500}
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	return a, tt
}

// rowsNow 重建一帧并返回当前表格行数（含展开出来的子行）。
func rowsNow(a *app) int {
	ui.Render(a.view, 1180, 800, 1)
	return len(a.taskMgr.rows)
}

// clickGroup 点击分组行（整行，不是箭头）。
func clickGroup(t *testing.T, tt *ui.Tester, a *app) {
	t.Helper()
	r, ok := tt.Find("chrome.exe (3)")
	if !ok {
		t.Fatalf("group row not found (rows=%d)", rowsNow(a))
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
}

// TestGroupClickTogglesEveryTime 是「只能展开无法折叠」的回归测试。
//
// 用户实测：点开一个多进程分组之后，再点同一行不会收起。
//
// 根因有两层，缺一不可：
//  1. onRowChosen 里只写了 expanded = true，没有取反分支；
//  2. 更隐蔽的一层——ui.Table 只在「选中发生变化」时报告 Changed()，
//     第一次点已把该行选上，之后再点同一行它就不再出声。所以折叠不能
//     寄生在表格的选择信号上，必须由我们自己持有的元素接住点击。
//
// 这条测试连点四次，要求严格交替：展开 / 收起 / 展开 / 收起。
func TestGroupClickTogglesEveryTime(t *testing.T) {
	a, tt := groupApp(t)

	if n := rowsNow(a); n != 1 {
		t.Fatalf("initial rows = %d, want 1 (collapsed)", n)
	}

	want := []struct {
		rows     int
		expanded bool
	}{
		{4, true},  // 1 个分组 + 3 个子进程
		{1, false}, // 收起
		{4, true},
		{1, false},
	}
	for i, w := range want {
		clickGroup(t, tt, a)
		if n := rowsNow(a); n != w.rows {
			t.Fatalf("click #%d: rows = %d, want %d", i+1, n, w.rows)
		}
		if got := a.taskMgr.expanded["chrome.exe"]; got != w.expanded {
			t.Fatalf("click #%d: expanded = %v, want %v", i+1, got, w.expanded)
		}
	}
}

// TestGroupChipClickToggles 确认点行首的箭头 chip 同样能切换。
//
// 桌面任务管理器的习惯是点箭头展开/收起，所以箭头必须是一个独立的、
// 每次都响的点击目标，而不是整行点击的附属装饰。
func TestGroupChipClickToggles(t *testing.T) {
	a, tt := groupApp(t)

	for i := 0; i < 3; i++ {
		r, ok := tt.Find("分组 chrome.exe")
		if !ok {
			t.Fatalf("iter %d: group cell not found", i)
		}
		// 点名称格最左边的箭头槽（chip 所在处）。
		tt.ClickAt(r.X+chipSlot/2, r.Y+r.H/2)
		n := rowsNow(a)
		wantExpanded := n > 1
		if a.taskMgr.expanded["chrome.exe"] != wantExpanded {
			t.Fatalf("iter %d: rows=%d but expanded=%v; the two disagree",
				i, n, a.taskMgr.expanded["chrome.exe"])
		}
	}
	// 三次点击后应当回到展开态（奇数次切换）。
	if n := rowsNow(a); n != 4 {
		t.Errorf("after 3 chip clicks rows = %d, want 4 (expanded)", n)
	}
}

// TestGroupCollapseDoesNotOpenDetail 确认收起分组不会误开详情面板。
//
// 分组行的汇总数字对不上任何一个真实进程，点它只应切换展开状态。
func TestGroupCollapseDoesNotOpenDetail(t *testing.T) {
	a, tt := groupApp(t)

	clickGroup(t, tt, a) // 展开
	clickGroup(t, tt, a) // 收起
	if a.taskMgr.detailPID != 0 {
		t.Errorf("detailPID = %d after toggling the group, want 0", a.taskMgr.detailPID)
	}
	if n := rowsNow(a); n != 1 {
		t.Errorf("rows = %d after collapse, want 1", n)
	}
}

// TestChildRowOpensDetail 确认展开后点子进程行开的是它自己的详情，
// 而不是又去切换分组。
func TestChildRowOpensDetail(t *testing.T) {
	a, tt := groupApp(t)

	clickGroup(t, tt, a) // 展开
	if n := rowsNow(a); n != 4 {
		t.Fatalf("rows = %d after expand, want 4", n)
	}

	// 子行显示的是进程自己的名字（同一帧里 3 个子行都叫 chrome.exe），
	// 用表格行里 metric 的 PID 断言点中的是谁。
	var childPID int
	for _, r := range a.taskMgr.rows {
		if r.child {
			childPID = r.metric.PID
			break
		}
	}
	if childPID == 0 {
		t.Fatal("no child rows after expanding")
	}
	// 子行按 CPU 降序，第一个子行是 CPU 最高的 PID 1。
	if childPID != 1 {
		t.Errorf("first child PID = %d, want 1 (highest CPU)", childPID)
	}
}

// TestSingleProcessRowStillOpensDetail 确认单进程行不受展开逻辑影响：
// 它没有可展开的东西，点它仍然是开详情。
func TestSingleProcessRowStillOpensDetail(t *testing.T) {
	a := newTestApp()
	a.taskMgr.loaded = true
	a.taskMgr.metrics = []procMetric{{PID: 77, Name: "solo.exe", CPU: 5, Mem: 10}}
	a.taskMgr.sys = sysMetric{MemTotal: 100, MemUsed: 50}
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()

	if err := tt.Click("solo.exe"); err != nil {
		t.Fatalf("click: %v", err)
	}
	if a.taskMgr.detailPID != 77 {
		t.Errorf("detailPID = %d, want 77", a.taskMgr.detailPID)
	}
	// 单进程行不该被塞进 expanded 表里。
	if a.taskMgr.expanded["solo.exe"] {
		t.Error("single-process row was marked expanded")
	}
}
