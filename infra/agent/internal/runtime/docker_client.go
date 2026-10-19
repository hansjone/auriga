package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// EngineClient talks to the Docker / Podman Engine API over HTTP
// (unix socket, named pipe, or TCP). It never shells out to the docker CLI.
type EngineClient struct {
	HTTP    *http.Client
	BaseURL string // e.g. http://docker or http://127.0.0.1:2375
}

// DialEngine builds a client for host.
// host forms: unix:///var/run/docker.sock, npipe:////./pipe/docker_engine, tcp://host:2375, http://host:2375
func DialEngine(host string, timeout time.Duration) (*EngineClient, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	u, transport, err := engineTransport(host)
	if err != nil {
		return nil, err
	}
	return &EngineClient{
		HTTP: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
		BaseURL: u,
	}, nil
}

func engineTransport(host string) (baseURL string, rt http.RoundTripper, err error) {
	if host == "" {
		host = defaultDockerHost()
	}
	switch {
	case strings.HasPrefix(host, "unix://"):
		path := strings.TrimPrefix(host, "unix://")
		return "http://docker", &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", path)
			},
		}, nil
	case strings.HasPrefix(host, "npipe://"):
		path := strings.TrimPrefix(host, "npipe://")
		return "http://docker", &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialNamedPipe(ctx, path)
			},
		}, nil
	case strings.HasPrefix(host, "tcp://"):
		u, e := url.Parse(host)
		if e != nil {
			return "", nil, e
		}
		return "http://" + u.Host, http.DefaultTransport, nil
	case strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://"):
		u, e := url.Parse(host)
		if e != nil {
			return "", nil, e
		}
		return u.Scheme + "://" + u.Host, http.DefaultTransport, nil
	default:
		return "", nil, fmt.Errorf("docker: unsupported host %q", host)
	}
}

func (c *EngineClient) doJSON(ctx context.Context, method, path string, in any, wantStatuses ...int) ([]byte, int, error) {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, 0, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	ok := len(wantStatuses) == 0
	for _, s := range wantStatuses {
		if resp.StatusCode == s {
			ok = true
			break
		}
	}
	if !ok {
		return raw, resp.StatusCode, fmt.Errorf("docker: %s %s: status %d: %s", method, path, resp.StatusCode, truncate(string(raw), 200))
	}
	return raw, resp.StatusCode, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (c *EngineClient) Ping(ctx context.Context) error {
	_, _, err := c.doJSON(ctx, http.MethodGet, "/_ping", nil, http.StatusOK)
	return err
}

func (c *EngineClient) PullImage(ctx context.Context, image string) error {
	// image must be repo@sha256:… — Engine verifies digest.
	q := url.Values{}
	repo, digest, ok := strings.Cut(image, "@")
	if ok {
		q.Set("fromImage", repo)
		q.Set("tag", digest)
	} else {
		name, tag, found := strings.Cut(image, ":")
		if !found {
			name, tag = image, "latest"
		}
		q.Set("fromImage", name)
		q.Set("tag", tag)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/images/create?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker: pull %s: status %d", image, resp.StatusCode)
	}
	return nil
}

type containerCreateResponse struct {
	ID string `json:"Id"`
}

func (c *EngineClient) ContainerCreate(ctx context.Context, name string, body any) (string, error) {
	path := "/containers/create"
	if name != "" {
		path += "?name=" + url.QueryEscape(name)
	}
	raw, _, err := c.doJSON(ctx, http.MethodPost, path, body, http.StatusCreated, http.StatusOK)
	if err != nil {
		return "", err
	}
	var out containerCreateResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

func (c *EngineClient) ContainerStart(ctx context.Context, id string) error {
	_, _, err := c.doJSON(ctx, http.MethodPost, "/containers/"+id+"/start", nil, http.StatusNoContent, http.StatusOK, http.StatusNotModified)
	return err
}

func (c *EngineClient) ContainerStop(ctx context.Context, id string, timeoutSec int) error {
	path := fmt.Sprintf("/containers/%s/stop?t=%d", id, timeoutSec)
	_, status, err := c.doJSON(ctx, http.MethodPost, path, nil, http.StatusNoContent, http.StatusOK, http.StatusNotModified, http.StatusNotFound)
	if status == http.StatusNotFound {
		return nil
	}
	return err
}

func (c *EngineClient) ContainerRemove(ctx context.Context, id string, force bool) error {
	path := "/containers/" + id + "?v=true"
	if force {
		path += "&force=true"
	}
	_, status, err := c.doJSON(ctx, http.MethodDelete, path, nil, http.StatusNoContent, http.StatusOK, http.StatusNotFound)
	if status == http.StatusNotFound {
		return nil
	}
	return err
}

func (c *EngineClient) ContainerInspect(ctx context.Context, id string) (map[string]any, error) {
	raw, status, err := c.doJSON(ctx, http.MethodGet, "/containers/"+id+"/json", nil, http.StatusOK, http.StatusNotFound)
	if status == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *EngineClient) ContainerStats(ctx context.Context, id string) (map[string]any, error) {
	raw, status, err := c.doJSON(ctx, http.MethodGet, "/containers/"+id+"/stats?stream=false", nil, http.StatusOK, http.StatusNotFound)
	if status == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *EngineClient) ContainerList(ctx context.Context, all bool, filters map[string][]string) ([]map[string]any, error) {
	q := url.Values{}
	if all {
		q.Set("all", "true")
	}
	if len(filters) > 0 {
		fb, _ := json.Marshal(filters)
		q.Set("filters", string(fb))
	}
	raw, _, err := c.doJSON(ctx, http.MethodGet, "/containers/json?"+q.Encode(), nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *EngineClient) NetworkCreate(ctx context.Context, name string, internal bool, labels map[string]string) (string, error) {
	body := map[string]any{
		"Name":     name,
		"Internal": internal,
		"Labels":   labels,
		"Driver":   "bridge",
	}
	raw, status, err := c.doJSON(ctx, http.MethodPost, "/networks/create", body, http.StatusCreated, http.StatusOK, http.StatusConflict)
	if status == http.StatusConflict {
		return name, nil
	}
	if err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"Id"`
	}
	_ = json.Unmarshal(raw, &out)
	return out.ID, nil
}

func (c *EngineClient) NetworkRemove(ctx context.Context, idOrName string) error {
	_, status, err := c.doJSON(ctx, http.MethodDelete, "/networks/"+idOrName, nil, http.StatusNoContent, http.StatusOK, http.StatusNotFound)
	if status == http.StatusNotFound {
		return nil
	}
	return err
}
