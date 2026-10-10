package main

import (
	"testing"
)

// TestIconCacheDetailSize 是「详情面板图标糊」的回归测试。
//
// 曾经的问题：iconCache 只按 iconSize(20) 栅格化一张位图，表格里显示 16px
// 没问题，但详情面板把它拉到 36px —— 那是把小图放大，边缘发糊。
// 现在按用途各存一张：get 给表格（iconSize），getDetail 给详情（iconDetailSize）。
//
// 断言三件事：
//  1. 两张位图的像素尺寸确实不同，且各自等于自己的栅格化边长；
//  2. 同一路径重复取，返回同一张（缓存命中，不重新提取）；
//  3. 两种尺寸共用一个 misses 记录——路径读不到时不该反复去碰它。
func TestIconCacheDetailSize(t *testing.T) {
	exe := famousExe(t)
	if exe == "" {
		t.Skip("no system exe with an icon available")
	}
	c := newIconCache()

	small := c.get(exe)
	if small == nil {
		t.Fatal("get returned nil for an exe that has an icon")
	}
	large := c.getDetail(exe)
	if large == nil {
		t.Fatal("getDetail returned nil for an exe that has an icon")
	}

	if w, h := small.Size(); w != iconSize || h != iconSize {
		t.Errorf("table icon is %dx%d, want %dx%d", w, h, iconSize, iconSize)
	}
	if w, h := large.Size(); w != iconDetailSize || h != iconDetailSize {
		t.Errorf("detail icon is %dx%d, want %dx%d", w, h, iconDetailSize, iconDetailSize)
	}
	// 详情图标的框比表格大，位图也必须更大；相等就说明又退回了同一张。
	if large == small {
		t.Error("detail icon reuses the small bitmap; it would be upscaled and blurry")
	}

	// 缓存：同一路径、同一尺寸，第二次必须复用同一张。
	// MyGo 的 bitmap 常驻 GPU，每帧新建会不断吃显存。
	if again := c.getDetail(exe); again != large {
		t.Error("second getDetail returned a different bitmap; the cache missed")
	}
	if again := c.get(exe); again != small {
		t.Error("second get returned a different bitmap; the cache missed")
	}

	// 取不到图标的路径：两种尺寸都该失败，且只记一次 miss。
	bad := "C:" + string(rune(92)) + "no" + "such" + "file.exe"
	if c.getDetail(bad) != nil {
		t.Fatal("getDetail on a bad path returned a bitmap")
	}
	c.mu.Lock()
	missed := c.misses[bad]
	c.mu.Unlock()
	if !missed {
		t.Error("a failed detail extraction was not remembered")
	}
	if c.get(bad) != nil {
		t.Fatal("get on a path already known to miss returned a bitmap")
	}
}

// TestIconCacheEmptyPathDetail 确认空路径对两种尺寸都返回 nil，
// 且不会往缓存里塞东西。
func TestIconCacheEmptyPathDetail(t *testing.T) {
	c := newIconCache()
	if c.getDetail("") != nil {
		t.Error("getDetail on an empty path returned a bitmap")
	}
	c.mu.Lock()
	n := len(c.byPath) + len(c.byDetail)
	c.mu.Unlock()
	if n != 0 {
		t.Errorf("empty path added %d cache entries", n)
	}
}
