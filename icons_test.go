package main

import (
	"os"
	"strings"
	"testing"
)

// famousExe 返回一个一定有图标的系统可执行文件，用于不依赖具体进程的测试。
func famousExe(t *testing.T) string {
	t.Helper()
	for _, p := range []string{
		"C:\\Windows\\explorer.exe",
		"C:\\Windows\\System32\\notepad.exe",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// TestRenderExeIcon 验证图标提取真的画出了内容。
//
// 注意不能用本进程的 exe：go test 生成的临时二进制没有图标资源，
// 那是正常情况，不是提取失败。
func TestRenderExeIcon(t *testing.T) {
	exe := famousExe(t)
	if exe == "" {
		t.Skip("no system exe with an icon available")
	}
	img, err := renderExeIcon(exe, iconSize)
	if err != nil {
		t.Fatalf("renderExeIcon(%s): %v", exe, err)
	}
	b := img.Bounds()
	if b.Dx() != iconSize || b.Dy() != iconSize {
		t.Fatalf("icon is %dx%d, want %dx%d", b.Dx(), b.Dy(), iconSize, iconSize)
	}
	opaque := 0
	colors := map[uint32]bool{}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.RGBAAt(x, y)
			if c.A > 0 {
				opaque++
			}
			colors[uint32(c.R)<<16|uint32(c.G)<<8|uint32(c.B)] = true
		}
	}
	// 全透明的结果会让界面显示成空白，必须当失败。
	if opaque == 0 {
		t.Fatal("icon is fully transparent; DrawIconEx did not draw")
	}
	// 一个真图标有丰富的颜色；纯色块说明画错了。
	if len(colors) < 3 {
		t.Errorf("icon has only %d distinct colours (%d opaque px); looks blank",
			len(colors), opaque)
	}
	t.Logf("icon %s: %d opaque px, %d colours", exe, opaque, len(colors))
}

// TestRenderExeIconBadPath 确认路径不存在时报错而不是给空白图标。
func TestRenderExeIconBadPath(t *testing.T) {
	if _, err := renderExeIcon(`C:
osuchile.exe`, iconSize); err == nil {
		t.Error("renderExeIcon on a missing file returned no error")
	}
}

// TestRenderExeIconNoIconResource 确认没有图标资源的 exe 报错。
// svchost 这类系统宿主确实不带图标，界面要能落到占位图标上。
func TestRenderExeIconNoIconResource(t *testing.T) {
	p := "C:\\Windows\\System32\\svchost.exe"
	if _, err := os.Stat(p); err != nil {
		t.Skip("svchost.exe not present")
	}
	if _, err := renderExeIcon(p, iconSize); err == nil {
		t.Skip("svchost.exe now has an icon resource; nothing to assert")
	}
}

// TestIconCache 确认图标按路径缓存，第二次不再重新提取。
func TestIconCache(t *testing.T) {
	exe := famousExe(t)
	if exe == "" {
		t.Skip("no system exe with an icon available")
	}
	c := newIconCache()
	first := c.get(exe)
	if first == nil {
		t.Fatal("first get returned nil for an exe that has an icon")
	}
	if n := c.len(); n != 1 {
		t.Errorf("cache holds %d entries, want 1", n)
	}
	// 同一路径必须复用同一张位图：MyGo 的 bitmap 常驻 GPU，
	// 每帧重建会不断吃掉显存。
	second := c.get(exe)
	if second != first {
		t.Error("second get returned a different bitmap; the cache missed")
	}
	if n := c.len(); n != 1 {
		t.Errorf("cache grew to %d entries after a repeat get", n)
	}
	if c.get("") != nil {
		t.Error("get on an empty path returned a bitmap")
	}
	if n := c.len(); n != 1 {
		t.Errorf("empty path added a cache entry: %d", n)
	}
}

// TestIconCacheRemembersMisses 确认取不到图标的路径被记住，
// 不会每一帧都重新提取一遍（受保护进程会拖慢整个列表）。
func TestIconCacheRemembersMisses(t *testing.T) {
	c := newIconCache()
	bad := `C:
osuchile.exe`
	if c.get(bad) != nil {
		t.Fatal("get on a bad path returned a bitmap")
	}
	c.mu.Lock()
	missed := c.misses[bad]
	c.mu.Unlock()
	if !missed {
		t.Error("a failed extraction was not remembered")
	}
	// 第二次应当直接命中 miss 记录。
	if c.get(bad) != nil {
		t.Fatal("second get on a bad path returned a bitmap")
	}
}

// TestIconExtractionWorksAcrossProcesses 确认图标提取对真实进程整体可用。
//
// 这条测试的意图是抓「提取整体失效」——例如 ExtracIconExW 调用写错、
// 位图转换挂掉，结果每个进程都拿不到图标。
//
// 它**不能**用「有图标的进程占比」来表达：这个比例完全是机器角色的函数。
// 实测数据（同一份代码）：
//
//	Windows 11 桌面  421 个进程有路径，339 个有图标（81%）
//	GitHub Server 版 150 个进程有路径， 18 个有图标（12%），其中
//	                 svchost.exe 一个就占 86 个（57%）
//
// Server 镜像上绝大多数是系统宿主与 Azure/Defender 代理，本来就不带
// 图标资源，比例低是正常的，不是回归。所以这里改成一个与机器无关的
// 判据：提取必须在「确定有图标资源的系统 exe」上成功，并且在真实进程
// 里至少成功若干个——整体失效时这两条都会挂。
func TestIconExtractionWorksAcrossProcesses(t *testing.T) {
	// 1) 先证明提取管线本身是好的。famousExe 是 explorer.exe / notepad.exe，
	//    一定有图标资源；它拿不到图标就说明提取坏了。
	exe := famousExe(t)
	if exe == "" {
		t.Skip("no system exe with an icon available")
	}
	if newIconCache().get(exe) == nil {
		t.Fatalf("no icon extracted from %s; extraction is broken", exe)
	}

	// 2) 再证明它在真实进程上也能用：至少要成功若干个。
	//    阈值取得很低（>=3），只用于区分「整体失效」（0-2）与
	//    「正常但机器图标资源少」，不随机器角色漂移。
	procs, err := listProcesses()
	if err != nil {
		t.Fatal(err)
	}
	c := newIconCache()
	tried, got := 0, 0
	for _, p := range procs {
		path := processExePath(uint32(p.PID))
		if path == "" {
			continue
		}
		tried++
		if c.get(path) != nil {
			got++
		}
	}
	if tried < 10 {
		t.Skipf("only %d processes with a resolvable path; too few to judge", tried)
	}
	t.Logf("icons: %d/%d processes with a resolvable path had an icon", got, tried)
	if got < 3 {
		t.Errorf("only %d of %d processes had an icon; extraction looks broken", got, tried)
	}
}

// TestProcessExePath 确认能取到路径，且受保护进程返回空串而不是乱码。
func TestProcessExePath(t *testing.T) {
	self := processExePath(uint32(getpid()))
	if self == "" {
		t.Fatal("cannot resolve our own exe path")
	}
	if !strings.HasSuffix(strings.ToLower(self), ".exe") {
		t.Errorf("own exe path = %q, want a .exe", self)
	}
	// PID 4 是 System，必然打不开。
	if got := processExePath(4); got != "" {
		t.Errorf("processExePath(4) = %q, want empty for a protected process", got)
	}
}
