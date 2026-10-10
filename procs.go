// 进程枚举：GUI 的列表视图需要一份本机进程清单，而 witr 没有「列出全部」
// 模式（裸调用会等待输入）。这里直接用 Windows 的 Toolhelp32 快照，
// 它在标准库 syscall 里，不引入新依赖。
package main

import (
	"os"
	"sort"
	"strings"
	"syscall"
	"unsafe"
)

// procInfo 是进程清单里的一行。
type procInfo struct {
	PID  int
	PPID int
	Name string
	// Ports 是该进程正在监听的端口，来自 netstat 的补充扫描。
	Ports []int
}

// 不显示的进程名。这是照 Windows 任务管理器的实际行为拟的名单，
// 不是靠某个推导出来的规则。
//
// 我试过用「名字不以 .exe 结尾」当判据，但那会连带滤掉 System、
// Secure System、Registry —— 而实测（用户截图 + 本机对照）表明
// **任务管理器是显示这三个的**，只有 Memory Compression 不显示。
//
// 这四个内核对象在 API 层面完全一样：都没有可执行文件、没有路径、
// 打不开句柄、杀不掉、会话都是 0、优先级都是 8。所以没有任何属性
// 能把 Memory Compression 单独区分出来——任务管理器用的就是一份
// 硬编码名单。
//
// 为什么单独漏掉 Memory Compression：它报出的内存（本机实测 8.8 GB）
// 是「被压缩存放的其他进程的页面」，本来就算在那些进程头上。列出来
// 等于重复计算，还会因为数值巨大而长期霸占按内存排序的第一屏。
// 注意 Windows 自己的 \Process(_Total) 是**包含**它的，所以它并非
// 凭空多出来的数字，只是不该按「一个程序」来展示。
var hiddenProcessNames = map[string]bool{
	"memory compression": true,
}

// isHiddenProcess 报告一个进程是否应当从列表里隐藏。
func isHiddenProcess(name []uint16) bool {
	return hiddenProcessNames[strings.ToLower(strings.TrimSpace(syscall.UTF16ToString(name)))]
}

// listProcesses 枚举本机进程，按名字排序。
//
// 用 CreateToolhelp32Snapshot 一次取快照再遍历，比逐个 OpenProcess 快得多，
// 也不需要进程句柄权限。
func listProcesses() ([]procInfo, error) {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer syscall.CloseHandle(snap)

	var entry syscall.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := syscall.Process32First(snap, &entry); err != nil {
		return nil, err
	}

	var procs []procInfo
	for {
		// PID 0 是「System Idle Process」这个空转的伪进程。witr 明确拒绝它
		// （invalid pid: must be a positive integer），列出来只会让用户点到
		// 一个必然失败的条目，因此不收。
		if pid := int(entry.ProcessID); pid > 0 && !isHiddenProcess(entry.ExeFile[:]) {
			procs = append(procs, procInfo{
				PID:  pid,
				PPID: int(entry.ParentProcessID),
				Name: syscall.UTF16ToString(entry.ExeFile[:]),
			})
		}
		if err := syscall.Process32Next(snap, &entry); err != nil {
			// ERROR_NO_MORE_FILES 是正常结束。
			break
		}
	}

	sort.Slice(procs, func(i, j int) bool {
		a, b := strings.ToLower(procs[i].Name), strings.ToLower(procs[j].Name)
		if a != b {
			return a < b
		}
		return procs[i].PID < procs[j].PID
	})
	return procs, nil
}

// processExePath 返回进程可执行文件的完整路径。
// 受保护进程打不开句柄，返回空串。
func processExePath(pid uint32) string {
	h, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer syscall.CloseHandle(syscall.Handle(h))
	var buf [1024]uint16
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcessImageNameW.Call(h, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:size])
}

// filterProcs 按名字、PID 或监听端口筛选进程。query 为空时返回全部。
func filterProcs(procs []procInfo, query string) []procInfo {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return procs
	}
	var out []procInfo
	for _, p := range procs {
		if strings.Contains(strings.ToLower(p.Name), q) ||
			strings.Contains(strings.ToLower(itoa(p.PID)), q) {
			out = append(out, p)
			continue
		}
		for _, port := range p.Ports {
			if strings.Contains(itoa(port), q) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// itoa 是 strconv.Itoa 的小包装，避免为这一处引入 strconv。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// getpid 返回本进程 PID。
func getpid() int { return os.Getpid() }
