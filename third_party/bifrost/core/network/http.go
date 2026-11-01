// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package network

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
	xproxy "golang.org/x/net/proxy"
)

type proxyTunnelConn struct{ net.Conn }

var DefaultClientConfig = struct {
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	MaxIdleConnDuration time.Duration
	MaxConnDuration     time.Duration
	MaxConnsPerHost     int
}{
	ReadTimeout:         60 * time.Second,
	WriteTimeout:        60 * time.Second,
	MaxIdleConnDuration: 30 * time.Second,
	MaxConnDuration:     300 * time.Second,
	MaxConnsPerHost:     200,
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}
type liveHTTPTransport struct {
	factory *HTTPClientFactory
	key     httpClientKey
}
type HTTPClientFactory struct {
	mu          sync.RWMutex
	proxyConfig *GlobalProxyConfig

	liveFasthttp map[ClientPurpose]*fasthttp.Client
	liveHTTP     map[httpClientKey]*http.Client

	fasthttpClients map[fasthttpClientKey]*fasthttp.Client
	httpClients     map[httpClientKey]*http.Client

	readBufferSize  int
	writeBufferSize int

	logger schemas.Logger
}
type defaultTransport struct {
	purpose ClientPurpose
}

var defaultFactory atomic.Pointer[HTTPClientFactory]

type GlobalProxyConfig struct {
	Enabled       bool            `json:"enabled"`
	Type          GlobalProxyType `json:"type"`
	URL           string          `json:"url"`
	Username      string          `json:"username,omitempty"`
	Password      string          `json:"password,omitempty"`
	NoProxy       string          `json:"no_proxy,omitempty"`
	Timeout       int             `json:"timeout,omitempty"`
	SkipTLSVerify bool            `json:"skip_tls_verify,omitempty"`

	EnableForSCIM      bool `json:"enable_for_scim"`
	EnableForInference bool `json:"enable_for_inference"`
	EnableForAPI       bool `json:"enable_for_api"`
}
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}
type fasthttpClientKey struct {
	purpose ClientPurpose
	rawPath bool
}
type liveFasthttpTransport struct {
	factory *HTTPClientFactory
	purpose ClientPurpose
}
type GlobalProxyType string
type ClientPurpose string
type httpClientKey struct {
	purpose ClientPurpose
	tls     *tls.Config
	policy  *DialPolicy
	compat  bool
}
type DialPolicy struct {
	dial          func(ctx context.Context, netw, addr string) (net.Conn, error)
	resolveTarget func(ctx context.Context, host string) ([]net.IP, error)
}

const (
	ClientPurposeSCIM ClientPurpose = "scim"

	ClientPurposeInference ClientPurpose = "inference"

	ClientPurposeAPI ClientPurpose = "api"
)

const (
	GlobalProxyTypeHTTP   GlobalProxyType = "http"
	GlobalProxyTypeSOCKS5 GlobalProxyType = "socks5"
	GlobalProxyTypeTCP    GlobalProxyType = "tcp"
)

func (f *HTTPClientFactory) isProxyEnabledForPurpose(purpose ClientPurpose) bool {
	if f.proxyConfig == nil || !f.proxyConfig.Enabled {
		return false
	}

	switch purpose {
	case ClientPurposeSCIM:
		return f.proxyConfig.EnableForSCIM
	case ClientPurposeInference:
		return f.proxyConfig.EnableForInference
	case ClientPurposeAPI:
		return f.proxyConfig.EnableForAPI
	default:
		return false
	}
}

func shouldBypassProxy(host, pattern string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	pattern = strings.ToLower(strings.TrimSpace(pattern))

	if pattern == "*" {
		return true
	}
	if pattern == host {
		return true
	}

	if strings.HasPrefix(pattern, ".") {
		suffix := pattern[1:]
		return host == suffix || strings.HasSuffix(host, pattern)
	}

	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:]
		return strings.HasSuffix(host, suffix)
	}
	return false
}

func IsLiveFasthttpClient(client *fasthttp.Client) bool {
	if client == nil {
		return false
	}
	_, ok := client.Transport.(*liveFasthttpTransport)
	return ok
}

func (f *HTTPClientFactory) currentFasthttpClientFor(key fasthttpClientKey) *fasthttp.Client {
	f.mu.RLock()
	client, ok := f.fasthttpClients[key]
	f.mu.RUnlock()
	if ok {
		return client
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if client, ok := f.fasthttpClients[key]; ok {
		return client
	}
	client = f.createFasthttpClient(key.purpose)
	client.DisablePathNormalizing = key.rawPath
	f.fasthttpClients[key] = client
	return client
}

func (t *liveFasthttpTransport) RoundTrip(outer *fasthttp.HostClient, req *fasthttp.Request, resp *fasthttp.Response) (bool, error) {

	key := fasthttpClientKey{purpose: t.purpose, rawPath: outer != nil && outer.DisablePathNormalizing}
	return false, t.factory.currentFasthttpClientFor(key).Do(req, resp)
}

func (p *DialPolicy) DialContext(ctx context.Context, netw, addr string, proxyURL *url.URL) (net.Conn, error) {
	if proxyURL == nil {
		return p.dial(ctx, netw, addr)
	}
	if factory := defaultFactory.Load(); factory != nil {
		factory.mu.RLock()
		configuredURL := factory.proxyURLForPurpose(ClientPurposeAPI)
		if configuredURL != nil && configuredURL.String() == proxyURL.String() {
			config := *factory.proxyConfig
			factory.mu.RUnlock()
			return policyProxyDial(p, proxyURL, &config, config.proxyTLS())(ctx, netw, addr)
		}
		factory.mu.RUnlock()
	}
	return policyProxyDial(p, proxyURL, &GlobalProxyConfig{}, nil)(ctx, netw, addr)
}

func (f *HTTPClientFactory) CompatTransport(purpose ClientPurpose) http.RoundTripper {
	return f.liveHTTPClient(httpClientKey{purpose: purpose, compat: true}).Transport
}

func defaultTransportProxy(req *http.Request) (*url.URL, error) {
	if transport, ok := http.DefaultTransport.(*http.Transport); ok && transport.Proxy != nil {
		return transport.Proxy(req)
	}
	return nil, nil
}

func DefaultTransport(purpose ClientPurpose) http.RoundTripper {
	return defaultTransport{purpose: purpose}
}

func (t defaultTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if factory := defaultFactory.Load(); factory != nil && factory.proxyEnabledFor(t.purpose) {
		return factory.CompatTransport(t.purpose).RoundTrip(req)
	}

	return http.DefaultTransport.RoundTrip(req)
}

func (f *HTTPClientFactory) proxyEnabledFor(purpose ClientPurpose) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.proxyURLForPurpose(purpose) != nil
}

func (f *HTTPClientFactory) liveHTTPClient(key httpClientKey) *http.Client {
	f.mu.RLock()
	client, ok := f.liveHTTP[key]
	f.mu.RUnlock()
	if ok {
		return client
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if client, ok := f.liveHTTP[key]; ok {
		return client
	}
	client = &http.Client{Transport: &liveHTTPTransport{factory: f, key: key}}
	f.liveHTTP[key] = client
	return client
}

func (f *HTTPClientFactory) currentHTTPClient(key httpClientKey) *http.Client {
	f.mu.RLock()
	client, ok := f.httpClients[key]
	f.mu.RUnlock()
	if ok {
		return client
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if client, ok := f.httpClients[key]; ok {
		return client
	}
	client = f.createHTTPClient(key)
	f.httpClients[key] = client
	return client
}

func (t *liveHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	inner := t.factory.currentHTTPClient(t.key)
	if inner.Timeout <= 0 {
		return inner.Transport.RoundTrip(req)
	}
	ctx, cancel := context.WithTimeout(req.Context(), inner.Timeout)
	resp, err := inner.Transport.RoundTrip(req.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

func (f *HTTPClientFactory) createFasthttpClient(purpose ClientPurpose) *fasthttp.Client {
	client := f.newFasthttpBaseClient(purpose)

	f.configureFasthttpProxy(client, purpose)

	if f.proxyConfig != nil {
		if f.proxyConfig.SkipTLSVerify {
			f.logger.Warn("skipping TLS verification for fasthttp client because skip TLS verify is set to true. It's not recommended to use this in production.")
		}
		client.TLSConfig = &tls.Config{
			InsecureSkipVerify: f.proxyConfig.SkipTLSVerify,
			MinVersion:         tls.VersionTLS12,
		}
	}

	return client
}

func (f *HTTPClientFactory) newFasthttpBaseClient(purpose ClientPurpose) *fasthttp.Client {
	client := &fasthttp.Client{
		ReadTimeout:         DefaultClientConfig.ReadTimeout,
		WriteTimeout:        DefaultClientConfig.WriteTimeout,
		MaxIdleConnDuration: DefaultClientConfig.MaxIdleConnDuration,
		MaxConnDuration:     DefaultClientConfig.MaxConnDuration,
		MaxConnsPerHost:     DefaultClientConfig.MaxConnsPerHost,
		MaxConnWaitTimeout:  DefaultClientConfig.ReadTimeout,
		ConnPoolStrategy:    fasthttp.FIFO,
		RetryIfErr:          StaleConnectionRetryIfErr,
	}

	if purpose == ClientPurposeSCIM {
		if f.readBufferSize > 0 {
			client.ReadBufferSize = f.readBufferSize
		}
		if f.writeBufferSize > 0 {
			client.WriteBufferSize = f.writeBufferSize
		}
	}
	return client
}

const maxStaleConnRetries = 3

func StaleConnectionRetryIfErr(_ *fasthttp.Request, attempts int, err error) (resetTimeout bool, retry bool) {
	if attempts > maxStaleConnRetries {
		return false, false
	}
	if err == nil {
		return false, false
	}
	errStr := strings.ToLower(err.Error())

	if errors.Is(err, fasthttp.ErrConnectionClosed) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		strings.Contains(errStr, "cannot find whitespace") ||
		strings.Contains(errStr, "connection reset by peer") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "use of closed network connection") ||
		strings.Contains(errStr, "server closed connection") {
		return true, true
	}
	return false, false
}

func (f *HTTPClientFactory) configureFasthttpProxy(client *fasthttp.Client, purpose ClientPurpose) {
	proxyURL := f.proxyURLForPurpose(purpose)
	if proxyURL == nil {
		return
	}
	proxyCfg := f.proxyConfig
	client.DialTimeout = func(addr string, timeout time.Duration) (net.Conn, error) {
		if timeout <= 0 || timeout > ProxyHandshakeTimeout {
			timeout = ProxyHandshakeTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if connectDirect(DialAddrHost(addr), proxyCfg) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		}
		return DialViaProxyTLS(ctx, proxyURL, addr, proxyCfg.proxyTLS())
	}
}

func MatchesNoProxy(host, noProxy string) bool {
	if strings.TrimSpace(noProxy) == "" {
		return false
	}
	for _, pattern := range strings.Split(noProxy, ",") {
		if strings.TrimSpace(pattern) != "" && shouldBypassProxy(host, pattern) {
			return true
		}
	}
	return false
}

func DialAddrHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		host = strings.Trim(addr, "[]")
	}
	return host
}

func (f *HTTPClientFactory) createHTTPClient(key httpClientKey) *http.Client {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		MaxIdleConns:          DefaultClientConfig.MaxConnsPerHost,
		MaxIdleConnsPerHost:   DefaultClientConfig.MaxConnsPerHost,
		IdleConnTimeout:       DefaultClientConfig.MaxIdleConnDuration,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: DefaultClientConfig.ReadTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    false,
		DisableKeepAlives:     false,

		TLSNextProto: make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
	}

	if key.tls != nil {
		transport.TLSClientConfig = key.tls.Clone()
	}
	if f.proxyConfig != nil {
		if f.proxyConfig.SkipTLSVerify {
			f.logger.Warn("skipping TLS verification for net/http client because skip TLS verify is set to true. It's not recommended to use this in production.")
		}
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		if f.proxyConfig.SkipTLSVerify {
			transport.TLSClientConfig.InsecureSkipVerify = true
		}
	}

	proxyURL := f.proxyURLForPurpose(key.purpose)
	if proxyURL != nil {
		f.configureHTTPProxy(transport, proxyURL)
	} else if key.compat {
		transport.Proxy = defaultTransportProxy
	}

	var roundTripper http.RoundTripper = transport
	timeout := DefaultClientConfig.ReadTimeout
	if f.proxyConfig != nil && f.proxyConfig.Timeout > 0 {
		timeout = time.Duration(f.proxyConfig.Timeout) * time.Second
	}
	if key.compat {

		timeout = 0
		transport.ResponseHeaderTimeout = 0
	}
	if policy := key.policy; policy != nil {

		timeout = 0
		transport.ResponseHeaderTimeout = 0
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		transport.DialContext = policy.dial
		if proxyURL != nil {

			transport.Proxy = nil
			transport.ProxyConnectHeader = nil
			transport.DialContext = policyProxyDial(policy, proxyURL, f.proxyConfig, f.proxyConfig.proxyTLS())
		}
	}

	return &http.Client{
		Transport: roundTripper,
		Timeout:   timeout,
	}
}

func policyProxyDial(policy *DialPolicy, proxyURL *url.URL, proxyCfg *GlobalProxyConfig, hopTLS *tls.Config) func(ctx context.Context, netw, addr string) (net.Conn, error) {
	return func(ctx context.Context, netw, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if connectDirect(host, proxyCfg) {
			return policy.dial(ctx, netw, addr)
		}
		ips, err := policy.resolveTarget(ctx, host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, ip := range ips {
			conn, err := DialViaProxyTLS(ctx, proxyURL, net.JoinHostPort(ip.String(), port), hopTLS)
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

func (f *HTTPClientFactory) proxyURLForPurpose(purpose ClientPurpose) *url.URL {
	if !f.isProxyEnabledForPurpose(purpose) || f.proxyConfig.URL == "" {
		return nil
	}
	raw := f.proxyConfig.URL
	if !strings.Contains(raw, "://") {

		scheme := "http"
		if f.proxyConfig.Type == GlobalProxyTypeSOCKS5 {
			scheme = "socks5"
		}
		raw = scheme + "://" + raw
	}
	proxyURL, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	if f.proxyConfig.Username != "" && f.proxyConfig.Password != "" {
		proxyURL.User = url.UserPassword(f.proxyConfig.Username, f.proxyConfig.Password)
	}
	return proxyURL
}

func (f *HTTPClientFactory) configureHTTPProxy(transport *http.Transport, proxyURL *url.URL) {

	if user := proxyURL.User; user != nil && (proxyURL.Scheme == "http" || proxyURL.Scheme == "https") {
		password, _ := user.Password()
		transport.ProxyConnectHeader = http.Header{
			"Proxy-Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte(user.Username()+":"+password))},
		}
	}

	proxyCfg := f.proxyConfig
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		host := req.URL.Hostname()
		if host == "" {
			host = req.URL.Host
		}
		if connectDirect(host, proxyCfg) {
			return nil, nil
		}
		return proxyURL, nil
	}
}

func (c *GlobalProxyConfig) proxyTLS() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: c != nil && c.SkipTLSVerify}
}

func connectDirect(host string, proxyCfg *GlobalProxyConfig) bool {
	return MatchesNoProxy(host, proxyCfg.NoProxy) || IsLocalOrMetadataHost(host)
}

func IsLocalOrMetadataHost(host string) bool {
	switch strings.ToLower(strings.TrimSuffix(host, ".")) {
	case "localhost", "metadata.google.internal", "metadata":
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || IsMetadataEndpoint(ip)
}

func proxyDialAddr(proxyURL *url.URL) string {
	port := proxyURL.Port()
	if port == "" {
		switch proxyURL.Scheme {
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		default:
			port = "80"
		}
	}
	return net.JoinHostPort(proxyURL.Hostname(), port)
}

const ProxyHandshakeTimeout = 30 * time.Second

func DialViaProxyTLS(ctx context.Context, proxyURL *url.URL, addr string, proxyTLS *tls.Config) (net.Conn, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, ProxyHandshakeTimeout)
		defer cancel()
	}
	dialer := &net.Dialer{KeepAlive: 30 * time.Second}
	switch proxyURL.Scheme {
	case "socks5", "socks5h":
		var auth *xproxy.Auth
		if user := proxyURL.User; user != nil {
			password, _ := user.Password()
			auth = &xproxy.Auth{User: user.Username(), Password: password}
		}
		socks, err := xproxy.SOCKS5("tcp", proxyDialAddr(proxyURL), auth, dialer)
		if err != nil {
			return nil, fmt.Errorf("socks5 proxy: %w", err)
		}
		return socks.(xproxy.ContextDialer).DialContext(ctx, "tcp", addr)
	case "http", "":
		return dialHTTPConnect(ctx, dialer, proxyURL, addr, nil)
	case "https":
		cfg := &tls.Config{MinVersion: tls.VersionTLS12}
		if proxyTLS != nil {
			cfg = proxyTLS.Clone()
		}
		if cfg.ServerName == "" {
			cfg.ServerName = proxyURL.Hostname()
		}

		cfg.NextProtos = []string{"http/1.1"}
		return dialHTTPConnect(ctx, dialer, proxyURL, addr, cfg)
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q", proxyURL.Scheme)
	}
}

func dialHTTPConnect(ctx context.Context, dialer *net.Dialer, proxyURL *url.URL, addr string, proxyTLS *tls.Config) (net.Conn, error) {
	conn, err := dialer.DialContext(ctx, "tcp", proxyDialAddr(proxyURL))
	if err != nil {
		return nil, fmt.Errorf("dial proxy: %w", err)
	}
	if proxyTLS != nil {
		tlsConn := tls.Client(conn, proxyTLS)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, fmt.Errorf("proxy TLS handshake with %s: %w", proxyDialAddr(proxyURL), err)
		}
		conn = tlsConn
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	var request strings.Builder
	request.WriteString("CONNECT " + addr + " HTTP/1.1\r\nHost: " + addr + "\r\n")
	if user := proxyURL.User; user != nil {
		password, _ := user.Password()
		request.WriteString("Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(user.Username()+":"+password)) + "\r\n")
	}
	request.WriteString("\r\n")
	if _, err := io.WriteString(conn, request.String()); err != nil {
		conn.Close()
		return nil, fmt.Errorf("proxy CONNECT to %s: %w", addr, err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("proxy CONNECT to %s: %w", addr, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("proxy refused CONNECT to %s: %s", addr, resp.Status)
	}
	if !stop() {

		return nil, ctx.Err()
	}
	_ = conn.SetDeadline(time.Time{})
	if reader.Buffered() > 0 {
		return &bufferedConn{Conn: conn, reader: reader}, nil
	}
	if proxyTLS != nil {

		return proxyTunnelConn{Conn: conn}, nil
	}
	return conn, nil
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
