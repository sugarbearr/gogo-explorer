// 设计令牌：UI 现代化改造的唯一真相。
//
// 这里定义圆角、间距、行高、字阶与全部颜色。业务代码只引用这些名字，
// 不要再写裸的 ui.Hex —— 颜色散落在各处就没法整体调，改一次要找十处。
//
// 取舍依据与诊断见 docs/plans/2026-10-10-ui-modernization.md。
package main

import "github.com/egoist/mygo/ui"

// 圆角。越大越「软」，但要克制：卡片 12、控件 8、小元素 6。
const (
	rSm float32 = 6
	rMd float32 = 8
	rLg float32 = 12
	rXl float32 = 16
)

// 8px 栅格。sp1=4 是半格，用于图标与文字之间这类紧凑间隙。
const (
	sp1 float32 = 4
	sp2 float32 = 8
	sp3 float32 = 12
	sp4 float32 = 16
	sp5 float32 = 20
	sp6 float32 = 24
)

// 行高与控件尺寸。行高从 28 提到 34：数据行太挤是「不现代」的第一观感来源。
const (
	rowH      float32 = 34  // 表格数据行高
	headH     float32 = 38  // 表头带高度
	iconSizeV float32 = 16  // 进程图标显示尺寸
	toolbarH  float32 = 44  // 标题栏
	searchW   float32 = 320 // 搜索框宽（比原来宽，现代桌面应用更舒展）
	searchH   float32 = 32  // 搜索框高
)

// 字阶。数字用 fsMetric，正文 fsBody，次要信息 fsSmall/fsCaption。
const (
	fsCaption float32 = 11
	fsSmall   float32 = 12
	fsBody    float32 = 13
	fsTitle   float32 = 14
	fsMetric  float32 = 26
)

// 颜色。固定浅色语义，不随系统深色模式切换。
var (
	// 底色分层：窗口 < 下沉区 < 卡片，靠极浅的色差拉层次，不用重色块。
	cBG       = ui.Hex("#ffffff") // 窗口底
	cBGSubtle = ui.Hex("#fafafa") // 斑马纹 / 次级区
	cBGSunken = ui.Hex("#f5f5f7") // 标题栏 / 表头带
	cSurface  = ui.Hex("#ffffff") // 卡片面

	// 分隔线。line 是极轻的装饰线，lineStrong 才是控件描边。
	cLine       = ui.Hex("#ebebef")
	cLineStrong = ui.Hex("#dcdce3")

	cText  = ui.Hex("#14141a")
	cMuted = ui.Hex("#6b6b76") // 对白底 5.3:1，达 WCAG AA 正文

	// 交互态。
	cHover   = ui.Hex("#f2f2f5")
	cPressed = ui.Hex("#e8e8ee")
	cFocus   = ui.RGBA(37, 99, 235, 0.55)

	// 语义色。
	cAccent  = ui.Hex("#2563eb")
	cDanger  = ui.Hex("#dc2626")
	cWarning = ui.Hex("#d97706")
	cSuccess = ui.Hex("#16a34a")

	// 指标色：跨整窗稳定，让「CPU 蓝 / 内存紫 / 磁盘橙 / 网络青」一眼可辨。
	cCPU  = ui.Hex("#2563eb")
	cMem  = ui.Hex("#7c3aed")
	cDisk = ui.Hex("#ea580c")
	cNet  = ui.Hex("#0891b2")

	// 阴影。卡片用极浅的一层，浮层才用明显的一层——全靠边框会显平。
	shadowCard   = ui.RGBA(0, 0, 0, 0.05)
	shadowFloats = ui.RGBA(0, 0, 0, 0.10)
)
