// 进程表切片：主区里「进程表」这一块的全部视图代码。
//
// 本文件只负责进程表。整窗外壳（标题栏）在 shell.go，底部性能区在
// perf_ui.go，右侧详情在 detail_ui.go；采样与数据模型在
// taskmgr_state.go / procs.go，本文件只读不改。
//
// 视觉上做三件事：
//  1. 信息密度：行高由 Lead 在 view() 里把 t.Spacing 调到 5（≈34px，见 rowH）
//     统一控制，这里只保证单元格内边距、对齐与字号好看。名称列占满剩余
//     空间，数字列固定宽右对齐，行末不再是一排忽长忽短的字。
//  2. CPU 迷你条：以前是 2px 的裸色块，低压时缩成一个「故障点」。现在先画
//     一条 40x4 的浅色轨道（cLine），再按负载叠上分档配色的填充，于是
//     「没有 CPU 占用」也读得出来是一条空轨道，而不是缺了一块。
//  3. 空态：以前是 Padding(16) 贴在左上角的一行字。现在是居中的图标 +
//     主文案 + 次要说明，容器带 Label，测试能定位。
//
// 颜色、圆角、间距、字阶一律取自 tokens.go，不再出现裸的 ui.Hex。
package main

import (
	"github.com/egoist/mygo/ui"
)

// 表格列身份。这个顺序也是默认列序；名称列不设宽，占满别人剩下的空间。
const (
	colIDName = "name"
	colIDPID  = "pid"
	colIDCPU  = "cpu"
	colIDMem  = "mem"
	colIDDisk = "disk"
)

// tableColumnIDs 是默认列序，也是「合法列 ID」的清单。
// 用户把列拖来拖去之后，靠它把本帧的列下标还原成列的身份。
var tableColumnIDs = []string{colIDName, colIDPID, colIDCPU, colIDMem, colIDDisk}

// CPU 迷你条的尺寸与单元格内部间距。
//
// 迷你条固定 40 宽：列宽减去左右内边距与两处 gap 之后刚好放得下，
// 不用给单元格加 min-width——那会反过来把名称列挤窄。
const (
	barW     float32 = 40 // 迷你条总宽
	barH     float32 = 4  // 迷你条高
	cpuNumW  float32 = 46 // 迷你条左边那串百分数的固定宽
	cellGap  float32 = 8  // 单元格内元素之间的间距
	chipSlot float32 = 16 // 名称列左边的箭头槽宽，也是子行的缩进量
)

// tbEmptyIcon 是空态用的示意图标：一块屏幕加一条折线。
//
// 放在本文件而不是 icons.go，是因为 icons.go 属于别人的写作用域；
// 画法与 main.go 里那组保持一致（24x24、stroke-width 2、currentColor）。
var tbEmptyIcon = ui.MustParseSVG([]byte(
	`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" ` +
		`stroke="currentColor" stroke-width="2" stroke-linecap="round" ` +
		`stroke-linejoin="round">` +
		`<rect x="2" y="4" width="20" height="13" rx="2"/>` +
		`<path d="M7 20h10"/>` +
		`<path d="M9 12.5l2.5-2.5L14 12.5"/>` +
		`</svg>`))

// processesView 是主区里的进程表：空/未加载/失败三态，或者表格本身，
// 选中进程后右侧叠上详情面板。
//
// 行高（t.Spacing -> rowH）由 Lead 在 view() 里统一设置，这里绝不调
// c.SetTheme：主题是全局的，各切片自己改会互相打架。
func (a *app) processesView(c *ui.Context, t *ui.Theme) {
	st := a.taskMgr
	ui.Row(c).Grow(1).Clip().AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Grow(1).Clip().Children(func() {
			if st.loaded && st.loadErr == "" {
				a.table(c, t)
				return
			}
			a.emptyProcesses(c, t)
		})
		if st.detailPID != 0 {
			a.detailPanel(c, t)
		}
	})
}

// table 画进程表。
//
// 行为契约（有测试守着，别动）：
//   - 列 ID 必须是 name/pid/cpu/mem/disk，标题文案不变；
//   - st.syncSortFromList() 必须在建表前调用：排序状态的持有者是 ui.Table，
//     反过来每帧写进去会把用户的点击覆盖掉，表现为「点表头没反应」；
//   - st.list.Sort 只在为 nil 时初始化，给首帧一个「内存降序」的起点
//     （对齐 Windows 任务管理器）；
//   - st.reselect(rows) 必须在建表前调用，否则高亮会随重排漂走。
func (a *app) table(c *ui.Context, t *ui.Theme) {
	st := a.taskMgr

	st.syncSortFromList()
	if st.list.Sort == nil {
		st.list.Sort = &ui.SortOrder{Column: st.sortColumnID(), Descending: st.desc}
	}

	rows := flattenGroups(st.groups())
	st.rows = rows
	st.reselect(rows)
	st.list.Selected = &st.sel
	st.list.Key = func(i int) any {
		if i < 0 || i >= len(rows) {
			return i
		}
		return rows[i].key
	}
	st.list.Label = func(i int) string {
		if i < 0 || i >= len(rows) {
			return ""
		}
		return rows[i].name
	}
	a.ensureColumnIDs()

	cols := []ui.TableColumn{
		{Title: "名称", ID: colIDName, Sortable: true},
		{Title: "PID", ID: colIDPID, Width: pidColWidth, Align: ui.End, Sortable: true},
		{Title: "CPU", ID: colIDCPU, Width: cpuColWidth, Align: ui.End, Sortable: true},
		{Title: "内存", ID: colIDMem, Width: memColWidth, Align: ui.End, Sortable: true},
		{Title: "磁盘", ID: colIDDisk, Width: diskColWidth, Align: ui.End, Sortable: true},
	}

	tbl := ui.Table(c, &st.list, cols, len(rows), func(row, col int) {
		a.cell(c, t, rows[row], col)
	}).Grow(1).Clip()

	// 表格把「选中变化」和「双击」分成两个信号（见 ui.Table 的文档：
	// Changed 报告新的选择，Submitted 报告双击或回车）。我们的折叠需求正好
	// 落在两者的缝里，所以两条都要接：
	//
	//   - Changed：用户点了**另一**行。选中的是分组就切换展开/收起。
	//   - Submitted：用户双击了某一行（含重复双击同一行）。单击同一行时
	//     ui.Table 的 choose() 发现选择没变，不会发 Changed，所以「再点一次
	//     同一行收起」这条路靠双击与行首 chip 兜住。
	//
	// 一次双击会先产生 Changed（选中变化）再产生 Submitted，因此用
	// doubleHandled 避免同一帧里被处理两遍、开合互相抵消。
	if tbl.Changed() {
		// 行号每秒都会被重排打乱，所以用 chosenRow 按本帧的 rows 取行。
		if r, ok := st.chosenRow(); ok {
			if r.key != st.lastChosenKey {
				st.lastChosenKey = r.key
				st.chosenKey = r.key
				a.onRowChosen(r)
			}
		}
	}
	if tbl.Submitted() {
		if r, ok := st.chosenRow(); ok {
			a.onRowDoubleClicked(r)
		}
	}
}

// onRowChosen 处理用户选中了一行。
//
// 单进程的行直接打开详情；多进程的分组切换展开/收起，让用户挑具体的那个——
// 分组行的汇总数字对不上任何一个真实进程，直接弹详情会指错人。
//
// 这里必须是「切换」而不是「只能展开」：任务管理器里点一下展开、再点一下
// 收起是最基本的预期，之前只写 expanded = true，于是展开后就再也收不回去。
func (a *app) onRowChosen(r tmRow) {
	if r.group != nil {
		if len(r.group.Procs) == 1 {
			a.openDetail(r.group.Procs[0])
			return
		}
		a.toggleGroup(r.group.Name)
		return
	}
	if r.metric != nil {
		a.openDetail(*r.metric)
	}
}

// toggleGroup 翻转一个分组的展开状态。
//
// 展开状态只认 st.expanded 这一份数据，行首的 chip 与整行点击都走这里，
// 两条路径因此不会各记一份状态、互相覆盖。
func (a *app) toggleGroup(name string) {
	st := a.taskMgr
	if st.expanded[name] {
		delete(st.expanded, name)
		return
	}
	st.expanded[name] = true
}

// onRowDoubleClicked 处理双击一行。
//
// 双击要能收起分组：ui.Table 只在「选中发生变化」时触发 Changed()，而连点
// 两次同一行时选中并没有变化，第二次点击因此走不到 onRowChosen 里——
// 这就是「双击也收不起来」的原因。双击必须单独接一条路径。
//
// 单进程行双击开详情，与单击一致（任务管理器里双击进程行也是看详情）。
func (a *app) onRowDoubleClicked(r tmRow) {
	if r.group != nil {
		if len(r.group.Procs) == 1 {
			a.openDetail(r.group.Procs[0])
			return
		}
		a.toggleGroup(r.group.Name)
		return
	}
	if r.metric != nil {
		a.openDetail(*r.metric)
	}
}

// ensureColumnIDs 记住本帧的列序，供 cell 回调把下标还原成列的身份。
//
// ui.Table 只把列下标交给 cell，而用户可以拖动列改变顺序，所以必须
// 自己留一份「下标 -> ID」的映射。用户拖动留下的顺序优先，没提到的
// 列按默认列序补齐。
func (a *app) ensureColumnIDs() {
	st := a.taskMgr
	ids := make([]string, 0, len(tableColumnIDs))
	seen := map[string]bool{}
	for _, id := range st.list.Columns.Order {
		if seen[id] || !containsString(tableColumnIDs, id) {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, id := range tableColumnIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	st.columnIDs = ids
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// columnIDAt 把本帧的列下标还原成列的身份。
func (st *taskMgrState) columnIDAt(col int) string {
	if col >= 0 && col < len(st.columnIDs) {
		return st.columnIDs[col]
	}
	// 兜底：还没走过 ensureColumnIDs 的极早期，用默认列序。
	if col >= 0 && col < len(tableColumnIDs) {
		return tableColumnIDs[col]
	}
	return ""
}

// cell 画一行里的一格。col 是「本帧的列下标」，身份靠 columnIDAt 还原。
func (a *app) cell(c *ui.Context, t *ui.Theme, r tmRow, col int) {
	switch a.taskMgr.columnIDAt(col) {
	case colIDName:
		a.nameCell(c, t, r)
	case colIDPID:
		ui.Text(c, itoa(r.pid())).SingleLine().FontSize(fsSmall).TextColor(cMuted)
	case colIDCPU:
		cpuCell(c, t, r.cpu())
	case colIDMem:
		ui.Text(c, humanBytes(r.mem())).SingleLine().FontSize(fsSmall).TextColor(cText)
	case colIDDisk:
		ui.Text(c, humanRate(r.disk())).SingleLine().FontSize(fsSmall).TextColor(cMuted)
	}
	// 列分隔线：极浅一条，只是让眼睛在宽行里有个落点，不是边框。
	if col > 0 && !r.child {
		ui.Box(c).Width(1).MinHeight(iconSizeV).Background(cLine)
	}
}

// pid / cpu / mem / disk / exePath 把「分组行 vs 子进程行」的取值收敛到
// 一处：分组行取汇总值，子行取自己的那一条。
func (r tmRow) pid() int {
	if r.metric != nil {
		return r.metric.PID
	}
	if r.group != nil {
		return r.group.PID
	}
	return 0
}

func (r tmRow) cpu() float64 {
	if r.metric != nil {
		return r.metric.CPU
	}
	if r.group != nil {
		return r.group.CPU
	}
	return 0
}

func (r tmRow) mem() uint64 {
	if r.metric != nil {
		return r.metric.Mem
	}
	if r.group != nil {
		return r.group.Mem
	}
	return 0
}

func (r tmRow) disk() float64 {
	if r.metric != nil {
		return r.metric.Disk
	}
	if r.group != nil {
		return r.group.Disk
	}
	return 0
}

// exePath 返回这一行该用哪个可执行文件去查图标。
func (r tmRow) exePath() string {
	if r.exe != "" {
		return r.exe
	}
	if r.metric != nil {
		return r.metric.Exe
	}
	if r.group != nil {
		return r.group.Exe
	}
	return ""
}

// nameCell 画名称列：展开 chip + 进程图标 + 名字。
//
// chip 只在「同名进程不止一个」时给：单进程行给了 chip 也点不出东西，
// 看着像坏了。子行按旧行为缩进 16px，但没有 chip。
//
// 分组行的 chip 自己带点击处理，不依赖 ui.Table 的 Changed()。
// 这是「展开后收不起来」的正解：表格只在**选中发生变化**时报告一次点击，
// 而用户再点同一行时选中并没有变，Changed() 不会再触发——所以折叠不能
// 寄生在表格的选择信号上，必须由我们自己的控件接住。
func (a *app) nameCell(c *ui.Context, t *ui.Theme, r tmRow) {
	// 多进程的分组：整个名称格是一个可点目标，点一下切换展开/收起。
	// 单进程行与子行保持原样（点它们由表格的选择信号开详情）。
	//
	// 这里必须自己接住点击，不能让折叠寄生在 ui.Table 的 Changed() 上：
	// Changed() 只在「选中发生变化」时报告一次，选中之后再点同一行它就不再
	// 出声——表现就是「能展开、收不起来」。同理也不能只用双击：慢速点两下
	// 间隔超过平台的 500ms 双击窗口，第二下就丢了。
	g := r.group
	if g == nil || len(g.Procs) <= 1 {
		a.nameCellBody(c, t, r, nil, false)
		return
	}

	cell := ui.Row(c).Gap(sp1).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Label("分组 " + g.Name)
	cell.Children(func() {
		a.nameCellBody(c, t, r, g, g.expanded)
	})
	if cell.Clicked() {
		a.toggleGroup(g.Name)
	}
}

// nameCellBody 画名称格的内容：箭头槽 + 图标 + 名字。
//
// 展开 chip 单独抽出来，是因为它也要能点：用户习惯点箭头切换，而不是点整行。
// g 为 nil 表示这一行不是多进程分组（单进程行或子行），此时箭头槽留空占位，
// 子行因此自然缩进 16px、与分组行的图标列对齐。
func (a *app) nameCellBody(c *ui.Context, t *ui.Theme, r tmRow, g *tmGroup, expanded bool) {
	ui.Row(c).Gap(sp1).AlignItems(ui.Center).Children(func() {
		// 箭头槽固定 16 宽。Shrink(0) 不能省——Row 里的空盒子默认会被压成
		// 0 宽，缩进就没了。
		slot := ui.Box(c).Size(chipSlot, chipSlot).Shrink(0).
			AlignItems(ui.Center).Justify(ui.Center)
		if g != nil {
			slot.Cursor(ui.CursorPointer).Tooltip("展开/收起")
			slot.Children(func() {
				drawExpandChip(c, expanded)
			})
			// chip 与整格共用 toggleGroup，状态只有 st.expanded 一份。
			// 两者在同一帧都命中时会切换两次（等于没切），所以 chip 命中时
			// 就把事件吃掉，不让它冒泡到整格。
			if slot.Clicked() {
				a.toggleGroup(g.Name)
			}
		}
		ui.Box(c).Size(iconSizeV, iconSizeV).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			a.processIcon(c, r.exePath(), t)
		})
		ui.Text(c, r.displayName()).SingleLine().FontSize(fsBody).FontWeight(r.weight()).TextColor(cText)
	})
}

// displayName 是名字列上的文案：分组行带进程个数，子行只是自己的名字。
func (r tmRow) displayName() string {
	if r.group != nil && len(r.group.Procs) > 1 {
		return r.group.Name + " (" + itoa(len(r.group.Procs)) + ")"
	}
	return r.name
}

// weight 让分组行比子行重一点，扫一眼就知道谁是谁的父。
func (r tmRow) weight() int {
	if r.group != nil {
		return 500
	}
	return 400
}

// drawExpandChip 画 16x16 的展开箭头 chip，悬停时给一层 cHover 底色。
//
// 形状用 Path 画，不引入图标资源。两个方向都画成「实心的小三角」而不是
// 折线箭头：16px 见方里描边折线会糊成一团，实心三角在 1x 下也认得出。
// 收起时朝右、展开时朝下，是桌面应用里通行的语义。
func drawExpandChip(c *ui.Context, expanded bool) {
	ui.Box(c).Size(chipSlot, chipSlot).Shrink(0).Radius(rSm).Children(func() {
		chip := ui.Box(c).Fill()
		chip.Draw(func(p *ui.Painter, r ui.Rect) {
			if chip.Hovered() {
				p.Fill(r, cHover, rSm)
			}
			// 实心三角的底边到顶点的距离占盒子的一半，四周各留 1/4，
			// 于是它看起来是个 8px 的小箭头，不会顶满 16px 的槽。
			var path ui.Path
			switch {
			case expanded:
				path.MoveTo(r.X+r.W*0.25, r.Y+r.H*0.36).
					LineTo(r.X+r.W*0.75, r.Y+r.H*0.36).
					LineTo(r.X+r.W/2, r.Y+r.H*0.66).
					Close()
			default:
				path.MoveTo(r.X+r.W*0.38, r.Y+r.H*0.26).
					LineTo(r.X+r.W*0.66, r.Y+r.H/2).
					LineTo(r.X+r.W*0.38, r.Y+r.H*0.74).
					Close()
			}
			p.FillPath(&path, cMuted)
		})
	})
}

// processIcon 画进程图标。取不到图标时退回一个浅色占位方块，不留空洞——
// 一列空洞会让整张表看起来像加载失败。
//
// 占位块比真图标小一圈（画布仍是 iconSizeV，块本身缩到 12）：真图标自带
// 留白，占位块若顶满格子会显得比旁边的真图标重，整列看着毛毛的。
func (a *app) processIcon(c *ui.Context, exe string, _ *ui.Theme) {
	if a.icons != nil {
		if b := a.icons.get(exe); b != nil {
			ui.Image(c, b).Size(iconSizeV, iconSizeV).Fit(ui.Contain)
			return
		}
	}
	const ph float32 = 12
	ui.Box(c).Size(ph, ph).Radius(rSm-2).Background(cBGSunken).Border(1, cLine)
}

// cpuCell 画 CPU 这一格：左边固定宽右对齐的数值，右边一条带轨道的迷你条。
//
// 轨道必须画，而且必须先于填充存在：空值行只有轨道，看上去是一条「归零的
// 进度条」；没有轨道那一行就是一格空白，像掉了一块。
func cpuCell(c *ui.Context, t *ui.Theme, v float64) {
	frac := v / 100
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	numColor := cText
	if v < 0.05 {
		numColor = cMuted
	}
	ui.Row(c).Gap(cellGap).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmtPercent(v)).SingleLine().Width(cpuNumW).FontSize(fsSmall).
			FontWeight(500).TextColor(numColor)
		// 轨道：先铺一条浅灰底，空值行因此仍然是一条「归零的进度条」。
		// 填充在 DrawOver 里叠上去，宽度按 frac 算，圆角与轨道一致。
		ui.Box(c).Width(barW).Height(barH).Shrink(0).Radius(rSm).Background(cLine).
			DrawOver(func(p *ui.Painter, r ui.Rect) {
				if frac <= 0 {
					return
				}
				w := barW * float32(frac)
				// 填充太窄时圆角会把它抹成看不见的一点，兜到 barH。
				if w < barH {
					w = barH
				}
				p.Fill(ui.Rect{X: r.X, Y: r.Y, W: w, H: r.H}, cpuColor(t, v), rSm)
			})
	})
}

// cpuColor 按负载分档给迷你条上色。
//
// 颜色取自 tokens.go 的语义色，跨整窗稳定：轻负载是强调蓝，中负载转琥珀，
// 重负载转危险红。以前那版是随手算出来的，同一个进程两帧之间会在蓝/橙/红
// 之间跳，看着像故障而不是数据。
//
// 最低一档也用 cAccent，不用 cLineStrong：轨道本身就是 cLineStrong 一族的
// 浅灰，填充再用浅灰就跟轨道糊在一起，0.1% 的行看起来像「条没了」。
// 用蓝色则无论多细都读得出来是一段真实的占用。
func cpuColor(t *ui.Theme, v float64) ui.Color {
	_ = t
	switch {
	case v >= 70:
		return cDanger
	case v >= 35:
		return cWarning
	default:
		return cAccent
	}
}

// fmtPercent 把 CPU 百分比写成固定一位小数：宽度稳定，右对齐才齐。
func fmtPercent(v float64) string {
	if v < 0 {
		v = 0
	}
	if v >= 100 {
		return "100.0%"
	}
	whole := int(v)
	dec := int((v-float64(whole))*10 + 0.5)
	if dec >= 10 {
		whole++
		dec = 0
	}
	return itoa(whole) + "." + itoa(dec) + "%"
}

// emptyProcesses 是表格位置上的未加载 / 失败态：居中的图标 + 主文案 +
// 次要说明。跟以前 Padding(16) 贴左上角的那一行字完全不同。
func (a *app) emptyProcesses(c *ui.Context, t *ui.Theme) {
	st := a.taskMgr
	iconColor := cMuted
	title := "正在读取进程…"
	hint := "第一次采样通常不到一秒"
	if st.loadErr != "" {
		iconColor = cDanger
		title = "读取进程失败：" + st.loadErr
		hint = "确认当前账户有权读取进程信息后重试"
	}
	ui.Column(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Gap(sp2).
		Label("进程表空态").Children(func() {
		ui.Box(c).Size(40, 40).Radius(rLg).Background(cBGSunken).
			AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Icon(c, tbEmptyIcon).Size(iconSizeV+4, iconSizeV+4).TextColor(iconColor)
		})
		ui.Text(c, title).SingleLine().FontSize(fsBody).FontWeight(500).TextColor(cText)
		ui.Text(c, hint).SingleLine().FontSize(fsSmall).TextColor(cMuted)
	})
}

// emptyTable 是空的表格：表头还在，正文位置给一条居中的提示。
//
// 「没有匹配的进程」这一支必须真的建表——测试靠表格的行元素定位，
// 换成空态就没有可点的东西了；未加载与失败才走整块空态。
func (a *app) emptyTable(c *ui.Context, t *ui.Theme) {
	st := a.taskMgr
	if st.loaded && st.loadErr == "" {
		st.list.Selected = nil
		st.columnIDs = tableColumnIDs
		a.table(c, t)
		ui.Text(c, "没有匹配的进程").SingleLine().FontSize(fsSmall).TextColor(cMuted)
		return
	}
	a.emptyProcesses(c, t)
}
