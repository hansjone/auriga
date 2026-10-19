//go:build windows && amd64

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func worker(args []string) {
	if len(args) < 2 {
		os.Exit(2)
	}
	out, mode := args[0], args[1]
	a := args[2:]
	result := map[string]any{}
	switch mode {
	case "identity":
		u, e := windows.GetCurrentProcessToken().GetTokenUser()
		if e != nil {
			os.Exit(3)
		}
		result["sid"] = u.User.Sid.String()
	case "read":
		for _, path := range a {
			st, se := os.Stat(path)
			var b []byte
			var e error
			count := 0
			isDir := se == nil && st.IsDir()
			if isDir {
				var entries []os.DirEntry
				entries, e = os.ReadDir(path)
				count = len(entries)
			} else {
				b, e = os.ReadFile(path)
			}
			entry := map[string]any{"readable": e == nil, "bytes": len(b), "isDirectory": isDir, "entryCount": count}
			if e != nil {
				entry["error"] = e.Error()
				var errno syscall.Errno
				if errors.As(e, &errno) {
					entry["win32Error"] = uint32(errno)
				}
			}
			result[path] = entry
		}
	case "network":
		for _, address := range a {
			start := time.Now()
			c, e := net.DialTimeout("tcp", address, 1500*time.Millisecond)
			entry := map[string]any{"connected": e == nil, "durationMs": time.Since(start).Milliseconds()}
			entry["win32Error"] = errorCode(e)
			if e != nil {
				entry["error"] = e.Error()
			} else {
				_ = c.Close()
			}
			result[address] = entry
		}
	case "write":
		if len(a) != 1 {
			os.Exit(2)
		}
		f, e := os.Create(a[0])
		var total int64
		if e == nil {
			block := make([]byte, 1<<20)
			for i := range block {
				block[i] = byte(i*31 + 17)
			}
			for total < 32<<20 {
				n, we := f.Write(block)
				total += int64(n)
				if we != nil {
					e = we
					break
				}
			}
			if e == nil {
				e = f.Sync()
			}
			_ = f.Close()
		}
		result["bytesWritten"] = total
		result["denied"] = e != nil
		if e != nil {
			result["error"] = e.Error()
			var errno syscall.Errno
			if errors.As(e, &errno) {
				result["win32Error"] = uint32(errno)
			}
		}
	case "memory":
		_ = writeJSON(out, map[string]any{"started": true})
		var blocks [][]byte
		for i := 0; i < 192; i++ {
			b := make([]byte, 1<<20)
			for j := 0; j < len(b); j += 4096 {
				b[j] = 1
			}
			blocks = append(blocks, b)
			if len(blocks)%4 == 0 {
				_ = writeJSON(out, map[string]any{"started": true, "allocatedMiB": len(blocks)})
			}
			time.Sleep(5 * time.Millisecond)
		}
		result["allocatedMiB"] = len(blocks)
		runtime.KeepAlive(blocks)
	case "cpu":
		_ = writeJSON(out, map[string]any{"started": true})
		runtime.GOMAXPROCS(runtime.NumCPU())
		var wg sync.WaitGroup
		end := time.Now().Add(9 * time.Second)
		for i := 0; i < runtime.NumCPU(); i++ {
			wg.Go(func() {
				var v uint64 = 1
				for time.Now().Before(end) {
					for n := 0; n < 100000; n++ {
						v = v*1664525 + 1013904223
					}
				}
				runtime.KeepAlive(v)
			})
		}
		wg.Wait()
		result["finished"] = true
	case "sleep":
		_ = writeJSON(out, map[string]any{"pid": os.Getpid()})
		time.Sleep(90 * time.Second)
		return
	case "pids":
		var children []*exec.Cmd
		var childPIDs []uint32
		defer func() {
			for _, c := range children {
				_ = c.Process.Kill()
				_ = c.Wait()
			}
		}()
		for i := 0; i < 12; i++ {
			c := exec.Command(os.Args[0], "_work", out+"-"+strconv.Itoa(i), "sleep")
			if len(a) == 0 || a[0] != "inherit-console" {
				c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
			}
			if e := c.Start(); e != nil {
				result["spawnError"] = e.Error()
				var code syscall.Errno
				if errors.As(e, &code) {
					result["spawnWin32Error"] = uint32(code)
				}
				break
			}
			children = append(children, c)
			childPIDs = append(childPIDs, uint32(c.Process.Pid))
		}
		result["childrenStarted"] = len(children)
		result["childPIDs"] = childPIDs
		_ = writeJSON(out, result)
		time.Sleep(10 * time.Second)
		return
	default:
		if !securityWorker(mode, a, result) {
			os.Exit(2)
		}
	}
	if e := writeJSON(out, result); e != nil {
		os.Exit(4)
	}
}
func identityExperiment(o options, r *Result) (err error) {
	f, e := newFixture(o, "i")
	if e != nil {
		return e
	}
	defer func() { err = join(err, f.close()) }()
	u, e := f.token.GetTokenUser()
	if e != nil {
		return e
	}
	r.check("batch_logon_primary_token", u.User.Sid.String() == f.sid.String(), u.User.Sid.String())
	for _, kind := range []uint32{2, 3, 5} {
		t, le := logon(f.Name, f.password, kind)
		if t != 0 {
			_ = t.Close()
		}
		r.check(fmt.Sprintf("deny_logon_type_%d", kind), le != nil && containsError(le, windows.ERROR_LOGON_TYPE_NOT_GRANTED), errorString(le))
	}
	rights, re := accountRightNames(f.sid)
	if re != nil {
		return re
	}
	for _, name := range []string{"SeBatchLogonRight", "SeDenyInteractiveLogonRight", "SeDenyRemoteInteractiveLogonRight", "SeDenyNetworkLogonRight", "SeDenyServiceLogonRight"} {
		r.check("LSA_"+name, slices.Contains(rights, name), rights)
	}
	r.unsupported("S4U_without_batch_release_gate", "No persistent password is stored; the shim receives a duplicated token. LsaLogonUser/MSV1_0 S4U needs a separately verified authentication package, logon-type policy, token and privilege design. Passwordless S4U alone does not prove Task Scheduler/WMI escape prevention.")
	data, e := runWork(f, "identity")
	if e != nil {
		return e
	}
	r.check("CreateProcessAsUser_identity", data["sid"] == f.sid.String(), data)
	groups, e := f.token.GetTokenGroups()
	if e != nil {
		return e
	}
	var sids []string
	for _, g := range groups.AllGroups() {
		sids = append(sids, g.Sid.String())
	}
	r.observe("token_groups", sids)
	gs, _, _, e := windows.LookupSID("", f.Group)
	if e != nil {
		return e
	}
	has := false
	admin := false
	for _, s := range sids {
		has = has || s == gs.String()
		admin = admin || s == "S-1-5-32-544"
	}
	r.check("dedicated_group_without_administrators", has && !admin, sids)
	localGroups, ge := directGroups(f.Name)
	if ge != nil {
		return ge
	}
	r.check("only_dedicated_local_group_joined", len(localGroups) == 1 && strings.EqualFold(localGroups[0], f.Group), localGroups)
	// Try the alternative under the exact same deny-interactive policy. Failure is expected evidence.
	si := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
	var pi windows.ProcessInformation
	le := boolCall(adv.NewProc("CreateProcessWithLogonW"), uintptr(unsafe.Pointer(u16(f.Name))), uintptr(unsafe.Pointer(u16("."))), uintptr(unsafe.Pointer(u16(f.password))), 0, uintptr(unsafe.Pointer(u16(probePath(o)))), uintptr(unsafe.Pointer(u16(windows.ComposeCommandLine([]string{probePath(o), "_work", filepath.Join(f.Dir, "alternative.json"), "identity"})))), windows.CREATE_SUSPENDED, 0, uintptr(unsafe.Pointer(u16(f.Dir))), uintptr(unsafe.Pointer(&si)), uintptr(unsafe.Pointer(&pi)))
	if le == nil {
		p := process{pi}
		p.kill()
		p.close()
	}
	r.observe("CreateProcessWithLogonW_under_deny_interactive", map[string]any{"created": le == nil, "error": errorString(le)})
	text, e := command("whoami.exe", "/priv")
	r.observe("caller_privileges", map[string]any{"output": text, "error": errorString(e)})
	return nil
}
func join(a, b error) error {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return fmt.Errorf("%w; cleanup: %w", a, b)
}
func errorString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
func containsError(e, target error) bool { return errors.Is(e, target) }

func jobExperiment(o options, r *Result) (err error) {
	f, e := newFixture(o, "j")
	if e != nil {
		return e
	}
	defer func() { err = join(err, f.close()) }()
	// Each limit has its own job, so one failure cannot masquerade as another limit.
	for _, kind := range []string{"memory", "cpu-baseline", "cpu", "pids-console", "pids", "tree"} {
		e = jobCase(f, r, kind)
		if e != nil {
			r.check(kind+"_setup", false, e.Error())
		}
	}
	return nil
}
func jobCase(f *fixture, r *Result, kind string) error {
	memory, pids, cpu := uintptr(0), uint32(0), uint32(0)
	if kind == "memory" {
		memory = 96 << 20
	}
	if kind == "pids" || kind == "pids-console" {
		pids = 4
	}
	if kind == "cpu" {
		cpu = 1000
	}
	h, e := f.job(f.Job+"-"+kind, memory, pids, cpu)
	if e != nil {
		return e
	}
	defer windows.CloseHandle(h)
	defer windows.TerminateJobObject(h, 97)
	out := filepath.Join(f.workDir(), kind+".json")
	exe := probePath(f.o)
	args := []string{"_work", out, kind}
	if kind == "cpu-baseline" {
		args = []string{"_work", out, "cpu"}
	}
	if kind == "pids-console" {
		args = []string{"_work", out, "pids", "inherit-console"}
	}
	if kind == "tree" {
		exe = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
		node := filepath.Join(f.o.Root, "tools", "node.exe")
		script := filepath.Join(f.workDir(), "tree.js")
		body := `const cp=require('child_process');const fs=require('fs');let c=cp.spawn(process.argv[2],['_work',process.argv[3]+'-leaf','sleep']);fs.writeFileSync(process.argv[3],JSON.stringify({node:process.pid,child:c.pid}));setInterval(()=>{},1000);`
		if e = writePlainFile(script, []byte(body), 0600); e != nil {
			return e
		}
		args = []string{"/d", "/s", "/c", windows.ComposeCommandLine([]string{node, script, probePath(f.o), out})}
	}
	start := time.Now()
	p, e := launch(f.token, exe, args, f.workDir(), h)
	if e != nil {
		return e
	}
	defer p.close()
	defer p.kill()
	switch kind {
	case "memory":
		code, we := p.wait(15 * time.Second)
		var data map[string]any
		re := readJSON(out, &data)
		var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		qe := windows.QueryInformationJobObject(h, 9, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil)
		allocated, _ := data["allocatedMiB"].(float64)
		r.check("memory_worker_started", re == nil, data)
		r.check("memory_96MiB_rejects_192MiB", we == nil && code == 2 && re == nil && qe == nil && allocated >= 16 && allocated < 192 && limits.PeakJobMemoryUsed >= 64<<20 && limits.JobMemoryLimit == 96<<20, map[string]any{"exit": code, "waitError": errorString(join(we, qe)), "worker": data, "peakJobCommitBytes": limits.PeakJobMemoryUsed, "jobLimitBytes": limits.JobMemoryLimit})
	case "cpu", "cpu-baseline":
		code, we := p.wait(20 * time.Second)
		a, ae := jobAccounting(h)
		var worker map[string]any
		re := readJSON(out, &worker)
		complete := we == nil && code == 0 && re == nil && worker["finished"] == true
		elapsed := time.Since(start).Seconds()
		fraction := float64(a.User+a.Kernel) / 1e7 / elapsed / float64(runtime.NumCPU())
		if kind == "cpu-baseline" {
			f.CPUBaseline = fraction
			r.check("CPU_unlimited_completed", complete && ae == nil, map[string]any{"hostFraction": fraction, "worker": worker, "exit": code})
			if fraction < 0.25 {
				r.unsupported("CPU_unlimited_saturation", fraction)
			}
		} else {
			r.check("cpu_hard_cap_10_percent", complete && ae == nil && fraction > 0.015 && fraction < 0.18 && f.CPUBaseline >= 0.25 && fraction < f.CPUBaseline*0.5, map[string]any{"hostFraction": fraction, "unlimitedFraction": f.CPUBaseline, "worker": worker, "exit": code, "wallSeconds": elapsed, "accounting": a, "error": errorString(join(we, ae))})
		}
	case "pids", "pids-console":
		if e = awaitFile(out, 10*time.Second); e != nil {
			return e
		}
		var data struct {
			ChildrenStarted uint32   `json:"childrenStarted"`
			ChildPIDs       []uint32 `json:"childPIDs"`
			SpawnWin32Error uint32   `json:"spawnWin32Error"`
			SpawnError      string   `json:"spawnError"`
		}
		if e = readJSON(out, &data); e != nil {
			return e
		}
		var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		if e = windows.QueryInformationJobObject(h, 9, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil); e != nil {
			return e
		}
		var samples []processLimitSample
		ok := false
		until := time.Now().Add(2 * time.Second)
		for {
			ids, pe := jobPIDs(h)
			if pe != nil {
				return pe
			}
			a, ae := jobAccounting(h)
			if ae != nil {
				return ae
			}
			sample := processLimitSample{RawPIDs: ids, Active: a.Active, Processes: inspectProcesses(ids)}
			samples = append(samples, sample)
			ok = processLimitSatisfied(limits.BasicLimitInformation.ActiveProcessLimit, p.ProcessId, data.ChildPIDs, data.SpawnWin32Error, sample) && data.ChildrenStarted == uint32(len(data.ChildPIDs))
			if ok || time.Now().After(until) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		evidence := map[string]any{"worker": data, "configuredLimit": limits.BasicLimitInformation.ActiveProcessLimit, "samples": samples, "consolePolicy": "CREATE_NO_WINDOW for every workload child; no process name is exempt from the limit"}
		if kind == "pids-console" {
			evidence["consolePolicy"] = "original default console behavior, diagnostic comparison only"
			r.observe("original_console_process_limit_diagnostics", evidence)
		} else {
			r.check("active_process_limit_4", ok && limits.BasicLimitInformation.ActiveProcessLimit == 4, evidence)
		}
	case "tree":
		if e = awaitFile(out, 10*time.Second); e != nil {
			return e
		}
		var data struct{ Node, Child uint32 }
		if e = readJSON(out, &data); e != nil {
			return e
		}
		ids, e := jobPIDs(h)
		if e != nil {
			return e
		}
		r.check("cmd_node_grandchild_membership", pidIn(ids, p.ProcessId) && pidIn(ids, data.Node) && pidIn(ids, data.Child), ids)
		handles := []windows.Handle{}
		defer func() {
			for _, v := range handles {
				_ = windows.CloseHandle(v)
			}
		}()
		for _, id := range []uint32{p.ProcessId, data.Node, data.Child} {
			v, e := windows.OpenProcess(windows.SYNCHRONIZE, false, id)
			if e != nil {
				return e
			}
			handles = append(handles, v)
		}
		if e = windows.TerminateJobObject(h, 96); e != nil {
			return e
		}
		all := true
		for _, v := range handles {
			s, e := windows.WaitForSingleObject(v, 5000)
			all = all && e == nil && s == windows.WAIT_OBJECT_0
		}
		r.check("terminate_entire_tree", all, data)
	}
	return nil
}
func pidIn(ids []uintptr, pid uint32) bool {
	for _, v := range ids {
		if v == uintptr(pid) {
			return true
		}
	}
	return false
}
func awaitFile(path string, timeout time.Duration) error {
	until := time.Now().Add(timeout)
	for time.Now().Before(until) {
		if b, e := os.ReadFile(path); e == nil && json.Valid(b) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", filepath.Base(path))
}

func aclExperiment(o options, r *Result) (err error) {
	a, e := newFixture(o, "a")
	if e != nil {
		return e
	}
	defer func() { err = join(err, a.close()) }()
	b, e := newFixture(o, "b")
	if e != nil {
		return e
	}
	defer func() { err = join(err, b.close()) }()
	own := filepath.Join(a.workDir(), "sentinel")
	peer := filepath.Join(b.workDir(), "sentinel")
	for _, p := range []string{own, peer} {
		if e = writePlainFile(p, []byte("public synthetic sentinel"), 0600); e != nil {
			return e
		}
	}
	public := filepath.Join(o.Root, "public-sentinel")
	if e = writePlainFile(public, []byte("synthetic public data"), 0600); e != nil {
		return e
	}
	if e = dacl(public, adminACL+"(A;;GR;;;AU)"); e != nil {
		return e
	}
	system := filepath.Join(os.Getenv("SystemRoot"), "System32", "drivers", "etc", "hosts")
	// Test real pre-existing path permissions without uploading file contents.
	paths := []string{own, peer, b.Dir, public, system, os.Getenv("SystemRoot"), filepath.Join(os.Getenv("SystemRoot"), "System32"), os.Getenv("ProgramFiles"), os.Getenv("ProgramData"), filepath.Join(filepath.VolumeName(os.Getenv("SystemRoot"))+`\`, "Users"), os.Getenv("PUBLIC"), os.Getenv("USERPROFILE"), filepath.Join(os.Getenv("ProgramData"), "Microsoft", "Windows", "Start Menu", "Programs", "desktop.ini"), filepath.Join(os.Getenv("USERPROFILE"), "NTUSER.DAT"), filepath.Join(os.Getenv("PUBLIC"), "Desktop", "desktop.ini")}
	data, e := runWork(a, append([]string{"read"}, paths...)...)
	if e != nil {
		return e
	}
	readable := func(p string) bool { v, ok := data[p].(map[string]any); return ok && v["readable"] == true }
	r.check("own_instance_read", readable(own), data[own])
	r.check("peer_instance_denied", !readable(peer) && accessDenied(data[peer]), data[peer])
	r.check("public_ProgramData_readable", readable(public), data[public])
	r.observe("host_path_readability_no_contents", data)
	sd, e := windows.GetNamedSecurityInfo(a.Dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return e
	}
	control, _, e := sd.Control()
	if e != nil {
		return e
	}
	r.check("inheritance_disabled", control&windows.SE_DACL_PROTECTED != 0, sd.String())
	negative, e := runWork(a, "root-security", a.Dir)
	if e != nil {
		return e
	}
	r.check("junction_positive_control", win32Code(negative["junctionControl"]) == 0, negative["junctionControl"])
	for _, name := range []string{"junctionWrite", "rootWriteDACL", "rootDelete"} {
		r.check("root_denies_"+name, win32Code(negative[name]) == 5, negative[name])
	}
	writes := []string{a.Name, a.workDir(), a.Dir, os.Getenv("ProgramData"), os.Getenv("PUBLIC"), filepath.Join(os.Getenv("SystemRoot"), "Temp"), filepath.Join(o.Root, "tools")}
	writeResults, e := runWork(a, append([]string{"write-probes"}, writes...)...)
	if e != nil {
		return e
	}
	for _, path := range writes[1:] {
		expected := uint32(5)
		if path == a.workDir() {
			expected = 0
		}
		r.check("write_policy_"+path, win32Code(writeResults[path]) == expected, writeResults[path])
	}
	r.unsupported("machine_shared_directory_write_gate", "A vanilla runner may allow writes to Public/Windows Temp/ProgramData. Production needs a restricted token/AppContainer/VM or dedicated machine ACL policy. The spike does not change global directory ACLs.")
	return nil
}
func accessDenied(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	return m["win32Error"] == float64(windows.ERROR_ACCESS_DENIED)
}
func directGroups(name string) ([]string, error) {
	var buf *byte
	var count, total uint32
	e := statusCall(netapi.NewProc("NetUserGetLocalGroups"), 0, uintptr(unsafe.Pointer(u16(name))), 0, 0, uintptr(unsafe.Pointer(&buf)), 0xffffffff, uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&total)))
	if e != nil {
		return nil, e
	}
	defer netapi.NewProc("NetApiBufferFree").Call(uintptr(unsafe.Pointer(buf)))
	entries := unsafe.Slice((**uint16)(unsafe.Pointer(buf)), int(count))
	groups := make([]string, count)
	for i, n := range entries {
		groups[i] = windows.UTF16PtrToString(n)
	}
	return groups, nil
}
