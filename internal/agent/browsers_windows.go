//go:build windows

package agent

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type winProcesses struct{}

func newPlatformProcessControl() ProcessControl { return winProcesses{} }

// each calls fn for every process whose executable is one of names.
func (winProcesses) each(names []string, fn func(pid uint32, exe string)) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		exe := windows.UTF16ToString(e.ExeFile[:])
		for _, n := range names {
			if strings.EqualFold(exe, n) {
				fn(e.ProcessID, n)
				break
			}
		}
	}
}

func (p winProcesses) Running(names []string) []string {
	var out []string
	seen := map[string]bool{}
	p.each(names, func(_ uint32, exe string) {
		if !seen[exe] {
			seen[exe] = true
			out = append(out, exe)
		}
	})
	return out
}

func (p winProcesses) Kill(names []string) int {
	n := 0
	p.each(names, func(pid uint32, _ string) {
		h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
		if err != nil {
			return
		}
		defer windows.CloseHandle(h)
		if windows.TerminateProcess(h, 1) == nil {
			n++
			_, _ = windows.WaitForSingleObject(h, 5000)
		}
	})
	return n
}
