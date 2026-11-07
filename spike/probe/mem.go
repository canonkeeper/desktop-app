// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/process"
)

// processTreeMemory sums the resident memory of a process and its descendants. On Linux it
// also sums PSS, which splits pages shared between processes (PostgreSQL shared buffers,
// shared libraries) instead of counting them once per process.
func processTreeMemory(pid int32, extra func(*process.Process) bool) map[string]any {
	root, err := process.NewProcess(pid)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	seen := map[int32]bool{}
	var walk func(p *process.Process)
	members := []*process.Process{}
	walk = func(p *process.Process) {
		if seen[p.Pid] {
			return
		}
		seen[p.Pid] = true
		members = append(members, p)
		children, _ := p.Children()
		for _, child := range children {
			walk(child)
		}
	}
	walk(root)
	if extra != nil {
		all, _ := process.Processes()
		for _, p := range all {
			if !seen[p.Pid] && extra(p) {
				walk(p)
			}
		}
	}
	var rss, pss uint64
	names := map[string]int{}
	for _, p := range members {
		if info, err := p.MemoryInfo(); err == nil {
			rss += info.RSS
		}
		if runtime.GOOS == "linux" {
			pss += linuxPSS(p.Pid)
		}
		if name, err := p.Name(); err == nil {
			names[name]++
		}
	}
	result := map[string]any{"processes": len(members), "rss_bytes": rss, "names": names}
	if runtime.GOOS == "linux" {
		result["pss_bytes"] = pss
	}
	return result
}

func linuxPSS(pid int32) uint64 {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(int(pid)) + "/smaps_rollup")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "Pss:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseUint(fields[1], 10, 64)
				return kb * 1024
			}
		}
	}
	return 0
}
