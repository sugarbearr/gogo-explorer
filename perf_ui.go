// 底部性能区：整机概要 + 四张指标卡 + 每核网格。
//
// 这一片是「性能」切片，只负责把 taskMgrState 里的采样结果画出来：
//
//	performanceStrip  标题行（可点折叠）+ 展开态的四张卡与每核网格
//	coreGrid          每核 CPU 的小格子网格
//	statCard          单张指标卡：色点标题 + 大数字 + 副标题 + 曲线
//	sparkline         卡片底部的历史曲线
//
// 视觉上刻意做三个层次：整块底 cBGSubtle、卡片面 cSurface + 1px cLine 描边 +
// 一层极浅阴影。原来性能区上下各一条 1px 硬边框，看起来是「表格被切了一块」；
// 现在只用顶部一条 cLine 细分隔线，配合更浅的底色，读起来是一个独立分区。
package main

import (
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"
)

// pfCardGap 是卡片之间的水平间距。
const pfCardGap = sp3

// pfSparkH 是卡片里曲线区的高度。低于 28 就看不出趋势，只剩一排点。
const pfSparkH float32 = 32

// pfCardH 是四张指标卡的统一高度：标题行 + 大数字 + 副标题 + 曲线。
// 固定高度让四张卡的主数字基线齐平——不齐的卡片一眼就「没做设计」。
const pfCardH float32 = 132

// pfLegendDot 是标题行图例色点的直径，pfCardDot 是卡片标题色点的直径。
const (
	pfLegendDot float32 = 6
	pfCardDot   float32 = 8
)

// pfCoreCellH 是每核格子的高度。定高让格子读起来是一颗颗离散的小片，
// 被行高拉伸的长条则会糊成一整块色带。
const pfCoreCellH float32 = 10

// pfCoreCellW 是每核格子的宽度。
//
// 格子必须定宽（而不是 Grow 撑满列）：卡片有 150~230px 宽，让格子去分
// 列宽会得到 40~60px 宽、10px 高的长条，横看是一排色带。定成 14×10
// 的小片 + 4px 间隙，才读得出「一颗一颗的核」（实测 60×10 就是色带）。
const pfCoreCellW float32 = 14

// pfCoreMaxRows 是每核网格的可用行数。
//
// 卡片高度是固定的（pfCardH），标题行 + 内边距吃掉一部分，剩下的高度
// 最多放得下 5 行「格子 + 间隙」。核数多于 5×列数时只画得下的那些——
// 硬塞会把最后一行压成 2px 的碎片（实测过），比少画几个更难看。
const pfCoreMaxRows = 5

// pfCoreCols 按核数选出列数，让行数落在 pfCoreMaxRows 以内。
//
// 列数随核数变化，是为了让格子保持接近正方：28 核用 6 列（5 行），
// 8 核用 4 列（2 行）。固定 4 列会让 28 核变成 7 行细长条，既放不下
// 又完全不像「格子」。
func pfCoreCols(n int) int {
	if n <= 4 {
		return 2
	}
	cols := (n + pfCoreMaxRows - 1) / pfCoreMaxRows
	if cols < 4 {
		cols = 4
	}
	return cols
}

// performanceStrip 画底部的性能区。整块常驻在表格下方，点标题行折叠/展开。
//
// 高度由 st.perfHeight() 定：折叠 34、展开 200。这里不自己算高度，
// 否则会和 perfHeight 的测试打架。
func (a *app) performanceStrip(c *ui.Context, t *ui.Theme) {
	st := a.taskMgr

	ui.Column(c).WidthPercent(100).Height(st.perfHeight()).Background(cBGSubtle).Shrink(0).Children(func() {
		// 顶部只用一条细分隔线把性能区和表格区分开：上面的表格是白底，
		// 下面这块是 cBGSubtle，靠一条 cLine + 一点色差分层，不用双边框。
		ui.Box(c).WidthPercent(100).Height(1).Shrink(0).Background(cLine)
		a.perfHeader(c, t)
		if !st.perfOpen {
			return
		}
		a.perfBody(c, t)
	})
}

// perfHeader 画「▾ 性能 ...」这一行。
//
// 整行可点：测试用 tt.Click("性能") 触发折叠，所以「性能」这几个字必须
// 在可点元素内部，且点击区域要覆盖整行而不是只有箭头。
func (a *app) perfHeader(c *ui.Context, t *ui.Theme) {
	st := a.taskMgr

	caret := "▾"
	if !st.perfOpen {
		caret = "▸"
	}

	ui.Row(c).WidthPercent(100).Height(34).Shrink(0).Padding(0, sp4).
		AlignItems(ui.Center).Gap(sp2).
		OnClick(func() { st.perfOpen = !st.perfOpen }).
		Children(func() {
			ui.Text(c, caret).FontSize(fsCaption).TextColor(cMuted)
			// 「性能」是这块的锚点文案，测试与用户都靠它定位。
			ui.Text(c, "性能").FontSize(fsBody).Bold().TextColor(cText)

			// 中间留白，把概要/图例推到右边。
			ui.Box(c).Grow(1)

			a.perfLegend(c, t)
		})
}

// perfLegend 画标题行右侧的四个概要项：色点 + 数值。展开态与折叠态共用。
//
// 原来这里是一串裸的彩色文字（"CPU 31%" 直接上蓝字），颜色即数据、
// 文字也是数据，读起来分不清哪个是标签。改成「色点 + 文案」后，
// 色点只负责指示颜色语义，文字统一用 cText。
//
// 折叠态必须保留 "CPU 31%" 这种文案（测试断言 HasText("CPU 31%")），
// 格式严格是 fmt.Sprintf("CPU %.0f%%", st.sys.CPUPercent)，一个字都不能变。
// 展开态和折叠态用同一段代码，就不会出现「改了展开态、折叠态忘了改」。
func (a *app) perfLegend(c *ui.Context, t *ui.Theme) {
	sys := a.taskMgr.sys
	pfLegendItem(c, t, cCPU, fmt.Sprintf("CPU %.0f%%", sys.CPUPercent))
	pfLegendItem(c, t, cMem, fmt.Sprintf("内存 %.0f%%", percent(sys.MemUsed, sys.MemTotal)))
	pfLegendItem(c, t, cDisk, fmt.Sprintf("磁盘 %s", humanBytes(sys.DiskFree)))
	pfLegendItem(c, t, cNet, fmt.Sprintf("网络 %s", humanRate(sys.NetRx+sys.NetTx)))
}

// pfLegendItem 画一个「色点 + 文案」的图例项。
func pfLegendItem(c *ui.Context, t *ui.Theme, dot ui.Color, label string) {
	ui.Row(c).AlignItems(ui.Center).Gap(sp1).Shrink(0).Children(func() {
		pfDot(c, dot, pfLegendDot)
		ui.Text(c, label).FontSize(fsSmall).TextColor(cText).SingleLine()
	})
}

// pfDot 画一个直径 d 的实心圆点：把圆角拉满就是一个圆。
func pfDot(c *ui.Context, col ui.Color, d float32) {
	ui.Box(c).Width(d).Height(d).Radius(d / 2).Background(col).Shrink(0)
}

// perfBody 画展开态的主体：四张指标卡 + 每核网格。
func (a *app) perfBody(c *ui.Context, t *ui.Theme) {
	st := a.taskMgr
	sys := st.sys

	ui.Row(c).WidthPercent(100).Grow(1).Padding(0, sp4, sp4).Gap(pfCardGap).
		AlignItems(ui.Stretch).Children(func() {
		a.statCard(c, t, "CPU",
			fmt.Sprintf("%.0f%%", sys.CPUPercent),
			fmt.Sprintf("%d 个逻辑处理器", numCPU()),
			cCPU, st.cpuHist, 100)
		a.statCard(c, t, "内存",
			fmt.Sprintf("%.0f%%", percent(sys.MemUsed, sys.MemTotal)),
			fmt.Sprintf("%s / %s", humanBytes(sys.MemUsed), humanBytes(sys.MemTotal)),
			cMem, st.memHist, 100)
		a.statCard(c, t, "磁盘",
			humanBytes(sys.DiskFree),
			fmt.Sprintf("可用 %s / 共 %s", humanBytes(sys.DiskFree), humanBytes(sys.DiskTotal)),
			cDisk, nil, 1)
		a.statCard(c, t, "网络",
			humanRate(sys.NetRx+sys.NetTx),
			fmt.Sprintf("↓ %s  ↑ %s", humanRate(sys.NetRx), humanRate(sys.NetTx)),
			cNet, st.netHist, 0)
		a.coreGrid(c, t, st.cores)
	})
}

// statCard 画一张指标卡。主次是这张卡的全部意义：
// 标题 fsSmall/cMuted、主数字 fsMetric/Bold、副标题 fsCaption/cMuted。
// 原来四张卡的数字全是 24px 同色，视线没有落点。
//
// maxValue 为 0 表示曲线自适应峰值（网络这种没有理论上限的指标用）。
func (a *app) statCard(c *ui.Context, t *ui.Theme, title, value, sub string, accent ui.Color, hist []float64, maxValue float64) {
	ui.Column(c).Grow(1).Basis(0).Height(pfCardH).Shrink(0).
		Background(cSurface).Radius(rLg).Border(1, cLine).
		Shadow(0, 1, 3, 0, shadowCard).
		Padding(sp3).
		Children(func() {
			// 标题行：色点 + 标题。色点把这张卡和曲线、图例的颜色语义绑在一起。
			ui.Row(c).WidthPercent(100).AlignItems(ui.Center).Gap(sp1).Shrink(0).Children(func() {
				pfDot(c, accent, pfCardDot)
				ui.Text(c, title).FontSize(fsSmall).TextColor(cMuted).SingleLine()
			})

			// 主数字：整张卡唯一的重心。
			ui.Text(c, value).FontSize(fsMetric).Bold().TextColor(cText).
				SingleLine().Shrink(0).PaddingY(sp1)

			ui.Text(c, sub).FontSize(fsCaption).TextColor(cMuted).
				SingleLine().Shrink(0)

			// 弹性留白：让曲线永远贴在卡片底部，四张卡的基线也齐平。
			ui.Box(c).Grow(1)

			sparkline(c, t, hist, accent, maxValue)
		})
}

// pfSparkBars 是一条曲线最多画多少条柱子。
//
// 这是「干净」的关键：历史有 60 个点，卡片可用宽度只有 150px 上下，
// 全画出来每根不到 2.5px，看着就是一片噪点。降采样到 28 条以后
// 每根有 5px 上下，加上 Gap 才有「柱状趋势」的读感，而不是糊成一条带子。
const pfSparkBars = 28

// sparkline 画卡片底部的历史曲线。
//
// 原来是每点 1px 宽的竖条、紧紧挨着，看起来像一片噪点而不是曲线。
// 现在：先降采样到 pfSparkBars 条，每条 Grow(1) 配 Gap(1) 留出呼吸；
// 高度按统一的峰值算百分比，底部压一条 cLine 基线，
// 让曲线「站在」一条线上而不是浮在卡里。
//
// maxValue 为 0 时按数据峰值自适应。没有数据时不画任何东西，
// 留白比画一条假的线诚实。
func sparkline(c *ui.Context, t *ui.Theme, data []float64, accent ui.Color, maxValue float64) {
	bars := pfDownsample(data, pfSparkBars)
	if len(bars) == 0 {
		return
	}

	peak := maxValue
	if peak <= 0 {
		for _, v := range bars {
			if v > peak {
				peak = v
			}
		}
	}
	if peak <= 0 {
		// 有数据但全是 0：一条贴底的线，表示「有过采样，值为零」。
		ui.Column(c).WidthPercent(100).Height(pfSparkH).Shrink(0).Justify(ui.End).Children(func() {
			ui.Box(c).WidthPercent(100).Height(1).Shrink(0).Background(cLine)
		})
		return
	}

	ui.Column(c).WidthPercent(100).Height(pfSparkH).Shrink(0).
		Justify(ui.End).Children(func() {
		ui.Row(c).WidthPercent(100).Grow(1).Gap(1).AlignItems(ui.End).Children(func() {
			for _, v := range bars {
				h := float32(v/peak) * 100
				if h < 6 {
					// 再小的值也留 6% 高度：完全贴底的点会被基线吞掉，
					// 曲线看起来像断了一段。
					h = 6
				}
				if h > 100 {
					h = 100
				}
				ui.Box(c).Grow(1).Basis(0).MinWidth(2).HeightPercent(h).
					Radius(1).Background(accent.Alpha(0.85))
			}
		})
		// 基线：让曲线有一条明确的落脚线。
		ui.Box(c).WidthPercent(100).Height(1).Shrink(0).Background(cLine)
	})
}

// pfDownsample 把一段历史按「每桶取最大值」压到最多 n 个点。
//
// 取最大值而不是取平均：曲线是用来发现尖峰的，平均会把尖峰抹平，
// 而任务管理器里恰恰是那一下尖峰最值得看。
func pfDownsample(data []float64, n int) []float64 {
	if n <= 0 || len(data) == 0 {
		return nil
	}
	if len(data) <= n {
		return data
	}
	out := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		lo := i * len(data) / n
		hi := (i + 1) * len(data) / n
		if hi <= lo {
			hi = lo + 1
		}
		peak := data[lo]
		for _, v := range data[lo:hi] {
			if v > peak {
				peak = v
			}
		}
		out = append(out, peak)
	}
	return out
}

// coreGrid 画每核 CPU：一个 rLg 卡片，里面是等宽的小格子。
//
// 格子高度 10、圆角 3、Gap 4；空格子用 cLine 打底，让「闲着的核」退进背景，
// 忙的核自己跳出来，整片网格不会花。
func (a *app) coreGrid(c *ui.Context, t *ui.Theme, cores []float64) {
	ui.Column(c).Grow(1).Basis(0).Height(pfCardH).Shrink(0).
		Background(cSurface).Radius(rLg).Border(1, cLine).
		Shadow(0, 1, 3, 0, shadowCard).
		Padding(sp3).
		Children(func() {
			ui.Row(c).WidthPercent(100).AlignItems(ui.Center).Gap(sp1).Shrink(0).Children(func() {
				pfDot(c, cCPU, pfCardDot)
				ui.Text(c, "CPU 每核").FontSize(fsSmall).TextColor(cMuted).SingleLine()
				ui.Box(c).Grow(1)
				ui.Text(c, pfCoreSummary(cores)).FontSize(fsCaption).TextColor(cMuted).SingleLine()
			})

			if len(cores) == 0 {
				ui.Box(c).Grow(1)
				return
			}

			// 格子的宽高都定死（pfCoreCellW × pfCoreCellH），行与行、列与列
			// 之间留 sp1 的间隙。
			//
			// 之前两个坑都踩过：用 Grow 分行高会把格子拉成长横条，用 Grow 分
			// 列宽会得到 60px×10px 的色带；两者都让网格糊成一整块。定尺寸 +
			// Clip 之后，格子多的机型只是被截掉多余的行，不会被压扁。
			cols := pfCoreCols(len(cores))
			rows := (len(cores) + cols - 1) / cols
			if rows > pfCoreMaxRows {
				rows = pfCoreMaxRows
			}
			// 行高用 Grow 把网格铺满卡片剩余高度，格子本身再按这个行高
			// 撑满——这样网格不会缩在标题下面留一大片空白，
			// 也不会因为行高被拉长而变成色带（宽度仍是定值 pfCoreCellW）。
			ui.Column(c).WidthPercent(100).Grow(1).Gap(sp1).Justify(ui.Start).
				Clip().Children(func() {
				for r := 0; r < rows; r++ {
					ui.Row(c).WidthPercent(100).Grow(1).Gap(sp1).Children(func() {
						for i := 0; i < cols; i++ {
							idx := r*cols + i
							if idx >= len(cores) {
								continue
							}
							ui.Box(c).Width(pfCoreCellW).HeightPercent(100).Shrink(0).
								Radius(3).Background(pfCoreFill(cores[idx]))
						}
					})
				}
			})
		})
}

// pfCoreFill 给出某个核的格子底色。
//
// 0 占用也要有一个可见但安静的底色（cLine），否则空格子会变成一个洞。
func pfCoreFill(v float64) ui.Color {
	if v <= 0.5 {
		return cLine
	}
	return coreColor(theme, v)
}

// pfCoreSummary 是网格标题行右侧的「N 核 均 X%」。
func pfCoreSummary(cores []float64) string {
	if len(cores) == 0 {
		return "无数据"
	}
	sum := 0.0
	for _, v := range cores {
		sum += v
	}
	return fmt.Sprintf("%d 核 均 %.0f%%", len(cores), sum/float64(len(cores)))
}

// coreColor 按占用率给每核格子选色。
//
// 三档而不是连续渐变：连续渐变的网格像热力图，反而看不出「哪个核满了」。
// 低负载统一走 CPU 的蓝色语义，只是随占用加深；50% 起转警告黄，
// 80% 起转危险红。
func coreColor(t *ui.Theme, v float64) ui.Color {
	switch {
	case v >= 80:
		return cDanger
	case v >= 50:
		return cWarning
	default:
		return cCPU.Alpha(float32(0.45 + math.Min(v, 50)/50*0.4))
	}
}
