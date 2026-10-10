package main

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// TestDiagIconInventory is a temporary diagnostic: it lists every process whose
// exe path resolves but whose icon does not, so the CI-only host set can be
// derived from data instead of guessed.
func TestDiagIconInventory(t *testing.T) {
	procs, err := listProcesses()
	if err != nil {
		t.Fatal(err)
	}
	c := newIconCache()
	noIcon := map[string]int{}
	withIcon := map[string]int{}
	total, ok := 0, 0
	for _, p := range procs {
		exe := processExePath(uint32(p.PID))
		if exe == "" {
			continue
		}
		total++
		if c.get(exe) != nil {
			ok++
			withIcon[strings.ToLower(p.Name)]++
		} else {
			noIcon[strings.ToLower(p.Name)]++
		}
	}
	keys := func(m map[string]int) []string {
		var k []string
		for n := range m {
			k = append(k, n)
		}
		sort.Slice(k, func(i, j int) bool { return m[k[i]] > m[k[j]] })
		return k
	}
	fmt.Printf("DIAG total=%d withIcon=%d\n", total, ok)
	fmt.Println("DIAG --- NO ICON (count name) ---")
	for _, n := range keys(noIcon) {
		fmt.Printf("DIAG noicon %3d %s\n", noIcon[n], n)
	}
	fmt.Println("DIAG --- WITH ICON (count name) ---")
	for _, n := range keys(withIcon) {
		fmt.Printf("DIAG yesicon %3d %s\n", withIcon[n], n)
	}
}
