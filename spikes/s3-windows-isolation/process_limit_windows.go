//go:build windows && amd64

package main

import (
	"errors"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processEntry struct {
	PID                uint32  `json:"pid"`
	Image              string  `json:"image"`
	Alive              bool    `json:"alive"`
	Error              string  `json:"error,omitempty"`
	PrivateCommitBytes uintptr `json:"privateCommitBytes"`
	WorkingSetBytes    uintptr `json:"workingSetBytes"`
	MemoryError        string  `json:"memoryError,omitempty"`
}
type processLimitSample struct {
	RawPIDs            []uintptr      `json:"rawPIDs"`
	Active             uint32         `json:"accountingActive"`
	Processes          []processEntry `json:"processes"`
	PeakJobCommitBytes uintptr        `json:"peakJobCommitBytes"`
}

// PROCESS_MEMORY_COUNTERS_EX, Windows amd64 SDK layout (80 bytes).
type processMemoryCounters struct {
	Size, PageFaults                                                     uint32
	PeakWorkingSet, WorkingSet, PeakPagedPool, PagedPool                 uintptr
	PeakNonpagedPool, NonpagedPool, Pagefile, PeakPagefile, PrivateUsage uintptr
}

func consoleFlags(mode string) uint32 {
	switch mode {
	case "inherit-console":
		return 0
	case "no-window":
		return windows.CREATE_NO_WINDOW
	default:
		return windows.DETACHED_PROCESS
	}
}

func inspectProcesses(ids []uintptr) []processEntry {
	entries := make([]processEntry, 0, len(ids))
	for _, id := range ids {
		entry := processEntry{PID: uint32(id)}
		h, e := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(id))
		if e != nil {
			if !errors.Is(e, windows.ERROR_INVALID_PARAMETER) && !errors.Is(e, windows.ERROR_NOT_FOUND) {
				entry.Error = e.Error()
			}
			entries = append(entries, entry)
			continue
		}
		state, e := windows.WaitForSingleObject(h, 0)
		entry.Alive = e == nil && state == uint32(windows.WAIT_TIMEOUT)
		if e != nil {
			entry.Error = e.Error()
		}
		var image [32768]uint16
		size := uint32(len(image))
		if ie := windows.QueryFullProcessImageName(h, 0, &image[0], &size); ie == nil {
			entry.Image = filepath.Base(windows.UTF16ToString(image[:size]))
		} else if entry.Alive {
			entry.Error = ie.Error()
		}
		var memory processMemoryCounters
		memory.Size = uint32(unsafe.Sizeof(memory))
		if me := boolCall(kernel.NewProc("K32GetProcessMemoryInfo"), uintptr(h), uintptr(unsafe.Pointer(&memory)), uintptr(memory.Size)); me == nil {
			entry.PrivateCommitBytes = memory.PrivateUsage
			entry.WorkingSetBytes = memory.WorkingSet
		} else {
			entry.MemoryError = me.Error()
		}
		_ = windows.CloseHandle(h)
		entries = append(entries, entry)
	}
	return entries
}
func processLimitSatisfied(limit, root uint32, children []uint32, spawnError uint32, s processLimitSample) bool {
	if limit == 0 || uint32(len(children))+1 != limit || spawnError != 1816 || s.Active != limit {
		return false
	}
	expected := map[uint32]bool{root: false}
	for _, pid := range children {
		if _, exists := expected[pid]; exists {
			return false
		}
		expected[pid] = false
	}
	live := uint32(0)
	for _, entry := range s.Processes {
		if entry.Error != "" {
			return false
		}
		if !entry.Alive {
			continue
		}
		live++
		if _, known := expected[entry.PID]; !known {
			return false
		}
		expected[entry.PID] = true
	}
	if live != limit {
		return false
	}
	for _, seen := range expected {
		if !seen {
			return false
		}
	}
	return true
}
