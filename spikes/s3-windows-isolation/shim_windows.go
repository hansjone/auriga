//go:build windows && amd64

package main

import (
	"fmt"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type probeService struct {
	f           fixture
	sourcePID   uint32
	sourceToken windows.Handle
	shimName    string
}

func (s *probeService) Execute(_ []string, requests <-chan svc.ChangeRequest, states chan<- svc.Status) (bool, uint32) {
	states <- svc.Status{State: svc.StartPending}
	var job windows.Handle
	var payload *process
	var log *os.File
	if s.shimName != "" {
		m, e := mgr.Connect()
		if e != nil {
			return false, 1
		}
		defer m.Disconnect()
		shim, e := m.OpenService(s.shimName)
		if e != nil {
			return false, 2
		}
		defer shim.Close()
		if e = shim.Start(); e != nil {
			return false, 3
		}
	} else {
		source, e := windows.OpenProcess(windows.PROCESS_DUP_HANDLE, false, s.sourcePID)
		if e != nil {
			return false, 4
		}
		defer windows.CloseHandle(source)
		var token windows.Handle
		if e = windows.DuplicateHandle(source, s.sourceToken, windows.CurrentProcess(), &token, 0, false, windows.DUPLICATE_SAME_ACCESS); e != nil {
			return false, 5
		}
		defer windows.CloseHandle(token)
		job, e = newJob(s.f.Job, 192<<20, 16, 2000)
		if e != nil {
			return false, 6
		}
		defer windows.CloseHandle(job)
		defer windows.TerminateJobObject(job, 92)
		payload, e = launch(windows.Token(token), probePath(s.f.o), []string{"_work", filepath.Join(s.f.workDir(), "payload.json"), "sleep"}, s.f.workDir(), job)
		if e != nil {
			return false, 7
		}
		defer payload.close()
		defer payload.kill()
		log, e = os.OpenFile(filepath.Join(s.f.Dir, "instance.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if e != nil {
			return false, 8
		}
		defer log.Close()
		current, e := windows.GetCurrentProcessToken().GetTokenUser()
		if e != nil {
			return false, 9
		}
		if e = writeJSON(filepath.Join(s.f.Dir, "shim-ready.json"), map[string]any{"shimPID": os.Getpid(), "payloadPID": payload.ProcessId, "shimSID": current.User.Sid.String()}); e != nil {
			return false, 10
		}
	}
	states <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	lease := time.NewTimer(90 * time.Second)
	defer lease.Stop()
	for {
		select {
		case req := <-requests:
			switch req.Cmd {
			case svc.Stop, svc.Shutdown:
				states <- svc.Status{State: svc.StopPending}
				return false, 0
			case svc.Interrogate:
				states <- req.CurrentStatus
			}
		case <-tick.C:
			if log != nil {
				_, _ = fmt.Fprintf(log, "%s payload=%d\n", time.Now().UTC().Format(time.RFC3339Nano), payload.ProcessId)
				_ = log.Sync()
			}
		case <-lease.C:
			return false, 0
		}
	}
}
func serviceWorker(mode string, args []string) {
	current, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil || current.User.Sid.String() != "S-1-5-18" {
		os.Exit(2)
	}
	var handler probeService
	var name string
	if mode == "_shim-service" {
		if len(args) != 7 {
			os.Exit(2)
		}
		name = args[0]
		o := options{Root: args[1], ID: args[2]}
		if validateOptions(o) != nil {
			os.Exit(2)
		}
		pid, e := strconv.ParseUint(args[3], 10, 32)
		if e != nil {
			os.Exit(2)
		}
		token, e := strconv.ParseUint(args[4], 10, 64)
		if e != nil {
			os.Exit(2)
		}
		handler.f = fixture{o: o, journal: journal{Dir: args[5], Job: args[6]}}
		handler.sourcePID = uint32(pid)
		handler.sourceToken = windows.Handle(token)
	} else {
		if len(args) != 4 {
			os.Exit(2)
		}
		name = args[0]
		if validateOptions(options{Root: args[2], ID: args[3]}) != nil {
			os.Exit(2)
		}
		handler.shimName = args[1]
	}
	if e = svc.Run(name, &handler); e != nil {
		os.Exit(3)
	}
}
func waitService(s *mgr.Service, state svc.State, timeout time.Duration) error {
	until := time.Now().Add(timeout)
	for time.Now().Before(until) {
		v, e := s.Query()
		if e != nil {
			return e
		}
		if v.State == state {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("service did not reach %d", state)
}
func stopDeleteService(name string) error {
	m, e := mgr.Connect()
	if e != nil {
		return e
	}
	defer m.Disconnect()
	s, e := m.OpenService(name)
	if e == windows.ERROR_SERVICE_DOES_NOT_EXIST {
		return nil
	}
	if e != nil {
		return e
	}
	defer s.Close()
	v, e := s.Query()
	if e != nil {
		return e
	}
	if v.State != svc.Stopped {
		_, e = s.Control(svc.Stop)
		if e != nil {
			return e
		}
		if e = waitService(s, svc.Stopped, 10*time.Second); e != nil {
			return e
		}
	}
	return s.Delete()
}
func shimExperiment(o options, r *Result) (err error) {
	f, e := newFixture(o, "s")
	if e != nil {
		return e
	}
	defer func() { err = join(err, f.close()) }()
	m, e := mgr.Connect()
	if e != nil {
		return e
	}
	defer m.Disconnect()
	f.Services = []string{"AurigaS3-" + o.ID + "-shim", "AurigaS3-" + o.ID + "-agent"}
	if e = f.save(); e != nil {
		return e
	}
	sh, e := m.CreateService(f.Services[0], probePath(o), mgr.Config{StartType: mgr.StartManual, ServiceStartName: "LocalSystem"}, "_shim-service", f.Services[0], o.Root, o.ID, strconv.Itoa(os.Getpid()), strconv.FormatUint(uint64(f.token), 10), f.Dir, f.Job)
	if e != nil {
		return e
	}
	defer sh.Close()
	agent, e := m.CreateService(f.Services[1], probePath(o), mgr.Config{StartType: mgr.StartManual, ServiceStartName: "LocalSystem"}, "_agent-service", f.Services[1], f.Services[0], o.Root, o.ID)
	if e != nil {
		return e
	}
	defer agent.Close()
	if e = agent.Start(); e != nil {
		return e
	}
	if e = waitService(agent, svc.Running, 15*time.Second); e != nil {
		return e
	}
	if e = awaitFile(filepath.Join(f.Dir, "shim-ready.json"), 15*time.Second); e != nil {
		return e
	}
	var ready struct {
		ShimPID, PayloadPID uint32
		ShimSID             string
	}
	if e = readJSON(filepath.Join(f.Dir, "shim-ready.json"), &ready); e != nil {
		return e
	}
	r.check("shim_runs_as_SYSTEM", ready.ShimSID == "S-1-5-18", ready)
	if _, e = agent.Control(svc.Stop); e != nil {
		return e
	}
	if e = waitService(agent, svc.Stopped, 10*time.Second); e != nil {
		return e
	}
	v, e := sh.Query()
	r.check("shim_survives_node_service_stop", e == nil && v.State == svc.Running, v)
	h, e := openJob(f.Job)
	if e != nil {
		return e
	}
	defer windows.CloseHandle(h)
	ids, e := jobPIDs(h)
	if e != nil {
		return e
	}
	r.check("new_agent_reopens_job", pidIn(ids, ready.PayloadPID), ids)
	log, e := os.Open(filepath.Join(f.Dir, "instance.log"))
	if e != nil {
		return e
	}
	offset, e := log.Seek(0, io.SeekEnd)
	if e != nil {
		_ = log.Close()
		return e
	}
	_ = log.Close()
	time.Sleep(500 * time.Millisecond)
	log, e = os.Open(filepath.Join(f.Dir, "instance.log"))
	if e != nil {
		return e
	}
	defer log.Close()
	_, e = log.Seek(offset, io.SeekStart)
	if e != nil {
		return e
	}
	tail, e := io.ReadAll(io.LimitReader(log, 64<<10))
	if e != nil {
		return e
	}
	r.check("new_agent_resumes_file_logs", len(tail) > 0, map[string]any{"offset": offset, "newBytes": len(tail)})
	sd, e := windows.GetSecurityInfo(h, windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return e
	}
	owner, _, e := sd.Owner()
	if e != nil {
		return e
	}
	r.check("job_owner_SYSTEM", owner.String() == "S-1-5-18", sd.String())
	negative, e := runWork(f, "job-security", f.Job, strconv.FormatUint(uint64(ready.ShimPID), 10))
	if e != nil {
		return e
	}
	r.check("instance_own_job_positive_control", win32Code(negative["ownJobControl"]) == 0, negative["ownJobControl"])
	control, ce := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_DUP_HANDLE|windows.PROCESS_VM_WRITE, false, ready.ShimPID)
	if ce == nil {
		_ = windows.CloseHandle(control)
	}
	r.check("SYSTEM_can_open_shim_control", ce == nil, errorString(ce))
	for _, name := range []string{"jobOpen", "jobWriteDACL", "shimOpen"} {
		r.check("instance_denied_"+name, win32Code(negative[name]) == 5, negative[name])
	}
	if e = windows.TerminateJobObject(h, 95); e != nil {
		return e
	}
	until := time.Now().Add(5 * time.Second)
	ended := false
	for time.Now().Before(until) {
		ids, qe := jobPIDs(h)
		if qe != nil {
			return qe
		}
		if len(ids) == 0 {
			ended = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	r.check("adopted_job_terminated", ended, nil)
	return nil
}
