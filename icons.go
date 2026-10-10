// 进程图标：把进程对应的可执行文件图标取出来，画成 MyGo 能用的位图。
//
// 两条路线都实测过：
//   - 直接读图标的位图（GetIconInfo + GetDIBits）在探测尺寸这步就失败：
//     GetDIBits 传 NULL 缓冲区不返回尺寸。
//   - 改用 CreateDIBSection 建一块 32 位自上而下的画布，再用 DrawIconEx
//     把图标画上去。这条路把所有图标格式统一处理，实测 4/4 成功。
package main

import (
	"image"
	"image/color"
	"sync"
	"syscall"
	"unsafe"

	"github.com/egoist/mygo/ui"
)

var (
	moduser32  = syscall.NewLazyDLL("user32.dll")
	modshell32 = syscall.NewLazyDLL("shell32.dll")
	modgdi32   = syscall.NewLazyDLL("gdi32.dll")

	procExtractIconExW     = modshell32.NewProc("ExtractIconExW")
	procDestroyIcon        = moduser32.NewProc("DestroyIcon")
	procGetDC              = moduser32.NewProc("GetDC")
	procReleaseDC          = moduser32.NewProc("ReleaseDC")
	procDrawIconEx         = moduser32.NewProc("DrawIconEx")
	procCreateDIBSection   = modgdi32.NewProc("CreateDIBSection")
	procSelectObject       = modgdi32.NewProc("SelectObject")
	procDeleteObject       = modgdi32.NewProc("DeleteObject")
	procDeleteDC           = modgdi32.NewProc("DeleteDC")
	procCreateCompatibleDC = modgdi32.NewProc("CreateCompatibleDC")
)

// iconSize 是图标渲染的边长（像素）。任务管理器用 16；这里用 20，
// 在高分屏下更清晰，也不至于让行太高。
const iconSize = 20

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type rgbQuad struct{ Blue, Green, Red, Reserved byte }

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]rgbQuad
}

// renderExeIcon 把可执行文件的图标画成一张 RGBA 图。
func renderExeIcon(path string, size int) (*image.RGBA, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var large, small uintptr
	n, _, _ := procExtractIconExW.Call(uintptr(unsafe.Pointer(p)), 0,
		uintptr(unsafe.Pointer(&large)), uintptr(unsafe.Pointer(&small)), 1)
	if n == 0 || large == 0 {
		return nil, errNoIcon
	}
	defer procDestroyIcon.Call(large)
	if small != 0 {
		procDestroyIcon.Call(small)
	}

	dc, _, _ := procGetDC.Call(0)
	if dc == 0 {
		return nil, errNoIcon
	}
	defer procReleaseDC.Call(0, dc)
	memDC, _, _ := procCreateCompatibleDC.Call(dc)
	if memDC == 0 {
		return nil, errNoIcon
	}
	defer procDeleteDC.Call(memDC)

	bi := bitmapInfo{}
	bi.Header.Size = uint32(unsafe.Sizeof(bi.Header))
	bi.Header.Width = int32(size)
	// 负高度 = 自上而下，省掉一次翻转。
	bi.Header.Height = -int32(size)
	bi.Header.Planes = 1
	bi.Header.BitCount = 32
	bi.Header.Compression = 0 // BI_RGB

	var bits unsafe.Pointer
	dib, _, _ := procCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&bi)), 0,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if dib == 0 || bits == nil {
		return nil, errNoIcon
	}
	defer procDeleteObject.Call(dib)
	old, _, _ := procSelectObject.Call(memDC, dib)
	defer procSelectObject.Call(memDC, old)

	// DI_NORMAL = 3：画图标本体并做透明混合。
	procDrawIconEx.Call(memDC, 0, 0, large,
		uintptr(size), uintptr(size), 0, 0, 3)

	// DIB 里是 BGRA。
	buf := unsafe.Slice((*byte)(bits), size*size*4)
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	opaque := 0
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			i := (y*size + x) * 4
			a := buf[i+3]
			if a > 0 {
				opaque++
			}
			img.SetRGBA(x, y, color.RGBA{R: buf[i+2], G: buf[i+1], B: buf[i], A: a})
		}
	}
	// DrawIconEx 在某些图标上不写 alpha（整块不透明）。这时整张图会
	// 是不透明的黑底，直接用会显示成黑方块，所以判为失败。
	if opaque == 0 {
		return nil, errNoIcon
	}
	return img, nil
}

var errNoIcon = errIcon("no icon")

type errIcon string

func (e errIcon) Error() string { return string(e) }

// iconDetailSize 是详情面板里图标的渲染边长（像素）。
//
// 图标按显示尺寸栅格化，不能拿 20px 的位图去填 36px 的框——那样是放大
// 一张小图，边缘会糊。表格里显示 16px，用 iconSize(20) 的位图缩放即可；
// 详情面板显示 36px，必须单独渲染一张够大的。
const iconDetailSize = 36

// iconCache 缓存进程图标。
//
// 必须缓存：MyGo 会把 bitmap 常驻 GPU，在 view 里反复 NewBitmap 会不断
// 占显存。而且图标只取决于可执行文件路径，同一路径的所有进程共用一张。
//
// 每个路径缓存两张位图：byPath 给 16px 的表格行用，byDetail 给 36px 的
// 详情面板用。合成一张再缩放会牺牲其中一处的清晰度，索性按用途分开。
type iconCache struct {
	mu       sync.Mutex
	byPath   map[string]*ui.Bitmap
	byDetail map[string]*ui.Bitmap
	misses   map[string]bool
}

func newIconCache() *iconCache {
	return &iconCache{
		byPath:   map[string]*ui.Bitmap{},
		byDetail: map[string]*ui.Bitmap{},
		misses:   map[string]bool{},
	}
}

// get 返回某个可执行文件的图标位图，失败返回 nil（调用方画占位图标）。
// 这张位图按 iconSize(20) 栅格化，供表格的 16px 图标使用。
func (c *iconCache) get(exePath string) *ui.Bitmap {
	return c.getSized(exePath, iconSize)
}

// getDetail 返回详情面板用的图标位图，按显示尺寸 iconDetailSize(36) 栅格化。
// 取不到时返回 nil，调用方画占位图标。
func (c *iconCache) getDetail(exePath string) *ui.Bitmap {
	return c.getSized(exePath, iconDetailSize)
}

// getSized 按指定像素边长取图标，懒渲染并缓存。
//
// 命中失败会记进 misses，避免每次重建视图都去碰一次打不开的进程。
// 栅格化失败对尺寸是无关的（文件读不到两种情况都读不到），所以 misses
// 只按路径记一份，两种尺寸共享。
func (c *iconCache) getSized(exePath string, size int) *ui.Bitmap {
	if exePath == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cache := c.byPath
	if size > iconSize {
		cache = c.byDetail
	}
	if b, ok := cache[exePath]; ok {
		return b
	}
	if c.misses[exePath] {
		return nil
	}
	img, err := renderExeIcon(exePath, size)
	if err != nil {
		c.misses[exePath] = true
		return nil
	}
	b := ui.NewBitmap(img)
	cache[exePath] = b
	return b
}

// len 报告已缓存的图标数量，供测试与诊断使用。
func (c *iconCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.byPath)
}
