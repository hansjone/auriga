//go:build windows && amd64

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWFP32BitStatus(t *testing.T) {
	for _, value := range []uintptr{0x80320003, 0xffffffff80320003} {
		t.Run(fmt.Sprintf("%016x", value), func(t *testing.T) {
			present, e := wfpPresence(value, 0x80320003, "lookup")
			if present || e != nil {
				t.Fatalf("missing filter: present=%v err=%v", present, e)
			}
			if status32(value) != 0x80320003 {
				t.Fatal("wrong low DWORD")
			}
		})
	}
	present, e := wfpPresence(0, 0x80320003, "lookup")
	if !present || e != nil {
		t.Fatal("existing filter misclassified")
	}
	if _, e = wfpPresence(0xffffffff80320005, 0x80320003, "lookup"); e == nil || !strings.Contains(e.Error(), "80320005") {
		t.Fatal("unrelated WFP failure suppressed")
	}
}
func TestProcessLimitSemantics(t *testing.T) {
	base := processLimitSample{RawPIDs: []uintptr{1, 2, 3, 4}, Active: 4, Processes: []processEntry{{PID: 1, Alive: true}, {PID: 2, Alive: true}, {PID: 3, Alive: true}, {PID: 4, Alive: true}}}
	cases := []struct {
		name   string
		sample processLimitSample
		code   uint32
		want   bool
	}{{"four_live_and_quota_denial", base, 1816, true}, {"different_creation_error", base, 5, false}}
	extra := base
	extra.RawPIDs = append(append([]uintptr(nil), base.RawPIDs...), 5)
	extra.Processes = append(append([]processEntry(nil), base.Processes...), processEntry{PID: 5, Image: "s3probe.exe", Alive: false})
	cases = append(cases, struct {
		name   string
		sample processLimitSample
		code   uint32
		want   bool
	}{"terminated_rejected_process_in_raw_list", extra, 1816, true})
	console := extra
	console.Processes = append([]processEntry(nil), extra.Processes...)
	console.Processes[4] = processEntry{PID: 5, Image: "conhost.exe", Alive: true}
	cases = append(cases, struct {
		name   string
		sample processLimitSample
		code   uint32
		want   bool
	}{"live_conhost_not_exempt", console, 1816, false})
	excessive := extra
	excessive.Active = 5
	cases = append(cases, struct {
		name   string
		sample processLimitSample
		code   uint32
		want   bool
	}{"five_accounted_active", excessive, 1816, false})
	missing := base
	missing.Processes = base.Processes[:3]
	cases = append(cases, struct {
		name   string
		sample processLimitSample
		code   uint32
		want   bool
	}{"missing_workload_process", missing, 1816, false})
	unknown := extra
	unknown.Processes = append([]processEntry(nil), extra.Processes...)
	unknown.Processes[4].Error = "access denied"
	cases = append(cases, struct {
		name   string
		sample processLimitSample
		code   uint32
		want   bool
	}{"uninspectable_pid_not_ignored", unknown, 1816, false})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := processLimitSatisfied(4, 1, []uint32{2, 3, 4}, c.code, c.sample); got != c.want {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}
func TestUnicodeEnvironment(t *testing.T) {
	source := map[string]string{"WINDIR": `C:\Windows`, "LOCALAPPDATA": `C:\Users\alice\AppData\Local`, "APPDATA": `C:\Users\alice\AppData\Roaming`, "TEMP": `C:\测试目录`}
	block, e := encodeEnvironment(source)
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := decodeEnvironment(block)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(decoded, source) {
		t.Fatal("Unicode environment round trip failed")
	}
	if windows.UTF16ToString(block) != "APPDATA="+source["APPDATA"] {
		t.Fatal("environment is not sorted")
	}
	empty, e := encodeEnvironment(map[string]string{})
	if e != nil || !reflect.DeepEqual(empty, []uint16{0, 0}) {
		t.Fatal("empty block is not double-NUL")
	}
	for _, invalid := range []map[string]string{{"BAD=KEY": "x"}, {"X": "bad\x00value"}} {
		if _, e = encodeEnvironment(invalid); e == nil {
			t.Fatal("invalid entry accepted")
		}
	}
	if _, e = decodeEnvironment([]uint16{'X', '=', 'a', 0}); e == nil {
		t.Fatal("single-NUL block accepted")
	}
}
func TestAppEnvironmentProfileAndSecrets(t *testing.T) {
	target := map[string]string{"SYSTEMROOT": `C:\Windows`, "USERPROFILE": `C:\Users\alice`, "LOCALAPPDATA": `C:\Users\alice\AppData\Local`, "ACTIONS_RUNTIME_TOKEN": "must-not-leak"}
	owner := map[string]string{"LOCALAPPDATA": `C:\Windows\System32\config\systemprofile\AppData\Local`, "SECRET": "must-not-leak"}
	values, e := appEnvironmentValues(target, owner, `C:\scratch`)
	if e != nil {
		t.Fatal(e)
	}
	if values["LOCALAPPDATA"] != owner["LOCALAPPDATA"] || values["USERPROFILE"] != target["USERPROFILE"] {
		t.Fatal("wrong profile environment source")
	}
	if _, ok := values["ACTIONS_RUNTIME_TOKEN"]; ok {
		t.Fatal("runner credential leaked")
	}
	if _, ok := values["SECRET"]; ok {
		t.Fatal("arbitrary owner environment leaked")
	}
	if values["TEMP"] != `C:\scratch` {
		t.Fatal("wrong scratch")
	}
	if _, e = appEnvironmentValues(map[string]string{}, map[string]string{}, `C:\scratch`); e == nil {
		t.Fatal("missing required profile variables accepted")
	}
}
func TestQuotaDataACLAndAbsentFile(t *testing.T) {
	sid := "S-1-5-21-100-200-300-1001"
	sd, e := windows.SecurityDescriptorFromString(instanceACL(sid))
	if e != nil {
		t.Fatal(e)
	}
	acl, _, e := sd.DACL()
	if e != nil {
		t.Fatal(e)
	}
	if acl.AceCount != 3 {
		t.Fatal("volume data DACL principals changed")
	}
	path := filepath.Join(t.TempDir(), "large.bin")
	if e = removeExistingFile(path); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("synthetic"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = removeExistingFile(path); e != nil {
		t.Fatal(e)
	}
	if e = removeExistingFile(path); e != nil {
		t.Fatal("idempotent cleanup failed", e)
	}
}
