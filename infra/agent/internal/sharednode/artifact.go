package sharednode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"io"
	"net/http"
	"strings"
	"time"
)

// Artifact requires the dedicated resource adapter; private nodeDownload grants
// are never reused. The configured origin is installation-owned. Until that
// adapter is deployed a nonempty pool fails closed.
func (c *Client) Artifact(ctx context.Context, binding *nodev1.SharedDshBinding, lease, artifact string, maximum int64) ([]byte, time.Time, error) {
	fail := errors.New("shared resource artifact transport unavailable")
	if c.Resources == nil || binding == nil || binding.TenantId != c.Settings.TenantID || !uuid.MatchString(lease) || maximum <= 0 || maximum > 5*1024*1024 {
		return nil, time.Time{}, fail
	}
	b, e := wire(binding)
	if e != nil {
		return nil, time.Time{}, e
	}
	var artifactID any = artifact
	if artifact == "" {
		artifactID = nil
	}
	data, e := json.Marshal(map[string]any{"binding": b, "leaseId": lease, "artifactId": artifactID})
	if e != nil {
		return nil, time.Time{}, e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", c.Settings.SharedResources.Address+"/internal/shared/resource/artifact", bytes.NewReader(data))
	if e != nil {
		return nil, time.Time{}, e
	}
	token, e := readToken(c.Settings.ServiceTokenFile)
	if e != nil {
		return nil, time.Time{}, e
	}
	value := strings.TrimSpace(string(token))
	if value == "" || strings.ContainsAny(value, "\r\n\t ") {
		return nil, time.Time{}, fail
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("Authorization", "Bearer "+value)
	req.Header.Set("Auriga-Tenant-Id", c.Settings.TenantID)
	req.GetBody = nil
	response, e := c.Resources.Do(req)
	if e != nil {
		return nil, time.Time{}, e
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Range") != "" {
		return nil, time.Time{}, fail
	}
	trusted, e := http.ParseTime(response.Header.Get("Date"))
	if e != nil {
		return nil, time.Time{}, fail
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if e != nil || int64(len(raw)) > maximum {
		return nil, time.Time{}, fail
	}
	return raw, trusted.UTC(), nil
}
