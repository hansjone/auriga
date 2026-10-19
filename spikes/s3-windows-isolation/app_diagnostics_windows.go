//go:build windows && amd64

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func appReadACL(account, container string) string {
	return protectedAdminACL + "(A;OICI;0x1200a9;;;" + account + ")(A;OICI;0x1200a9;;;" + container + ")"
}
func appWriteACL(account, container string) string {
	return protectedAdminACL + "(A;OICI;0x1301bf;;;" + account + ")(A;OICI;0x1301bf;;;" + container + ")"
}
func lowLabel(path string) error {
	guard, e := lockPlainPath(path)
	if e != nil {
		return e
	}
	defer guard.close()
	sd, e := windows.SecurityDescriptorFromString("S:(ML;OICI;NW;;;LW)")
	if e != nil {
		return e
	}
	sacl, _, e := sd.SACL()
	if e != nil {
		return e
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, sacl)
}

type appStdio struct {
	in, out, err     windows.Handle
	outPath, errPath string
}

func newAppStdio(dir, name string) (*appStdio, error) {
	guard, e := lockPlainPath(dir)
	if e != nil {
		return nil, e
	}
	defer guard.close()
	s := &appStdio{outPath: filepath.Join(dir, name+".stdout.txt"), errPath: filepath.Join(dir, name+".stderr.txt")}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	s.in, e = windows.CreateFile(u16("NUL"), windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, &sa, windows.OPEN_EXISTING, 0, 0)
	if e == nil {
		s.out, e = windows.CreateFile(u16(s.outPath), windows.GENERIC_WRITE, windows.FILE_SHARE_READ, &sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	}
	if e == nil {
		s.err, e = windows.CreateFile(u16(s.errPath), windows.GENERIC_WRITE, windows.FILE_SHARE_READ, &sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	}
	if e != nil {
		s.close()
		return nil, e
	}
	return s, nil
}
func (s *appStdio) close() {
	for _, h := range []windows.Handle{s.in, s.out, s.err} {
		if h != 0 && h != windows.InvalidHandle {
			_ = windows.CloseHandle(h)
		}
	}
	s.in, s.out, s.err = 0, 0, 0
}
func boundedLog(path string) map[string]any {
	guard, e := lockPlainPath(path)
	if e != nil {
		return map[string]any{"error": e.Error()}
	}
	defer guard.close()
	f, e := os.Open(path)
	if e != nil {
		return map[string]any{"error": e.Error()}
	}
	defer f.Close()
	return diagnosticText(f)
}
func diagnosticText(reader io.Reader) map[string]any {
	b, e := io.ReadAll(io.LimitReader(reader, 32769))
	truncated := len(b) > 32768
	if truncated {
		b = b[:32768]
	}
	return map[string]any{"text": string(b), "truncated": truncated, "error": errorString(e)}
}
func (s *appStdio) diagnostics(p *process, job windows.Handle) map[string]any {
	state, se := windows.WaitForSingleObject(p.Process, 0)
	var code uint32
	ce := windows.GetExitCodeProcess(p.Process, &code)
	_ = windows.TerminateJobObject(job, 94)
	p.kill() // Finish the experiment, then close writers before inspecting log paths.
	s.close()
	return map[string]any{"exitedBeforeCleanup": se == nil && state == windows.WAIT_OBJECT_0, "exitCodeBeforeCleanup": code, "stateError": errorString(join(se, ce)), "stdout": boundedLog(s.outPath), "stderr": boundedLog(s.errPath), "consolePolicy": "DETACHED_PROCESS + explicit inherited stdin/stdout/stderr handle list"}
}
func appReportReady(data map[string]any) bool {
	_, client := data["outboundLoopback"].(bool)
	_, listener := data["listen"].(bool)
	return data["nodeStarted"] == true && data["shell"] != nil && client && listener
}
func awaitAppReport(p *process, out string, timeout time.Duration) (map[string]any, error) {
	until := time.Now().Add(timeout)
	var data map[string]any
	var readError error
	for time.Now().Before(until) {
		readError = readJSON(out, &data)
		if readError == nil && appReportReady(data) {
			return data, nil
		}
		state, e := windows.WaitForSingleObject(p.Process, 0)
		if e != nil {
			return data, e
		}
		if state == windows.WAIT_OBJECT_0 {
			var code uint32
			ce := windows.GetExitCodeProcess(p.Process, &code)
			return data, fmt.Errorf("Node exited before a complete report: exit=0x%x, exitQuery=%v, reportRead=%v", code, ce, readError)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return data, fmt.Errorf("Node report deadline while process still running: reportRead=%v", readError)
}

// Inline bootstrap reports path permissions to inherited stderr even if reading
// the script or writing its JSON fails. No reliance on the runner's Node location.
const appBootstrap = `const fs=require('fs'),path=require('path');
const [script,out]=process.argv.slice(1);let checks={};
for(const [key,p,mode] of [['node',process.execPath,fs.constants.R_OK|fs.constants.X_OK],['script',script,fs.constants.R_OK],['outputDirectory',path.dirname(out),fs.constants.W_OK]]){try{fs.accessSync(p,mode);checks[key]={ok:true,path:p}}catch(e){checks[key]={ok:false,path:p,code:e.code,errno:e.errno,message:e.message}}}
try{fs.writeFileSync(out,JSON.stringify({bootstrap:true,pathChecks:checks}));checks.outputWrite={ok:true}}catch(e){checks.outputWrite={ok:false,code:e.code,errno:e.errno,message:e.message}}
fs.writeSync(2,'S3_PREFLIGHT '+JSON.stringify(checks)+'\n');
try{require(script)}catch(e){fs.writeSync(2,String(e.stack||e)+'\n');process.exitCode=41;}`
