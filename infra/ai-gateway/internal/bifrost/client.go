// Package bifrost bridges live IAM/org member identity to the maintained core service.
package bifrost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type MemberPolicy struct {
	MonthlyBudgetMicros int64 `json:"monthlyBudgetMicros"`
	RequestsPerMinute   int64 `json:"requestsPerMinute"`
	MaxConcurrency      int64 `json:"maxConcurrency"`
	MaxInFlightTokens   int64 `json:"maxInFlightTokens"`
	MeteringOnly        *bool `json:"meteringOnly,omitempty"`
}
type Client struct {
	target       *url.URL
	passwordFile string
	policy       MemberPolicy
	http         *http.Client
	transport    *http.Transport
}

func NewClient(origin, passwordFile string, policy MemberPolicy) (*Client, error) {
	if policy.MeteringOnly == nil {
		enabled := true
		policy.MeteringOnly = &enabled
	} else {
		enabled := *policy.MeteringOnly
		policy.MeteringOnly = &enabled
	}
	if policy.MaxConcurrency < 0 || policy.MaxConcurrency > 100 || policy.MaxInFlightTokens < 0 || policy.MaxInFlightTokens > 1<<30 {
		return nil, errors.New("invalid relay member limits")
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(origin, "#") || passwordFile == "" || policy.MonthlyBudgetMicros < 0 || policy.MonthlyBudgetMicros > 9007199254740991 || policy.RequestsPerMinute < 0 || policy.RequestsPerMinute > 1000000 {
		return nil, errors.New("invalid Bifrost bridge configuration")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || u.Hostname() != "bifrost" && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("relay requires HTTPS or private deployment service")
		}
	}
	tr := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 165 * time.Second, IdleConnTimeout: 60 * time.Second, MaxIdleConnsPerHost: 32}
	return &Client{target: u, passwordFile: passwordFile, policy: policy, transport: tr, http: &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Close() { c.transport.CloseIdleConnections() }
func (c *Client) password() (string, error) {
	info, e := os.Lstat(c.passwordFile)
	if e != nil || !info.Mode().IsRegular() {
		return "", errors.New("common.unavailable")
	}
	raw, e := os.ReadFile(c.passwordFile)
	if e != nil {
		return "", errors.New("common.unavailable")
	}
	defer clear(raw)
	s := strings.TrimSpace(string(raw))
	if s == "" || len(s) > 1024 || strings.ContainsAny(s, "\r\n") {
		return "", errors.New("common.unavailable")
	}
	return s, nil
}
func memberName(tenant, account string) string {
	raw, _ := json.Marshal([]string{tenant, account})
	digest := sha256.Sum256(raw)
	return "auriga-member:" + hex.EncodeToString(digest[:])
}
func (c *Client) MemberKey(ctx context.Context, tenant, account string) (api.RelayKeyIssued, error) {
	var out api.RelayKeyIssued
	if tenant == "" || account == "" {
		return out, errors.New("common.unauthenticated")
	}
	raw, e := json.Marshal(api.RelayMemberEnsure{TenantId: tenant, AccountId: account, MaxConcurrency: &c.policy.MaxConcurrency, MaxInFlightTokens: &c.policy.MaxInFlightTokens, MonthlyBudgetMicros: c.policy.MonthlyBudgetMicros, RequestsPerMinute: c.policy.RequestsPerMinute, MeteringOnly: c.policy.MeteringOnly})
	if e != nil {
		return out, e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.target.String()+"/internal/members/ensure", bytes.NewReader(raw))
	if e != nil {
		return out, e
	}
	password, e := c.password()
	if e != nil {
		return out, e
	}
	req.SetBasicAuth("auriga-member", password)
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.http.Do(req)
	if e != nil {
		return out, errors.New("common.unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == 403 {
		return out, errors.New("common.forbidden")
	}
	if resp.StatusCode != 200 {
		return out, errors.New("common.unavailable")
	}
	d := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF {
		return out, errors.New("common.unavailable")
	}
	k := out.Key
	if k.Id != account || k.AccountId != account || k.Source != "member" || k.Name != memberName(tenant, account) || !strings.HasPrefix(out.Secret, "sk-bf-") || strings.ContainsAny(out.Secret, " \t\r\n") || !k.Active {
		return api.RelayKeyIssued{}, errors.New("common.unavailable")
	}
	return out, nil
}
