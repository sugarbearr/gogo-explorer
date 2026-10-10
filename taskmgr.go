// 独立的任务管理器：上半是进程表，下半是实时性能；选中进程后右侧弹出详情，
// 「为什么在跑」（witr 溯源）就在详情里。
//
// 表格用 ui.Table：它自带表头对齐、列宽拖动、列序拖动与排序，比手搓
// 一堆 Row 拼列靠谱得多——手搓那版表头和数据各对齐各的，对不上。
//
// 视图实现按切片拆到各文件：
//
//	shell.go      整窗外壳与标题栏
//	table_ui.go   进程表
//	perf_ui.go    底部性能区
//	detail_ui.go  右侧详情面板
//
// 本文件只留数据模型与两者共用的纯函数。
package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// 列宽。数据列给固定宽度，名称列留 0 表示「占满剩余空间」。
const (
	iconColWidth float32 = 28
	pidColWidth  float32 = 84
	cpuColWidth  float32 = 128
	memColWidth  float32 = 100
	diskColWidth float32 = 104
)

// 任务管理器自己的配色。取自 tokens.go 的指标色语义，
// 与主题色区分开，让「CPU 蓝 / 内存紫 / 磁盘橙 / 网络青」稳定可辨。
var (
	colorCPU  = cCPU
	colorMem  = cMem
	colorDisk = cDisk
	colorNet  = cNet
)

// tmGroup 是一组同名进程。任务管理器把同名进程合并成一行，
// 这里照做，展开后才是单个进程。
type tmGroup struct {
	Name  string
	Procs []procMetric
	CPU   float64
	Mem   uint64
	Disk  float64
	PID   int
	Exe   string

	expanded bool
}

// tmRow 是表格里的一行：要么是一个分组，要么是展开后的某个进程。
type tmRow struct {
	key    string
	name   string
	exe    string
	group  *tmGroup
	metric *procMetric
	child  bool
}

func flattenGroups(gs []*tmGroup) []tmRow {
	var out []tmRow
	for _, g := range gs {
		out = append(out, tmRow{key: g.Name, name: g.Name, exe: g.Exe, group: g})
		if g.expanded && len(g.Procs) > 1 {
			for i := range g.Procs {
				m := &g.Procs[i]
				out = append(out, tmRow{
					key:    fmt.Sprintf("%s#%d", g.Name, m.PID),
					name:   m.Name,
					exe:    m.Exe,
					metric: m,
					child:  true,
				})
			}
		}
	}
	return out
}

// sortColumnID 把 sortKey 映射成列的 ID。
func (st *taskMgrState) sortColumnID() string {
	switch st.sort {
	case sortByName:
		return "name"
	case sortByPID:
		return "pid"
	case sortByMem:
		return "mem"
	case sortByDisk:
		return "disk"
	default:
		return "cpu"
	}
}

func sortKeyForColumn(id string) (sortKey, bool) {
	switch id {
	case "name":
		return sortByName, true
	case "pid":
		return sortByPID, true
	case "cpu":
		return sortByCPU, true
	case "mem":
		return sortByMem, true
	case "disk":
		return sortByDisk, true
	}
	return sortByCPU, false
}

// percent 返回 a 占 b 的百分比，b 为 0 时返回 0。
func percent(a, b uint64) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b) * 100
}

// humanRate 把每秒字节数写成可读的速率。
func humanRate(bps float64) string {
	if bps < 1 {
		return "0 B/s"
	}
	return humanBytes(uint64(bps)) + "/s"
}

// sourceLabel 把 Source 拼成一句人话。
func sourceLabel(s witrSource) string {
	switch s.Type {
	case "windows_service":
		if s.Name != "" {
			return "Windows 服务：" + s.Name
		}
		return "Windows 服务"
	case "init":
		return "系统初始化进程"
	case "systemd":
		if s.UnitFile != "" {
			return "systemd 单元：" + s.UnitFile
		}
		return "systemd"
	case "launchd":
		return "launchd"
	case "shell":
		if s.Name != "" {
			return "Shell：" + s.Name
		}
		return "Shell"
	}
	if s.Name != "" {
		return s.Name
	}
	return s.Type
}

// 避免 ui 包因暂时没有引用而报未使用；表格与性能切片都会用到它。
var _ = ui.Hex
