// witr 是「Why is this running?」的 CLI：把进程、端口、文件或容器回溯到
// 启动它的那条因果链。本文件是它的数据层——执行、按退出码分发、解析 JSON。
//
// witr 不能作为 Go 库引用：它的实现全在 internal/（pipeline、proc、source、
// target），Go 禁止跨模块 import internal，而 pkg/ 下只有纯类型没有导出函数。
// 因此这里一律以子进程方式调用它。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// witrTimeout 限制单次查询。witr 不带目标时会等待标准输入而不返回，
// 因此除了下面的空目标保护，超时是第二道防线。
const witrTimeout = 30 * time.Second

// witrPath 覆盖 witr 可执行文件的位置；空则按内置副本、PATH 依次查找。
var witrPath string

// witrExeName 是内置副本的文件名。发行时 resources/witr.exe 由 mygo build
// 复制到可执行文件旁边（Windows 上就是安装目录），因此这里按「同目录」查找，
// 不需要 go:embed，也不需要运行时释放。
const witrExeName = "witr.exe"

// exeDir 覆盖「可执行文件所在目录」；仅测试用它指向构造出来的假安装目录。
var exeDir string

// witrInstallHint 是彻底找不到时给出的提示。
const witrInstallHint = "找不到 witr：内置副本缺失且 PATH 上也没有。请重新安装本程序，或执行 winget install PranshuParmar.witr"

// selfDir 返回当前可执行文件所在目录。测试可用 exeDir 覆盖。
func selfDir() (string, error) {
	if exeDir != "" {
		return exeDir, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	// os.Executable 可能返回符号链接路径，解析后再取目录，避免
	// 在符号链接安装的场景下找不到旁边的 witr.exe。
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe), nil
}

// bundledWitr 返回安装目录里那份 witr.exe；不在则返回空串。
func bundledWitr() string {
	dir, err := selfDir()
	if err != nil {
		return ""
	}
	p := filepath.Join(dir, witrExeName)
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return ""
	}
	return p
}

// witrTargetKind 是查询的类型。
type witrTargetKind int

const (
	witrByName witrTargetKind = iota
	witrByPID
	witrByPort
)

// witrTarget 是一次查询的目标。
type witrTarget struct {
	kind  witrTargetKind
	value string
}

func (t witrTarget) args() []string {
	switch t.kind {
	case witrByPID:
		return []string{"--pid", t.value}
	case witrByPort:
		return []string{"--port", t.value}
	default:
		return []string{t.value}
	}
}

func (t witrTarget) label() string {
	switch t.kind {
	case witrByPID:
		return "PID " + t.value
	case witrByPort:
		return "端口 " + t.value
	default:
		return t.value
	}
}

// witrResult 是 witr --json 的输出。字段与 witr/pkg/model 对齐，但只保留
// 界面用得上的部分；不引用该模块可以避免把它整套依赖（bubbletea 等）拖进来。
type witrResult struct {
	Target         witrTargetRef `json:"Target"`
	ResolvedTarget string        `json:"ResolvedTarget"`
	Process        witrProcess   `json:"Process"`
	RestartCount   int           `json:"RestartCount"`
	Ancestry       []witrProcess `json:"Ancestry"`
	Source         witrSource    `json:"Source"`
	Warnings       []string      `json:"Warnings"`
}

type witrTargetRef struct {
	Type  string `json:"Type"`
	Value string `json:"Value"`
}

type witrProcess struct {
	PID            int      `json:"PID"`
	PPID           int      `json:"PPID"`
	Command        string   `json:"Command"`
	Cmdline        string   `json:"Cmdline"`
	Exe            string   `json:"Exe"`
	StartedAt      string   `json:"StartedAt"`
	User           string   `json:"User"`
	CPUPercent     float64  `json:"CPUPercent"`
	MemoryRSS      uint64   `json:"MemoryRSS"`
	MemoryPercent  float64  `json:"MemoryPercent"`
	WorkingDir     string   `json:"WorkingDir"`
	GitRepo        string   `json:"GitRepo"`
	GitBranch      string   `json:"GitBranch"`
	Container      string   `json:"Container"`
	Service        string   `json:"Service"`
	Health         string   `json:"Health"`
	IntegrityLevel string   `json:"IntegrityLevel"`
	Env            []string `json:"Env"`
	ThreadCount    int      `json:"ThreadCount"`
}

// Display 返回进程最可读的名字。
func (p witrProcess) Display() string {
	for _, s := range []string{p.Command, p.Exe} {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return fmt.Sprintf("PID %d", p.PID)
}

// StartedAtTime 解析 witr 给出的 RFC3339 时间戳。
func (p witrProcess) StartedAtTime() (time.Time, bool) {
	if p.StartedAt == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, p.StartedAt)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

type witrSource struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Description string `json:"Description"`
	UnitFile    string `json:"UnitFile"`
}

// witrErrKind 区分失败的原因，让界面能给出对症的提示。
type witrErrKind int

const (
	witrErrNotFound    witrErrKind = iota // 没找到
	witrErrAmbiguous                      // 名字匹配到多个
	witrErrNoContainer                    // 没有该容器
	witrErrOther                          // 其它
)

// witrLookupError 是一次失败的查询。
type witrLookupError struct {
	kind       witrErrKind
	message    string
	candidates []witrCandidate
}

func (e *witrLookupError) Error() string { return e.message }

// witrCandidate 是名字匹配到多个时的一个候选。
type witrCandidate struct {
	PID  int
	Name string
}

// candidateRe 匹配 witr 在退出码 4 时打印的候选行，形如：
//
//	[1] pwsh.exe (pid 51944)
var candidateRe = regexp.MustCompile(`^\[(\d+)\]\s+(.+?)\s+\(pid\s+(\d+)\)\s*$`)

// parseCandidates 从「Multiple matching processes found」的纯文本里取候选。
// 退出码 4 时 stdout 不是 JSON，直接反序列化会失败，必须单独走这条路。
func parseCandidates(out []byte) []witrCandidate {
	var found []witrCandidate
	for _, line := range strings.Split(string(out), "\n") {
		m := candidateRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		pid, err := strconv.Atoi(m[3])
		if err != nil {
			continue
		}
		found = append(found, witrCandidate{PID: pid, Name: strings.TrimSpace(m[2])})
	}
	return found
}

// firstLine 取错误输出的首行，作为给用户的提示。
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
}

// findWitr 定位 witr 可执行文件，顺序为：
//
//  1. witrPath —— 显式覆盖（测试与排障用）
//  2. 可执行文件同目录的 witr.exe —— 发行时随程序一起安装的内置副本
//  3. PATH 上的 witr —— 开发机上 winget 装的那份
//
// 内置副本优先于 PATH，这样发行版用的一定是随包的那份，不会被用户机器上
// 另一个（可能不兼容的）版本悄悄顶替；同时保留 PATH 回退，开发时无需拷贝副本。
func findWitr() (string, error) {
	if witrPath != "" {
		return witrPath, nil
	}
	if p := bundledWitr(); p != "" {
		return p, nil
	}
	p, err := exec.LookPath("witr")
	if err != nil {
		return "", errors.New(witrInstallHint)
	}
	return p, nil
}

// runWitr 执行一次查询。退出码的含义（实测）：
//
//	0  成功，stdout 是 JSON
//	1  受保护进程（wininit、lsass、services 等），stdout 仍是完整 JSON
//	2  没找到
//	4  名字匹配到多个，stdout 是人类可读的候选列表，不是 JSON
//	5  没有该容器
//
// 退出码 1 带着可用数据，所以不能因为非零就丢弃结果。
func runWitr(target witrTarget, timeout time.Duration) (*witrResult, error) {
	// 空目标会让 witr 等待标准输入而不返回，绝不能调用。
	if strings.TrimSpace(target.value) == "" {
		return nil, errors.New("查询内容为空")
	}
	bin, err := findWitr()
	if err != nil {
		return nil, err
	}
	args := append(target.args(), "--json", "--no-color")

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	// 显式断开标准输入：即便 witr 想读也不会拿到任何东西。
	cmd.Stdin = nil
	// witr.exe 是控制台程序，而本程序是 GUI 程序：不隐藏的话，Windows
	// 会为每次溯源新建一个黑色控制台窗口，跟着查询挂好几秒才消失。
	// HideWindow 加 CREATE_NO_WINDOW 双保险，确保子进程完全无窗口。
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	code := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if ctx.Err() != nil {
			return nil, fmt.Errorf("witr 查询超时（%s）", timeout)
		} else {
			return nil, err
		}
	}

	switch code {
	case 0, 1:
		var r witrResult
		if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("witr 返回了无法解析的结果: %w", err)
		}
		return &r, nil
	case 4:
		// 退出码 4 通常表示名字匹配到多个，此时 stdout 是候选列表。但也有
		// 例外：witr 对非法目标（如 --pid 0）同样返回 4，只是 stdout 为空、
		// 原因写在 stderr 里。没有解析出候选时改用 stderr 的说明。
		cands := parseCandidates(stdout.Bytes())
		if len(cands) == 0 {
			msg := firstLine(stderr.String())
			if msg == "" {
				msg = "witr 无法解析这个查询"
			}
			return nil, &witrLookupError{kind: witrErrOther, message: msg}
		}
		return nil, &witrLookupError{
			kind:       witrErrAmbiguous,
			message:    fmt.Sprintf("匹配到 %d 个进程，请选一个", len(cands)),
			candidates: cands,
		}
	case 5:
		msg := firstLine(stderr.String())
		if msg == "" {
			msg = "没有该容器"
		}
		return nil, &witrLookupError{kind: witrErrNoContainer, message: msg}
	case 2:
		msg := firstLine(stderr.String())
		if msg == "" {
			msg = "没有找到匹配的进程或服务"
		}
		return nil, &witrLookupError{kind: witrErrNotFound, message: msg}
	default:
		msg := firstLine(stderr.String())
		if msg == "" {
			msg = fmt.Sprintf("witr 以退出码 %d 结束", code)
		}
		return nil, &witrLookupError{kind: witrErrOther, message: msg}
	}
}

// isNotFound 报告一次查询失败是否只是「没找到」，用来做降级重试。
func isNotFound(err error) bool {
	var le *witrLookupError
	return errors.As(err, &le) && le.kind == witrErrNotFound
}
