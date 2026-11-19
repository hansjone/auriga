package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/observer"
	"github.com/hansjone/auriga/infra/agent/internal/sharednode"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

type dedicatedReceipt struct {
	SchemaVersion  int             `json:"schemaVersion"`
	Binding        json.RawMessage `json:"binding"`
	ViewID         string          `json:"viewId"`
	IntentDigest   string          `json:"intentDigest"`
	Status         string          `json:"status"`
	Reason         string          `json:"reason"`
	HostInstanceID string          `json:"hostInstanceId"`
}

func (m *PersistentPools) closePoolIngress(pool string) {
	delete(m.active, pool)
	if m.Unpublish != nil {
		m.Unpublish(pool)
	}
}

func (m *PersistentPools) refreshPoolSnapshot(ctx context.Context, pool string) ([]*nodev1.SharedDshBinding, []*nodev1.SharedDshResourceView, error) {
	refresh, ok := m.Resources.(interface {
		Refresh(context.Context, *nodev1.SharedDshBinding) (*nodev1.SharedDshResourceView, error)
	})
	if !ok {
		return m.snapshot(pool)
	}
	bindings, err := m.Pools.Bindings(pool)
	if err != nil {
		return nil, nil, err
	}
	views := make([]*nodev1.SharedDshResourceView, len(bindings))
	for i, b := range bindings {
		view, err := refresh.Refresh(ctx, b)
		if err != nil || view == nil || !proto.Equal(view.Binding, b) {
			return bindings, nil, fmt.Errorf("current placement resource snapshot unavailable")
		}
		views[i] = view
	}
	if m.views == nil {
		m.views = map[string]*nodev1.SharedDshResourceView{}
	}
	for _, view := range views {
		m.views[view.Binding.PlacementId] = proto.Clone(view).(*nodev1.SharedDshResourceView)
	}
	return bindings, views, nil
}

func (m *PersistentPools) dedicatedObservation(ctx context.Context, view *nodev1.SharedDshResourceView) (dedicatedReceipt, error) {
	var zero dedicatedReceipt
	intent, ok := m.Resources.(interface {
		DedicatedIntent(string, *nodev1.SharedDshResourceView) (string, string, error)
	})
	if !ok || m.Driver == nil || m.Driver.Activation == nil || m.Driver.Activation.Store == nil {
		return zero, fmt.Errorf("dedicated observation unavailable")
	}
	pool := view.Binding.PhysicalInstanceId
	_, digest, err := intent.DedicatedIntent(pool, view)
	if err != nil {
		return zero, err
	}
	record, err := m.Driver.Activation.Store.GetExecution(pool)
	if err != nil || record == nil {
		return zero, fmt.Errorf("dedicated signed execution unavailable")
	}
	before, err := m.Driver.verifyContainer(ctx, record)
	if err != nil {
		return zero, err
	}
	process, err := containerProcess(before)
	if err != nil {
		return zero, err
	}
	path, err := m.Pools.Layout.Path(pool, "control", "process-state")
	if err != nil {
		return zero, err
	}
	var saved poolProcess
	if err = unifiedRead(path, &saved); err != nil {
		return zero, err
	}
	if saved.Container != record.ContainerID || saved.PID != process.pid || !saved.StartedAt.Equal(process.startedAt) {
		return zero, fmt.Errorf("dedicated process lifetime changed")
	}
	endpoint, err := m.Driver.Endpoint(pool)
	if err != nil {
		return zero, err
	}
	receipt, err := readDedicatedReceipt(ctx, endpoint.URL, view, digest)
	if err != nil {
		return receipt, err
	}
	after, err := m.Driver.verifyContainer(ctx, record)
	if err != nil {
		return receipt, err
	}
	current, err := containerProcess(after)
	if err != nil || current.pid != process.pid || !current.startedAt.Equal(process.startedAt) {
		return receipt, fmt.Errorf("dedicated receipt crossed a process change")
	}
	if latest, err := m.Driver.Endpoint(pool); err != nil || latest.URL != endpoint.URL {
		return receipt, fmt.Errorf("dedicated endpoint changed")
	}
	refresh, ok := m.Resources.(interface {
		Refresh(context.Context, *nodev1.SharedDshBinding) (*nodev1.SharedDshResourceView, error)
	})
	if !ok {
		return receipt, fmt.Errorf("dedicated current view authority unavailable")
	}
	latest, err := refresh.Refresh(ctx, view.Binding)
	if err != nil || !proto.Equal(latest, view) || ctx.Err() != nil {
		return receipt, fmt.Errorf("dedicated receipt view superseded")
	}
	return receipt, nil
}

func (m *PersistentPools) watchDedicated(ctx context.Context, bindings []*nodev1.SharedDshBinding, views []*nodev1.SharedDshResourceView) {
	pool := bindings[0].PhysicalInstanceId
	now := time.Now()
	if clock, ok := m.Resources.(interface{ Now() time.Time }); ok {
		now = clock.Now()
	}
	err := CheckPoolViews(bindings, views, now.Unix())
	if err == nil && (len(bindings) != 1 || bindings[0].Mode != nodev1.DshInstanceMode_DSH_INSTANCE_MODE_DEDICATED || m.Resources == nil) {
		err = fmt.Errorf("dedicated requires one current placement")
	}
	if err == nil {
		err = m.Resources.Verify(ctx, pool, views)
	}
	status, reason := "rejected", "cleanup-unconfirmed"
	if err != nil {
		trip := ""
		if errors.Is(err, observer.ErrPoolResourceExpired) {
			trip = "resource-expired"
			reason = "expired"
		}
		if errors.Is(err, observer.ErrPoolResourceRevoked) {
			trip = "resource-revoked"
			reason = "revoked"
		}
		if trip != "" {
			if impact, tripErr := m.Pools.Trip(ctx, pool, views[0].ViewId, trip); tripErr == nil {
				if impact.State == "stopped" {
					status = "stopped"
				}
				m.report(&nodev1.SessionRequest{Body: &nodev1.SessionRequest_SharedDshGuardianImpact{SharedDshGuardianImpact: impact}})
			}
		}
	} else {
		receipt, observeErr := m.dedicatedObservation(ctx, views[0])
		if observeErr == nil {
			file, pathErr := m.Pools.Layout.Path(pool, "control", "dedicated-resource-receipt")
			if pathErr == nil {
				observeErr = unifiedAtomic(file, receipt)
			} else {
				observeErr = pathErr
			}
		}
		if observeErr == nil && receipt.Status == "ready" {
			endpoint, endpointErr := m.Driver.Endpoint(pool)
			if endpointErr == nil {
				if m.Publish != nil {
					m.Publish(pool, endpoint)
				}
				if m.active == nil {
					m.active = map[string]bool{}
				}
				m.active[pool] = true
				status, reason = "ready", "verified"
			}
		}
	}
	if status != "ready" {
		m.closePoolIngress(pool)
	}
	for _, view := range views {
		m.report(&nodev1.SessionRequest{Body: &nodev1.SessionRequest_SharedDshResourceReport{SharedDshResourceReport: &nodev1.SharedDshResourceReport{SchemaVersion: 1, Binding: view.Binding, ViewId: view.ViewId, Status: status, Reason: reason, ObservedAt: now.Unix()}}})
	}
}

func dedicatedGet(ctx context.Context, endpoint, path string) ([]byte, int, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, 0, fmt.Errorf("invalid dedicated Host endpoint")
	}
	u.Path, u.RawPath = path, ""
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, ResponseHeaderTimeout: 2 * time.Second, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Encoding", "identity")
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil || len(raw) > 16384 || (response.StatusCode != 200 && response.StatusCode != 503) || strings.Split(response.Header.Get("Content-Type"), ";")[0] != "application/json" || response.Header.Get("Set-Cookie") != "" || response.Header.Get("Location") != "" {
		return nil, 0, fmt.Errorf("invalid dedicated Host response")
	}
	if _, err = DecodeUnifiedJSON(raw); err != nil {
		return nil, 0, err
	}
	return raw, response.StatusCode, nil
}

func readDedicatedReceipt(ctx context.Context, endpoint string, view *nodev1.SharedDshResourceView, digest string) (dedicatedReceipt, error) {
	var receipt dedicatedReceipt
	raw, status, err := dedicatedGet(ctx, endpoint, "/_auriga/dedicated-resources")
	if err != nil {
		return receipt, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&receipt); err != nil {
		return receipt, err
	}
	binding := new(nodev1.SharedDshBinding)
	if receipt.SchemaVersion != 1 || sharednode.DecodeHostValue(receipt.Binding, binding) != nil || !proto.Equal(binding, view.Binding) || receipt.ViewID != view.ViewId || receipt.IntentDigest != digest || !uUUID.MatchString(receipt.HostInstanceID) {
		return receipt, fmt.Errorf("dedicated receipt binding/view/intent mismatch")
	}
	valid := false
	switch receipt.Status {
	case "ready":
		valid = status == 200 && receipt.Reason == "verified"
	case "applying":
		valid = status == 503 && receipt.Reason == "resource.applying"
	case "restart-required":
		valid = status == 503 && receipt.Reason == "resource.restart_required"
	case "failed":
		valid = status == 503 && (receipt.Reason == "resource.apply_failed" || receipt.Reason == "resource.state_conflict" || receipt.Reason == "resource.signature_invalid" || receipt.Reason == "shared.revoked")
	}
	if !valid {
		return receipt, fmt.Errorf("invalid dedicated receipt state")
	}
	if receipt.Status != "ready" {
		return receipt, nil
	}
	raw, status, err = dedicatedGet(ctx, endpoint, "/_auriga/dedicated-profile")
	if err != nil || status != 200 {
		return receipt, fmt.Errorf("dedicated profile unavailable")
	}
	m, err := DecodeUnifiedJSON(raw)
	if err != nil {
		return receipt, err
	}
	caps, ok := m["capabilities"].(map[string]any)
	if len(m) != 4 || m["schemaVersion"] != json.Number("1") || m["physicalInstanceId"] != view.Binding.PhysicalInstanceId || m["profile"] != "auriga.dedicated.v1" || !ok || len(caps) != 4 || caps["pluginInstall"] != true || caps["privateSkills"] != true || caps["includeDefaultRoots"] != true || caps["toolPresentation"] != "native" {
		return receipt, fmt.Errorf("dedicated capabilities mismatch")
	}
	return receipt, nil
}
