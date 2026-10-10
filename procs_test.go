package main

import (
	"strings"
	"testing"
)

// TestListProcesses 确认枚举能拿到本机进程，且本进程在其中。
func TestListProcesses(t *testing.T) {
	procs, err := listProcesses()
	if err != nil {
		t.Fatalf("listProcesses: %v", err)
	}
	if len(procs) < 5 {
		t.Fatalf("got %d processes, want a realistic list", len(procs))
	}
	self := itoa(getpid())
	found := false
	for _, p := range procs {
		if p.Name == "" {
			t.Errorf("process %d has an empty name", p.PID)
		}
		if p.PID <= 0 {
			t.Errorf("process %q has a non-positive PID %d", p.Name, p.PID)
		}
		if itoa(p.PID) == self {
			found = true
		}
	}
	if !found {
		t.Errorf("this process (pid %s) is missing from the list", self)
	}
	// 按名字排序，便于界面直接展示。
	for i := 1; i < len(procs); i++ {
		a, b := strings.ToLower(procs[i-1].Name), strings.ToLower(procs[i].Name)
		if a > b {
			t.Fatalf("list is not sorted by name: %q before %q", a, b)
		}
	}
}

// TestFilterProcs 覆盖名字、PID 与端口三种筛选，以及空查询。
func TestFilterProcs(t *testing.T) {
	procs := []procInfo{
		{PID: 100, Name: "node.exe", Ports: []int{3000}},
		{PID: 200, Name: "nginx.exe", Ports: []int{80, 443}},
		{PID: 300, Name: "pwsh.exe"},
	}
	if got := filterProcs(procs, ""); len(got) != 3 {
		t.Errorf("empty query = %d rows, want all 3", len(got))
	}
	if got := filterProcs(procs, "  "); len(got) != 3 {
		t.Errorf("blank query = %d rows, want all 3", len(got))
	}
	if got := filterProcs(procs, "NODE"); len(got) != 1 || got[0].PID != 100 {
		t.Errorf("name query = %+v, want just pid 100", got)
	}
	if got := filterProcs(procs, "200"); len(got) != 1 || got[0].PID != 200 {
		t.Errorf("pid query = %+v, want just pid 200", got)
	}
	if got := filterProcs(procs, "443"); len(got) != 1 || got[0].PID != 200 {
		t.Errorf("port query = %+v, want just pid 200", got)
	}
	if got := filterProcs(procs, "nothing-matches"); len(got) != 0 {
		t.Errorf("no-match query = %+v, want none", got)
	}
}

// TestItoa 覆盖手写的小整数格式化。
func TestItoa(t *testing.T) {
	cases := map[int]string{0: "0", 1: "1", 42: "42", 1234567: "1234567", -7: "-7"}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Errorf("itoa(%d) = %q, want %q", in, got, want)
		}
	}
}
