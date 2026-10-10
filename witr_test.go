package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// witrInstalled 报告本机是否装了 witr；没装就跳过依赖它的用例。
func witrInstalled(t *testing.T) string {
	t.Helper()
	p, err := findWitr()
	if err != nil {
		t.Skip("witr 未安装，跳过")
	}
	return p
}

// fakeWitrDir 造一个「安装目录」，里面按需放一个假 witr.exe，并把
// exeDir/witrPath 切过去。返回清理函数。
func fakeWitrDir(t *testing.T, withWitr bool) string {
	t.Helper()
	dir := t.TempDir()
	if withWitr {
		if err := os.WriteFile(filepath.Join(dir, witrExeName), []byte("stub"), 0o755); err != nil {
			t.Fatalf("write stub witr: %v", err)
		}
	}
	prevDir, prevPath := exeDir, witrPath
	exeDir, witrPath = dir, ""
	t.Cleanup(func() { exeDir, witrPath = prevDir, prevPath })
	return dir
}

// TestFindWitrPrefersBundled 确认同目录的内置副本会被选中，而不是 PATH 上那份。
// 这是本次改动的核心保证：发行版必须用随包安装的 witr。
func TestFindWitrPrefersBundled(t *testing.T) {
	dir := fakeWitrDir(t, true)
	got, err := findWitr()
	if err != nil {
		t.Fatalf("findWitr: %v", err)
	}
	want := filepath.Join(dir, witrExeName)
	if got != want {
		t.Errorf("findWitr = %q, want the bundled copy %q", got, want)
	}
}

// TestFindWitrFallsBackToPath 确认内置副本缺席时回退到 PATH。
// 本机装了 witr 才有 PATH 可回退，否则只断言「没选中内置路径」。
func TestFindWitrFallsBackToPath(t *testing.T) {
	dir := fakeWitrDir(t, false)
	got, err := findWitr()
	if err != nil {
		// 本机连 PATH 上也没有，这正是「彻底找不到」的路径，可接受。
		if !strings.Contains(err.Error(), "找不到 witr") {
			t.Errorf("err = %v, want 找不到 witr 提示", err)
		}
		return
	}
	if strings.HasPrefix(got, dir) {
		t.Errorf("findWitr = %q, want a PATH result, not the (empty) install dir", got)
	}
}

// TestFindWitrOverrideWins 确认 witrPath 覆盖优先于一切。
func TestFindWitrOverrideWins(t *testing.T) {
	dir := fakeWitrDir(t, true)
	override := filepath.Join(dir, "somewhere-else.exe")
	prev := witrPath
	witrPath = override
	t.Cleanup(func() { witrPath = prev })

	got, err := findWitr()
	if err != nil {
		t.Fatalf("findWitr: %v", err)
	}
	if got != override {
		t.Errorf("findWitr = %q, want the override %q", got, override)
	}
}

// TestBundledWitrIgnoresDirectory 确认同目录下叫 witr.exe 的目录不会被误当成程序。
func TestBundledWitrIgnoresDirectory(t *testing.T) {
	dir := fakeWitrDir(t, false)
	if err := os.Mkdir(filepath.Join(dir, witrExeName), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got := bundledWitr(); got != "" {
		t.Errorf("bundledWitr = %q, want empty for a directory", got)
	}
}

// TestParseCandidates 覆盖退出码 4 的纯文本解析。这时 stdout 不是 JSON，
// 直接反序列化会失败，必须走单独的分支。
func TestParseCandidates(t *testing.T) {
	out := []byte("Multiple matching processes found:\n\n" +
		"[1] pwsh.exe (pid 51944)\n" +
		"    \"C:\\Program Files\\PowerShell\\7\\pwsh.exe\" -NoLogo\n" +
		"[2] node.exe (pid 5028)\n" +
		"    C:\\node.exe\n" +
		"\nSelect a process by number or use --pid to disambiguate.\n")
	got := parseCandidates(out)
	if len(got) != 2 {
		t.Fatalf("candidates = %d, want 2: %+v", len(got), got)
	}
	if got[0].PID != 51944 || got[0].Name != "pwsh.exe" {
		t.Errorf("first = %+v, want pid 51944 pwsh.exe", got[0])
	}
	if got[1].PID != 5028 || got[1].Name != "node.exe" {
		t.Errorf("second = %+v, want pid 5028 node.exe", got[1])
	}
}

// TestParseCandidatesEmpty 确认非候选文本不会误报。
func TestParseCandidatesEmpty(t *testing.T) {
	if got := parseCandidates([]byte("no process found\n")); len(got) != 0 {
		t.Errorf("candidates = %+v, want none", got)
	}
	if got := parseCandidates(nil); len(got) != 0 {
		t.Errorf("candidates = %+v, want none", got)
	}
}

// TestRunWitrInvalidPID 覆盖退出码 4 的例外：witr 对非法目标（--pid 0）
// 也返回 4，但 stdout 为空、原因在 stderr。这时不能报「匹配到多个」。
func TestRunWitrInvalidPID(t *testing.T) {
	witrInstalled(t)
	_, err := runWitr(witrTarget{kind: witrByPID, value: "0"}, 30*time.Second)
	if err == nil {
		t.Fatal("want error for --pid 0, got nil")
	}
	var le *witrLookupError
	if !errors.As(err, &le) {
		t.Fatalf("err = %T %v, want *witrLookupError", err, err)
	}
	if le.kind == witrErrAmbiguous {
		t.Errorf("kind = witrErrAmbiguous for an invalid pid; message %q", le.message)
	}
	if le.kind != witrErrOther {
		t.Errorf("kind = %v, want witrErrOther", le.kind)
	}
	if len(le.candidates) != 0 {
		t.Errorf("candidates = %+v, want none", le.candidates)
	}
}

// TestRunWitrEmptyTarget 确认空目标不会真的调用 witr。
// witr 不带目标时会等待标准输入而不返回，这里必须挡住。
func TestRunWitrEmptyTarget(t *testing.T) {
	for _, v := range []string{"", "   ", "\t"} {
		_, err := runWitr(witrTarget{kind: witrByName, value: v}, time.Second)
		if err == nil {
			t.Fatalf("value %q: want error, got nil", v)
		}
		if !strings.Contains(err.Error(), "为空") {
			t.Errorf("value %q: err = %v, want 空目标提示", v, err)
		}
	}
}

// TestRunWitrSelf 用本进程 PID 查一次，确认退出码 0 的路径能解析出完整结果。
func TestRunWitrSelf(t *testing.T) {
	witrInstalled(t)
	r, err := runWitr(witrTarget{kind: witrByPID, value: itoa(os.Getpid())}, 30*time.Second)
	if err != nil {
		t.Fatalf("runWitr(self): %v", err)
	}
	if r.Process.PID != os.Getpid() {
		t.Errorf("Process.PID = %d, want %d", r.Process.PID, os.Getpid())
	}
	if r.ResolvedTarget == "" {
		t.Error("ResolvedTarget is empty")
	}
	if len(r.Ancestry) == 0 {
		t.Error("Ancestry is empty, want at least the target itself")
	}
}

// TestRunWitrNotFound 覆盖退出码 2。
func TestRunWitrNotFound(t *testing.T) {
	witrInstalled(t)
	_, err := runWitr(witrTarget{kind: witrByName, value: "zzz-no-such-process-zzz"}, 30*time.Second)
	if err == nil {
		t.Fatal("want error for a missing process, got nil")
	}
	var le *witrLookupError
	if !errors.As(err, &le) {
		t.Fatalf("err = %T %v, want *witrLookupError", err, err)
	}
	if le.kind != witrErrNotFound {
		t.Errorf("kind = %v, want witrErrNotFound", le.kind)
	}
	if !isNotFound(err) {
		t.Error("isNotFound = false, want true")
	}
}

// TestRunWitrPort 覆盖端口查询，这是界面上的一个入口。
func TestRunWitrPort(t *testing.T) {
	witrInstalled(t)
	// 端口 1 上几乎不可能有监听者，用它覆盖「没找到」这条路径，
	// 从而确认端口参数确实被传下去了。
	_, err := runWitr(witrTarget{kind: witrByPort, value: "1"}, 30*time.Second)
	if err == nil {
		t.Skip("端口 1 上意外有监听者，跳过")
	}
	if !isNotFound(err) {
		t.Errorf("err = %v, want not-found for an unused port", err)
	}
}

// TestWitrTargetArgs 确认各类目标拼出的参数正确。
func TestWitrTargetArgs(t *testing.T) {
	cases := []struct {
		t    witrTarget
		want []string
	}{
		{witrTarget{witrByPID, "42"}, []string{"--pid", "42"}},
		{witrTarget{witrByPort, "8080"}, []string{"--port", "8080"}},
		{witrTarget{witrByName, "nginx"}, []string{"nginx"}},
	}
	for _, c := range cases {
		got := c.t.args()
		if len(got) != len(c.want) {
			t.Errorf("%v: args = %v, want %v", c.t, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%v: args = %v, want %v", c.t, got, c.want)
				break
			}
		}
	}
}

// TestProcessDisplayAndTime 覆盖展示层的两个小助手。
func TestProcessDisplayAndTime(t *testing.T) {
	if got := (witrProcess{Command: "node.exe", Exe: "C:/node.exe"}).Display(); got != "node.exe" {
		t.Errorf("Display = %q, want node.exe", got)
	}
	if got := (witrProcess{Exe: "C:/node.exe", PID: 7}).Display(); got != "C:/node.exe" {
		t.Errorf("Display = %q, want the Exe when Command is empty", got)
	}
	if got := (witrProcess{PID: 7}).Display(); got != "PID 7" {
		t.Errorf("Display = %q, want PID 7", got)
	}
	if _, ok := (witrProcess{StartedAt: "not a time"}).StartedAtTime(); ok {
		t.Error("StartedAtTime parsed a bad timestamp")
	}
	if _, ok := (witrProcess{}).StartedAtTime(); ok {
		t.Error("StartedAtTime parsed an empty timestamp")
	}
	if ts, ok := (witrProcess{StartedAt: "2026-10-09T17:20:26+08:00"}).StartedAtTime(); !ok || ts.IsZero() {
		t.Errorf("StartedAtTime = %v, %v; want a parsed time", ts, ok)
	}
}
