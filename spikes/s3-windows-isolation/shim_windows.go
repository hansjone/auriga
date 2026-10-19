//go:build windows && amd64

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

type controlRequest struct {
	Name, Password, Dir, Root, Job, Action string
	LogOffset                              int64
}
type controlReply struct {
	ShimPID   uint32    `json:"shimPID"`
	PIDs      []uintptr `json:"pids"`
	LogBytes  int64     `json:"logBytes"`
	TailBytes int       `json:"tailBytes"`
	Error     string    `json:"error,omitempty"`
}

func controllerWorker(_ []string) {
	var q controlRequest
	reply := controlReply{}
	e := json.NewDecoder(os.Stdin).Decode(&q)
	if e == nil {
		switch q.Action {
		case "start":
			var token windows.Token
			token, e = logon(q.Name, q.Password, 4)
			q.Password = ""
			if e == nil {
				var p *process
				p, e = launch(token, filepath.Join(q.Root, "tools", "s3probe.exe"), []string{"_shim", q.Dir, q.Job}, q.Dir, 0)
				_ = token.Close()
				if e == nil {
					reply.ShimPID = p.ProcessId
					p.close()
				}
			}
		case "adopt":
			var h windows.Handle
			h, e = openJob(q.Job)
			if e == nil {
				reply.PIDs, e = jobPIDs(h)
				if st, se := os.Stat(filepath.Join(q.Dir, "instance.log")); se == nil {
					reply.LogBytes = st.Size()
				} else {
					e = join(e, se)
				}
				log, le := os.Open(filepath.Join(q.Dir, "instance.log"))
				if le == nil {
					_, le = log.Seek(q.LogOffset, io.SeekStart)
					if le == nil {
						var tail []byte
						tail, le = io.ReadAll(io.LimitReader(log, 64<<10))
						reply.TailBytes = len(tail)
					}
					_ = log.Close()
				}
				e = join(e, le)
				_ = windows.CloseHandle(h)
			}
		case "terminate":
			var h windows.Handle
			h, e = openJob(q.Job)
			if e == nil {
				e = windows.TerminateJobObject(h, 95)
				_ = windows.CloseHandle(h)
			}
		default:
			e = fmt.Errorf("invalid controller action")
		}
	}
	if e != nil {
		reply.Error = e.Error()
	}
	emit(reply)
	if e != nil {
		os.Exit(1)
	}
}
func shimWorker(args []string) {
	if len(args) != 2 {
		os.Exit(2)
	}
	dir, name := args[0], args[1]
	h, e := newJob(name, 192<<20, 8, 2000)
	if e != nil {
		_ = writeJSON(filepath.Join(dir, "shim-error.json"), map[string]any{"error": e.Error()})
		os.Exit(3)
	}
	defer windows.CloseHandle(h)
	p, e := launch(0, os.Args[0], []string{"_work", filepath.Join(dir, "payload.json"), "sleep"}, dir, h)
	if e != nil {
		os.Exit(4)
	}
	defer p.close()
	defer p.kill()
	log, e := os.OpenFile(filepath.Join(dir, "instance.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		os.Exit(5)
	}
	defer log.Close()
	_ = writeJSON(filepath.Join(dir, "shim-ready.json"), map[string]any{"shimPID": os.Getpid(), "payloadPID": p.ProcessId})
	until := time.Now().Add(60 * time.Second)
	for time.Now().Before(until) {
		if _, e = os.Stat(filepath.Join(dir, "stop")); e == nil {
			return
		}
		_, _ = fmt.Fprintf(log, "%s payload=%d\n", time.Now().UTC().Format(time.RFC3339Nano), p.ProcessId)
		_ = log.Sync()
		time.Sleep(150 * time.Millisecond)
	}
}
func callController(q controlRequest) (controlReply, error) {
	// Anonymous stdin pipe only: the password never appears in command line, environment or files.
	b, e := json.Marshal(q)
	if e != nil {
		return controlReply{}, e
	}
	c := exec.Command(os.Args[0], "_controller")
	c.Stdin = bytes.NewReader(b)
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = &out
	e = c.Run()
	clear(b)
	var reply controlReply
	de := json.Unmarshal(out.Bytes(), &reply)
	if e != nil {
		return reply, fmt.Errorf("controller: %w (%s)", e, reply.Error)
	}
	return reply, de
}
func shimExperiment(o options, r *Result) (err error) {
	f, e := newFixture(o, "s")
	if e != nil {
		return e
	}
	defer func() { err = join(err, f.close()) }()
	reply, e := callController(controlRequest{Name: f.Name, Password: f.password, Dir: f.Dir, Root: o.Root, Job: f.Job, Action: "start"})
	if e != nil {
		return e
	}
	sh, e := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, reply.ShimPID)
	if e != nil {
		return e
	}
	defer windows.CloseHandle(sh)
	defer func() {
		_ = os.WriteFile(filepath.Join(f.Dir, "stop"), nil, 0600)
		_, _ = windows.WaitForSingleObject(sh, 8000)
	}()
	if e = awaitFile(filepath.Join(f.Dir, "shim-ready.json"), 15*time.Second); e != nil {
		return e
	}
	var ready struct{ ShimPID, PayloadPID uint32 }
	if e = readJSON(filepath.Join(f.Dir, "shim-ready.json"), &ready); e != nil {
		return e
	}
	// callController waited for the first controller to exit. No node-agent job handle remains.
	state, e := windows.WaitForSingleObject(sh, 0)
	r.check("shim_survives_controller_exit", e == nil && state == uint32(windows.WAIT_TIMEOUT), map[string]any{"shimPID": reply.ShimPID})
	a, e := callController(controlRequest{Dir: f.Dir, Job: f.Job, Action: "adopt"})
	if e != nil {
		return e
	}
	time.Sleep(500 * time.Millisecond)
	b, e := callController(controlRequest{Dir: f.Dir, Job: f.Job, Action: "adopt", LogOffset: a.LogBytes})
	if e != nil {
		return e
	}
	r.check("new_controller_opens_named_job", pidIn(b.PIDs, ready.PayloadPID), b)
	r.check("new_controller_resumes_file_logs", b.LogBytes > a.LogBytes && b.TailBytes > 0, map[string]any{"before": a.LogBytes, "after": b.LogBytes, "tailBytes": b.TailBytes})
	payload, e := windows.OpenProcess(windows.SYNCHRONIZE, false, ready.PayloadPID)
	if e != nil {
		return e
	}
	defer windows.CloseHandle(payload)
	_, e = callController(controlRequest{Dir: f.Dir, Job: f.Job, Action: "terminate"})
	if e != nil {
		return e
	}
	state, e = windows.WaitForSingleObject(payload, 5000)
	r.check("adopted_job_terminates_payload", e == nil && state == windows.WAIT_OBJECT_0, ready)
	return nil
}
