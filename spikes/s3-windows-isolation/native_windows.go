//go:build windows && amd64

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var adv = windows.NewLazySystemDLL("advapi32.dll")
var netapi = windows.NewLazySystemDLL("netapi32.dll")
var kernel = windows.NewLazySystemDLL("kernel32.dll")
var userenv = windows.NewLazySystemDLL("userenv.dll")

func u16(s string) *uint16 { return windows.StringToUTF16Ptr(s) }

// Windows DWORD/HRESULT/NTSTATUS returns are 32-bit even when an x64 thunk sign-extends RAX.
func status32(v uintptr) uint32 { return uint32(v) }

//go:uintptrescapes
func statusCall(p *windows.LazyProc, args ...uintptr) error {
	v, _, _ := p.Call(args...)
	if v != 0 {
		return fmt.Errorf("%s: 0x%08x", p.Name, status32(v))
	}
	return nil
}

//go:uintptrescapes
func boolCall(p *windows.LazyProc, args ...uintptr) error {
	v, _, e := p.Call(args...)
	if v == 0 {
		return fmt.Errorf("%s: %w", p.Name, e)
	}
	return nil
}
func randomHex(n int) string {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func writeJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	tmp := path + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return windows.MoveFileEx(u16(tmp), u16(path), windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
func readJSON(path string, v any) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}
func dacl(path, sddl string) error {
	sd, e := windows.SecurityDescriptorFromString(sddl)
	if e != nil {
		return e
	}
	a, _, e := sd.DACL()
	if e != nil {
		return e
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, a, nil)
}

const adminACL = "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

func instanceACL(sid string) string { return adminACL + "(A;OICI;FA;;;" + sid + ")" }
func command(name string, args ...string) (string, error) {
	c := exec.Command(name, args...)
	b, e := c.CombinedOutput()
	return string(b), e
}

// No password is ever serialized. The journal is written BEFORE resource creation.
type journal struct {
	Name, Group, Dir, Job, Profile string
	Filters                        []string
	VHD                            string
	SID                            string
	Loopback                       bool
	Jobs                           []string
}
type fixture struct {
	journal
	o           options
	password    string
	sid         *windows.SID
	token       windows.Token
	journalPath string
}

func (f *fixture) save() error { return writeJSON(f.journalPath, f.journal) }
func (f *fixture) job(name string, memory uintptr, pids, cpu uint32) (windows.Handle, error) {
	f.Jobs = append(f.Jobs, name)
	if e := f.save(); e != nil {
		return 0, e
	}
	return newJob(name, memory, pids, cpu)
}
func newFixture(o options, tag string) (*fixture, error) {
	if e := os.MkdirAll(o.Root, 0700); e != nil {
		return nil, e
	}
	if e := dacl(o.Root, adminACL+"(A;;GRGX;;;AU)"); e != nil {
		return nil, e
	}
	if e := dacl(filepath.Join(o.Root, "tools"), adminACL+"(A;OICI;GRGX;;;AU)"); e != nil {
		return nil, e
	}
	name := "s3" + o.ID + tag
	f := &fixture{o: o, journal: journal{Name: name, Group: name + "g", Dir: filepath.Join(o.Root, tag), Job: `Global\auriga-s3-` + o.ID + "-" + tag}, password: "Aa1!" + randomHex(32), journalPath: filepath.Join(o.Root, tag+".state.json")}
	if e := f.save(); e != nil {
		return nil, e
	}
	fail := func(e error) (*fixture, error) { return nil, join(e, f.close()) }
	type groupInfo struct{ Name, Comment *uint16 }
	g := groupInfo{u16(f.Group), u16("Auriga disposable S3 probe")}
	if e := statusCall(netapi.NewProc("NetLocalGroupAdd"), 0, 1, uintptr(unsafe.Pointer(&g)), 0); e != nil {
		return fail(e)
	}
	type userInfo struct {
		Name, Password    *uint16
		PasswordAge, Priv uint32
		Home, Comment     *uint16
		Flags             uint32
		Script            *uint16
	}
	ui := userInfo{Name: u16(name), Password: u16(f.password), Priv: 1, Comment: u16("Auriga disposable S3 probe"), Flags: 0x1 | 0x200 | 0x10000}
	if e := statusCall(netapi.NewProc("NetUserAdd"), 0, 1, uintptr(unsafe.Pointer(&ui)), 0); e != nil {
		return fail(e)
	}
	sid, _, _, e := windows.LookupSID("", name)
	if e != nil {
		return fail(e)
	}
	f.sid = sid
	f.SID = sid.String()
	if e = f.save(); e != nil {
		return fail(e)
	}
	member := struct{ SID *windows.SID }{sid}
	if e = statusCall(netapi.NewProc("NetLocalGroupAddMembers"), 0, uintptr(unsafe.Pointer(u16(f.Group))), 0, uintptr(unsafe.Pointer(&member)), 1); e != nil {
		return fail(e)
	}
	groups, e := directGroups(name)
	if e != nil {
		return fail(e)
	}
	for _, group := range groups {
		if !strings.EqualFold(group, f.Group) {
			if e = statusCall(netapi.NewProc("NetLocalGroupDelMembers"), 0, uintptr(unsafe.Pointer(u16(group))), 0, uintptr(unsafe.Pointer(&member)), 1); e != nil {
				return fail(e)
			}
		}
	}
	// Only the dedicated local group is joined; rights are scoped to this disposable SID.
	if e = accountRights(sid, []string{"SeBatchLogonRight", "SeDenyInteractiveLogonRight", "SeDenyRemoteInteractiveLogonRight"}, false); e != nil {
		return fail(e)
	}
	if e = os.MkdirAll(f.Dir, 0700); e != nil {
		return fail(e)
	}
	if e = dacl(f.Dir, instanceACL(sid.String())); e != nil {
		return fail(e)
	}
	f.token, e = logon(name, f.password, 4)
	if e != nil {
		return fail(e)
	}
	return f, nil
}
func logon(name, password string, kind uint32) (windows.Token, error) {
	var t windows.Token
	e := boolCall(adv.NewProc("LogonUserW"), uintptr(unsafe.Pointer(u16(name))), uintptr(unsafe.Pointer(u16("."))), uintptr(unsafe.Pointer(u16(password))), uintptr(kind), 0, uintptr(unsafe.Pointer(&t)))
	runtime.KeepAlive(password)
	return t, e
}

type lsaString struct {
	Length, MaximumLength uint16
	Buffer                *uint16
}
type lsaObject struct {
	Length     uint32
	Root       uintptr
	Name       *lsaString
	Attributes uint32
	SD, SQOS   uintptr
}

func accountRights(sid *windows.SID, names []string, remove bool) error {
	var h uintptr
	oa := lsaObject{}
	oa.Length = uint32(unsafe.Sizeof(oa))
	if e := statusCall(adv.NewProc("LsaOpenPolicy"), 0, uintptr(unsafe.Pointer(&oa)), 0x810, uintptr(unsafe.Pointer(&h))); e != nil {
		return e
	}
	defer adv.NewProc("LsaClose").Call(h)
	if remove {
		v, _, _ := adv.NewProc("LsaRemoveAccountRights").Call(h, uintptr(unsafe.Pointer(sid)), 1, 0, 0)
		if status32(v) == 0 || status32(v) == 0xc0000034 {
			return nil
		}
		return fmt.Errorf("LsaRemoveAccountRights: 0x%x", v)
	}
	rights := make([]lsaString, len(names))
	for i, n := range names {
		w := windows.StringToUTF16(n)
		rights[i] = lsaString{uint16((len(w) - 1) * 2), uint16(len(w) * 2), &w[0]}
	}
	return statusCall(adv.NewProc("LsaAddAccountRights"), h, uintptr(unsafe.Pointer(sid)), uintptr(unsafe.Pointer(&rights[0])), uintptr(len(rights)))
}
func (f *fixture) close() error {
	if f.token != 0 {
		_ = f.token.Close()
		f.token = 0
	}
	return cleanJournal(f.journal)
}
func cleanJournal(j journal) error {
	var errs []error
	add := func(e error) {
		if e != nil {
			errs = append(errs, e)
		}
	}
	for _, name := range append([]string{j.Job}, j.Jobs...) {
		if h, e := openJob(name); e == nil {
			_ = windows.TerminateJobObject(h, 99)
			_ = windows.CloseHandle(h)
		}
	}
	// The shim is outside its payload job. Its bounded lease and stop marker close its handle.
	_ = os.WriteFile(filepath.Join(j.Dir, "stop"), nil, 0600)
	if j.Profile != "" {
		present, pe := exemptionState(j.Profile)
		add(pe)
		if j.Loopback && present {
			profileID, ie := profileSID(j.Profile)
			add(ie)
			out, e := command("CheckNetIsolation.exe", "LoopbackExempt", "-d", "-p="+profileID)
			if e != nil {
				add(fmt.Errorf("remove loopback exemption: %s: %w", out, e))
			}
		}
		add(deleteProfile(j.Profile))
	}
	if len(j.Filters) > 0 {
		add(removeFilters(j.Filters))
	}
	if j.VHD != "" {
		add(detachVHD(j.VHD))
	}
	sid, _, _, lookupErr := windows.LookupSID("", j.Name)
	if lookupErr != nil && j.SID != "" {
		sid, _ = windows.StringToSid(j.SID)
	}
	if sid != nil {
		add(accountRights(sid, nil, true))
	}
	if lookupErr == nil {
		add(statusCall(netapi.NewProc("NetUserDel"), 0, uintptr(unsafe.Pointer(u16(j.Name)))))
	}
	if e := statusCall(netapi.NewProc("NetLocalGroupDel"), 0, uintptr(unsafe.Pointer(u16(j.Group)))); e != nil && !strings.Contains(e.Error(), "000008ac") {
		add(e)
	}
	// Resource directories are strict children of the validated run root, never user-supplied deletion targets.
	return errors.Join(errs...)
}

type process struct{ windows.ProcessInformation }

func (p *process) close() {
	if p.Thread != 0 {
		_ = windows.CloseHandle(p.Thread)
		p.Thread = 0
	}
	if p.Process != 0 {
		_ = windows.CloseHandle(p.Process)
		p.Process = 0
	}
}
func (p *process) kill() {
	_ = windows.TerminateProcess(p.Process, 98)
	_, _ = windows.WaitForSingleObject(p.Process, 5000)
}
func (p *process) resume() error { _, e := windows.ResumeThread(p.Thread); return e }
func (p *process) wait(timeout time.Duration) (uint32, error) {
	s, e := windows.WaitForSingleObject(p.Process, uint32(timeout.Milliseconds()))
	if e != nil {
		return 0, e
	}
	if s == uint32(windows.WAIT_TIMEOUT) {
		return 0, errors.New("process timeout")
	}
	var code uint32
	e = windows.GetExitCodeProcess(p.Process, &code)
	return code, e
}
func launch(token windows.Token, exe string, args []string, dir string, job windows.Handle) (*process, error) {
	cmd := windows.ComposeCommandLine(append([]string{exe}, args...))
	if strings.EqualFold(filepath.Base(exe), "cmd.exe") && len(args) == 4 && args[2] == "/c" {
		cmd = windows.EscapeArg(exe) + " /d /s /c \"" + args[3] + "\""
	}
	si := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
	p := &process{}
	// Minimal environment: no inherited host credentials or user profile paths.
	env := []string{"SystemRoot=" + os.Getenv("SystemRoot"), "WINDIR=" + os.Getenv("SystemRoot"), "PATH=" + filepath.Join(os.Getenv("SystemRoot"), "System32"), "TEMP=" + dir, "TMP=" + dir, "GITHUB_ACTIONS=true", "RUNNER_ENVIRONMENT=github-hosted"}
	var block []uint16
	for _, v := range env {
		block = append(block, windows.StringToUTF16(v)...)
	}
	block = append(block, 0)
	var e error
	if token == 0 {
		e = windows.CreateProcess(u16(exe), u16(cmd), nil, nil, false, windows.CREATE_SUSPENDED|windows.CREATE_UNICODE_ENVIRONMENT|windows.CREATE_NO_WINDOW, &block[0], u16(dir), &si, &p.ProcessInformation)
	} else {
		e = windows.CreateProcessAsUser(token, u16(exe), u16(cmd), nil, nil, false, windows.CREATE_SUSPENDED|windows.CREATE_UNICODE_ENVIRONMENT|windows.CREATE_NO_WINDOW, &block[0], u16(dir), &si, &p.ProcessInformation)
	}
	if e != nil {
		return nil, e
	}
	if job != 0 {
		if e = windows.AssignProcessToJobObject(job, p.Process); e != nil {
			p.kill()
			p.close()
			return nil, e
		}
	}
	if e = p.resume(); e != nil {
		p.kill()
		p.close()
		return nil, e
	}
	return p, nil
}
func probePath(o options) string { return filepath.Join(o.Root, "tools", "s3probe.exe") }
func runWork(f *fixture, args ...string) (map[string]any, error) {
	path := filepath.Join(f.Dir, "result-"+randomHex(4)+".json")
	all := append([]string{"_work", path}, args...)
	h, e := f.job(f.Job+"-worker-"+randomHex(3), 0, 0, 0)
	if e != nil {
		return nil, e
	}
	defer windows.CloseHandle(h)
	p, e := launch(f.token, probePath(f.o), all, f.Dir, h)
	if e != nil {
		return nil, e
	}
	defer p.close()
	defer p.kill()
	code, e := p.wait(20 * time.Second)
	if e != nil {
		return nil, e
	}
	if code != 0 {
		return nil, fmt.Errorf("worker exit %d", code)
	}
	var result map[string]any
	e = readJSON(path, &result)
	return result, e
}
func newJob(name string, memory uintptr, pids uint32, cpu uint32) (windows.Handle, error) {
	sd, e := windows.SecurityDescriptorFromString(adminACL)
	if e != nil {
		return 0, e
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	h, e := windows.CreateJobObject(&sa, u16(name))
	if e != nil {
		return 0, e
	}
	// kill-on-close belongs to the shim, not to an ephemeral node agent.
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if memory > 0 {
		limits.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_JOB_MEMORY
		limits.JobMemoryLimit = memory
	}
	if pids > 0 {
		limits.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
		limits.BasicLimitInformation.ActiveProcessLimit = pids
	}
	if _, e = windows.SetInformationJobObject(h, 9, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); e != nil {
		_ = windows.CloseHandle(h)
		return 0, e
	}
	if cpu > 0 {
		rate := struct{ Flags, Rate uint32 }{5, cpu}
		if _, e = windows.SetInformationJobObject(h, 15, uintptr(unsafe.Pointer(&rate)), uint32(unsafe.Sizeof(rate))); e != nil {
			_ = windows.CloseHandle(h)
			return 0, e
		}
	}
	return h, nil
}
func openJob(name string) (windows.Handle, error) {
	v, _, e := kernel.NewProc("OpenJobObjectW").Call(0x1f003f, 0, uintptr(unsafe.Pointer(u16(name))))
	if v == 0 {
		return 0, e
	}
	return windows.Handle(v), nil
}
func jobPIDs(h windows.Handle) ([]uintptr, error) {
	var data struct {
		Assigned, Count uint32
		PIDs            [128]uintptr
	}
	e := windows.QueryInformationJobObject(h, 3, uintptr(unsafe.Pointer(&data)), uint32(unsafe.Sizeof(data)), nil)
	if e != nil {
		return nil, e
	}
	if data.Count > 128 {
		return nil, errors.New("PID buffer too small")
	}
	return data.PIDs[:data.Count], nil
}

type accounting struct {
	User, Kernel, PeriodUser, PeriodKernel int64
	Faults, Total, Active, Terminated      uint32
}

func jobAccounting(h windows.Handle) (accounting, error) {
	var a accounting
	e := windows.QueryInformationJobObject(h, 1, uintptr(unsafe.Pointer(&a)), uint32(unsafe.Sizeof(a)), nil)
	return a, e
}
