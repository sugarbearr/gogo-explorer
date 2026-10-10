// 任务管理器的状态与采样循环。
package main

import (
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
)

// 排序字段。
type sortKey int

const (
	sortByCPU sortKey = iota
	sortByName
	sortByMem
	sortByDisk
	sortByPID
)

// taskMgrState 是任务管理器的全部状态。视图每次重建都从它读，绝不放元素。
type taskMgrState struct {
	metrics []procMetric
	sys     sysMetric
	sampler *sampler

	started bool
	loaded  bool
	loadErr string

	// matricesReady 让「刷新」按钮能强制重来一次基线采样。
	matricesReady bool

	sort  sortKey
	desc  bool
	sel   int
	query string
	list  ui.ListState
	// rows 是本帧拍平后的表格行，cell 回调按行号取它。
	rows []tmRow
	// chosenKey 是用户选中的那个进程的 Key。行号会随重排变化，Key 不会。
	chosenKey string
	// lastChosenKey 是上一次已经处理过的行，避免同一行被重复打开详情。
	lastChosenKey string
	// columnIDs 是本帧的列序（按 ID）。用户可以把列拖到别处，
	// 所以 cell 回调只给下标，必须靠这张表还原成列的身份。
	columnIDs []string

	// 性能区常驻在表格下方，可折叠。
	perfOpen bool
	// percpu 是每核 CPU 的采样器，cores 是它最近一次的读数。
	percpu *percpuSampler
	cores  []float64

	// expanded 记录哪些同名分组被展开。
	expanded map[string]bool

	// 详情面板
	detailPID    int
	detailName   string
	detailExe    string
	detail       *witrResult
	traceErr     string
	tracePending string
	cands        []witrCandidate
	traceCache   map[string]*witrResult

	// 性能页的历史曲线，最多保留 60 个点。
	cpuHist []float64
	memHist []float64
	netHist []float64
}

func newTaskMgrState() *taskMgrState {
	return &taskMgrState{
		sampler:    newSampler(),
		sort:       sortByMem,
		desc:       true,
		sel:        -1,
		expanded:   map[string]bool{},
		traceCache: map[string]*witrResult{},
		perfOpen:   true,
		percpu:     newPerCPUSampler(),
	}
}

// syncSortFromList 把 ui.Table 记录的排序读回我们的 sortKey。
//
// ui.Table 是排序状态的持有者：用户点表头时它直接改 ListState.Sort。
// 我们只在建表前把它读回来，绝不能反过来每帧写进去——那会把用户的
// 点击覆盖掉，表现为「点表头没反应」。
func (st *taskMgrState) syncSortFromList() {
	if st.list.Sort == nil {
		return
	}
	if key, ok := sortKeyForColumn(st.list.Sort.Column); ok {
		st.sort, st.desc = key, st.list.Sort.Descending
	}
}

// reselect 把选中行号重新对齐到 chosenKey 所指的进程。
//
// 必须在建表之前调用：表格按 CPU 每秒重排，行号所指的进程会变，
// 不对齐的话高亮会自己漂走。
func (st *taskMgrState) reselect(rows []tmRow) {
	if st.chosenKey == "" {
		return
	}
	for i, r := range rows {
		if r.key == st.chosenKey {
			st.sel = i
			return
		}
	}
	// 选中的进程已经退出：清掉选中，别让它指向别人。
	st.sel = -1
	st.chosenKey = ""
	st.lastChosenKey = ""
}

// chosenRow 返回当前选中的那一行。
//
// 选中状态在 ListState.Selected 里是一个行号，但表格每秒都会按 CPU
// 重排，行号所指的进程会变。这里用 Key 去本帧的行里反查，
// 保证拿到的是用户当初选的那个进程。
func (st *taskMgrState) chosenRow() (tmRow, bool) {
	if st.sel < 0 || st.sel >= len(st.rows) {
		return tmRow{}, false
	}
	// Changed 就在这一帧触发，sel 与 rows 同源，行号直接可用。
	return st.rows[st.sel], true
}

// perfHeight 返回性能区的高度：展开时 200，折叠时只留标题行。
func (st *taskMgrState) perfHeight() float32 {
	if !st.perfOpen {
		return 34
	}
	return 200
}

// toggleSort 切换排序列；点同一列则反向。
func (st *taskMgrState) toggleSort(key sortKey) {
	if st.sort == key {
		st.desc = !st.desc
		return
	}
	st.sort, st.desc = key, true
}

// metricFor 返回某个 PID 当前的指标。
func (st *taskMgrState) metricFor(pid int) (procMetric, bool) {
	for _, m := range st.metrics {
		if m.PID == pid {
			return m, true
		}
	}
	return procMetric{}, false
}

// filtered 按搜索框过滤进程。
func (st *taskMgrState) filtered() []procMetric {
	q := strings.ToLower(strings.TrimSpace(st.query))
	if q == "" {
		return st.metrics
	}
	var out []procMetric
	for _, m := range st.metrics {
		if strings.Contains(strings.ToLower(m.Name), q) ||
			strings.Contains(itoa(m.PID), q) {
			out = append(out, m)
		}
	}
	return out
}

// groups 按当前筛选与排序返回分组后的行。
func (st *taskMgrState) groups() []*tmGroup {
	rows := st.filtered()
	byName := map[string]*tmGroup{}
	var order []string
	for _, m := range rows {
		g, ok := byName[m.Name]
		if !ok {
			g = &tmGroup{Name: m.Name}
			byName[m.Name] = g
			order = append(order, m.Name)
		}
		g.Procs = append(g.Procs, m)
		g.CPU += m.CPU
		g.Mem += m.Mem
		g.Disk += m.Disk
		if g.Exe == "" && m.Exe != "" {
			g.Exe = m.Exe
		}
	}
	out := make([]*tmGroup, 0, len(order))
	for _, n := range order {
		g := byName[n]
		// 组内按 CPU 降序，最高的那个作为代表 PID。
		sort.Slice(g.Procs, func(i, j int) bool { return g.Procs[i].CPU > g.Procs[j].CPU })
		g.PID = g.Procs[0].PID
		g.expanded = st.expanded[n]
		out = append(out, g)
	}
	st.sortGroups(out)
	return out
}

// sortGroups 按当前排序字段排列分组。
//
// asc 是「升序时该用的比较」。desc 为真就把它反过来用——这样
// 表头上的 ↓/↑ 和实际顺序永远一致，不会出现「箭头朝下、数据朝上」。
func (st *taskMgrState) sortGroups(gs []*tmGroup) {
	ascLess := func(i, j int) bool {
		a, b := gs[i], gs[j]
		switch st.sort {
		case sortByName:
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		case sortByMem:
			return a.Mem < b.Mem
		case sortByDisk:
			return a.Disk < b.Disk
		case sortByPID:
			return a.PID < b.PID
		default: // sortByCPU
			if a.CPU != b.CPU {
				return a.CPU < b.CPU
			}
			return a.Mem < b.Mem
		}
	}
	if st.desc {
		sort.SliceStable(gs, func(i, j int) bool { return ascLess(j, i) })
		return
	}
	sort.SliceStable(gs, ascLess)
}

// pushHistory 把整机指标推进曲线，最多保留 60 个点（约 1 分钟）。
func (st *taskMgrState) pushHistory(sys sysMetric) {
	const maxPoints = 60
	st.cpuHist = appendCapped(st.cpuHist, sys.CPUPercent, maxPoints)
	mem := 0.0
	if sys.MemTotal > 0 {
		mem = float64(sys.MemUsed) / float64(sys.MemTotal) * 100
	}
	st.memHist = appendCapped(st.memHist, mem, maxPoints)
	st.netHist = appendCapped(st.netHist, sys.NetRx+sys.NetTx, maxPoints)
}

func appendCapped(s []float64, v float64, max int) []float64 {
	s = append(s, v)
	if len(s) > max {
		s = s[len(s)-max:]
	}
	return s
}

// ensureSampling 启动采样循环。
//
// 采样全部 500+ 进程约 4ms，1 秒一次的开销可以忽略。采样在后台
// goroutine 里，结果经 post 回主线程。
func (a *app) ensureSampling() {
	st := a.taskMgr
	if st.started || (a.win == nil && a.postFn == nil) {
		return
	}
	st.started = true
	go func() {
		// 首帧先采一次，让内存这类不需要差分的指标立刻有值。
		metrics, sys := st.sampler.sample()
		a.post(func() {
			st.metrics, st.sys = metrics, sys
			st.loaded = true
			st.matricesReady = true
		})
		for {
			a.sleep(sampleInterval)
			metrics, sys := st.sampler.sample()
			a.post(func() {
				st.metrics, st.sys = metrics, sys
				st.loaded = true
				st.matricesReady = true
				st.pushHistory(sys)
			})
		}
	}()
}

// refreshNow 丢掉基线、立刻重采一次。
func (a *app) refreshNow() {
	st := a.taskMgr
	a.post(func() { st.matricesReady = false })
}
