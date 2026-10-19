//go:build windows && amd64

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWFPConditionEncoding(t *testing.T) {
	blob := byteBlob{}
	v := userSDValue(&blob)
	if v.Type != 14 || v.Value != uintptr(unsafe.Pointer(&blob)) {
		t.Fatal("ALE_USER_ID must use an SD blob, not SID or address-mask type")
	}
	c := endpointConditions(443)
	if len(c) != 2 || c[0].Value.Type != 3 || c[0].Value.Value != 0x7f000001 || c[1].Value.Type != 2 || c[1].Value.Value != 443 {
		t.Fatal("incorrect IPv4 host-order integer or uint16 port encoding")
	}
	if !strings.EqualFold(c[0].Key.String(), "{B235AE9A-1D64-49B8-A44C-5FF3D9095045}") {
		t.Fatal("incorrect remote address condition GUID")
	}
}
func TestJournalExcludesCredentials(t *testing.T) {
	f := fixture{password: "synthetic-password-must-not-serialize", journal: journal{Name: "synthetic-account"}}
	b, e := json.Marshal(f.journal)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), f.password) || strings.Contains(strings.ToLower(string(b)), "password") {
		t.Fatal("credentials serialized")
	}
}
func TestAggregation(t *testing.T) {
	cases := []struct {
		Name   string
		Checks []Check
		Err    error
		Want   string
	}{{"empty", nil, nil, "error"}, {"pass", []Check{{Status: "pass"}}, nil, "pass"}, {"observation", []Check{{Status: "observed"}}, nil, "pass"}, {"unsupported", []Check{{Status: "unsupported"}}, nil, "unsupported"}, {"failure_overrides_unsupported", []Check{{Status: "unsupported"}, {Status: "fail"}}, nil, "fail"}, {"error_overrides_pass", []Check{{Status: "pass"}}, errors.New("failure"), "error"}}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if got := aggregate(c.Checks, c.Err); got != c.Want {
				t.Fatalf("got %s want %s", got, c.Want)
			}
		})
	}
}
func TestSDKLayouts(t *testing.T) {
	// These are Windows SDK x64 offsets, independent of values used by our encoder.
	cases := []struct {
		Name      string
		Got, Want uintptr
	}{{"FWP_VALUE0", unsafe.Sizeof(fwpValue{}), 16}, {"FWP_BYTE_BLOB", unsafe.Sizeof(byteBlob{}), 16}, {"FWPM_FILTER_CONDITION0", unsafe.Sizeof(filterCondition{}), 40}, {"FWPM_ACTION0", unsafe.Sizeof(filterAction{}), 20}, {"FWPM_FILTER0", unsafe.Sizeof(wfpFilter{}), 200}, {"filter.conditions", unsafe.Offsetof(wfpFilter{}.Conditions), 120}, {"filter.action", unsafe.Offsetof(wfpFilter{}.Action), 128}, {"filter.context", unsafe.Offsetof(wfpFilter{}.Context), 152}, {"filter.id", unsafe.Offsetof(wfpFilter{}.ID), 176}, {"FWPM_SESSION0", unsafe.Sizeof(wfpSession{}), 72}, {"SECURITY_CAPABILITIES", unsafe.Sizeof(securityCapabilities{}), 24}, {"LSA_UNICODE_STRING", unsafe.Sizeof(lsaString{}), 16}, {"LSA_OBJECT_ATTRIBUTES", unsafe.Sizeof(lsaObject{}), 48}, {"JOBOBJECT_BASIC_ACCOUNTING_INFORMATION", unsafe.Sizeof(accounting{}), 48}}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if c.Got != c.Want {
				t.Fatalf("got %d want SDK %d", c.Got, c.Want)
			}
		})
	}
}
func TestSIDAndSDDL(t *testing.T) {
	sid, e := windows.StringToSid("S-1-5-21-100-200-300-1001")
	if e != nil {
		t.Fatal(e)
	}
	if sid.String() != "S-1-5-21-100-200-300-1001" {
		t.Fatal("SID round trip")
	}
	sd, e := windows.SecurityDescriptorFromString(instanceACL(sid.String()))
	if e != nil {
		t.Fatal(e)
	}
	a, _, e := sd.DACL()
	if e != nil {
		t.Fatal(e)
	}
	if a.AceCount != 4 {
		t.Fatalf("ACL has %d entries", a.AceCount)
	}
	control, _, e := sd.Control()
	if e != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("DACL must be protected")
	}
	if !strings.Contains(sd.String(), sid.String()) {
		t.Fatal("missing instance SID")
	}
}
func TestCIGate(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("RUNNER_ENVIRONMENT", "")
	if gate(true, false) == nil {
		t.Fatal("local invocation accepted")
	}
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("RUNNER_ENVIRONMENT", "self-hosted")
	if gate(true, false) == nil {
		t.Fatal("self-hosted runner accepted")
	}
	t.Setenv("RUNNER_ENVIRONMENT", "github-hosted")
	if gate(false, false) == nil {
		t.Fatal("missing acknowledgment accepted")
	}
	if e := gate(true, false); e != nil {
		t.Fatal(e)
	}
}
func TestNamespaceValidation(t *testing.T) {
	o := options{ID: "0123456789", Root: filepath.Join(os.Getenv("ProgramData"), "auriga-s3-0123456789")}
	if e := validateOptions(o); e != nil {
		t.Fatal(e)
	}
	j := journal{Name: "s3" + o.ID + "i", Group: "s3" + o.ID + "ig", Dir: filepath.Join(o.Root, "i"), Job: `Global\auriga-s3-` + o.ID + "-i"}
	if e := validateJournal(o, j); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{`C:\`, o.Root + "-other", filepath.Dir(o.Root), filepath.Join(o.Root, "..", "victim")} {
		b := j
		b.Dir = bad
		if validateJournal(o, b) == nil {
			t.Fatalf("accepted unsafe directory %s", bad)
		}
	}
	b := j
	b.Name = "existing-user"
	if validateJournal(o, b) == nil {
		t.Fatal("accepted external user")
	}
	b = j
	b.VHD = filepath.Join(o.Root, "outside.vhd")
	if validateJournal(o, b) == nil {
		t.Fatal("accepted external VHD")
	}
}
