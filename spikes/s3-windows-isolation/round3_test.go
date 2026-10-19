//go:build windows && amd64

package main

import (
	"encoding/binary"
	"golang.org/x/sys/windows"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"
)

func TestPrivilegedObjectOwnerAndRights(t *testing.T) {
	for name, sddl := range map[string]string{"job": jobACL, "root": instanceACL("S-1-5-21-100-200-300-1001"), "data": dataACL("S-1-5-21-100-200-300-1001")} {
		t.Run(name, func(t *testing.T) {
			sd, e := windows.SecurityDescriptorFromString(sddl)
			if e != nil {
				t.Fatal(e)
			}
			owner, _, e := sd.Owner()
			if e != nil || owner.String() != "S-1-5-18" {
				t.Fatal("owner must be SYSTEM")
			}
			if !strings.Contains(sd.String(), "OW") {
				t.Fatal("missing OWNER RIGHTS ACE")
			}
		})
	}
	if strings.Contains(jobACL, "S-1-5-21-") {
		t.Fatal("job ACL grants instance access")
	}
	if !strings.Contains(dataACL("S-1-5-21-100-200-300-1001"), "OICIIO") {
		t.Fatal("child Modify must be inherit-only")
	}
}
func TestPayloadJobCannotBreakAway(t *testing.T) {
	limits := jobLimits(64<<20, 4)
	flags := limits.BasicLimitInformation.LimitFlags
	if flags&(windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK|windows.JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK) != 0 {
		t.Fatal("payload may break away")
	}
	required := uint32(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION | windows.JOB_OBJECT_LIMIT_JOB_MEMORY | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS)
	if flags&required != required || limits.JobMemoryLimit != 64<<20 || limits.BasicLimitInformation.ActiveProcessLimit != 4 {
		t.Fatal("missing hardening/limits")
	}
}
func TestDedicatedWFPPolicyABI(t *testing.T) {
	if unsafe.Sizeof(wfpProvider{}) != 64 || unsafe.Sizeof(wfpSubLayer{}) != 72 || unsafe.Offsetof(wfpSubLayer{}.Weight) != 64 {
		t.Fatal("SDK provider/sublayer ABI mismatch")
	}
	for _, c := range []struct {
		allow, persist bool
		flags          uint32
	}{{false, true, 9}, {false, false, 8}, {true, true, 1}, {true, false, 0}} {
		if got := policyFlags(c.allow, c.persist); got != c.flags {
			t.Fatalf("flags=%x want=%x", got, c.flags)
		}
	}
}
func TestJunctionEncoding(t *testing.T) {
	b := junctionBuffer(`C:\probe`)
	if binary.LittleEndian.Uint32(b) != 0xa0000003 || int(binary.LittleEndian.Uint16(b[4:])) != len(b)-8 {
		t.Fatal("invalid mount-point tag or length")
	}
	subLen := binary.LittleEndian.Uint16(b[10:])
	printOffset := binary.LittleEndian.Uint16(b[12:])
	if printOffset != subLen+2 {
		t.Fatal("missing UTF-16 separator")
	}
	var sub []uint16
	for i := 16; i < 16+int(subLen); i += 2 {
		sub = append(sub, binary.LittleEndian.Uint16(b[i:]))
	}
	if windows.UTF16ToString(sub) != `\??\C:\probe` {
		t.Fatal("incorrect NT substitute name")
	}
}
func TestWorkloadEnvironmentHasNoRunnerMarkers(t *testing.T) {
	t.Setenv("SystemRoot", `C:\Windows`)
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("ACTIONS_RUNTIME_TOKEN", "synthetic-secret")
	values := workloadEnvironment(`C:\scratch`)
	if len(values) != 5 {
		t.Fatal("unexpected payload environment")
	}
	for key := range values {
		if strings.Contains(key, "GITHUB") || strings.Contains(key, "TOKEN") {
			t.Fatal("runner environment leaked")
		}
	}
	block, e := encodeEnvironment(values)
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := decodeEnvironment(block)
	if e != nil || decoded["TEMP"] != `C:\scratch` {
		t.Fatal("payload environment invalid")
	}
}
func TestJournalServiceScope(t *testing.T) {
	o := options{ID: "0123456789", Root: filepath.Join(t.TempDir(), "unused")}
	j := journal{Name: "s3" + o.ID + "s", Group: "s3" + o.ID + "sg", Dir: filepath.Join(o.Root, "s"), Job: `Global\auriga-s3-` + o.ID + "-s", Services: []string{"AurigaS3-" + o.ID + "-shim"}}
	if e := validateJournal(o, j); e != nil {
		t.Fatal(e)
	}
	j.Services = []string{"Schedule"}
	if validateJournal(o, j) == nil {
		t.Fatal("accepted unrelated Windows service")
	}
	j.Services = nil
	j.SID = "S-1-5-18"
	if validateJournal(o, j) == nil {
		t.Fatal("accepted SYSTEM as disposable account")
	}
	j.SID = ""
	j.Provider = "{00000000-0000-0000-0000-000000000001}"
	if validateJournal(o, j) == nil {
		t.Fatal("accepted incomplete WFP policy intent")
	}
}
func TestDenialRequiresSpecificWin32Code(t *testing.T) {
	for _, code := range []float64{0, 2, 1460, 10060, 10061} {
		if win32Code(map[string]any{"win32Error": code}) == 10013 {
			t.Fatal("unrelated error accepted as WFP denial")
		}
	}
	if win32Code(map[string]any{"win32Error": float64(10013)}) != 10013 {
		t.Fatal("lost WSAEACCES")
	}
}
