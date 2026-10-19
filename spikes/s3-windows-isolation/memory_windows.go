//go:build windows && amd64

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// Native commits avoid Go's allocator/GC retry path near a hard Job limit. Prepare
// all progress records first; free commits before allocating the final JSON report.
func memoryWork(out string) map[string]any {
	result := map[string]any{"started": true, "allocator": "VirtualAlloc MEM_RESERVE|MEM_COMMIT", "targetMiB": 192}
	if e := writeJSON(out, result); e != nil {
		return map[string]any{"error": e.Error()}
	}
	progress, e := os.OpenFile(out+".progress.jsonl", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		result["error"] = e.Error()
		return result
	}
	defer progress.Close()
	var records [193][]byte
	for i := range records {
		records[i] = []byte(fmt.Sprintf("{\"committedMiB\":%d,\"nextAttemptMiB\":%d}\n", i, i+1))
	}
	fill := windows.NewLazySystemDLL("ntdll.dll").NewProc("RtlFillMemory")
	if e = fill.Find(); e != nil {
		result["error"] = e.Error()
		return result
	}
	var blocks [192]uintptr
	allocated := 0
	var allocationError error
	var progressError error
	for allocated < len(blocks) {
		if _, progressError = progress.Write(records[allocated]); progressError != nil {
			break
		}
		if progressError = progress.Sync(); progressError != nil {
			break
		}
		address, ae := windows.VirtualAlloc(0, 1<<20, windows.MEM_COMMIT|windows.MEM_RESERVE, windows.PAGE_READWRITE)
		if ae != nil {
			allocationError = ae
			break
		}
		fill.Call(address, 1<<20, 1) // Touch every page, outside the Go heap.
		blocks[allocated] = address
		allocated++
	}
	var releaseError error
	for _, address := range blocks[:allocated] {
		if fe := windows.VirtualFree(address, 0, windows.MEM_RELEASE); fe != nil {
			releaseError = join(releaseError, fe)
		}
	}
	result["allocatedMiB"] = allocated
	result["rejected"] = allocationError != nil
	result["allocationWin32Error"] = errorCode(allocationError)
	result["allocationError"] = errorString(allocationError)
	result["progressError"] = errorString(progressError)
	result["releaseError"] = errorString(releaseError)
	result["finished"] = progressError == nil && releaseError == nil
	return result
}

func memoryRejection(data map[string]any) bool {
	allocated, _ := data["allocatedMiB"].(float64)
	code, _ := data["allocationWin32Error"].(float64)
	return data["finished"] == true && data["rejected"] == true && allocated >= 16 && allocated < 192 &&
		(code == 8 || code == 1455 || code == 1816)
}
