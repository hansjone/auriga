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
func appLaunch(token windows.Token, exe string, args []string, dir string, sid *windows.SID, capIDs []string, job windows.Handle) (*process, error) {
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
	attrs, e := windows.NewProcThreadAttributeList(1)
	if e != nil {
		return nil, e
	}
	defer attrs.Delete()
	if e = attrs.Update(0x20009, unsafe.Pointer(&sc), unsafe.Sizeof(sc)); e != nil {
		return nil, e
	}
	si := windows.StartupInfoEx{}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.ProcThreadAttributeList = attrs.List()
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
	e = windows.CreateProcessAsUser(token, u16(exe), u16(windows.ComposeCommandLine(append([]string{exe}, args...))), nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_SUSPENDED|windows.CREATE_UNICODE_ENVIRONMENT|windows.CREATE_NO_WINDOW, &env[0], u16(dir), &si.StartupInfo, &p.ProcessInformation)
	runtime.KeepAlive(sc)
	runtime.KeepAlive(caps)
	runtime.KeepAlive(env)
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
	// Grant only this AppContainer, not ALL APPLICATION PACKAGES, access to its scratch files.
	acl := instanceACL(f.sid.String()) + "(A;OICI;FA;;;" + sid.String() + ")"
	if e = dacl(appDir, acl); e != nil {
		return e
	}
	low, e := windows.SecurityDescriptorFromString("S:(ML;OICI;NW;;;LW)")
	if e != nil {
		return e
	}
	sacl, _, e := low.SACL()
	if e != nil {
		return e
	}
	if e = windows.SetNamedSecurityInfo(appDir, windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, sacl); e != nil {
		return e
	}
	if e = dacl(f.Dir, instanceACL(f.sid.String())+"(A;;GRGX;;;"+sid.String()+")"); e != nil {
		return e
	}
	// AppContainer traverses the run root but receives executable access only to the copied tools.
	if e = dacl(o.Root, adminACL+"(A;;GRGX;;;AU)(A;;GRGX;;;"+sid.String()+")"); e != nil {
		return e
	}
	nodeSource := filepath.Join(o.Root, "tools", "node.exe")
	node := filepath.Join(appDir, "node.exe")
	b, e := os.ReadFile(nodeSource)
	if e != nil {
		return e
	}
	if e = os.WriteFile(node, b, 0700); e != nil {
		return e
	}
	script := filepath.Join(appDir, "probe.js")
	source := `'use strict';
const fs=require('fs'),net=require('net'),cp=require('child_process');
const [out,hostPort]=process.argv.slice(2);let report={nodeStarted:true,pid:process.pid};
function save(){fs.writeFileSync(out,JSON.stringify(report));}
try {let p=cp.spawnSync(process.env.SystemRoot+'\\System32\\cmd.exe',['/d','/c','echo auriga-s3'],{encoding:'utf8',timeout:3000});report.shell={status:p.status,error:p.error?.message,ok:p.status===0&&p.stdout.trim()==='auriga-s3'};}catch(e){report.shell={ok:false,error:e.message};}
let client=net.connect({host:'127.0.0.1',port:+hostPort});client.setTimeout(1500);client.on('connect',()=>{report.outboundLoopback=true;client.end();save()});client.on('error',e=>{report.outboundLoopback=false;report.clientError=e.code;save()});client.on('timeout',()=>{report.outboundLoopback=false;client.destroy();save()});
let server=net.createServer(c=>{c.end('auriga-s3');});server.on('error',e=>{report.listen=false;report.listenError=e.code;save()});server.listen(0,'127.0.0.1',()=>{report.listen=true;report.port=server.address().port;save()});
setTimeout(()=>process.exit(),15000);save();`
	if e = os.WriteFile(script, []byte(source), 0600); e != nil {
		return e
	}
	// Establish that the copied executable and its ACL work for the same account before
	// adding LowBox attributes, so a missing image/ordinary ACL failure is not misdiagnosed.
	baselineJob, be := f.job(f.Job+"-node-baseline", 256<<20, 4, 0)
	if be != nil {
		return be
	}
	defer windows.CloseHandle(baselineJob)
	baseline, bpe := launch(f.token, node, []string{"--version"}, appDir, baselineJob)
	if bpe != nil {
		return fmt.Errorf("Node plain-account baseline: %w", bpe)
	}
	code, bwe := baseline.wait(5 * time.Second)
	baseline.kill()
	baseline.close()
	r.check("Node_plain_account_baseline", bwe == nil && code == 0, map[string]any{"exit": code, "error": errorString(bwe)})
	if bwe != nil || code != 0 {
		return fmt.Errorf("Node plain-account baseline failed")
	}
	host, e := listening("tcp4", "127.0.0.1:0")
	if e != nil {
		return e
	}
	defer host.Close()
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
		e = appCase(f, r, cfg.Name, node, script, appDir, hostPort, sid, cfg.Caps)
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
				ie = appCase(f, r, "network_inbound_exemption", node, script, appDir, hostPort, sid, []string{"S-1-15-3-1", "S-1-15-3-2", "S-1-15-3-3"})
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
	p, e := appLaunch(f.token, node, []string{script, out, hostPort}, dir, sid, caps, h)
	if e != nil {
		return e
	}
	defer p.close()
	defer p.kill()
	if e = awaitFile(out, 8*time.Second); e != nil {
		return e
	}
	time.Sleep(1800 * time.Millisecond)
	var data map[string]any
	if e = readJSON(out, &data); e != nil {
		return e
	}
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
	r.check(name+"_Node_executes", data["nodeStarted"] == true, data)
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
