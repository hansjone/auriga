//go:build windows && amd64

package main

import (
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestMemoryRejectionEvidence(t *testing.T) {
	for _, c := range []struct {
		name                     string
		allocated, code          float64
		finished, rejected, want bool
	}{
		{"out_of_memory", 80, 8, true, true, true},
		{"commitment_limit", 80, 1455, true, true, true},
		{"quota", 80, 1816, true, true, true},
		{"access_denied_is_not_memory_limit", 80, 5, true, true, false},
		{"timeout_is_not_denial", 80, 8, false, true, false},
		{"full_allocation_is_not_denial", 192, 8, true, true, false},
		{"startup_failure_is_not_denial", 0, 8, true, true, false},
		{"success_is_not_denial", 80, 0, true, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			data := map[string]any{"allocatedMiB": c.allocated, "allocationWin32Error": c.code, "finished": c.finished, "rejected": c.rejected}
			if memoryRejection(data) != c.want {
				t.Fatal("incorrect memory denial classification", data)
			}
		})
	}
}

func TestAppReportCompletion(t *testing.T) {
	for _, c := range []struct {
		name string
		data map[string]any
		want bool
	}{
		{"precreated_file", map[string]any{}, false},
		{"bootstrap_only", map[string]any{"bootstrap": true}, false},
		{"started_before_network_callbacks", map[string]any{"nodeStarted": true, "shell": map[string]any{}}, false},
		{"both_network_callbacks_denied", map[string]any{"nodeStarted": true, "shell": map[string]any{"ok": false}, "listen": false, "outboundLoopback": false}, true},
		{"successful_callbacks", map[string]any{"nodeStarted": true, "shell": map[string]any{"ok": true}, "listen": true, "outboundLoopback": true}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if appReportReady(c.data) != c.want {
				t.Fatal("incorrect completion classification")
			}
		})
	}
}

func TestAppPermissionsDualPrincipals(t *testing.T) {
	account, container := "S-1-5-21-100-200-300-1001", "S-1-15-2-1-2-3-4-5-6-7"
	for name, sddl := range map[string]string{"code": appReadACL(account, container), "output": appWriteACL(account, container)} {
		t.Run(name, func(t *testing.T) {
			sd, e := windows.SecurityDescriptorFromString(sddl)
			if e != nil {
				t.Fatal(e)
			}
			acl, _, e := sd.DACL()
			if e != nil || acl.AceCount != 5 {
				t.Fatal("unexpected DACL")
			}
			if !strings.Contains(sddl, account) || !strings.Contains(sddl, container) || strings.Contains(sddl, "S-1-15-2-1)") {
				t.Fatal("missing principal or broad package grant")
			}
			for i := uint32(3); i < uint32(acl.AceCount); i++ {
				var header *windows.ACCESS_ALLOWED_ACE
				if e = windows.GetAce(acl, i, &header); e != nil {
					t.Fatal(e)
				}
				mask := header.Mask
				if mask&(windows.WRITE_DAC|windows.WRITE_OWNER) != 0 {
					t.Fatal("payload can change permissions")
				}
				if name == "code" && mask&(windows.FILE_WRITE_DATA|windows.FILE_APPEND_DATA|windows.DELETE) != 0 {
					t.Fatal("payload can change executable code")
				}
				if name == "output" && mask&windows.FILE_WRITE_DATA == 0 {
					t.Fatal("output not writable")
				}
			}
		})
	}
}

func TestMemoryCountersSDKLayout(t *testing.T) {
	var m processMemoryCounters
	if unsafe.Sizeof(m) != 80 || unsafe.Offsetof(m.WorkingSet) != 16 || unsafe.Offsetof(m.PrivateUsage) != 72 {
		t.Fatal("PROCESS_MEMORY_COUNTERS_EX amd64 layout mismatch")
	}
}

func TestBoundedDiagnosticLogs(t *testing.T) {
	data := diagnosticText(strings.NewReader(strings.Repeat("x", 40000)))
	if data["truncated"] != true || len(data["text"].(string)) != 32768 || data["error"] != "" {
		t.Fatal(data["error"])
	}
	data = diagnosticText(strings.NewReader("startup error"))
	if data["text"] != "startup error" || data["truncated"] != false {
		t.Fatal("short diagnostic changed")
	}
}
