// 整窗外壳与标题栏。
//
// 这里只有两件事：把三块内容（标题栏 / 进程表 / 性能区）按顺序装进窗口，
// 以及画那条决定第一观感的标题栏。
//
// 标题栏的分层思路（对照 docs/plans/2026-10-10-ui-modernization.md 的诊断）：
//   - 底色用 cBGSunken（#f5f5f7）而不是纯白，与内容区（cBG）拉开一层极浅的
//     色差，窗口于是有了「头」；分界靠一条 1px cLine，不用重色块。
//   - 高度 44（toolbarH）比原先舒展，标题与搜索框都落在视觉中线上。
//   - 应用名用 fsTitle + Bold + cText，是标题栏里最重的一档字。
//   - 搜索框 320×32（searchW/searchH），圆角 rMd，白底 + cLineStrong 描边，
//     在浅灰标题栏上像一块「浮起来的输入面」；它有真实交互态：悬停、聚焦
//     和「有内容」三种，见 searchBox。
package main

import (
	"github.com/egoist/mygo/ui"
)

// 标题栏自己的尺寸与比例。tokens.go 管跨切片的公共尺度，这几个是这一条
// 栏内部的关系，改动不会波及别处。
const (
	shIconBtn  = 30   // iconButton 的边长（原 26，太挤）
	shClearBtn = 20   // 搜索框里清除按钮的边长，比常规图标按钮紧凑
	shTitleX   = 16   // 应用名距左边缘
	shHoverMix = 0.55 // 悬停时描边朝 cMuted 走多远
	shFillMix  = 0.5  // 「有内容」时描边在 cLineStrong 与 cAccent 之间的位置
)

// view 画出整窗内容：标题栏、进程表、底部性能区。
//
// 主题固定浅色：系统是深色时把帧的主题换回浅色，之后所有颜色都取自
// tokens.go，所以两套系统外观下观感一致。
func (a *app) view(c *ui.Context) {
	if c.Theme().Dark {
		c.SetTheme(theme)
	}

	// 采样循环只在这里启动一次；它一直跑，把结果 post 回主线程。
	a.ensureSampling()

	t := c.Theme()
	ui.Column(c).Fill().Background(cBG).Children(func() {
		a.titlebar(c, t)

		// 表格的行高由主题的 Spacing 决定：ui.Table 的表头高与数据行最低
		// 高都是 Space(8)。只给进程表一段更舒展的副本——Spacing 5 得到
		// 表头 40 / 数据行 40，落在 tokens 里 headH(38) 与 rowH(34) 的
		// 量级上。Context.SetTheme 只影响其后创建的控件：恢复之后建立的
		// 性能区与详情面板仍用原主题，于是只有表格的行变高。
		table := *t
		table.Spacing = 5
		c.SetTheme(&table)
		a.processesView(c, t)
		c.SetTheme(t)

		a.performanceStrip(c, t)
	})
}

// titlebar 画一条 toolbarH(44) 的标题栏：左边应用名，中间搜索框。
//
// 应用名必须绝对定位，不能留在行内：行内它会占掉左侧弹性区里的位置，
// 搜索框被推右约 65px。TestTitlebarCentering 量的正是这个，所以左右两个
// 弹性区必须严格等宽（Grow(1)/Shrink(1)/Basis(0) 且内容相同）。
func (a *app) titlebar(c *ui.Context, t *ui.Theme) {
	bar := c.TitleBar()
	pad := max(bar.Right+10, float32(14))
	if bar.Left+10 > pad {
		pad = bar.Left + 10
	}

	// 整条栏向下让出 1px，底边正好压住一条 cLine：表头带与标题栏都是
	// cBGSunken，没有这条线两者会糊成一片（这就是原来的样子）。
	// 用 Margin 而不是在自己内部画线，是为了让栏高恒等于 toolbarH。
	ui.Column(c).Shrink(0).Children(func() {
		ui.Row(c).Height(toolbarH).Shrink(0).Background(cBGSunken).
			Padding(0, pad).AlignItems(ui.Center).DragWindow().Children(func() {
			// 左右两侧的弹性区。它们必须一模一样，中间的东西才会居中。
			ui.Box(c).Grow(1).Shrink(1).Basis(0)
			a.searchBox(c, t)
			ui.Box(c).Grow(1).Shrink(1).Basis(0)

			// 应用名：monitorIcon + 「任务管理器」，fsTitle / Bold / cText。
			// Absolute 让它脱离上面的布局，两侧弹性区因此严格等宽。
			ui.Row(c).Height(toolbarH).Gap(sp2).Shrink(0).
				Absolute().Left(shTitleX).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, monitorIcon).Size(sp5, sp5).TextColor(cAccent)
				ui.Text(c, "任务管理器").FontSize(fsTitle).Bold().SingleLine().TextColor(cText)
			})
		})
		// 1px 的 cLine：标题栏与表头带之间的分界。
		ui.Box(c).WidthPercent(100).Height(1).Shrink(0).Background(cLine)
	})
}

// searchBox 是标题栏中央的搜索框：searchW×searchH，圆角 rMd，白底 1px 描边。
//
// 交互态就是「像 2010 年的输入框」那条诊断的正解。样式在本帧构建时直接
// 写到元素上（库不提供延迟的样式回调，见 ui/form.go 的 inputBorder）：
//   - 静止：描边 cLineStrong，白框浮在 cBGSunken 的标题栏上。
//   - 悬停：描边朝 cMuted 加深，指针一进来就知道它可以点。
//   - 聚焦：描边 cAccent + 一层极浅的光环，键盘用户也看得见。
//   - 有内容：描边向 cAccent 靠拢并显示清除按钮——一眼看出表格里
//     现在是过滤后的结果。
//
// 交互态读的是上一帧的命中结果（Hovered/Focused 在构建期即有效），所以
// 状态变化会在下一帧生效；描边色因此从元素当前的 borderC 出发做插值，
// 避免闪一下。
func (a *app) searchBox(c *ui.Context, t *ui.Theme) ui.Element {
	box := ui.Row(c).Width(searchW).Height(searchH).Shrink(0).
		Radius(rMd).Background(cBG).Border(1, cLineStrong).
		Padding(0, sp2).AlignItems(ui.Center).Gap(sp2)
	// 无障碍名，同时是测试量居中用的稳定锚点：量的是这个容器本身，
	// 不是里面的输入框（输入框左边还有放大镜图标，量它会偏左）。
	box.Label("搜索框")

	var in ui.Element
	box.Children(func() {
		ui.Icon(c, searchIcon).Size(iconSizeV, iconSizeV).TextColor(cMuted)
		in = ui.TextInputBase(c, &a.taskMgr.query).Grow(1).Shrink(1).
			FontSize(fsBody).TextColor(cText).Placeholder("搜索进程名或 PID")
		if a.taskMgr.query != "" {
			clear := iconButton(c, t, closeIcon, "清除搜索")
			clear.Width(shClearBtn).Height(shClearBtn)
			if clear.Clicked() {
				a.taskMgr.query = ""
			}
		}
	})

	switch {
	case in.Focused():
		box.BorderColor(cAccent).Shadow(0, 1, 3, 0, cLineStrong)
	case a.taskMgr.query != "":
		box.BorderColor(cLineStrong.Mix(cAccent, shFillMix))
	case box.Hovered():
		box.BorderColor(cLineStrong.Mix(cMuted, shHoverMix))
	}
	return box
}

// iconButton 是一个纯图标按钮：圆角 rMd，悬停一层 cHover，按下更深。
// 图标是 cMuted——次要动作不该和标题抢注意力。tip 非空时挂 tooltip，
// 并当作无障碍名（图标本身对辅助技术是不可见的）。
func iconButton(c *ui.Context, t *ui.Theme, svg *ui.SVG, tip string) ui.Element {
	b := ui.Row(c).Size(shIconBtn, shIconBtn).Shrink(0).Radius(rMd).
		Center().Background(ui.Transparent)
	b.Children(func() {
		ui.Icon(c, svg).Size(iconSizeV, iconSizeV).TextColor(cMuted)
	})
	if tip != "" {
		b.Label(tip).Tooltip(tip)
	}
	switch {
	case b.Pressed():
		b.Background(cPressed)
	case b.Hovered():
		b.Background(cHover)
	}
	return b
}
