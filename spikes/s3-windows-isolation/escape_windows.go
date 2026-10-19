//go:build windows && amd64

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func psLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func brokerWorker(kind, name, out string) map[string]any {
	cmd := windows.ComposeCommandLine([]string{os.Args[0], "_work", out, "sleep"})
	script := `$ErrorActionPreference='Stop'; $r=@{control=$false;win32Error=0;pid=0}; try { `
	if kind == "wmi" {
		script += `$null=Get-CimInstance Win32_OperatingSystem; $r.control=$true; $p=Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{CommandLine=` + psLiteral(cmd) + `}; $r.methodReturn=[int]$p.ReturnValue; if($p.ReturnValue -eq 0){$r.pid=[int]$p.ProcessId} elseif($p.ReturnValue -eq 2){$r.win32Error=5} else{$r.win32Error=65535}`
	} else {
		script += `$null=Get-ScheduledTask; $r.control=$true; $a=New-ScheduledTaskAction -Execute ` + psLiteral(os.Args[0]) + ` -Argument ` + psLiteral(windows.ComposeCommandLine([]string{"_work", out, "sleep"})) + `; $p=New-ScheduledTaskPrincipal -UserId ` + psLiteral(name) + ` -LogonType S4U -RunLevel Limited; $null=Register-ScheduledTask -TaskName ` + psLiteral(name+"-escape") + ` -Action $a -Principal $p; Start-ScheduledTask -TaskName ` + psLiteral(name+"-escape") + `; $until=[DateTime]::UtcNow.AddSeconds(5); while(-not(Test-Path -LiteralPath ` + psLiteral(out) + `)){if([DateTime]::UtcNow -gt $until){throw [ComponentModel.Win32Exception]::new(1460)};Start-Sleep -Milliseconds 100}; $r.pid=[int]((Get-Content -Raw -LiteralPath ` + psLiteral(out) + `) | ConvertFrom-Json).pid`
	}
	script += ` } catch {$r.error=$_.Exception.Message; $r.win32Error=($_.Exception.HResult -band 65535); if($_.Exception -is [ComponentModel.Win32Exception]){$r.win32Error=$_.Exception.NativeErrorCode}}; $r | ConvertTo-Json -Compress`
	utf := windows.StringToUTF16(script)
	b := make([]byte, (len(utf)-1)*2)
	for i, v := range utf[:len(utf)-1] {
		binary.LittleEndian.PutUint16(b[2*i:], v)
	}
	c := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(b))
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	e := c.Run()
	result := map[string]any{}
	if je := json.Unmarshal(stdout.Bytes(), &result); je != nil {
		return map[string]any{"control": false, "win32Error": errorCode(e), "error": je.Error(), "stderr": stderr.String()}
	}
	return result
}
func escapeExperiment(o options, r *Result) (err error) {
	f, e := newFixture(o, "e")
	if e != nil {
		return e
	}
	defer func() { err = join(err, f.close()) }()
	negative, e := runWork(f, "breakaway", filepath.Join(f.workDir(), "breakaway-child.json"))
	if e != nil {
		return e
	}
	r.check("CREATE_BREAKAWAY_FROM_JOB_denied", win32Code(negative["create"]) == 5, negative)
	for _, kind := range []string{"wmi", "task"} {
		h, e := f.job(f.Job+"-"+kind, 0, 16, 0)
		if e != nil {
			return e
		}
		func() {
			defer windows.CloseHandle(h)
			defer windows.TerminateJobObject(h, 90)
			out := filepath.Join(f.workDir(), kind+".json")
			childOut := filepath.Join(f.workDir(), kind+"-child.json")
			p, pe := launch(f.token, probePath(o), []string{"_work", out, "broker", kind, f.Name, childOut}, f.workDir(), h)
			if pe != nil {
				r.check(kind+"_probe_launch", false, pe.Error())
				return
			}
			defer p.close()
			defer p.kill()
			code, we := p.wait(20 * time.Second)
			if we != nil || code != 0 {
				r.check(kind+"_probe_result", false, errorString(we))
				return
			}
			var data map[string]any
			if re := readJSON(out, &data); re != nil {
				r.check(kind+"_probe_result", false, re.Error())
				return
			}
			if data["control"] != true {
				r.unsupported(kind+"_positive_control", data)
				return
			}
			pid, _ := data["pid"].(float64)
			denied := win32Code(data) == 5
			ids, qe := jobPIDs(h)
			inside := qe == nil && pid > 0 && pidIn(ids, uint32(pid))
			r.check(kind+"_must_fail_access_denied_or_stay_in_job", denied || inside, map[string]any{"result": data, "jobPIDs": ids, "jobQueryError": errorString(qe), "createdPID": strconv.Itoa(int(pid))})
		}()
	}
	r.unsupported("out_of_process_COM_escape_gate", "No controlled out-of-process COM server is installed in these images; WMI/task results do not cover arbitrary brokered COM. Require a disposable signed COM fixture or use a VM boundary.")
	r.unsupported("untracked_external_files_cleanup_gate", "Known canaries, profiles and SID-owned tasks/processes are cleaned. Arbitrary files written elsewhere by a malicious instance require a filesystem boundary or a reliable per-SID inventory; do not recursively sweep Windows/Public by guessed names.")
	r.observe("account_process_cleanup", "all discoverable account-SID processes are terminated before account deletion, including broker-created processes")
	return nil
}
