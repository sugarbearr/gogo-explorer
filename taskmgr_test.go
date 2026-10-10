package main

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// syncApp 造一个把异步派发改成同步的 app，让整条后台链路能在测试里跑完。
//
// postFn 会在首采、每次刷新、溯源回调等多处被调用，次数并不固定，
// 所以用一个计数器而不是 WaitGroup：Done 多于 Add 会让 WaitGroup 变负。
func syncApp() (*app, *postCounter) {
	a := newTestApp()
	pc := &postCounter{}
	a.postFn = func(fn func()) {
		fn()
		pc.add(1)
	}
	return a, pc
}

// postCounter 统计 post 被调用的次数，供测试等待后台结果。
type postCounter struct {
	mu sync.Mutex
	n  int
}

func (p *postCounter) add(n int) {
	p.mu.Lock()
	p.n += n
	p.mu.Unlock()
}

func (p *postCounter) get() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n
}

// waitFor 等到计数超过 want，或超时。
func (p *postCounter) waitFor(t *testing.T, want int) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for p.get() <= want {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for post #%d (got %d)", want+1, p.get())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// TestGrouping 覆盖同名进程合并：任务管理器的核心行为。
func TestGrouping(t *testing.T) {
	st := newTaskMgrState()
	st.metrics = []procMetric{
		{PID: 1, Name: "chrome.exe", CPU: 5, Mem: 100, Exe: "C:\\chrome.exe"},
		{PID: 2, Name: "chrome.exe", CPU: 7, Mem: 200, Exe: "C:\\chrome.exe"},
		{PID: 3, Name: "node.exe", CPU: 1, Mem: 50},
	}
	st.loaded = true
	gs := st.groups()
	if len(gs) != 2 {
		t.Fatalf("groups = %d, want 2 (chrome + node)", len(gs))
	}
	var chrome *tmGroup
	for _, g := range gs {
		if g.Name == "chrome.exe" {
			chrome = g
		}
	}
	if chrome == nil {
		t.Fatal("chrome group missing")
	}
	if len(chrome.Procs) != 2 {
		t.Errorf("chrome has %d procs, want 2", len(chrome.Procs))
	}
	if chrome.CPU != 12 {
		t.Errorf("chrome CPU = %v, want 12 (sum)", chrome.CPU)
	}
	if chrome.Mem != 300 {
		t.Errorf("chrome Mem = %d, want 300 (sum)", chrome.Mem)
	}
	// 代表 PID 取组内 CPU 最高的。
	if chrome.PID != 2 {
		t.Errorf("chrome PID = %d, want 2 (highest CPU)", chrome.PID)
	}
	if chrome.Exe == "" {
		t.Error("group lost its exe path; the icon would be missing")
	}
	if gs[0].Name != "chrome.exe" {
		t.Errorf("first group = %q, want chrome.exe", gs[0].Name)
	}
}

// TestFilter 覆盖搜索框过滤。
func TestFilter(t *testing.T) {
	st := newTaskMgrState()
	st.metrics = []procMetric{{PID: 10, Name: "chrome.exe"}, {PID: 20, Name: "node.exe"}}
	st.query = "CHROME"
	if got := st.filtered(); len(got) != 1 || got[0].PID != 10 {
		t.Errorf("name filter = %+v", got)
	}
	st.query = "20"
	if got := st.filtered(); len(got) != 1 || got[0].PID != 20 {
		t.Errorf("pid filter = %+v", got)
	}
	st.query = ""
	if got := st.filtered(); len(got) != 2 {
		t.Errorf("empty filter = %d rows, want 2", len(got))
	}
}

// TestSort 覆盖各列排序，且方向由 desc 控制。
//
// 旧版本的 sortGroups 对 CPU/内存/磁盘永远降序、只在名字与 PID 上
// 做一次反向，desc 基本没用；表头箭头会和数据方向对不上。
func TestSort(t *testing.T) {
	st := newTaskMgrState()
	st.metrics = []procMetric{
		{PID: 3, Name: "c", CPU: 1, Mem: 300, Disk: 5},
		{PID: 1, Name: "a", CPU: 9, Mem: 100, Disk: 50},
		{PID: 2, Name: "b", CPU: 5, Mem: 200, Disk: 20},
	}
	first := func(key sortKey, desc bool) string {
		st.sort, st.desc = key, desc
		return st.groups()[0].Name
	}

	// 降序：大的在前。
	if got := first(sortByCPU, true); got != "a" {
		t.Errorf("CPU desc first = %q, want a", got)
	}
	if got := first(sortByMem, true); got != "c" {
		t.Errorf("Mem desc first = %q, want c", got)
	}
	if got := first(sortByDisk, true); got != "a" {
		t.Errorf("Disk desc first = %q, want a", got)
	}
	if got := first(sortByPID, true); got != "c" {
		t.Errorf("PID desc first = %q, want c (pid 3)", got)
	}
	if got := first(sortByName, true); got != "c" {
		t.Errorf("Name desc first = %q, want c", got)
	}

	// 升序：小的在前。
	if got := first(sortByCPU, false); got != "c" {
		t.Errorf("CPU asc first = %q, want c", got)
	}
	if got := first(sortByMem, false); got != "a" {
		t.Errorf("Mem asc first = %q, want a", got)
	}
	if got := first(sortByDisk, false); got != "c" {
		t.Errorf("Disk asc first = %q, want c", got)
	}
	if got := first(sortByPID, false); got != "a" {
		t.Errorf("PID asc first = %q, want a (pid 1)", got)
	}
	if got := first(sortByName, false); got != "a" {
		t.Errorf("Name asc first = %q, want a", got)
	}
}

// TestToggleSort 确认点同一列会反向，点别的列会换列并回到降序。
func TestToggleSort(t *testing.T) {
	st := newTaskMgrState()
	st.sort, st.desc = sortByCPU, true
	st.toggleSort(sortByCPU)
	if st.sort != sortByCPU || st.desc {
		t.Errorf("toggle same column: sort=%v desc=%v, want desc=false", st.sort, st.desc)
	}
	st.toggleSort(sortByMem)
	if st.sort != sortByMem || !st.desc {
		t.Errorf("toggle new column: sort=%v desc=%v, want sortByMem desc=true", st.sort, st.desc)
	}
}

// TestExpand 覆盖展开分组后子进程进入表格。
func TestExpand(t *testing.T) {
	st := newTaskMgrState()
	st.metrics = []procMetric{
		{PID: 1, Name: "chrome.exe", CPU: 5},
		{PID: 2, Name: "chrome.exe", CPU: 7},
	}
	gs := st.groups()
	rows := flattenGroups(gs)
	if len(rows) != 1 {
		t.Fatalf("collapsed rows = %d, want 1", len(rows))
	}
	if rows[0].group == nil {
		t.Error("first row is not a group row")
	}
	st.expanded["chrome.exe"] = true
	rows = flattenGroups(st.groups())
	if len(rows) != 3 {
		t.Fatalf("expanded rows = %d, want 3 (1 group + 2 children)", len(rows))
	}
	if !rows[1].child || !rows[2].child {
		t.Error("child rows not marked")
	}
	if rows[1].metric.PID != 2 {
		t.Errorf("first child pid = %d, want 2 (higher CPU)", rows[1].metric.PID)
	}
	// 子行也要带上 exe，否则展开后图标会丢。
	if rows[1].exe != rows[0].exe {
		t.Errorf("child exe = %q, want %q", rows[1].exe, rows[0].exe)
	}
}

// TestHistoryCapped 覆盖历史曲线有上限。
func TestHistoryCapped(t *testing.T) {
	st := newTaskMgrState()
	for i := 0; i < 200; i++ {
		st.pushHistory(sysMetric{CPUPercent: float64(i), MemTotal: 100, MemUsed: 50})
	}
	if len(st.cpuHist) != 60 {
		t.Errorf("cpuHist = %d points, want capped at 60", len(st.cpuHist))
	}
	if len(st.memHist) != 60 || len(st.netHist) != 60 {
		t.Errorf("mem/net history not capped: %d / %d", len(st.memHist), len(st.netHist))
	}
	if st.cpuHist[len(st.cpuHist)-1] != 199 {
		t.Errorf("last point = %v, want 199", st.cpuHist[len(st.cpuHist)-1])
	}
}

// TestMetricFor 覆盖按 PID 取指标。
func TestMetricFor(t *testing.T) {
	st := newTaskMgrState()
	st.metrics = []procMetric{{PID: 7, Name: "a", CPU: 3}}
	if m, ok := st.metricFor(7); !ok || m.CPU != 3 {
		t.Errorf("metricFor(7) = %+v, %v", m, ok)
	}
	if _, ok := st.metricFor(99); ok {
		t.Error("metricFor(99) reported found")
	}
}

// TestOpenDetailAndClose 确认详情能打开，并由关闭按钮收起。
func TestOpenDetailAndClose(t *testing.T) {
	a, _ := syncApp()
	m := procMetric{PID: 4242, Name: "x.exe"}
	a.openDetail(m)
	if a.taskMgr.detailPID != 4242 {
		t.Fatalf("detailPID = %d, want 4242", a.taskMgr.detailPID)
	}
	// 再点同一个进程应当保持不变，而不是收起——收起交给关闭按钮，
	// 否则在表格里点同一行会闪掉详情。
	a.openDetail(m)
	if a.taskMgr.detailPID != 4242 {
		t.Errorf("detailPID = %d after re-selecting the same row, want 4242",
			a.taskMgr.detailPID)
	}
	a.closeDetail()
	if a.taskMgr.detailPID != 0 {
		t.Errorf("detailPID = %d after closeDetail, want 0", a.taskMgr.detailPID)
	}
}

// TestStaleTraceDropped 覆盖过期溯源结果被丢弃：用户换了进程后，
// 先返回的旧结果不能覆盖新状态。
func TestStaleTraceDropped(t *testing.T) {
	a, _ := syncApp()
	st := a.taskMgr
	st.tracePending = "PID 200"
	// 旧目标的结果回来了，应当被丢掉。
	st.tracePending = "PID 200" // 保持与请求一致
	if st.tracePending != "PID 200" {
		t.Fatal("setup")
	}
	// 模拟 runTrace 的回调体：标签不匹配就不采用。
	apply := func(label string, r *witrResult) {
		if st.tracePending != label {
			return
		}
		st.tracePending = ""
		st.detail = r
	}
	apply("PID 100", &witrResult{ResolvedTarget: "stale"})
	if st.detail != nil {
		t.Errorf("stale result was applied: %+v", st.detail)
	}
	apply("PID 200", &witrResult{ResolvedTarget: "fresh"})
	if st.detail == nil || st.detail.ResolvedTarget != "fresh" {
		t.Errorf("fresh result not applied: %+v", st.detail)
	}
}

// TestSamplingStarts 确认采样循环能启动并把指标送回来。
func TestSamplingStarts(t *testing.T) {
	a, pc := syncApp()
	a.ensureSampling()
	pc.waitFor(t, 0)
	if !a.taskMgr.loaded {
		t.Fatal("loaded never became true")
	}
	if len(a.taskMgr.metrics) == 0 {
		t.Error("no metrics after the first sample")
	}
	if !a.taskMgr.matricesReady {
		t.Error("matricesReady is false after the first sample")
	}
}

// TestWarningLabel 覆盖 witr 英文告警的翻译，以及未知告警保留原文。
func TestWarningLabel(t *testing.T) {
	cases := map[string]string{
		"Process is using high CPU (>2h total)":      "该进程累计占用 CPU 超过 2 小时",
		"High memory usage":                          "内存占用偏高",
		"Executable was deleted":                     "可执行文件已被删除",
		"Process is listening on a public interface": "正在公网接口上监听",
	}
	for in, want := range cases {
		if got := warningLabel(in); got != want {
			t.Errorf("warningLabel(%q) = %q, want %q", in, got, want)
		}
	}
	// 没见过的告警要原样保留，不能丢信息。
	unknown := "Some brand new witr warning"
	if got := warningLabel(unknown); got != unknown {
		t.Errorf("warningLabel(%q) = %q, want the original text", unknown, got)
	}
}

// TestSourceLabel 覆盖 Source 的呈现。
func TestSourceLabel(t *testing.T) {
	if got := sourceLabel(witrSource{Type: "windows_service", Name: "Spooler"}); !strings.Contains(got, "Spooler") {
		t.Errorf("sourceLabel = %q", got)
	}
	if got := sourceLabel(witrSource{Type: "init"}); got != "系统初始化进程" {
		t.Errorf("sourceLabel(init) = %q", got)
	}
	if got := sourceLabel(witrSource{Type: "unknown"}); got != "unknown" {
		t.Errorf("sourceLabel(unknown) = %q", got)
	}
}

// TestSelectionSurvivesResort 复现真机上的「高亮自己漂走」。
//
// 表格每秒按 CPU 重排，而 ListState.Selected 是行号。若不按 Key 重新
// 对齐，同一个行号在下一帧就指向另一个进程——实测高亮会 20 → 23 → 17
// 自己跳，点到的也不是当初选的那个。
func TestSelectionSurvivesResort(t *testing.T) {
	st := newTaskMgrState()
	st.metrics = []procMetric{
		{PID: 1, Name: "aaa.exe", CPU: 9},
		{PID: 2, Name: "bbb.exe", CPU: 5},
		{PID: 3, Name: "ccc.exe", CPU: 1},
	}
	// 用户点了 bbb（此时排第 1）。
	rows := flattenGroups(st.groups())
	st.sel, st.chosenKey = 1, rows[1].key
	if rows[1].key != "bbb.exe" {
		t.Fatalf("setup: rows[1] = %q, want bbb.exe", rows[1].key)
	}

	// 下一秒 CPU 变了，bbb 掉到最后。
	st.metrics = []procMetric{
		{PID: 1, Name: "aaa.exe", CPU: 50},
		{PID: 2, Name: "bbb.exe", CPU: 1},
		{PID: 3, Name: "ccc.exe", CPU: 30},
	}
	rows = flattenGroups(st.groups())
	st.reselect(rows)
	if st.sel < 0 || rows[st.sel].key != "bbb.exe" {
		t.Fatalf("after resort, sel=%d points at %q, want bbb.exe",
			st.sel, rows[st.sel].key)
	}
}

// TestSelectionClearedWhenProcessExits 确认选中的进程退出后不再指向别人。
func TestSelectionClearedWhenProcessExits(t *testing.T) {
	st := newTaskMgrState()
	st.metrics = []procMetric{{PID: 1, Name: "aaa.exe"}, {PID: 2, Name: "bbb.exe"}}
	rows := flattenGroups(st.groups())
	st.sel, st.chosenKey = 1, rows[1].key

	// bbb 退出了。
	st.metrics = []procMetric{{PID: 1, Name: "aaa.exe"}}
	st.reselect(flattenGroups(st.groups()))
	if st.sel != -1 {
		t.Errorf("sel = %d after the chosen process exited, want -1", st.sel)
	}
	if st.chosenKey != "" {
		t.Errorf("chosenKey = %q, want empty", st.chosenKey)
	}
}

// TestFrame 渲染进程页与性能页，供人工比对布局。
func TestFrame(t *testing.T) {
	dir := os.Getenv("MYGO_VERIFY_DIR")
	if dir == "" {
		t.Skip("MYGO_VERIFY_DIR not set")
	}
	a := newTestApp()
	a.taskMgr.loaded = true
	a.taskMgr.metrics = []procMetric{
		{PID: 12316, Name: "DeepSeek Harness.exe", CPU: 23.4, Mem: 518 << 20, Disk: 1461288, Exe: "C:\\Windows\\explorer.exe"},
		{PID: 38272, Name: "DeepSeek Harness.exe", CPU: 16.9, Mem: 216 << 20, Disk: 1365035, Exe: "C:\\Windows\\explorer.exe"},
		{PID: 14668, Name: "chrome.exe", CPU: 33.8, Mem: 1152 << 20, Disk: 806348, Exe: "C:\\Windows\\explorer.exe"},
		{PID: 10736, Name: "chrome.exe", CPU: 11.7, Mem: 527 << 20, Disk: 767951, Exe: "C:\\Windows\\explorer.exe"},
		{PID: 58300, Name: "Weixin.exe", CPU: 11.7, Mem: 447 << 20},
		{PID: 23872, Name: "msedgewebview2.exe", CPU: 5.2, Mem: 56 << 20, Disk: 29294},
		{PID: 32048, Name: "Qoder.exe", CPU: 3.9, Mem: 338 << 20, Disk: 331},
		{PID: 14188, Name: "ToDesk.exe", CPU: 3.9, Mem: 165 << 20},
		{PID: 27288, Name: "Trae.exe", CPU: 3.9, Mem: 418 << 20, Disk: 82858},
		{PID: 11528, Name: "svchost.exe", CPU: 1.2, Mem: 223 << 20},
	}
	a.taskMgr.sys = sysMetric{
		CPUPercent: 31, MemTotal: 68 << 30, MemUsed: 56 << 30,
		DiskTotal: 322 << 30, DiskFree: 46 << 30,
		NetRx: 288697, NetTx: 55470,
	}
	// 28 个核，做成有高有低的形状，便于看网格是否正常。
	for i := 0; i < numCPU(); i++ {
		a.taskMgr.cores = append(a.taskMgr.cores,
			float64((i*37)%100))
	}
	for i := 0; i < 60; i++ {
		a.taskMgr.pushHistory(sysMetric{
			CPUPercent: 20 + float64(i%17),
			MemTotal:   68 << 30, MemUsed: (50 + uint64(i%8)) << 30,
			NetRx: float64(100000 + (i*7919)%900000),
			NetTx: float64(40000 + (i*3571)%200000),
		})
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 主视图：进程表 + 底部性能区，展开 chrome。
	a.taskMgr.expanded["chrome.exe"] = true
	img := ui.Render(a.view, 1180, 800, 1)
	writePNG(t, filepath.Join(dir, "tm-processes.png"), img)

	// 带详情面板的进程页：选中一个真实进程，并塞入一份 witr 结果，
	// 这样启动链的排版也能一起看。
	a.taskMgr.detailPID = 12316
	a.taskMgr.detailName = "DeepSeek Harness.exe"
	a.taskMgr.detailExe = "C:\\Windows\\explorer.exe"
	a.taskMgr.detail = &witrResult{
		ResolvedTarget: "DeepSeek Harness.exe",
		Target:         witrTargetRef{Type: "pid", Value: "12316"},
		Source:         witrSource{Type: "windows_service", Name: "Service Control Manager"},
		Ancestry: []witrProcess{
			{PID: 4, Command: "System", User: "NT AUTHORITY\\SYSTEM"},
			{PID: 4836, Command: "services.exe", User: "NT AUTHORITY\\SYSTEM"},
			{PID: 12316, Command: "DeepSeek Harness.exe", User: "DESKTOP\\Administrator"},
		},
	}
	img = ui.Render(a.view, 1180, 800, 1)
	writePNG(t, filepath.Join(dir, "tm-detail.png"), img)

	// 折叠性能区，确认折叠态也有信息。
	a.taskMgr.perfOpen = false
	a.taskMgr.detailPID = 0
	a.taskMgr.detail = nil
	img = ui.Render(a.view, 1180, 800, 1)
	writePNG(t, filepath.Join(dir, "tm-collapsed.png"), img)
}

// TestTitlebarCentering 钉住标题栏的三条约定：搜索框水平居中、进程数和
// 刷新按钮都已移除。居中靠一对等宽弹性区实现，任何一侧多出元素都会把
// 搜索框推偏（应用名留在行内时实测偏了 65px），所以这里量真实几何，
// 不靠肉眼。
//
// 量的对象必须是搜索框容器本身（Label "搜索框"）：里面的输入框左边还有
// 放大镜图标，量它会得到偏左的假象。
func TestTitlebarCentering(t *testing.T) {
	a, _ := syncApp()
	for _, w := range []int{820, 1180, 1500} {
		tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, w, 760)
		tt.Frame()
		r, ok := tt.Find("搜索框")
		if !ok {
			t.Fatalf("w=%d: search box not found", w)
		}
		off := r.X + r.W/2 - float32(w)/2
		if off < -0.5 || off > 0.5 {
			t.Errorf("w=%d: search box off-center by %.1fpx (x=%.1f w=%.1f)", w, off, r.X, r.W)
		}
		if _, ok := tt.Find("刷新"); ok {
			t.Errorf("w=%d: refresh button should be gone", w)
		}
		if tt.HasText("个进程") {
			t.Errorf("w=%d: process count text should be gone", w)
		}
	}
}

func writePNG(t *testing.T, path string, img *image.RGBA) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
