// 右侧详情面板：选中进程后弹出，上半是进程事实，下半是「为什么在跑」的
// 启动链（witr 溯源）。
//
// 这一片从 taskmgr.go 搬出来并现代化。数据逻辑一字未改（tracePending 的
// 过期丢弃、traceCache 的复用都在原样保留），改的全是排版：
//   - 面板本身是一张浮层卡片：圆角 rXl、描边 cLine、shadowFloats 阴影，
//     靠阴影而不是靠重色块把它从表格上「抬」起来。
//   - 指标从「标签 + 值」两列改成三张并排小卡（CPU / 内存 / 磁盘），
//     标签弱、数值强且各自带指标色，扫一眼就知道哪个高。
//   - 启动链的圆点与连线保留，但拉开间距、加大末节点圆点，让「链条」
//     读起来是一条链而不是一坨文字。
//
// 视觉令牌全部来自 tokens.go，本文件不写裸色值。
package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// dtPanelW 是详情面板的宽度。原来的 360 放三张指标卡偏挤，加到 380。
const dtPanelW float32 = 380

// dtIconBox 是头部进程图标的显示边长（原 28，加大到 36 更醒目）。
const dtIconBox float32 = 36

// openDetail 打开某个进程的详情。
//
// 同一个 PID 重复点不变、也不收起——收起交给关闭按钮，否则在表格里
// 点同一行会把详情闪掉。换 PID 才清空旧结果并发起新的溯源。
func (a *app) openDetail(m procMetric) {
	st := a.taskMgr
	if st.detailPID == m.PID {
		return
	}
	st.detailPID = m.PID
	st.detailName = m.Name
	st.detailExe = m.Exe
	st.detail = nil
	st.traceErr = ""
	st.cands = nil
	a.runTrace(m.PID)
}

// closeDetail 收起详情面板并清掉它的全部状态。
func (a *app) closeDetail() {
	st := a.taskMgr
	st.detailPID = 0
	st.detail = nil
	st.traceErr = ""
	st.cands = nil
}

// runTrace 在后台跑一次 witr 溯源，结果经 post 回主线程。
//
// 过期结果靠 tracePending 丢弃：用户可能在结果回来之前就换了进程，
// 那时 tracePending 已经变成新目标的标签，旧结果走到回调里对不上，
// 直接扔掉，避免旧进程的启动链盖在新进程的详情上。
func (a *app) runTrace(pid int) {
	st := a.taskMgr
	label := fmt.Sprintf("PID %d", pid)

	// 命中缓存就直接用，不再起子进程。
	if r, ok := st.traceCache[label]; ok {
		st.detail = r
		return
	}

	st.tracePending = label
	go func() {
		res, err := runWitr(witrTarget{kind: witrByPID, value: itoa(pid)}, witrTimeout)
		var le *witrLookupError
		hasLookup := asLookup(err, &le)

		a.post(func() {
			// 过期丢弃：等待的不是这个目标就什么都不做。
			if st.tracePending != label {
				return
			}
			st.tracePending = ""
			if err != nil {
				if hasLookup {
					st.traceErr = le.message
					st.cands = le.candidates
					return
				}
				st.traceErr = err.Error()
				return
			}
			st.traceErr = ""
			st.cands = nil
			st.detail = res
			st.traceCache[label] = res
		})
	}()
}

// detailPanel 画右侧详情面板。
//
// 面板贴在窗口右缘，用 Padding 把它从边上收进来一点，再整体加圆角与阴影，
// 这样它不是「一条贴边的帘子」，而是一张浮在内容之上的卡片。
func (a *app) detailPanel(c *ui.Context, t *ui.Theme) {
	st := a.taskMgr
	if st.detailPID == 0 {
		return
	}

	// 头部要用的当前指标：进程可能已经退出，取不到就退回空指标。
	m, _ := st.metricFor(st.detailPID)
	if m.PID == 0 {
		m = procMetric{PID: st.detailPID, Name: st.detailName, Exe: st.detailExe}
	}
	if m.Name == "" {
		m.Name = st.detailName
	}
	if m.Exe == "" {
		m.Exe = st.detailExe
	}

	// 注意用 FillHeight 而不是 Fill：Fill 会把宽度也设成 100%，
	// 把 Width(dtPanelW) 覆盖掉，面板就会横着铺满整个窗口。
	ui.Column(c).
		Key("dt-panel").
		Width(dtPanelW).
		FillHeight().
		Padding(sp3).
		Children(func() {
			ui.Column(c).
				Key("dt-card").
				Grow(1).
				Gap(sp3).
				Padding(sp4).
				Background(cSurface).
				Radius(rXl).
				Border(1, cLine).
				Shadow(0, 8, 24, -4, shadowFloats).
				Clip().
				Children(func() {
					a.dtHeader(c, t, m)
					// 头部与正文之间一条极轻的分隔线。
					ui.Box(c).Height(1).Background(cLine)
					a.dtScrollBody(c, t, m)
				})
		})
}

// dtHeader 画进程图标、名字、PID 与关闭按钮。
func (a *app) dtHeader(c *ui.Context, t *ui.Theme, m procMetric) {
	ui.Row(c).
		Key("dt-head").
		Gap(sp3).
		AlignItems(ui.Center).
		Children(func() {
			a.dtAppIcon(c, m)
			ui.Column(c).
				Grow(1).
				Gap(1).
				Children(func() {
					ui.Text(c, m.Name).
						FontSize(fsTitle).
						Bold().
						TextColor(cText).
						SingleLine()
					ui.Textf(c, "PID %d", m.PID).
						FontSize(fsCaption).
						TextColor(cMuted).
						SingleLine()
				})
			a.dtCloseButton(c, t)
		})
}

// dtAppIcon 画进程图标：取不到可执行文件图标时退回一个通用的方块，
// 保证头部左侧永远有个锚点，不会因为图标缺失而塌陷。
func (a *app) dtAppIcon(c *ui.Context, m procMetric) {
	// 用 getDetail：这张位图按 36px 栅格化，与下面的显示尺寸一致。
	// 拿 get 的 20px 位图来填 36px 的框是放大一张小图，边缘会糊。
	if b := a.icons.getDetail(m.Exe); b != nil {
		ui.Image(c, b).Width(dtIconBox).Height(dtIconBox)
		return
	}
	ui.Box(c).
		Width(dtIconBox).
		Height(dtIconBox).
		Background(cBGSunken).
		Radius(rMd).
		Children(func() {
			ui.Icon(c, monitorIcon).
				Width(dtIconBox - sp2*2).
				Height(dtIconBox - sp2*2).
				TextColor(cMuted)
		})
}

// dtCloseButton 是面板右上角的关闭按钮。
//
// iconButton 是 shell.go 提供的既有契约，签名固定为
// iconButton(c, t, svg, tip) ui.Element —— 它只负责把元素造出来，
// 点击语义要在返回的元素上用 Clicked() 查询，不能传回调。
func (a *app) dtCloseButton(c *ui.Context, t *ui.Theme) {
	if iconButton(c, t, closeIcon, "关闭详情").Clicked() {
		a.closeDetail()
	}
}

// dtScrollBody 是面板的正文区：事实卡 + 溯源链。内容可能比面板高，
// 所以做成可滚动的，长启动链不会把面板撑破。
func (a *app) dtScrollBody(c *ui.Context, t *ui.Theme, m procMetric) {
	ui.Scroll(c).
		Key("dt-body").
		Grow(1).
		Children(func() {
			ui.Column(c).
				Gap(sp4).
				Children(func() {
					a.detailFacts(c, t, m)
					a.traceSection(c, t)
				})
		})
}

// detailFacts 画三张并排的指标小卡：CPU / 内存 / 磁盘。
//
// 原来是一张「标签 + 值」的两列表格，标签和值一样重，扫视时抓不到重点。
// 改成卡片后：标签小且灰，数值大且带该指标自己的颜色，横向对比一眼可见。
func (a *app) detailFacts(c *ui.Context, t *ui.Theme, m procMetric) {
	ui.Column(c).
		Key("dt-facts").
		Gap(sp3).
		Children(func() {
			ui.Row(c).
				Gap(sp2).
				Children(func() {
					a.dtMetricCard(c, "CPU", dtPercent(m.CPU), colorCPU)
					a.dtMetricCard(c, "内存", humanBytes(m.Mem), colorMem)
					a.dtMetricCard(c, "磁盘", humanRate(m.Disk), colorDisk)
				})
			// 路径单独一行，超长截断，不让它把面板撑破。
			ui.Text(c, dtExeLine(m)).
				FontSize(fsCaption).
				TextColor(cMuted).
				SingleLine()
		})
}

// dtMetricCard 是一张指标小卡：上标签、下数值。
func (a *app) dtMetricCard(c *ui.Context, label, value string, color ui.Color) {
	ui.Column(c).
		Grow(1).
		Gap(sp1).
		Padding(sp3, sp3).
		Background(cBGSubtle).
		Radius(rLg).
		Children(func() {
			ui.Text(c, label).
				FontSize(fsCaption).
				TextColor(cMuted).
				SingleLine()
			ui.Text(c, value).
				FontSize(fsTitle).
				Bold().
				TextColor(color).
				SingleLine()
		})
}

// dtPercent 把进程 CPU 百分比写成一位小数。进程 CPU 通常很小，
// 整数会把它压成一片 0%。
func dtPercent(v float64) string {
	return fmt.Sprintf("%.1f%%", v)
}

// dtExeLine 拼出「路径」那一行的文案。
func dtExeLine(m procMetric) string {
	if m.Exe == "" {
		return "路径 —"
	}
	return "路径 " + m.Exe
}

// traceSection 画「为什么在跑」：witr 溯源出来的启动链。
func (a *app) traceSection(c *ui.Context, t *ui.Theme) {
	st := a.taskMgr

	ui.Column(c).
		Key("dt-trace").
		Gap(sp2).
		Children(func() {
			// 块标题：小、灰，和上面的内容区分开，但不用分割线抢戏。
			ui.Text(c, "为什么在跑").
				FontSize(fsSmall).
				TextColor(cMuted).
				SingleLine()

			// 加载中：还没有结果、也没有错误，说明查询还在路上。
			if st.tracePending != "" && st.detail == nil && st.traceErr == "" {
				ui.Text(c, "正在溯源…").
					FontSize(fsCaption).
					TextColor(cMuted)
				return
			}

			// 失败态：候选列表可点，点了直接对该 PID 重新溯源。
			if st.traceErr != "" {
				a.dtTraceError(c, t)
				return
			}

			if st.detail == nil {
				ui.Text(c, "—").
					FontSize(fsCaption).
					TextColor(cMuted)
				return
			}

			a.dtAncestry(c, t, st.detail)
			a.dtWarnings(c, t, st.detail)
			a.dtSourceChip(c, t, st.detail)
		})
}

// dtAncestry 画启动链：每个节点一个圆点 + 一条连线，末节点是重点。
func (a *app) dtAncestry(c *ui.Context, t *ui.Theme, r *witrResult) {
	chain := r.Ancestry
	if len(chain) == 0 {
		// 没有启动链时退回进程自身，总比空白强。
		chain = []witrProcess{r.Process}
	}
	// 只在有真实数据时才画；连 Process 都是空的就显示占位。
	if len(chain) == 1 && chain[0].PID == 0 && chain[0].Display() == "" {
		ui.Text(c, "—").FontSize(fsCaption).TextColor(cMuted)
		return
	}

	ui.Column(c).
		Key("dt-chain").
		Gap(sp1).
		Children(func() {
			for i, p := range chain {
				a.dtChainNode(c, t, p, i == len(chain)-1)
			}
		})
}

// dtChainNode 是启动链上的一个节点。
//
// 圆点用一列固定宽的 Box 画，连线也在这一列里，这样名字与 PID 永远和
// 圆点左对齐，不会因为字号差一点就错位。last 表示这是链条的终点
// （也就是用户选中的那个进程），用 accent 色 + 更大的圆点强调。
func (a *app) dtChainNode(c *ui.Context, t *ui.Theme, p witrProcess, last bool) {
	dot := sp1 + 2 // 6
	if last {
		dot = sp2 // 8
	}
	dotColor := cLineStrong
	if last {
		dotColor = cAccent
	}

	// 左侧一列是轨道：一条贯穿整个节点的竖线 + 一个压在它上面的圆点。
	//
	// 竖线用 DrawOver 直接画在整列上，所以它一定从这一行的高处连到低处，
	// 相邻节点的竖线自然接成一条不断的链，不会出现「一截一截的刻度」。
	// 圆点则用一个等宽的菱形节点居中压住，制造「节点长在链上」的观感。
	ui.Row(c).
		Gap(sp3).
		AlignItems(ui.Start).
		Children(func() {
			// 轨道列：Draw 先画贯穿的竖线（在子元素之下），圆点再压在上面。
			ui.Box(c).
				Width(dot).
				Height(sp2 * 3).
				AlignItems(ui.Center).
				Draw(func(p *ui.Painter, r ui.Rect) {
					// 竖线画在圆点正中。末端节点（用户选中的那个进程）只画
					// 圆点之上的一段，链条到此为止，不再往下拖一条尾巴。
					x := r.X + r.W/2 - 0.75
					y, h := r.Y, r.H+sp1
					if last {
						y, h = r.Y, r.H/2
					}
					p.Fill(ui.Rect{X: x, Y: y, W: 1.5, H: h}, cLine, 0)
				}).
				Children(func() {
					ui.Box(c).
						Width(dot).
						Height(dot).
						Radius(dot / 2).
						Background(dotColor)
				})
			ui.Column(c).
				Grow(1).
				Gap(1).
				Children(func() {
					command := p.Display()
					if strings.TrimSpace(command) == "" {
						command = "—"
					}
					ui.Text(c, command).
						FontSize(fsSmall).
						TextColor(cText).
						SingleLine()
					ui.Text(c, dtNodeSub(p)).
						FontSize(fsCaption).
						TextColor(cMuted).
						SingleLine()
				})
		})
}

// dtNodeSub 拼出节点下面那行次要信息：PID 与用户。
func dtNodeSub(p witrProcess) string {
	parts := []string{fmt.Sprintf("PID %d", p.PID)}
	if p.User != "" {
		parts = append(parts, p.User)
	}
	return strings.Join(parts, " · ")
}

// dtSourceChip 把溯源结论做成一枚浅色 chip：左边极小的标签，右边是值。
func (a *app) dtSourceChip(c *ui.Context, t *ui.Theme, r *witrResult) {
	value := sourceLabel(r.Source)
	if strings.TrimSpace(value) == "" {
		// 没有来源信息时退回默认文案，不留空白。
		value = "未知"
	}

	ui.Row(c).
		Key("dt-source").
		Gap(sp2).
		AlignItems(ui.Center).
		Padding(sp1, sp2).
		Background(cBGSubtle).
		Radius(rSm).
		Children(func() {
			ui.Text(c, "起点").
				FontSize(fsCaption).
				TextColor(cMuted)
			ui.Text(c, value).
				FontSize(fsCaption).
				TextColor(cText).
				Grow(1).
				SingleLine()
		})
}

// dtWarnings 画告警卡片。witr 的告警是英文，先过一遍 warningLabel 翻译。
func (a *app) dtWarnings(c *ui.Context, t *ui.Theme, r *witrResult) {
	if len(r.Warnings) == 0 {
		return
	}
	for _, w := range r.Warnings {
		ui.Row(c).
			Gap(sp2).
			AlignItems(ui.Start).
			Padding(sp2).
			Background(cBGSubtle).
			Radius(rSm).
			Children(func() {
				ui.Icon(c, warningIcon).
					Width(fsSmall + sp1).
					Height(fsSmall + sp1).
					TextColor(cWarning)
				ui.Text(c, warningLabel(w)).
					FontSize(fsCaption).
					TextColor(cText).
					Grow(1)
			})
	}
}

// dtTraceError 画溯源失败态：一句原因，加可点的候选卡片。
func (a *app) dtTraceError(c *ui.Context, t *ui.Theme) {
	st := a.taskMgr

	ui.Row(c).
		Gap(sp2).
		AlignItems(ui.Start).
		Padding(sp2).
		Background(cBGSubtle).
		Radius(rSm).
		Children(func() {
			ui.Icon(c, warningIcon).
				Width(fsSmall + sp1).
				Height(fsSmall + sp1).
				TextColor(cWarning)
			ui.Text(c, st.traceErr).
				FontSize(fsCaption).
				TextColor(cText).
				Grow(1)
		})

	for _, cand := range st.cands {
		pid := cand.PID
		// 先按普通底色把行造出来，再问这一行自己有没有被悬浮。
		//
		// 悬浮态只能问元素本身，而 Background 又必须在构造时就给定：
		// 所以先建、先问、再在同一个元素上补背景色。MyGo 的 builder
		// 支持构造后继续链式配置，这一行仍是同一个元素，不会多出节点。
		row := ui.Row(c).
			Gap(sp2).
			AlignItems(ui.Center).
			Padding(sp1, sp2).
			Background(cSurface).
			Radius(rSm).
			Border(1, cLine).
			Children(func() {
				ui.Text(c, cand.Name).
					FontSize(fsCaption).
					TextColor(cText).
					Grow(1).
					SingleLine()
				ui.Textf(c, "PID %d", pid).
					FontSize(fsCaption).
					TextColor(cMuted)
			})
		if row.Hovered() {
			row.Background(cHover)
		}
		row.OnClick(func() { a.runTrace(pid) })
	}
}
