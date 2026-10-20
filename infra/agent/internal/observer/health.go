package observer

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ProcessStatus is provided by the runtime/reconciler (A2).
type ProcessStatus int

const (
	ProcessUnknown ProcessStatus = iota
	ProcessRunning
	ProcessStopped
	ProcessFailed
)

// InstanceTarget is what the observer probes.
type InstanceTarget struct {
	ID         string
	BaseURL    *url.URL // instance HTTP, used for /_auriga/health
	RootDir    string
	Process    ProcessStatus
	CgroupPath string // linux; empty on windows / process drivers without cgroup
}

// HealthResult combines HTTP probe and process state.
type HealthResult struct {
	InstanceID string
	Ready      bool
	HTTPOK     bool
	ProcessOK  bool
	Err        string
}

// ProbeHealth GETs /_auriga/health and combines with process status.
func ProbeHealth(ctx context.Context, client *http.Client, t InstanceTarget) HealthResult {
	res := HealthResult{InstanceID: t.ID, ProcessOK: t.Process == ProcessRunning}
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	if t.BaseURL == nil {
		res.Ready = res.ProcessOK
		return res
	}
	u := t.BaseURL.ResolveReference(&url.URL{Path: "/_auriga/health"})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	resp, err := client.Do(req)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	defer func() { _ = resp.Body.Close() }()
	res.HTTPOK = resp.StatusCode >= 200 && resp.StatusCode < 300
	res.Ready = res.HTTPOK && res.ProcessOK
	if !res.HTTPOK {
		res.Err = fmt.Sprintf("health status %d", resp.StatusCode)
	}
	return res
}
