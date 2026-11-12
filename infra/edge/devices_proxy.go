package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

const deviceAdminPath = "/api/iam/v1/admin/devices"
const deviceBindingsPath = deviceAdminPath + "/connection-bindings"

var devicePresenceSlots = make(chan struct{}, 4)

// Runs AFTER the ordinary proxy has selected the entry, CSRF, administrator
// bearer and authenticated IAM transport. IAM remains the permission authority.
type deviceAdminTransport struct {
	base    http.RoundTripper
	gateway *Gateway
}

func (g *Gateway) devicesTransport(base http.RoundTripper) http.RoundTripper {
	return &deviceAdminTransport{base: base, gateway: g}
}
func deviceResponse(request *http.Request, status int, value any) *http.Response {
	raw, _ := json.Marshal(value)
	contentType := "application/json"
	if status >= 400 {
		contentType = "application/problem+json"
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}, "Cache-Control": {"no-store"}}, Body: io.NopCloser(bytes.NewReader(raw)), ContentLength: int64(len(raw)), Request: request}
}
func (t *deviceAdminTransport) bindings(request *http.Request) ([]deviceBinding, error) {
	if t.gateway.DevicesRPC == nil {
		return nil, errDevicePresence
	}
	return t.gateway.DevicesRPC.ReadDeviceBindings(request.Context(), t.gateway.Cfg.Access.TenantID())
}
func (t *deviceAdminTransport) RoundTrip(original *http.Request) (*http.Response, error) {
	path := original.URL.Path
	if path == deviceBindingsPath {
		return deviceResponse(original, 404, map[string]string{"code": "common.not_found", "title": "Not Found"}), nil
	}
	if path != deviceAdminPath && (!strings.HasPrefix(path, deviceAdminPath+"/") || strings.Contains(strings.TrimPrefix(path, deviceAdminPath+"/"), "/")) {
		return t.base.RoundTrip(original)
	}
	if original.Method != http.MethodGet && original.Method != http.MethodDelete {
		return t.base.RoundTrip(original)
	}
	request := original.Clone(original.Context())
	u := *original.URL
	request.URL = &u
	filter := ""
	if path == deviceAdminPath && request.Method == http.MethodGet {
		q := request.URL.Query()
		filter = q.Get("online")
		if len(q["online"]) > 1 || filter != "" && filter != "online" && filter != "offline" && filter != "unknown" {
			return deviceResponse(original, 400, map[string]string{"code": "common.validation_failed", "title": "Bad Request"}), nil
		}
		q.Del("online")
		request.URL.RawQuery = q.Encode()
	}
	request.Header.Set("Accept-Encoding", "identity")
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	// Never query ownership, monitoring or KICK after a failed permission/step-up check.
	if (request.Method == http.MethodGet && response.StatusCode != 200) || (request.Method == http.MethodDelete && response.StatusCode != 204) {
		return response, nil
	}
	ctx, cancel := context.WithTimeout(request.Context(), 3*time.Second)
	defer cancel()
	query := request.WithContext(ctx)
	var bindings []deviceBinding
	bindingErr := errDevicePresence
	acquired := false
	select {
	case devicePresenceSlots <- struct{}{}:
		acquired = true
		defer func() { <-devicePresenceSlots }()
	default:
	}
	if acquired {
		bindings, bindingErr = t.bindings(query)
	}
	var heartbeats *deviceHeartbeats
	if t.gateway.networkWSS != nil {
		heartbeats = &t.gateway.networkWSS.heartbeats
	}
	var presence *devicePresence
	presenceErr := errDevicePresence
	if acquired && bindingErr == nil && len(bindings) > 0 {
		presence, presenceErr = openDevicePresence(t.gateway.Cfg.DevicePresence, heartbeats)
	}
	if presence != nil {
		defer presence.close()
	}
	if request.Method == http.MethodDelete {
		id := strings.ToLower(strings.TrimPrefix(path, deviceAdminPath+"/"))
		selected := make([]deviceBinding, 0)
		for _, b := range bindings {
			if strings.ToLower(b.Device) == id {
				selected = append(selected, b)
			}
		}
		if bindingErr == nil && len(selected) == 0 {
			return response, nil
		}
		if bindingErr == nil && presenceErr == nil {
			presenceErr = presence.disconnect(ctx, selected)
		}
		if bindingErr != nil || presenceErr != nil {
			_ = response.Body.Close()
			return deviceResponse(original, 202, map[string]any{"id": id, "credentialRevoked": true, "disconnectStatus": "pending"}), nil
		}
		return response, nil
	}
	states := map[string]deviceState(nil)
	if bindingErr == nil && presenceErr == nil {
		states, presenceErr = presence.snapshot(ctx, bindings)
	}
	boundDevices := make(map[string]bool, len(bindings))
	for _, binding := range bindings {
		boundDevices[strings.ToLower(binding.Device)] = true
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	_ = response.Body.Close()
	if readErr != nil || len(data) > 8<<20 {
		return nil, errDevicePresence
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(data, &body) != nil {
		return nil, errDevicePresence
	}
	decorate := func(row map[string]any) {
		row["online"] = "unknown"
		delete(row, "lastHeartbeatAt")
		if row["status"] == "revoked" {
			row["disconnectStatus"] = "pending"
		}
		if bindingErr != nil {
			return
		}
		id := row["id"].(string)
		state, ok := states[id]
		if !boundDevices[strings.ToLower(id)] {
			state.Online = "offline"
		} else if presenceErr != nil || !ok {
			return
		}
		row["online"] = state.Online
		if row["status"] == "revoked" {
			if state.Online == "offline" {
				row["disconnectStatus"] = "confirmed"
			} else {
				row["disconnectStatus"] = "pending"
			}
		}
		if state.Online != "unknown" && !state.Heartbeat.IsZero() {
			row["lastHeartbeatAt"] = state.Heartbeat.Format(time.RFC3339Nano)
		}
		if !state.Activity.IsZero() {
			row["lastSeenAt"] = state.Activity.Format(time.RFC3339Nano)
		}
	}
	if path == deviceAdminPath {
		var rows []map[string]any
		if json.Unmarshal(body["items"], &rows) != nil {
			return nil, errDevicePresence
		}
		selected := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			if _, ok := row["id"].(string); !ok {
				return nil, errDevicePresence
			}
			decorate(row)
			if filter == "" || row["online"] == filter {
				selected = append(selected, row)
			}
		}
		body["items"], _ = json.Marshal(selected)
		return deviceResponse(original, 200, body), nil
	}
	var row map[string]any
	if json.Unmarshal(data, &row) != nil {
		return nil, errDevicePresence
	}
	if _, ok := row["id"].(string); !ok {
		return nil, errDevicePresence
	}
	decorate(row)
	return deviceResponse(original, 200, row), nil
}
