// 各处共用的小工具。
package main

import (
	"fmt"
	"strings"
)

// post 把 fn 送回主线程执行。生产环境走窗口的 Update；测试里可以用
// app.postFn 换成同步执行，从而在无窗口的情况下覆盖整条异步链路。
func (a *app) post(fn func()) bool {
	if a.postFn != nil {
		a.postFn(fn)
		return true
	}
	if a.win == nil {
		return false
	}
	a.win.Update(fn)
	return true
}

// asLookup 把 err 取成 *witrLookupError。
func asLookup(err error, target **witrLookupError) bool {
	le, ok := err.(*witrLookupError)
	if ok {
		*target = le
	}
	return ok
}

// warningLabel 把 witr 的英文告警翻成中文。
//
// witr 的告警是英文短语，原样展示会和界面上其它中文混在一起。
// 没见过的告警保留原文，总比丢掉信息好。
func warningLabel(w string) string {
	lower := strings.ToLower(w)
	switch {
	case strings.Contains(lower, "high cpu") && strings.Contains(lower, ">2h"):
		return "该进程累计占用 CPU 超过 2 小时"
	case strings.Contains(lower, "high cpu"):
		return "CPU 占用偏高"
	case strings.Contains(lower, "high memory") || strings.Contains(lower, "high mem"):
		return "内存占用偏高"
	case strings.Contains(lower, "deleted"):
		return "可执行文件已被删除"
	case strings.Contains(lower, "zombie"):
		return "进程处于僵尸状态"
	case strings.Contains(lower, "parent") && strings.Contains(lower, "exit"):
		return "父进程已退出"
	case strings.Contains(lower, "listening") && strings.Contains(lower, "public"):
		return "正在公网接口上监听"
	case strings.Contains(lower, "listening"):
		return "正在监听端口"
	case strings.Contains(lower, "privileged") || strings.Contains(lower, "elevated"):
		return "以高权限运行"
	case strings.Contains(lower, "restart"):
		return "进程反复重启"
	}
	return w
}

// pick 返回条件为真时的 a，否则 b。
func pick[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

// humanBytes 把字节数写成可读的大小。
func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n)
	for _, u := range units {
		v /= unit
		if v < unit {
			return fmt.Sprintf("%.1f %s", v, u)
		}
	}
	return fmt.Sprintf("%.1f PB", v/unit)
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
