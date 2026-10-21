package observer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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
	if t.Process == ProcessStopped || t.Process == ProcessFailed {
		return res
	}
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	if t.BaseURL == nil {
		return res
	}
	// Even supplied clients must never follow a health endpoint to another target.
	local := *client
	if local.Transport == nil {
		local.Transport = &http.Transport{Proxy: nil, DisableKeepAlives: true}
	}
	local.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	u := t.BaseURL.ResolveReference(&url.URL{Path: "/_auriga/health"})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	resp, err := local.Do(req)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		res.Err = fmt.Sprintf("health status %d", resp.StatusCode)
		return res
	}
	const maxHealthBytes = 8 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHealthBytes+1))
	if err != nil || len(body) > maxHealthBytes {
		res.Err = "invalid health response"
		return res
	}
	var health struct {
		Status        string `json:"status"`
		Ready         bool   `json:"ready"`
		Protocol      int    `json:"protocol"`
		PluginVersion string `json:"pluginVersion"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&health) != nil || decoder.Decode(new(any)) != io.EOF ||
		health.Status != "ok" || !health.Ready || health.Protocol != 1 || strings.TrimSpace(health.PluginVersion) == "" {
		res.Err = "invalid health response"
		return res
	}
	res.HTTPOK = true
	res.Ready = res.ProcessOK
	return res
}
