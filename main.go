// 独立的任务管理器：整窗是一张进程表，选中进程后在右侧看它的详情与
// 「为什么在跑」（witr 溯源）。没有侧栏——进程表就是主界面。
//
//	go run .
package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// theme fixes the light appearance regardless of the system's.
var theme = ui.LightTheme()

// app is the state the window shows. Its view builds the interface from
// it, on the main thread, whenever the window needs a frame.
type app struct {
	tab int // 顶部标签：进程 / 性能

	taskMgr *taskMgrState // 任务管理器的状态
	icons   *iconCache    // 进程图标，按可执行文件路径缓存

	win    *mygo.Window // 后台任务回主线程用
	postFn func(func()) // 测试用的同步派发，覆盖 win
}

// icon parses the shapes of a 24×24 stroked icon, drawn in currentColor
// as icon sets draw them.
func icon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + shapes + `</svg>`))
}

var (
	monitorIcon = icon(`<rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8M12 17v4"/>`)
	searchIcon  = icon(`<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>`)
	closeIcon   = icon(`<path d="M18 6 6 18M6 6l12 12"/>`)
	refreshIcon = icon(`<path d="M21 12a9 9 0 1 1-2.6-6.4"/><path d="M21 3v6h-6"/>`)
	warningIcon = icon(`<path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z"/><path d="M12 9v4M12 17h.01"/>`)
)

func main() {
	a := &app{taskMgr: newTaskMgrState(), icons: newIconCache()}
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "任务管理器",
			Width:     1180,
			Height:    760,
			MinWidth:  820,
			MinHeight: 560,
			StateKey:  "main",
			// 隐藏原生标题栏，窗口按钮仍由系统画在右上角。
			TitleBarStyle:  mygo.TitleBarHidden,
			TitleBarHeight: 40,
			Content:        ui.View(a.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
