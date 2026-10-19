//go:build windows && amd64

package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type securityCapabilities struct {
	AppContainerSID *windows.SID
	Capabilities    *windows.SIDAndAttributes
	Count, Reserved uint32
}

func profileSID(name string) (string, error) {
	var sid *windows.SID
	e := statusCall(userenv.NewProc("DeriveAppContainerSidFromAppContainerName"), uintptr(unsafe.Pointer(u16(name))), uintptr(unsafe.Pointer(&sid)))
	if e != nil {
		return "", e
	}
	defer adv.NewProc("FreeSid").Call(uintptr(unsafe.Pointer(sid)))
	return sid.String(), nil
}
func exemptionState(name string) (bool, error) {
	sid, e := profileSID(name)
	if e != nil {
		return false, e
	}
	text, e := command("CheckNetIsolation.exe", "LoopbackExempt", "-s")
	return strings.Contains(text, sid), e
}

func deleteProfile(name string) error {
	v, _, _ := userenv.NewProc("DeleteAppContainerProfile").Call(uintptr(unsafe.Pointer(u16(name))))
	if status32(v) == 0 || status32(v) == 0x80070002 {
		return nil
	}
	return fmt.Errorf("DeleteAppContainerProfile: 0x%x", v)
}
func appLaunch(token windows.Token, exe string, args []string, dir string, sid *windows.SID, capIDs []string, job windows.Handle, stdio *appStdio) (*process, error) {
	caps := make([]windows.SIDAndAttributes, len(capIDs))
	for i, id := range capIDs {
		s, e := windows.StringToSid(id)
		if e != nil {
			return nil, e
		}
		caps[i] = windows.SIDAndAttributes{Sid: s, Attributes: windows.SE_GROUP_ENABLED}
	}
	sc := securityCapabilities{AppContainerSID: sid, Count: uint32(len(caps))}
	if len(caps) > 0 {
		sc.Capabilities = &caps[0]
	}
	count := uint32(1)
	if sid != nil {
		count++
	}
	attrs, e := windows.NewProcThreadAttributeList(count)
	if e != nil {
		return nil, e
	}
	defer attrs.Delete()
	if sid != nil {
		if e = attrs.Update(0x20009, unsafe.Pointer(&sc), unsafe.Sizeof(sc)); e != nil {
			return nil, e
		}
	}
	handles := []windows.Handle{stdio.in, stdio.out, stdio.err}
	if e = attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); e != nil {
		return nil, e
	}
	si := windows.StartupInfoEx{}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.ProcThreadAttributeList = attrs.List()
	si.Flags = windows.STARTF_USESTDHANDLES
	si.StdInput, si.StdOutput, si.StdErr = stdio.in, stdio.out, stdio.err
	p := &process{}
	if !filepath.IsAbs(exe) {
		return nil, fmt.Errorf("Node executable path must be absolute")
	}
	if _, e = os.Stat(exe); e != nil {
		return nil, fmt.Errorf("Node executable: %w", e)
	}
	env, _, e := appEnvironment(token, dir)
	if e != nil {
		return nil, e
	}
	e = windows.CreateProcessAsUser(token, u16(exe), u16(windows.ComposeCommandLine(append([]string{exe}, args...))), nil, nil, true, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_SUSPENDED|windows.CREATE_UNICODE_ENVIRONMENT|windows.DETACHED_PROCESS, &env[0], u16(dir), &si.StartupInfo, &p.ProcessInformation)
	runtime.KeepAlive(sc)
	runtime.KeepAlive(caps)
	runtime.KeepAlive(env)
	runtime.KeepAlive(handles)
	if e != nil {
		return nil, fmt.Errorf("CreateProcessAsUser(AppContainer, Unicode environment): %w", e)
	}
	if e = windows.AssignProcessToJobObject(job, p.Process); e != nil {
		p.kill()
		p.close()
		return nil, e
	}
	if e = p.resume(); e != nil {
		p.kill()
		p.close()
		return nil, e
	}
	return p, nil
}
func appContainerExperiment(o options, r *Result) (err error) {
	f, e := newFixture(o, "c")
	if e != nil {
		return e
	}
	defer func() { err = join(err, f.close()) }()
	peer, pe := newFixture(o, "b")
	if pe != nil {
		return pe
	}
	defer func() { err = join(err, peer.close()) }()
	f.PeerCanary = filepath.Join(peer.workDir(), "app-peer-canary")
	if e = writePlainFile(f.PeerCanary, []byte("synthetic peer"), 0600); e != nil {
		return e
	}
	f.HostCanary = filepath.Join(f.Dir, "host-sensitive-canary")
	if e = writePlainFile(f.HostCanary, []byte("synthetic privileged data"), 0600); e != nil {
		return e
	}
	if e = dacl(f.HostCanary, protectedAdminACL); e != nil {
		return e
	}
	f.Profile = "auriga.s3." + o.ID
	if e = f.save(); e != nil {
		return e
	}
	var sid *windows.SID
	e = statusCall(userenv.NewProc("CreateAppContainerProfile"), uintptr(unsafe.Pointer(u16(f.Profile))), uintptr(unsafe.Pointer(u16("Auriga S3"))), uintptr(unsafe.Pointer(u16("Disposable isolation experiment"))), 0, 0, uintptr(unsafe.Pointer(&sid)))
	if e != nil {
		r.unsupported("create_AppContainer_profile", e.Error())
		return nil
	}
	defer adv.NewProc("FreeSid").Call(uintptr(unsafe.Pointer(sid)))
	appDir := filepath.Join(f.Dir, "app")
	if e = os.Mkdir(appDir, 0700); e != nil {
		return e
	}
	// Both the account and restricting package SID need access. Keep executable
	// code immutable and explicitly label the separate writable output Low IL.
	if e = dacl(appDir, appReadACL(f.SID, sid.String())); e != nil {
		return e
	}
	binDir := filepath.Join(appDir, "bin")
	outputDir := filepath.Join(appDir, "output")
	for _, dir := range []string{binDir, outputDir} {
		if e = os.Mkdir(dir, 0700); e != nil {
			return e
		}
	}
	if e = dacl(binDir, appReadACL(f.SID, sid.String())); e != nil {
		return e
	}
	if e = dacl(outputDir, appWriteACL(f.SID, sid.String())); e != nil {
		return e
	}
	if e = lowLabel(outputDir); e != nil {
		return e
	}
	if e = dacl(f.Dir, instanceACL(f.sid.String())+"(A;;GRGX;;;"+sid.String()+")"); e != nil {
		return e
	}
	// AppContainer traverses the run root but receives executable access only to the copied tools.
	if e = dacl(o.Root, protectedAdminACL+"(A;;GX;;;"+f.SID+")(A;;GRGX;;;"+sid.String()+")"); e != nil {
		return e
	}
	nodeSource := filepath.Join(o.Root, "tools", "node.exe")
	node := filepath.Join(binDir, "node.exe")
	b, e := os.ReadFile(nodeSource)
	if e != nil {
		return e
	}
	if e = writePlainFile(node, b, 0700); e != nil {
		return e
	}
	if e = dacl(node, appReadACL(f.SID, sid.String())); e != nil {
		return e
	}
	script := filepath.Join(binDir, "probe.js")
	source := `'use strict';
const fs=require('fs'),net=require('net'),cp=require('child_process');
const [out,hostPort,peerFile,hostFile]=process.argv.slice(2);let report={nodeStarted:true,pid:process.pid,fileAccess:{}};
for(const [key,path] of Object.entries({peer:peerFile,host:hostFile})){try{fs.readFileSync(path);report.fileAccess[key]={readable:true}}catch(e){report.fileAccess[key]={readable:false,code:e.code,errno:e.errno}}}
function save(){fs.writeFileSync(out,JSON.stringify(report));}
try {let p=cp.spawnSync(process.env.SystemRoot+'\\System32\\cmd.exe',['/d','/c','echo auriga-s3'],{encoding:'utf8',timeout:3000});report.shell={status:p.status,error:p.error?.message,ok:p.status===0&&p.stdout.trim()==='auriga-s3'};}catch(e){report.shell={ok:false,error:e.message};}
let client=net.connect({host:'127.0.0.1',port:+hostPort});client.setTimeout(1500);client.on('connect',()=>{report.outboundLoopback=true;client.end();save()});client.on('error',e=>{report.outboundLoopback=false;report.clientError=e.code;save()});client.on('timeout',()=>{report.outboundLoopback=false;client.destroy();save()});
let server=net.createServer(c=>{c.end('auriga-s3');});server.on('error',e=>{report.listen=false;report.listenError=e.code;save()});server.listen(0,'127.0.0.1',()=>{report.listen=true;report.port=server.address().port;save()});
setTimeout(()=>process.exit(),15000);save();`
	if e = writePlainFile(script, []byte(source), 0600); e != nil {
		return e
	}
	if e = dacl(script, appReadACL(f.SID, sid.String())); e != nil {
		return e
	}
	aclEvidence := map[string]any{"accountSID": f.SID, "appContainerSID": sid.String(), "nodeSource": nodeSource, "nodeLaunch": node, "noAllApplicationPackagesGrant": true}
	for _, path := range []string{o.Root, f.Dir, appDir, binDir, outputDir, node, script} {
		sd, se := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.LABEL_SECURITY_INFORMATION)
		if se != nil {
			return se
		}
		aclEvidence[path] = sd.String()
	}
	r.observe("AppContainer_explicit_path_permissions", aclEvidence)
	// Establish that the copied executable and its ACL work for the same account before
	// adding LowBox attributes, so a missing image/ordinary ACL failure is not misdiagnosed.
	baselineJob, be := f.job(f.Job+"-node-baseline", 256<<20, 4, 0)
	if be != nil {
		return be
	}
	defer windows.CloseHandle(baselineJob)
	baselineIO, be := newAppStdio(f.Dir, "node-baseline")
	if be != nil {
		return be
	}
	defer baselineIO.close()
	plainOut := filepath.Join(outputDir, "plain-account.json")
	plainProbe := `const fs=require('fs');fs.readFileSync(process.argv[1]);fs.writeFileSync(process.argv[2],JSON.stringify({nodeStarted:true,nodeVersion:process.version,scriptReadable:true,outputWritable:true}));`
	baseline, bpe := appLaunch(f.token, node, []string{"-e", plainProbe, script, plainOut}, outputDir, nil, nil, baselineJob, baselineIO)
	if bpe != nil {
		return fmt.Errorf("Node plain-account baseline: %w", bpe)
	}
	code, bwe := baseline.wait(5 * time.Second)
	baselineDiagnostics := baselineIO.diagnostics(baseline, baselineJob)
	baseline.close()
	var plainData map[string]any
	plainRead := readJSON(plainOut, &plainData)
	plainOK := bwe == nil && code == 0 && plainRead == nil && plainData["scriptReadable"] == true && plainData["outputWritable"] == true
	r.check("Node_plain_account_baseline", plainOK, map[string]any{"exit": code, "error": errorString(join(bwe, plainRead)), "worker": plainData, "process": baselineDiagnostics})
	if !plainOK {
		return fmt.Errorf("Node plain-account baseline failed")
	}
	host, e := listening("tcp4", "127.0.0.1:0")
	if e != nil {
		return e
	}
	defer host.Close()
	positive, he := runWork(f, "network", host.Addr().String())
	if he != nil {
		return he
	}
	r.check("AppContainer_network_positive_control", connected(positive, host.Addr().String()), positive)
	_, hostPort, _ := net.SplitHostPort(host.Addr().String())
	configs := []struct {
		Name   string
		Caps   []string
		Exempt bool
	}{{"none", nil, false}, {"network", []string{"S-1-15-3-1", "S-1-15-3-2", "S-1-15-3-3"}, false}, {"network_outbound_exemption", []string{"S-1-15-3-1", "S-1-15-3-2", "S-1-15-3-3"}, true}}
	for _, cfg := range configs {
		if cfg.Exempt {
			f.Loopback = true
			if e = f.save(); e != nil {
				return e
			}
			text, ce := command("CheckNetIsolation.exe", "LoopbackExempt", "-a", "-n="+f.Profile)
			if ce != nil {
				r.unsupported("outbound_loopback_exemption", map[string]any{"output": text, "error": ce.Error()})
				continue
			}
		}
		e = appCase(f, r, cfg.Name, node, script, outputDir, hostPort, sid, cfg.Caps)
		if e != nil {
			r.check("AppContainer_"+cfg.Name+"_launch", false, e.Error())
		}
	}
	// -is is a separate temporary inbound developer exemption, not the outbound -a switch.
	inboundJob, ie := f.job(f.Job+"-inbound-exemption", 0, 2, 0)
	if ie != nil {
		r.unsupported("inbound_loopback_exemption", ie.Error())
	} else {
		defer windows.CloseHandle(inboundJob)
		defer windows.TerminateJobObject(inboundJob, 93)
		helper, he := launch(0, filepath.Join(os.Getenv("SystemRoot"), "System32", "CheckNetIsolation.exe"), []string{"LoopbackExempt", "-is", "-n=" + f.Profile}, f.Dir, inboundJob)
		if he != nil {
			r.unsupported("inbound_loopback_exemption", he.Error())
		} else {
			time.Sleep(time.Second)
			state, se := windows.WaitForSingleObject(helper.Process, 0)
			if se != nil || state != uint32(windows.WAIT_TIMEOUT) {
				r.unsupported("inbound_loopback_exemption", "CheckNetIsolation -is did not remain active on this image")
			} else {
				ie = appCase(f, r, "network_inbound_exemption", node, script, outputDir, hostPort, sid, []string{"S-1-15-3-1", "S-1-15-3-2", "S-1-15-3-3"})
				if ie != nil {
					r.check("AppContainer_inbound_launch", false, ie.Error())
				}
			}
			helper.kill()
			helper.close()
		}
	}
	r.observe("inbound_exemption_scope", "CheckNetIsolation -is is a developer test; success does not establish a production hosting mechanism")
	return nil
}
func appCase(f *fixture, r *Result, name, node, script, dir, hostPort string, sid *windows.SID, caps []string) error {
	_, metadata, ee := appEnvironment(f.token, dir)
	if ee != nil {
		return ee
	}
	r.observe(name+"_environment", metadata)
	h, e := f.job(f.Job+"-"+name, 256<<20, 12, 0)
	if e != nil {
		return e
	}
	defer windows.CloseHandle(h)
	defer windows.TerminateJobObject(h, 94)
	out := filepath.Join(dir, name+".json")
	if e = writePlainFile(out, []byte("{}"), 0600); e != nil {
		return e
	}
	if e = dacl(out, appWriteACL(f.SID, sid.String())); e != nil {
		return e
	}
	if e = lowLabel(out); e != nil {
		return e
	}
	sd, e := windows.GetNamedSecurityInfo(out, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.LABEL_SECURITY_INFORMATION)
	if e != nil {
		return e
	}
	r.observe(name+"_output_permissions", sd.String())
	stdio, e := newAppStdio(f.Dir, name)
	if e != nil {
		return e
	}
	defer stdio.close()
	p, e := appLaunch(f.token, node, []string{"-e", appBootstrap, script, out, hostPort, f.PeerCanary, f.HostCanary}, dir, sid, caps, h, stdio)
	if e != nil {
		stdio.close()
		r.observe(name+"_process_creation_failure", map[string]any{"error": e.Error(), "win32Error": errorCode(e), "stdout": boundedLog(stdio.outPath), "stderr": boundedLog(stdio.errPath)})
		return e
	}
	defer p.close()
	defer func() { r.observe(name+"_process_diagnostics", stdio.diagnostics(p, h)) }()
	var token windows.Token
	if e = windows.OpenProcessToken(p.Process, windows.TOKEN_QUERY, &token); e != nil {
		return e
	}
	defer token.Close()
	user, ue := token.GetTokenUser()
	if ue != nil {
		return ue
	}
	r.check(name+"_instance_account_token", user.User.Sid.String() == f.sid.String(), user.User.Sid.String())
	var isContainer uint32
	var size uint32
	if e = windows.GetTokenInformation(token, 29, (*byte)(unsafe.Pointer(&isContainer)), 4, &size); e != nil {
		return e
	}
	r.check(name+"_actual_AppContainer_token", isContainer == 1, isContainer)
	data, e := awaitAppReport(p, out, 8*time.Second)
	if e != nil {
		r.observe(name+"_incomplete_report", data)
		return e
	}
	r.check(name+"_Node_executes", data["nodeStarted"] == true, data)
	access, _ := data["fileAccess"].(map[string]any)
	for _, key := range []string{"peer", "host"} {
		entry, _ := access[key].(map[string]any)
		r.check(name+"_denies_"+key+"_file", entry["code"] == "EACCES", entry)
	}
	if name == "none" {
		r.check("AppContainer_no_network_capability_denies_connect", data["outboundLoopback"] == false && data["clientError"] == "EACCES", data)
	}
	shell, _ := data["shell"].(map[string]any)
	r.check(name+"_shell_child", shell["ok"] == true, shell)
	r.check(name+"_listen_127_0_0_1", data["listen"] == true, data)
	reached := false
	if port, ok := data["port"].(float64); ok {
		c, ce := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", int(port)), 1500*time.Millisecond)
		if ce == nil {
			reached = true
			_ = c.Close()
		}
	}
	r.observe(name+"_host_connects_to_listener", reached)
	r.observe(name+"_outbound_loopback", data["outboundLoopback"])
	r.observe(name+"_capabilities", caps)
	return nil
}
