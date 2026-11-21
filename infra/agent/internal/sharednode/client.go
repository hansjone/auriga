// Package sharednode is the installation-owned Host relay. It never accepts an
// endpoint, service credential or delegated tenant from a Host request.
package sharednode

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	workspacev1 "github.com/hansjone/auriga/infra/gen/auriga/workspace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type Endpoint struct {
	Address string `json:"address"`
	CAFile  string `json:"caFile"`
}
type Settings struct {
	TenantID         string    `json:"tenantId"`
	Workspace        Endpoint  `json:"workspace"`
	IAM              Endpoint  `json:"iam"`
	ServiceTokenFile string    `json:"serviceTokenFile"`
	SharedModel      *Endpoint `json:"sharedModel,omitempty"`
	SharedMessages   *Endpoint `json:"sharedMessages,omitempty"`
}
// Same-generation Resolve reuse (plugin AUTHORITY_REUSE_MS). Watch ticks once
// per second; ≤1s coalesce cuts ResolveSharedDsh without delaying revoke.
const resolveReuse = time.Second

type resolveHit struct {
	at       time.Time
	response *workspacev1.ResolveSharedDshResponse
}

type Client struct {
	Settings       Settings
	Workspace, IAM *grpc.ClientConn
	Model          *http.Client
	Messages       *http.Client

	resolveMu    sync.Mutex
	resolveCache map[string]resolveHit
}

func roots(path string) (*x509.CertPool, error) {
	info, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !filepath.IsAbs(path) || !info.Mode().IsRegular() || info.Size() > 1024*1024 || (runtime.GOOS != "windows" && info.Mode().Perm()&0022 != 0) {
		return nil, errors.New("protected dedicated CA file required")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	p := x509.NewCertPool()
	if !p.AppendCertsFromPEM(b) {
		return nil, fmt.Errorf("invalid dedicated CA")
	}
	return p, nil
}
func Open(s Settings, certificate tls.Certificate) (*Client, error) {
	if !uuid.MatchString(s.TenantID) || !filepath.IsAbs(s.ServiceTokenFile) {
		return nil, errors.New("fixed installation tenant and protected token file required")
	}
	c := &Client{Settings: s}
	dial := func(e Endpoint) (*grpc.ClientConn, error) {
		u, err := url.Parse(e.Address)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("HTTPS RPC origin required")
		}
		p, err := roots(e.CAFile)
		if err != nil {
			return nil, err
		}
		return grpc.NewClient(u.Host, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: p, Certificates: []tls.Certificate{certificate}})))
	}
	var err error
	if c.Workspace, err = dial(s.Workspace); err != nil {
		return nil, err
	}
	if c.IAM, err = dial(s.IAM); err != nil {
		c.Close()
		return nil, err
	}
	for _, service := range []struct {
		endpoint *Endpoint
		client   **http.Client
	}{{s.SharedModel, &c.Model}, {s.SharedMessages, &c.Messages}} {
		if service.endpoint == nil {
			continue
		}
		u, e := url.Parse(service.endpoint.Address)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			c.Close()
			return nil, errors.New("HTTPS shared service origin required")
		}
		p, e := roots(service.endpoint.CAFile)
		if e != nil {
			c.Close()
			return nil, e
		}
		*service.client = &http.Client{Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: p, Certificates: []tls.Certificate{certificate}}, ResponseHeaderTimeout: 10 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return c, nil
}
func (c *Client) Close() {
	if c.Workspace != nil {
		_ = c.Workspace.Close()
	}
	if c.IAM != nil {
		_ = c.IAM.Close()
	}
	if c.Model != nil {
		c.Model.CloseIdleConnections()
	}
	if c.Messages != nil {
		c.Messages.CloseIdleConnections()
	}
}
func (c *Client) call(ctx context.Context, iam bool, method string, in, out proto.Message) error {
	return c.callTimeout(ctx, iam, method, in, out, 10*time.Second)
}

type notDispatchedError struct{}

func (*notDispatchedError) Error() string { return "common.unavailable" }

func (c *Client) callTimeout(ctx context.Context, iam bool, method string, in, out proto.Message, timeout time.Duration) error {
	b, err := readToken(c.Settings.ServiceTokenFile)
	if err != nil {
		return &notDispatchedError{}
	}
	token := strings.TrimSpace(string(b))
	if token == "" || strings.ContainsAny(token, "\r\n\t ") {
		return &notDispatchedError{}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token, "auriga-tenant-id", c.Settings.TenantID))
	conn, prefix := c.Workspace, "/auriga.workspace.v1.SharedDshService/"
	if iam {
		conn, prefix = c.IAM, "/auriga.iam.v1.SharedTaskService/"
	}
	if ctx.Err() != nil || conn == nil {
		return &notDispatchedError{}
	}
	return conn.Invoke(ctx, prefix+method, in, out)
}

func readToken(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) || info.Size() > 16384 {
		return nil, errors.New("protected internal service token file required")
	}
	return os.ReadFile(path)
}

// Host JSON uses safe numeric int64s and exact public enum names, unlike
// protojson. Conversion is driven by descriptors, never field-name guessing.
func HostValue(message proto.Message) (any, error) { return wire(message) }

// DecodeHostValue consumes the same finite public enum and safe integer wire.
func DecodeHostValue(raw []byte, message proto.Message) error { return decode(raw, message) }

func wire(message proto.Message) (any, error) {
	raw, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(message)
	if err != nil {
		return nil, err
	}
	var value any
	if err = json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	err = wireFields(value, message.ProtoReflect().Descriptor())
	return value, err
}
func wireFields(value any, descriptor protoreflect.MessageDescriptor) error {
	m, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	fields := descriptor.Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		v, exists := m[f.JSONName()]
		if !exists || v == nil {
			continue
		}
		one := func(v any) (any, error) {
			switch f.Kind() {
			case protoreflect.Int64Kind, protoreflect.Uint64Kind:
				var n json.Number
				switch x := v.(type) {
				case string:
					n = json.Number(x)
				case float64:
					n = json.Number(fmt.Sprint(x))
				default:
					return nil, errors.New("unsafe integer")
				}
				x, e := n.Int64()
				if e != nil || x > 9007199254740991 || x < -9007199254740991 {
					return nil, errors.New("unsafe integer")
				}
				return x, nil
			case protoreflect.EnumKind:
				if f.Enum().FullName() == "auriga.node.v1.DshInstanceMode" {
					switch v {
					case "DSH_INSTANCE_MODE_SHARED":
						return "shared", nil
					case "DSH_INSTANCE_MODE_DEDICATED":
						return "dedicated", nil
					default:
						return nil, errors.New("invalid mode")
					}
				}
			case protoreflect.MessageKind:
				if e := wireFields(v, f.Message()); e != nil {
					return nil, e
				}
			}
			return v, nil
		}
		if f.IsList() {
			a, _ := v.([]any)
			for j := range a {
				x, e := one(a[j])
				if e != nil {
					return e
				}
				a[j] = x
			}
		} else {
			x, e := one(v)
			if e != nil {
				return e
			}
			m[f.JSONName()] = x
		}
	}
	return nil
}
func decode(raw []byte, message proto.Message) error {
	var value any
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if e := d.Decode(&value); e != nil {
		return e
	}
	var convert func(any) error
	convert = func(v any) error {
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				if k == "mode" {
					switch v {
					case "shared":
						x[k] = "DSH_INSTANCE_MODE_SHARED"
					case "dedicated":
						x[k] = "DSH_INSTANCE_MODE_DEDICATED"
					default:
						return errors.New("invalid mode")
					}
				} else if e := convert(v); e != nil {
					return e
				}
			}
		case []any:
			for _, v := range x {
				if e := convert(v); e != nil {
					return e
				}
			}
		case json.Number:
			n, e := x.Int64()
			if e != nil || n > 9007199254740991 || n < -9007199254740991 {
				return errors.New("unsafe integer")
			}
		}
		return nil
	}
	if err := convert(value); err != nil {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return protojson.Unmarshal(b, message)
}
func (c *Client) Check(ctx context.Context, b map[string]any, session string) error {
	raw, _ := json.Marshal(b)
	expected := new(nodev1.SharedDshBinding)
	if decode(raw, expected) != nil || expected.TenantId != c.Settings.TenantID {
		return errors.New("shared.binding_mismatch")
	}
	key := expected.TenantId + "\x00" + expected.MemberId + "\x00" + expected.PlacementId + "\x00" + session
	now := time.Now()
	c.resolveMu.Lock()
	if c.resolveCache == nil {
		c.resolveCache = map[string]resolveHit{}
	}
	if hit, ok := c.resolveCache[key]; ok && now.Sub(hit.at) <= resolveReuse {
		out := hit.response
		c.resolveMu.Unlock()
		if out == nil || out.State != "ready" || out.AuthGeneration < 1 || !proto.Equal(expected, out.Binding) {
			return errors.New("shared.revoked")
		}
		return nil
	}
	c.resolveMu.Unlock()
	out := new(workspacev1.ResolveSharedDshResponse)
	if err := c.call(ctx, false, "ResolveSharedDsh", &workspacev1.ResolveSharedDshRequest{TenantId: expected.TenantId, MemberId: expected.MemberId, PlacementId: expected.PlacementId, PlatformSessionId: session}, out); err != nil {
		c.resolveMu.Lock()
		delete(c.resolveCache, key)
		c.resolveMu.Unlock()
		return errors.New("common.unavailable")
	}
	if out.State != "ready" || out.AuthGeneration < 1 || !proto.Equal(expected, out.Binding) {
		c.resolveMu.Lock()
		delete(c.resolveCache, key)
		c.resolveMu.Unlock()
		return errors.New("shared.revoked")
	}
	c.resolveMu.Lock()
	c.resolveCache[key] = resolveHit{at: time.Now(), response: proto.Clone(out).(*workspacev1.ResolveSharedDshResponse)}
	c.resolveMu.Unlock()
	return nil
}

func (c *Client) IsShared(ctx context.Context, id string) (bool, error) {
	mode, err := c.PoolMode(ctx, id)
	return mode != "", err
}

// PoolMode is always read from W; a locally registered managed endpoint does
// not authorize probing a different mode or falling back to an owner route.
func (c *Client) PoolMode(ctx context.Context, id string) (string, error) {
	if !uuid.MatchString(id) {
		return "", errors.New("shared.binding_mismatch")
	}
	out := new(workspacev1.GetSharedDshPoolResponse)
	if e := c.call(ctx, false, "GetSharedDshPool", &workspacev1.GetSharedDshPoolRequest{TenantId: c.Settings.TenantID, PhysicalInstanceId: id}, out); e != nil {
		return "", errors.New("common.unavailable")
	}
	if out.TenantId != c.Settings.TenantID || out.PhysicalInstanceId != id {
		return "", errors.New("common.unavailable")
	}
	if !out.Registered {
		if out.Mode != "" || out.State != "" {
			return "", errors.New("common.unavailable")
		}
		return "", nil
	}
	if !has([]string{"shared", "dedicated"}, out.Mode) || !has([]string{"pending", "ready", "blocked"}, out.State) {
		return "", errors.New("common.unavailable")
	}
	return out.Mode, nil
}
func (c *Client) Watch(ctx context.Context, b map[string]any, session string, cancel context.CancelFunc) (func(), error) {
	if err := c.Check(ctx, b, session); err != nil {
		return nil, err
	}
	ctx, stop := context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if c.Check(ctx, b, session) != nil {
					cancel()
					return
				}
			}
		}
	}()
	return stop, nil
}
func (c *Client) Snapshot(ctx context.Context, b *nodev1.SharedDshBinding) (*workspacev1.GetSharedDshResourceSnapshotResponse, error) {
	if b == nil || b.TenantId != c.Settings.TenantID {
		return nil, errors.New("shared.binding_mismatch")
	}
	out := new(workspacev1.GetSharedDshResourceSnapshotResponse)
	err := c.call(ctx, false, "GetSharedDshResourceSnapshot", &workspacev1.GetSharedDshResourceSnapshotRequest{Binding: b}, out)
	return out, err
}
