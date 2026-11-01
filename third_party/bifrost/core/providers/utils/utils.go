// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package utils

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"weak"

	"github.com/bytedance/sonic"
	"github.com/cespare/xxhash/v2"

	"github.com/maximhq/bifrost/core/network"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/valyala/fasthttp"
	"golang.org/x/net/http/httpproxy"
)

type upstreamTimingBody struct {
	inner io.ReadCloser
	ctx   context.Context
}
type BifrostErrorCarrier interface {
	error
	BifrostError() *schemas.BifrostError
}

var anthropicUnsafeToolUseIDCharRegex = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

type StreamBodyKind int
type streamCloserWithError interface {
	CloseWithError(err error) error
}

var errBypassProxy = errors.New("target matches no_proxy: dial directly")

type ToolSchemaPolicy struct {
	NormalizeNULEscape bool
	StripLookaround    bool
	DropPattern        bool
	StringFormats      []string
}

var proxyHandshakeTimeout = network.ProxyHandshakeTimeout

type fasthttpRoundTripper struct {
	client  *fasthttp.Client
	schemes *sync.Map
}
type NonSSEStreamBody struct {
	Kind        StreamBodyKind
	ContentType string
	Sample      []byte
	ReadErr     error
}

var sseFieldNames = [][]byte{
	[]byte("data:"),
	[]byte("event:"),
	[]byte("id:"),
	[]byte("retry:"),
}

type RequestBodyWithExtraParams interface {
	GetExtraParams() map[string]interface{}
}

var logger atomic.Pointer[schemas.Logger]
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}
var hopByHopHeaders = map[string]bool{
	"connection":          true,
	"proxy-connection":    true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
}
var skipVerifyScopes sync.Map
var supportedBufferedContentEncodings = map[string]struct{}{
	"identity": {},
	"gzip":     {},
	"x-gzip":   {},
	"deflate":  {},
	"br":       {},
	"zstd":     {},
}

type JSONLParseResult struct {
	Errors []schemas.BatchError
}

var ErrStreamClosed = errors.New("stream closed")

type RequestBodyConverter func() (RequestBodyWithExtraParams, error)

var noop = func() {}
var ErrStreamIdleTimeout = errors.New("stream idle timeout: no data received within configured window")

type noopLogger struct{}

var UnsupportedSpeechStreamModels = []string{"tts-1", "tts-1-hd"}

type RequestBodyGetter interface {
	GetRawRequestBody() []byte
}
type idleTimeoutReader struct {
	ctx           *schemas.BifrostContext
	reader        io.Reader
	bodyStream    io.Reader
	timeout       time.Duration
	timer         *time.Timer
	once          sync.Once
	cleanupOnce   sync.Once
	timerDoneOnce sync.Once
	timerDone     chan struct{}
	fired         atomic.Bool
}

var providerResponseFilterHeaders = map[string]bool{
	"content-length":                   true,
	"content-encoding":                 true,
	"content-type":                     true,
	"transfer-encoding":                true,
	"connection":                       true,
	"keep-alive":                       true,
	"proxy-connection":                 true,
	"proxy-authenticate":               true,
	"proxy-authorization":              true,
	"authorization":                    true,
	"x-goog-api-key":                   true,
	"x-api-key":                        true,
	"api-key":                          true,
	"cookie":                           true,
	"set-cookie":                       true,
	"set-cookie2":                      true,
	"www-authenticate":                 true,
	"te":                               true,
	"trailer":                          true,
	"upgrade":                          true,
	"host":                             true,
	"date":                             true,
	"server":                           true,
	"alt-svc":                          true,
	"strict-transport-security":        true,
	"access-control-allow-origin":      true,
	"access-control-allow-methods":     true,
	"access-control-allow-headers":     true,
	"access-control-expose-headers":    true,
	"access-control-allow-credentials": true,
	"access-control-max-age":           true,
}

type invalidRequestError struct{ msg string }

var sortedAPI = sonic.Config{SortMapKeys: true}.Froze()
var passthroughPathDelimiters = strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23")
var supportedStreamingContentEncodings = map[string]struct{}{
	"identity": {},
	"gzip":     {},
	"x-gzip":   {},
}
var errStreamParkedAfterFinish = errors.New("stream ended on finish_reason with the connection still open")

const ThoughtSignatureSeparator = "_ts_"

const maxSanitizedAnthropicToolUseIDLen = 64

func SanitizeAnthropicToolUseID(id string) string {

	if id != "" && !anthropicUnsafeToolUseIDCharRegex.MatchString(id) {
		return id
	}

	hash := fmt.Sprintf("%016x", xxhash.Sum64String(id))
	semantic := strings.Trim(anthropicUnsafeToolUseIDCharRegex.ReplaceAllString(id, "_"), "_")
	if semantic == "" {
		return hash
	}
	if maxSemanticLen := maxSanitizedAnthropicToolUseIDLen - len(hash) - 1; len(semantic) > maxSemanticLen {
		semantic = strings.Trim(semantic[:maxSemanticLen], "_")
	}
	if semantic == "" {
		return hash
	}
	return hash + "_" + semantic
}

func SanitizeAnthropicToolUseIDPtr(id *string) *string {
	if id == nil {
		return nil
	}
	sanitized := SanitizeAnthropicToolUseID(*id)
	return &sanitized
}

func StripThoughtSignature(callID string) string {
	if base, _, found := strings.Cut(callID, ThoughtSignatureSeparator); found {
		return base
	}
	return callID
}

const ReasoningItemIDMarker = "brid_"

const ReasoningItemIDSeparator = ":"

func ShouldEmbedReasoningItemID(ctx *schemas.BifrostContext, provider schemas.ModelProvider, model string) bool {
	switch schemas.ResolveBaseProvider(ctx, provider) {
	case schemas.OpenAI:
		return true
	case schemas.Azure, schemas.BedrockMantle, schemas.Vertex:
		return schemas.IsOpenAIModel(model)
	default:
		return false
	}
}

func EmbedReasoningItemID(id *string, payload string) string {
	if id == nil || *id == "" {
		return payload
	}
	return ReasoningItemIDMarker + *id + ReasoningItemIDSeparator + payload
}

func ExtractReasoningItemID(payload string) (id *string, rest string, ok bool) {
	if !strings.HasPrefix(payload, ReasoningItemIDMarker) {
		return nil, payload, false
	}
	body := strings.TrimPrefix(payload, ReasoningItemIDMarker)
	extracted, remaining, found := strings.Cut(body, ReasoningItemIDSeparator)
	if !found || extracted == "" {

		return nil, payload, false
	}
	return &extracted, remaining, true
}

func MarshalSorted(v interface{}) ([]byte, error) {
	return sortedAPI.Marshal(v)
}

func MarshalProviderRequest(v interface{}) ([]byte, error) {
	if m, ok := v.(json.Marshaler); ok {
		return m.MarshalJSON()
	}
	return MarshalSorted(v)
}

func SetJSONField(data []byte, path string, value interface{}) ([]byte, error) {
	return sjson.SetBytes(data, path, value)
}

func SetRawJSONField(data []byte, path string, value []byte) ([]byte, error) {
	return sjson.SetRawBytes(data, path, value)
}

func DeleteJSONField(data []byte, path string) ([]byte, error) {
	return sjson.DeleteBytes(data, path)
}

func JSONFieldExists(data []byte, path string) bool {
	return gjson.GetBytes(data, path).Exists()
}

func GetJSONField(data []byte, path string) gjson.Result {
	return gjson.GetBytes(data, path)
}

func (noopLogger) Debug(string, ...any)                   {}
func (noopLogger) Info(string, ...any)                    {}
func (noopLogger) Warn(string, ...any)                    {}
func (noopLogger) Error(string, ...any)                   {}
func (noopLogger) Fatal(string, ...any)                   {}
func (noopLogger) SetLevel(schemas.LogLevel)              {}
func (noopLogger) SetOutputType(schemas.LoggerOutputType) {}
func (noopLogger) LogHTTPRequest(schemas.LogLevel, string) schemas.LogEventBuilder {
	return schemas.NoopLogEvent
}

func init() {
	var noop schemas.Logger = &noopLogger{}
	logger.Store(&noop)
}

func SetLogger(l schemas.Logger) {
	logger.Store(&l)
}

func getLogger() schemas.Logger {
	return *logger.Load()
}

func SetErrorLatency(bifrostErr *schemas.BifrostError, latency time.Duration) *schemas.BifrostError {
	if bifrostErr != nil {
		bifrostErr.ExtraFields.Latency = latency.Milliseconds()
	}
	return bifrostErr
}

func makeRequestWithDoFunc(ctx context.Context, do func() error) (time.Duration, *schemas.BifrostError, func()) {
	startTime := time.Now()
	errChan := make(chan error, 1)

	go func() {

		errChan <- do()
	}()

	select {
	case <-ctx.Done():

		latency := time.Since(startTime)

		schemas.AddUpstreamLatency(ctx, latency)

		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			statusCode := 504
			errorType := schemas.RequestTimedOut
			return latency, &schemas.BifrostError{
				IsBifrostError: true,
				StatusCode:     &statusCode,
				Error: &schemas.ErrorField{
					Type:    &errorType,
					Message: fmt.Sprintf("Request timed out by context: %v", ctx.Err()),
					Error:   ctx.Err(),
				},
				ExtraFields: schemas.BifrostErrorExtraFields{Latency: latency.Milliseconds()},
			}, func() { <-errChan }
		}
		statusCode := 499
		errorType := schemas.RequestCancelled
		return latency, &schemas.BifrostError{
			IsBifrostError: true,
			StatusCode:     &statusCode,
			Error: &schemas.ErrorField{
				Type:    &errorType,
				Message: fmt.Sprintf("Request cancelled by context: %v", ctx.Err()),
				Error:   ctx.Err(),
			},
			ExtraFields: schemas.BifrostErrorExtraFields{Latency: latency.Milliseconds()},
		}, func() { <-errChan }
	case err := <-errChan:

		latency := time.Since(startTime)

		schemas.AddUpstreamLatency(ctx, latency)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return latency, &schemas.BifrostError{
					IsBifrostError: false,
					Error: &schemas.ErrorField{
						Type:    schemas.Ptr(schemas.RequestCancelled),
						Message: schemas.ErrRequestCancelled,
						Error:   err,
					},
					ExtraFields: schemas.BifrostErrorExtraFields{Latency: latency.Milliseconds()},
				}, noop
			}

			if errors.Is(err, fasthttp.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
				return latency, SetErrorLatency(NewBifrostTimeoutError(schemas.ErrProviderRequestTimedOut, err), latency), noop
			}

			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				return latency, SetErrorLatency(NewBifrostTimeoutError(schemas.ErrProviderRequestTimedOut, err), latency), noop
			}

			var opErr *net.OpError
			var dnsErr *net.DNSError
			if errors.As(err, &opErr) || errors.As(err, &dnsErr) {
				return latency, SetErrorLatency(NewBifrostUpstreamConnectionError(schemas.ErrProviderNetworkError, err), latency), noop
			}

			return latency, SetErrorLatency(NewBifrostUpstreamConnectionError(schemas.ErrProviderDoRequest, err), latency), noop
		}

		return latency, nil, noop
	}
}

func MakeRequestWithContext(ctx context.Context, client *fasthttp.Client, req *fasthttp.Request, resp *fasthttp.Response) (time.Duration, *schemas.BifrostError, func()) {

	unbind := bindRequestContext(req, ctx)
	latency, bifrostErr, wait := makeRequestWithDoFunc(ctx, func() error {
		defer unbind()
		return client.Do(req, resp)
	})
	return latency, bifrostErr, wait
}

func MakeRequestWithContextFollowRedirects(ctx context.Context, client *fasthttp.Client, req *fasthttp.Request, resp *fasthttp.Response, maxRedirects int) (time.Duration, *schemas.BifrostError, func()) {
	unbind := bindRequestContext(req, ctx)
	latency, bifrostErr, wait := makeRequestWithDoFunc(ctx, func() error {
		defer unbind()
		return client.DoRedirects(req, resp, maxRedirects)
	})
	return latency, bifrostErr, wait
}

func DoStreamingRequest(ctx context.Context, client *fasthttp.Client, req *fasthttp.Request, resp *fasthttp.Response) error {
	unbind := bindRequestContext(req, ctx)
	startTime := time.Now()
	err := client.Do(req, resp)
	unbind()
	schemas.AddUpstreamLatency(ctx, time.Since(startTime))
	return err
}

func (b *upstreamTimingBody) Read(p []byte) (int, error) {
	start := time.Now()
	n, err := b.inner.Read(p)
	schemas.AddUpstreamLatency(b.ctx, time.Since(start))
	return n, err
}

func (b *upstreamTimingBody) Close() error { return b.inner.Close() }

func DoHTTPRequest(client *http.Client, req *http.Request) (*http.Response, error) {
	if abort := AttemptAbortFromContext(req.Context()); abort != nil {
		if abort.Fired() {
			return nil, ErrStreamFirstTokenTimeout
		}
		attemptCtx, cancel := context.WithCancelCause(req.Context())
		go func() {
			select {
			case <-abort.Done():
				cancel(ErrStreamFirstTokenTimeout)
			case <-abort.Stopped():
			case <-attemptCtx.Done():
			}
		}()
		req = req.WithContext(attemptCtx)
	}
	startTime := time.Now()
	resp, err := client.Do(req)
	schemas.AddUpstreamLatency(req.Context(), time.Since(startTime))
	if err == nil && resp != nil && resp.Body != nil {
		resp.Body = &upstreamTimingBody{inner: resp.Body, ctx: req.Context()}
	}
	return resp, err
}

func ConfigureDialer(client *fasthttp.Client, allowPrivateNetwork bool) *fasthttp.Client {

	if network.IsLiveFasthttpClient(client) {
		return client
	}

	client.RetryIfErr = network.StaleConnectionRetryIfErr

	client.Transport = NewContextTransport()

	existingDial := client.Dial
	existingDialTimeout := client.DialTimeout

	keepAliveCfg := net.KeepAliveConfig{
		Enable:   true,
		Idle:     10 * time.Second,
		Interval: 5 * time.Second,
		Count:    3,
	}

	client.Dial = func(addr string) (net.Conn, error) {
		var conn net.Conn
		var err error

		viaExistingDial := existingDial != nil
		bypassed := false
		if viaExistingDial {

			conn, err = existingDial(addr)

			if errors.Is(err, errBypassProxy) {
				viaExistingDial = false
				bypassed = true
				conn, err = nil, nil
			}
		}

		switch {
		case viaExistingDial:
		case existingDialTimeout != nil && !bypassed:

			conn, err = existingDialTimeout(addr, client.ReadTimeout)
		default:

			host, port, splitErr := net.SplitHostPort(addr)
			if splitErr != nil {
				return nil, splitErr
			}

			resolveCtx := context.Background()
			if client.ReadTimeout > 0 {
				var cancel context.CancelFunc
				resolveCtx, cancel = context.WithTimeout(resolveCtx, client.ReadTimeout)
				defer cancel()
			}
			ips, resolveErr := net.DefaultResolver.LookupIP(resolveCtx, "ip", host)
			if resolveErr != nil {
				return nil, resolveErr
			}
			dialer := &net.Dialer{
				Timeout:         client.ReadTimeout,
				KeepAliveConfig: keepAliveCfg,
			}
			var lastErr error
			for _, ip := range ips {

				if ip.IsUnspecified() {
					return nil, fmt.Errorf("connection to unspecified IP %s is not allowed", ip)
				}
				if network.IsLinkLocal(ip) {
					return nil, fmt.Errorf("connection to link-local IP %s is not allowed", ip)
				}

				if !allowPrivateNetwork && (ip.IsLoopback() || network.IsPrivateIP(ip)) {
					return nil, fmt.Errorf("connection to private IP %s is not allowed", ip)
				}
				conn, err = dialer.Dial("tcp", net.JoinHostPort(ip.String(), port))
				if err == nil {
					break
				}
				lastErr = err
			}
			if conn == nil {
				if lastErr != nil {
					return nil, lastErr
				}
				return nil, fmt.Errorf("no usable address resolved for %s", host)
			}
		}
		if err != nil {
			return nil, err
		}

		if tcpConn, ok := conn.(*net.TCPConn); ok {
			_ = tcpConn.SetKeepAliveConfig(keepAliveCfg)
		}
		return conn, nil
	}

	return client
}

func ConfigureProxy(client *fasthttp.Client, proxyConfig *schemas.ProxyConfig, logger schemas.Logger) *fasthttp.Client {
	return configureProxy(client, proxyConfig, logger, nil)
}

func configureProxy(client *fasthttp.Client, proxyConfig *schemas.ProxyConfig, logger schemas.Logger, schemeFor func(addr string) string) *fasthttp.Client {
	if proxyConfig == nil {
		return client
	}

	if proxyConfig.Type == schemas.NoProxy {
		return client
	}

	if proxyConfig.CACertPEM != nil && proxyConfig.CACertPEM.IsFromSecret() && proxyConfig.CACertPEM.GetValue() == "" {
		errMsg := fmt.Sprintf("invalid proxy configuration: %s references %q but it resolved to an empty value", "proxy.ca_cert_pem", proxyConfig.CACertPEM.GetRawRef())
		getLogger().Error(errMsg)
		client.Dial = dialErrorFunc(errMsg)
		return client
	}
	var proxyTLS *tls.Config
	if proxyCACertPEM := proxyConfig.CACertPEM.GetValue(); proxyCACertPEM != "" {
		tlsConfig, err := createTLSConfigWithCA(proxyCACertPEM)
		if err != nil {
			getLogger().Warn("Failed to configure custom CA certificate: %v", err)
		} else {
			proxyTLS = tlsConfig
		}
	}
	proxyTLS = withProxySkipVerify(proxyTLS, proxyConfig.SkipTLSVerify)

	var dialFunc fasthttp.DialFunc

	switch proxyConfig.Type {
	case schemas.HTTPProxy:
		if proxyConfig.URL != nil && proxyConfig.URL.IsFromSecret() && proxyConfig.URL.GetValue() == "" {
			errMsg := fmt.Sprintf("invalid proxy configuration: %s references %q but it resolved to an empty value", "proxy.url", proxyConfig.URL.GetRawRef())
			getLogger().Error(errMsg)
			client.Dial = dialErrorFunc(errMsg)
			return client
		}
		proxyURLValue := proxyConfig.URL.GetValue()
		if proxyURLValue == "" {
			getLogger().Warn("Warning: HTTP proxy URL is required for setting up proxy")
			return client
		}
		proxyURL := proxyURLValue
		proxyUsername := proxyConfig.Username.GetValue()
		proxyPassword := proxyConfig.Password.GetValue()
		if proxyUsername != "" && proxyPassword != "" {
			parsedURL, err := url.Parse(proxyURLValue)
			if err != nil {
				getLogger().Warn("Invalid proxy configuration: invalid HTTP proxy URL")
				return client
			}

			parsedURL.User = url.UserPassword(proxyUsername, proxyPassword)
			proxyURL = parsedURL.String()
		}
		dialFunc = proxyDialFunc(proxyURL, "http", proxyTLS)
	case schemas.Socks5Proxy:
		if proxyConfig.URL != nil && proxyConfig.URL.IsFromSecret() && proxyConfig.URL.GetValue() == "" {
			errMsg := fmt.Sprintf("invalid proxy configuration: %s references %q but it resolved to an empty value", "proxy.url", proxyConfig.URL.GetRawRef())
			getLogger().Error(errMsg)
			client.Dial = dialErrorFunc(errMsg)
			return client
		}
		proxyURLValue := proxyConfig.URL.GetValue()
		if proxyURLValue == "" {
			getLogger().Warn("Warning: SOCKS5 proxy URL is required for setting up proxy")
			return client
		}
		proxyURL := proxyURLValue

		proxyUsername := proxyConfig.Username.GetValue()
		proxyPassword := proxyConfig.Password.GetValue()
		if proxyUsername != "" && proxyPassword != "" {
			parsedURL, err := url.Parse(proxyURLValue)
			if err != nil {
				getLogger().Warn("Invalid proxy configuration: invalid SOCKS5 proxy URL")
				return client
			}

			parsedURL.User = url.UserPassword(proxyUsername, proxyPassword)
			proxyURL = parsedURL.String()
		}
		dialFunc = proxyDialFunc(proxyURL, "socks5", proxyTLS)
	case schemas.EnvProxy:

		dialFunc = envProxyDialFunc(proxyTLS, schemeFor)
	default:
		getLogger().Warn("Invalid proxy configuration: unsupported proxy type: %s", proxyConfig.Type)
		return client
	}

	if dialFunc != nil {
		if noProxy := proxyConfig.NoProxy; noProxy != "" {
			proxyDial := dialFunc
			dialFunc = func(addr string) (net.Conn, error) {
				if network.MatchesNoProxy(network.DialAddrHost(addr), noProxy) {

					return nil, errBypassProxy
				}
				return proxyDial(addr)
			}
		}
		client.Dial = dialFunc
	}

	if proxyTLS != nil {

		client.TLSConfig = scopeSkipVerify(proxyTLS, proxyConfig.NoProxy)
	}

	return client
}

func scopeSkipVerify(tlsConfig *tls.Config, noProxy string) *tls.Config {
	if tlsConfig == nil || !tlsConfig.InsecureSkipVerify || strings.TrimSpace(noProxy) == "" {
		return tlsConfig
	}
	scoped := tlsConfig.Clone()
	roots := scoped.RootCAs
	scoped.VerifyConnection = func(state tls.ConnectionState) error {
		if state.ServerName == "" {

			return verifyPeerCertificate(state, roots)
		}
		if !network.MatchesNoProxy(state.ServerName, noProxy) {
			return nil
		}
		return verifyPeerCertificate(state, roots)
	}
	key := weak.Make(scoped)
	skipVerifyScopes.Store(key, noProxy)
	runtime.AddCleanup(scoped, func(k weak.Pointer[tls.Config]) { skipVerifyScopes.Delete(k) }, key)
	return scoped
}

func RescopeProxySkipVerify(base, merged *tls.Config, providerSkipVerify bool) *tls.Config {
	if base == nil || merged == nil {
		return merged
	}
	noProxy, ok := skipVerifyScopes.Load(weak.Make(base))
	if !ok {
		return merged
	}
	if providerSkipVerify {
		merged.VerifyConnection = nil
		return merged
	}
	return scopeSkipVerify(merged, noProxy.(string))
}

func verifyPeerCertificate(state tls.ConnectionState, roots *x509.CertPool) error {
	if len(state.PeerCertificates) == 0 {
		return fmt.Errorf("tls: %s presented no certificate", state.ServerName)
	}
	intermediates := x509.NewCertPool()
	for _, cert := range state.PeerCertificates[1:] {
		intermediates.AddCert(cert)
	}
	_, err := state.PeerCertificates[0].Verify(x509.VerifyOptions{
		DNSName:       state.ServerName,
		Roots:         roots,
		Intermediates: intermediates,
	})
	return err
}

func withProxySkipVerify(tlsConfig *tls.Config, skip bool) *tls.Config {
	if !skip {
		return tlsConfig
	}
	if tlsConfig == nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	tlsConfig.InsecureSkipVerify = true
	return tlsConfig
}

func NewProviderHTTPClient(proxyConfig *schemas.ProxyConfig, networkConfig schemas.NetworkConfig, logger schemas.Logger) *http.Client {
	timeout := time.Duration(networkConfig.DefaultRequestTimeoutInSeconds) * time.Second
	client := &fasthttp.Client{
		ReadTimeout:              timeout,
		WriteTimeout:             timeout,
		MaxIdleConnDuration:      30 * time.Second,
		MaxResponseBodySize:      maxAuthResponseBytes,
		NoDefaultUserAgentHeader: true,
	}
	effective := proxyConfig
	if !namesProxy(proxyConfig) {
		effective = &schemas.ProxyConfig{Type: schemas.EnvProxy}
	}
	schemes := &sync.Map{}
	client = configureProxy(client, effective, logger, func(addr string) string {
		if scheme, ok := schemes.Load(addr); ok {
			return scheme.(string)
		}
		return ""
	})
	client = ConfigureTLS(client, networkConfig, logger)
	client.Dial = directForLocalTargets(client.Dial, timeout)
	client.Transport = NewContextTransport()
	return &http.Client{Transport: &fasthttpRoundTripper{client: client, schemes: schemes}, Timeout: timeout}
}

func namesProxy(proxyConfig *schemas.ProxyConfig) bool {
	return proxyConfig != nil && proxyConfig.Type != "" && proxyConfig.Type != schemas.NoProxy
}

func directForLocalTargets(proxyDial fasthttp.DialFunc, timeout time.Duration) fasthttp.DialFunc {
	dialer := &net.Dialer{Timeout: timeout}
	direct := func(addr string) (net.Conn, error) {
		return dialer.Dial("tcp", addr)
	}
	if proxyDial == nil {
		return direct
	}
	return func(addr string) (net.Conn, error) {
		if network.IsLocalOrMetadataHost(network.DialAddrHost(addr)) {
			return direct(addr)
		}
		conn, err := proxyDial(addr)
		if errors.Is(err, errBypassProxy) {
			return direct(addr)
		}
		return conn, err
	}
}

const maxAuthResponseBytes = 4 << 20

func dialAddr(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}

func (rt *fasthttpRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil && req.Body != http.NoBody {
		var err error
		body, err = io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, err
		}
	}

	freq := fasthttp.AcquireRequest()
	fresp := fasthttp.AcquireResponse()
	release := func() {
		fasthttp.ReleaseRequest(freq)
		fasthttp.ReleaseResponse(fresp)
	}
	if rt.schemes != nil {
		rt.schemes.Store(dialAddr(req.URL), req.URL.Scheme)
	}
	freq.SetRequestURI(req.URL.String())
	freq.Header.SetMethod(req.Method)
	for name, values := range req.Header {
		for _, value := range values {
			freq.Header.Add(name, value)
		}
	}
	if req.Host != "" && req.Host != req.URL.Host {
		freq.UseHostHeader = true
		freq.Header.SetHost(req.Host)
	}
	if body != nil {
		freq.SetBody(body)
	}

	ctx := req.Context()
	unbind := bindRequestContext(freq, ctx)
	done := make(chan error, 1)
	go func() {
		done <- rt.client.Do(freq, fresp)
	}()

	var err error
	select {
	case err = <-done:
		unbind()
	case <-ctx.Done():

		go func() {
			<-done
			unbind()
			release()
		}()
		return nil, ctx.Err()
	}
	defer release()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}

	statusCode := fresp.StatusCode()
	resp := &http.Response{
		Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		StatusCode: statusCode,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Request:    req,
	}
	for name, value := range fresp.Header.All() {
		resp.Header.Add(string(name), string(value))
	}
	respBody := append([]byte(nil), fresp.Body()...)
	resp.Body = io.NopCloser(bytes.NewReader(respBody))
	resp.ContentLength = int64(len(respBody))
	return resp, nil
}

func createTLSConfigWithCA(caCertPEM string) (*tls.Config, error) {

	rootCAs, err := x509.SystemCertPool()
	if err != nil {

		rootCAs = x509.NewCertPool()
	}

	if !rootCAs.AppendCertsFromPEM([]byte(caCertPEM)) {
		return nil, fmt.Errorf("failed to parse CA certificate PEM")
	}

	return &tls.Config{
		RootCAs:    rootCAs,
		MinVersion: tls.VersionTLS12,
	}, nil
}

func ConfigureTLS(client *fasthttp.Client, networkConfig schemas.NetworkConfig, logger schemas.Logger) *fasthttp.Client {
	tlsConfig, err := networkTLSConfig(client.TLSConfig, networkConfig, logger)
	if err != nil {
		logger.Error(err.Error())
		client.Dial = dialErrorFunc(err.Error())
		return client
	}
	client.TLSConfig = tlsConfig
	return client
}

func networkTLSConfig(base *tls.Config, networkConfig schemas.NetworkConfig, logger schemas.Logger) (*tls.Config, error) {
	if networkConfig.CACertPEM != nil && networkConfig.CACertPEM.IsFromSecret() && networkConfig.CACertPEM.GetValue() == "" {
		return nil, fmt.Errorf("invalid provider configuration: %s references %q but it resolved to an empty value", "network_config.ca_cert_pem", networkConfig.CACertPEM.GetRawRef())
	}

	caCertPEM := networkConfig.CACertPEM.GetValue()
	if !networkConfig.InsecureSkipVerify && caCertPEM == "" {
		return base, nil
	}

	tlsConfig := base
	if tlsConfig == nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		tlsConfig = tlsConfig.Clone()
	}

	if networkConfig.InsecureSkipVerify {
		logger.Warn("insecure_skip_verify is enabled for provider — TLS certificate verification is disabled. Not recommended for production.")
		tlsConfig.InsecureSkipVerify = true
	}

	if caCertPEM != "" {
		caTLSConfig, err := createTLSConfigWithCA(caCertPEM)
		if err != nil {
			logger.Warn("Failed to configure custom CA certificate for provider: %v", err)
		} else {
			if tlsConfig.RootCAs != nil {
				tlsConfig.RootCAs = tlsConfig.RootCAs.Clone()

				if !tlsConfig.RootCAs.AppendCertsFromPEM([]byte(caCertPEM)) {
					logger.Warn("Failed to append CA certificate to existing TLS config")
				}
			} else {
				tlsConfig.RootCAs = caTLSConfig.RootCAs
			}
		}
	}

	return RescopeProxySkipVerify(base, tlsConfig, networkConfig.InsecureSkipVerify), nil
}

func proxyDialFunc(rawURL, defaultScheme string, proxyTLS *tls.Config) fasthttp.DialFunc {
	if !strings.Contains(rawURL, "://") {
		rawURL = defaultScheme + "://" + rawURL
	}
	proxyURL, err := url.Parse(rawURL)
	if err != nil {
		return dialErrorFunc("invalid proxy configuration: invalid proxy URL")
	}
	return func(addr string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(context.Background(), proxyHandshakeTimeout)
		defer cancel()
		return network.DialViaProxyTLS(ctx, proxyURL, addr, proxyTLS)
	}
}

func envProxyDialFunc(proxyTLS *tls.Config, schemeFor func(addr string) string) fasthttp.DialFunc {
	proxyFunc := httpproxy.FromEnvironment().ProxyFunc()
	return func(addr string) (net.Conn, error) {
		scheme := ""
		if schemeFor != nil {
			scheme = schemeFor(addr)
		}
		if scheme == "" {
			scheme = "http"
			if strings.HasSuffix(addr, ":443") {
				scheme = "https"
			}
		}
		proxyURL, err := proxyFunc(&url.URL{Scheme: scheme, Host: addr})
		if err != nil {
			return nil, fmt.Errorf("invalid proxy configuration: %w", err)
		}
		if proxyURL == nil {
			return nil, errBypassProxy
		}
		ctx, cancel := context.WithTimeout(context.Background(), proxyHandshakeTimeout)
		defer cancel()
		return network.DialViaProxyTLS(ctx, proxyURL, addr, proxyTLS)
	}
}

func dialErrorFunc(message string) fasthttp.DialFunc {
	return func(_ string) (net.Conn, error) {
		return nil, fmt.Errorf("%s", message)
	}
}

func filterHeaders(headers map[string][]string) map[string][]string {
	filtered := make(map[string][]string, len(headers))
	for k, v := range headers {
		if !hopByHopHeaders[strings.ToLower(k)] {
			filtered[k] = v
		}
	}
	return filtered
}

func shouldFilterProviderResponseHeader(nameLower string) bool {
	return providerResponseFilterHeaders[nameLower] || schemas.IsSensitiveHeader(nameLower)
}

func ExtractProviderResponseHeaders(resp *fasthttp.Response) map[string]string {
	if resp == nil {
		return nil
	}
	headers := make(map[string]string)
	resp.Header.VisitAll(func(key, value []byte) {
		k := string(key)
		if shouldFilterProviderResponseHeader(strings.ToLower(k)) {
			return
		}
		v := string(value)
		if existing, ok := headers[k]; ok && existing != "" {
			headers[k] = existing + ", " + v
		} else {
			headers[k] = v
		}
	})
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func ExtractPassthroughProviderResponseHeaders(resp *fasthttp.Response) map[string]string {
	if resp == nil {
		return nil
	}
	resp.Header.SetNoDefaultContentType(true)
	headers := make(map[string]string)
	resp.Header.VisitAll(func(key, value []byte) {
		k := string(key)
		kLower := strings.ToLower(k)
		if shouldFilterProviderResponseHeader(kLower) && kLower != "content-type" {
			return
		}
		v := string(value)
		if existing, ok := headers[k]; ok && existing != "" {
			headers[k] = existing + ", " + v
		} else {
			headers[k] = v
		}
	})
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func SetExtraHeaders(ctx context.Context, req *fasthttp.Request, extraHeaders map[string]schemas.SecretVar, skipHeaders []string) {
	for key, value := range extraHeaders {
		canonicalKey := textproto.CanonicalMIMEHeaderKey(key)
		if skipHeaders != nil {
			if slices.Contains(skipHeaders, key) {
				continue
			}
		}

		if len(req.Header.Peek(canonicalKey)) == 0 {
			req.Header.Set(canonicalKey, value.GetValue())
		}
	}

	if extraHeaders, ok := (ctx).Value(schemas.BifrostContextKeyExtraHeaders).(map[string][]string); ok {
		for k, values := range filterHeaders(extraHeaders) {
			if skipHeaders != nil && slices.Contains(skipHeaders, strings.ToLower(k)) {
				continue
			}
			for i, v := range values {
				if i == 0 {
					req.Header.Set(k, v)
				} else {
					req.Header.Add(k, v)
				}
			}
		}
	}
}

const internalHeaderPrefix = "x-bf-"

func filterSupportedAcceptEncodings(values []string, supportedEncodings map[string]struct{}) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		for item := range strings.SplitSeq(value, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}

			coding := item
			if separator := strings.IndexByte(coding, ';'); separator >= 0 {
				coding = coding[:separator]
			}
			coding = strings.ToLower(strings.TrimSpace(coding))
			if _, ok := supportedEncodings[coding]; ok {

				filtered = append(filtered, item)
			}
		}
	}
	return filtered
}

func SetPassthroughHeaders(ctx context.Context, req *fasthttp.Request, provider schemas.ModelProvider, skipHeaders []string) {
	setPassthroughHeaders(ctx, req, provider, skipHeaders, supportedBufferedContentEncodings)
}

func SetPassthroughHeadersForStreaming(ctx context.Context, req *fasthttp.Request, provider schemas.ModelProvider, skipHeaders []string) {
	setPassthroughHeaders(ctx, req, provider, skipHeaders, supportedStreamingContentEncodings)
}

func setPassthroughHeaders(ctx context.Context, req *fasthttp.Request, provider schemas.ModelProvider, skipHeaders []string, supportedEncodings map[string]struct{}) {

	if provider != schemas.Anthropic {
		return
	}
	headers, ok := (ctx).Value(schemas.BifrostContextKeyPassthroughHeaders).(map[string][]string)
	if !ok {
		return
	}
	for k, values := range filterHeaders(headers) {
		lower := strings.ToLower(k)
		if strings.HasPrefix(lower, internalHeaderPrefix) {
			continue
		}
		if skipHeaders != nil && slices.Contains(skipHeaders, lower) {
			continue
		}

		if lower == "content-type" {
			continue
		}
		if lower == "accept-encoding" {

			if supported := filterSupportedAcceptEncodings(values, supportedEncodings); len(supported) > 0 {
				req.Header.Set(k, strings.Join(supported, ", "))
			} else {
				req.Header.Set(k, "identity")
			}
			continue
		}
		for i, v := range values {
			if i == 0 {
				req.Header.Set(k, v)
			} else {
				req.Header.Add(k, v)
			}
		}
	}
}

func StripCallerAuthForInsecureURL(requestURL string, safeHeaders map[string]string) {
	if len(safeHeaders) == 0 {
		return
	}
	u, err := url.Parse(requestURL)
	if err == nil && (strings.EqualFold(u.Scheme, "https") || isLoopbackHost(u.Hostname())) {
		return
	}
	for k := range safeHeaders {
		if strings.EqualFold(k, "authorization") {
			delete(safeHeaders, k)
		}
	}
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func BuildPassthroughURL(baseURL, path, rawQuery string) (string, error) {
	base := strings.TrimRight(baseURL, "/")
	baseParsed, err := url.Parse(base)
	if err != nil || baseParsed.Scheme == "" || baseParsed.Host == "" {
		return "", fmt.Errorf("invalid provider base url %q", baseURL)
	}
	if path != "" && (!strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//")) {
		return "", fmt.Errorf("invalid passthrough path %q: must be a rooted path", path)
	}

	path = passthroughPathDelimiters.Replace(path)
	full := base + path
	if rawQuery != "" {
		full += "?" + rawQuery
	}
	resolved, err := url.Parse(full)
	if err != nil {
		return "", fmt.Errorf("invalid passthrough url: %w", err)
	}
	if resolved.Scheme != baseParsed.Scheme || resolved.Host != baseParsed.Host || resolved.User.String() != baseParsed.User.String() {
		return "", fmt.Errorf("passthrough url does not stay on provider host %q", baseParsed.Host)
	}
	return full, nil
}

func GetPathFromContext(ctx context.Context, defaultPath string) string {
	if pathInContext, ok := ctx.Value(schemas.BifrostContextKeyURLPath).(string); ok {
		return pathInContext
	}
	return defaultPath
}

func IsAbsoluteRequestURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	return err == nil && u != nil && u.IsAbs() && u.Host != ""
}

func GetRequestPath(ctx context.Context, defaultPath string, customProviderConfig *schemas.CustomProviderConfig, requestType schemas.RequestType) (string, bool) {

	if pathInContext, ok := ctx.Value(schemas.BifrostContextKeyURLPath).(string); ok {
		trimmed := strings.TrimSpace(pathInContext)
		if u, err := url.Parse(trimmed); err == nil && u != nil && u.IsAbs() && u.Host != "" {
			return trimmed, true
		}
		return trimmed, false
	}

	if customProviderConfig != nil && customProviderConfig.RequestPathOverrides != nil {
		if raw, ok := customProviderConfig.RequestPathOverrides[requestType]; ok {
			override := strings.TrimSpace(raw)
			if override == "" {
				return defaultPath, false
			}

			if IsAbsoluteRequestURL(override) {
				return override, true
			}

			if !strings.HasPrefix(override, "/") {
				override = "/" + override
			}
			return override, false
		}
	}

	return defaultPath, false
}

func CheckAndGetRawRequestBody(ctx context.Context, request RequestBodyGetter) ([]byte, bool) {
	if rawBody, ok := ctx.Value(schemas.BifrostContextKeyUseRawRequestBody).(bool); ok && rawBody {
		return request.GetRawRequestBody(), true
	}
	return nil, false
}

func IsLargePayloadPassthroughEnabled(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	isLargePayload, ok := ctx.Value(schemas.BifrostContextKeyLargePayloadMode).(bool)
	if !ok || !isLargePayload {
		return false
	}
	reader, ok := ctx.Value(schemas.BifrostContextKeyLargePayloadReader).(io.Reader)
	return ok && reader != nil
}

func ApplyLargePayloadRequestBody(ctx context.Context, req *fasthttp.Request) bool {
	return ApplyLargePayloadRequestBodyWithModelNormalization(ctx, req, "")
}

const largePayloadModelRewriteScanBytes = 256 * 1024

func ApplyLargePayloadRequestBodyWithModelNormalization(
	ctx context.Context,
	req *fasthttp.Request,
	defaultProvider schemas.ModelProvider,
) bool {
	if req == nil || !IsLargePayloadPassthroughEnabled(ctx) {
		return false
	}

	bodyReader, _ := ctx.Value(schemas.BifrostContextKeyLargePayloadReader).(io.Reader)
	bodySize := -1
	if contentLength, ok := ctx.Value(schemas.BifrostContextKeyLargePayloadContentLength).(int); ok {
		bodySize = contentLength
	}

	if contentType, ok := ctx.Value(schemas.BifrostContextKeyLargePayloadContentType).(string); ok && contentType != "" {
		ctLower := strings.ToLower(contentType)
		if metadata, ok := ctx.Value(schemas.BifrostContextKeyLargePayloadMetadata).(*schemas.LargePayloadMetadata); ok && metadata != nil {
			if rawModel := strings.TrimSpace(metadata.Model); rawModel != "" && defaultProvider != "" {
				_, normalizedModel := schemas.ParseModelString(rawModel, defaultProvider)
				if normalizedModel != "" && normalizedModel != rawModel {
					if strings.Contains(ctLower, "application/json") {
						rewrittenReader, sizeDelta := RewriteLargePayloadModelInJSONPrefix(bodyReader, rawModel, normalizedModel)
						bodyReader = rewrittenReader
						if bodySize >= 0 {
							bodySize += sizeDelta
						}
					} else if strings.Contains(ctLower, "multipart/form-data") {
						rewrittenReader, sizeDelta := RewriteLargePayloadModelInMultipartPrefix(bodyReader, rawModel, normalizedModel)
						bodyReader = rewrittenReader
						if bodySize >= 0 {
							bodySize += sizeDelta
						}
					}
				}
			}
		}
		req.Header.SetContentType(contentType)
	}
	req.SetBodyStream(bodyReader, bodySize)
	return true
}

func RewriteLargePayloadModelInJSONPrefix(reader io.Reader, fromModel, toModel string) (io.Reader, int) {
	if reader == nil || fromModel == "" || toModel == "" || fromModel == toModel {
		return reader, 0
	}
	prefix := make([]byte, largePayloadModelRewriteScanBytes)
	n, err := io.ReadFull(reader, prefix)
	if n == 0 && err != nil {
		return reader, 0
	}
	prefix = prefix[:n]

	rewrittenPrefix, changed := rewriteJSONModelValue(prefix, fromModel, toModel)
	if !changed {
		return io.MultiReader(bytes.NewReader(prefix), reader), 0
	}
	return io.MultiReader(bytes.NewReader(rewrittenPrefix), reader), len(rewrittenPrefix) - len(prefix)
}

func rewriteJSONModelValue(data []byte, fromModel, toModel string) ([]byte, bool) {
	if len(data) == 0 || fromModel == "" || toModel == "" || fromModel == toModel {
		return data, false
	}
	pattern := []byte(`"model"`)
	searchFrom := 0
	for {
		match := bytes.Index(data[searchFrom:], pattern)
		if match < 0 {
			return data, false
		}
		idx := searchFrom + match + len(pattern)

		for idx < len(data) && (data[idx] == ' ' || data[idx] == '\t' || data[idx] == '\r' || data[idx] == '\n') {
			idx++
		}
		if idx >= len(data) || data[idx] != ':' {
			searchFrom += match + len(pattern)
			continue
		}
		idx++
		for idx < len(data) && (data[idx] == ' ' || data[idx] == '\t' || data[idx] == '\r' || data[idx] == '\n') {
			idx++
		}
		if idx >= len(data) || data[idx] != '"' {
			searchFrom += match + len(pattern)
			continue
		}

		valueStart := idx + 1
		valueEnd := valueStart
		escaped := false
		for valueEnd < len(data) {
			ch := data[valueEnd]
			if escaped {
				escaped = false
				valueEnd++
				continue
			}
			if ch == '\\' {
				escaped = true
				valueEnd++
				continue
			}
			if ch == '"' {
				break
			}
			valueEnd++
		}
		if valueEnd >= len(data) {
			return data, false
		}

		if string(data[valueStart:valueEnd]) != fromModel {
			searchFrom = valueEnd + 1
			continue
		}

		rewritten := make([]byte, 0, len(data)-len(fromModel)+len(toModel))
		rewritten = append(rewritten, data[:valueStart]...)
		rewritten = append(rewritten, toModel...)
		rewritten = append(rewritten, data[valueEnd:]...)
		return rewritten, true
	}
}

func RewriteLargePayloadModelInMultipartPrefix(reader io.Reader, fromModel, toModel string) (io.Reader, int) {
	if reader == nil || fromModel == "" || toModel == "" || fromModel == toModel {
		return reader, 0
	}
	prefix := make([]byte, largePayloadModelRewriteScanBytes)
	n, err := io.ReadFull(reader, prefix)
	if n == 0 && err != nil {
		return reader, 0
	}
	prefix = prefix[:n]

	from := []byte(fromModel)
	to := []byte(toModel)
	if idx := bytes.Index(prefix, from); idx >= 0 {
		rewritten := make([]byte, 0, len(prefix)-len(from)+len(to))
		rewritten = append(rewritten, prefix[:idx]...)
		rewritten = append(rewritten, to...)
		rewritten = append(rewritten, prefix[idx+len(from):]...)
		return io.MultiReader(bytes.NewReader(rewritten), reader), len(rewritten) - len(prefix)
	}
	return io.MultiReader(bytes.NewReader(prefix), reader), 0
}

func DrainLargePayloadRemainder(ctx context.Context) {
	if !IsLargePayloadPassthroughEnabled(ctx) {
		return
	}
	bodyReader, _ := ctx.Value(schemas.BifrostContextKeyLargePayloadReader).(io.Reader)
	if bodyReader == nil {
		return
	}
	_, _ = io.Copy(io.Discard, bodyReader)
}

func CloneFastHTTPClientConfig(base *fasthttp.Client) *fasthttp.Client {
	if base == nil {
		return &fasthttp.Client{}
	}

	return &fasthttp.Client{
		Transport:                     base.Transport,
		DialTimeout:                   base.DialTimeout,
		Dial:                          base.Dial,
		TLSConfig:                     base.TLSConfig,
		RetryIfErr:                    base.RetryIfErr,
		ConfigureClient:               base.ConfigureClient,
		Name:                          base.Name,
		MaxConnsPerHost:               base.MaxConnsPerHost,
		MaxIdleConnDuration:           base.MaxIdleConnDuration,
		MaxConnDuration:               base.MaxConnDuration,
		MaxIdemponentCallAttempts:     base.MaxIdemponentCallAttempts,
		ReadBufferSize:                base.ReadBufferSize,
		WriteBufferSize:               base.WriteBufferSize,
		ReadTimeout:                   base.ReadTimeout,
		WriteTimeout:                  base.WriteTimeout,
		MaxResponseBodySize:           base.MaxResponseBodySize,
		MaxConnWaitTimeout:            base.MaxConnWaitTimeout,
		ConnPoolStrategy:              base.ConnPoolStrategy,
		NoDefaultUserAgentHeader:      base.NoDefaultUserAgentHeader,
		DialDualStack:                 base.DialDualStack,
		DisableHeaderNamesNormalizing: base.DisableHeaderNamesNormalizing,
		DisablePathNormalizing:        base.DisablePathNormalizing,
		StreamResponseBody:            base.StreamResponseBody,
	}
}

func BuildStreamingClient(base *fasthttp.Client) *fasthttp.Client {
	c := CloneFastHTTPClientConfig(base)
	c.StreamResponseBody = true
	if c.Transport == nil {
		c.Transport = NewContextTransport()
	}
	return c
}

func decompressBodyStreamIfGzip(resp *fasthttp.Response, stream io.Reader) (*gzip.Reader, io.Reader, bool) {
	ce := strings.ToLower(strings.TrimSpace(string(resp.Header.Peek("Content-Encoding"))))
	if !strings.Contains(ce, "gzip") {
		return nil, stream, false
	}
	gz, err := AcquireGzipReader(stream)
	if err != nil {
		ReleaseGzipReader(gz)
		return nil, stream, false
	}
	resp.Header.Del("Content-Encoding")
	return gz, gz, true
}

func DecompressStreamBody(resp *fasthttp.Response) (io.Reader, func()) {
	bodyStream := resp.BodyStream()
	if bodyStream == nil {

		return bytes.NewReader(nil), func() {}
	}
	gz, decompressed, wasGzip := decompressBodyStreamIfGzip(resp, bodyStream)
	if !wasGzip {
		return bodyStream, func() {}
	}
	return decompressed, func() {
		ReleaseGzipReader(gz)
	}
}

const nonSSESampleLimit = 8 * 1024

const nonSSEMessageLimit = 512

const sseProbeWindow = 16

const (
	StreamBodyNonSSE StreamBodyKind = iota

	StreamBodyEmpty

	StreamBodyIdleTimeout

	StreamBodyClosed

	StreamBodyReadError
)

func (k StreamBodyKind) String() string {
	switch k {
	case StreamBodyNonSSE:
		return "non-sse"
	case StreamBodyEmpty:
		return "empty"
	case StreamBodyIdleTimeout:
		return "idle-timeout"
	case StreamBodyClosed:
		return "closed"
	case StreamBodyReadError:
		return "read-error"
	default:
		return "unknown"
	}
}

func (b *NonSSEStreamBody) Err() error {
	switch b.Kind {
	case StreamBodyIdleTimeout:
		return fmt.Errorf("no data received on the stream before the idle timeout (content-type %q): %w", b.ContentType, ErrStreamIdleTimeout)
	case StreamBodyClosed:
		return fmt.Errorf("stream closed before the first byte arrived (content-type %q): %w", b.ContentType, ErrStreamClosed)
	case StreamBodyEmpty:
		return fmt.Errorf("provider returned an empty body for streaming request (content-type %q)", b.ContentType)
	case StreamBodyReadError:
		return fmt.Errorf("failed to read the streaming response body (content-type %q): %w", b.ContentType, b.ReadErr)
	default:
		excerpt := b.Sample
		if len(excerpt) > nonSSEMessageLimit {
			excerpt = excerpt[:nonSSEMessageLimit]
		}
		return fmt.Errorf("provider returned non-SSE response for streaming request (content-type %q): %s", b.ContentType, excerpt)
	}
}

func DrainNonSSEStreamReader(resp *fasthttp.Response, reader io.Reader) (io.Reader, *NonSSEStreamBody) {
	ct := strings.ToLower(string(resp.Header.ContentType()))

	if strings.Contains(ct, "text/event-stream") {
		return reader, nil
	}
	if reader == nil {
		return nil, &NonSSEStreamBody{Kind: StreamBodyEmpty, ContentType: ct}
	}

	br := bufio.NewReaderSize(reader, sseInitialBufSize)
	looksSSE, readErr := hasSSEPrefix(br)
	if looksSSE {
		return br, nil
	}

	info := &NonSSEStreamBody{ContentType: ct, ReadErr: readErr}
	switch {
	case readErr == nil:
		info.Kind = StreamBodyNonSSE
		info.Sample = drainWithSample(br)
	case errors.Is(readErr, ErrStreamIdleTimeout):
		info.Kind = StreamBodyIdleTimeout
	case errors.Is(readErr, ErrStreamClosed):
		info.Kind = StreamBodyClosed
	case errors.Is(readErr, io.EOF):
		info.Kind = StreamBodyEmpty
	default:
		info.Kind = StreamBodyReadError
	}
	return nil, info
}

func drainWithSample(r io.Reader) []byte {
	var sample bytes.Buffer
	if _, err := io.Copy(&sample, io.LimitReader(r, nonSSESampleLimit)); err != nil {
		return sample.Bytes()
	}
	_, _ = io.Copy(io.Discard, r)
	return sample.Bytes()
}

func hasSSEPrefix(reader *bufio.Reader) (bool, error) {
	first, err := reader.Peek(1)
	if err != nil {
		return false, err
	}
	if len(first) == 0 {
		return false, nil
	}

	n := min(reader.Buffered(), sseProbeWindow)
	window, err := reader.Peek(n)
	if err != nil {
		return false, err
	}

	trimmed := trimSSELeading(window)
	for inconclusiveSSELeading(trimmed) {
		if n >= sseProbeWindow {
			return false, nil
		}
		n++
		if window, err = reader.Peek(n); err != nil {
			return false, err
		}
		trimmed = trimSSELeading(window)
	}
	window = trimmed
	switch window[0] {
	case ':', '\n', '\r':
		return true, nil
	}
	for _, name := range sseFieldNames {
		if hasFoldedPrefix(window, name) {
			return true, nil
		}
	}
	return false, nil
}

func trimSSELeading(b []byte) []byte {
	b = bytes.TrimPrefix(b, utf8BOM)
	return bytes.TrimLeft(b, " \t")
}

func inconclusiveSSELeading(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	return len(b) < len(utf8BOM) && bytes.HasPrefix(utf8BOM, b)
}

func hasFoldedPrefix(b, name []byte) bool {
	n := min(len(b), len(name))
	if n == 0 {
		return false
	}
	return bytes.EqualFold(b[:n], name[:n])
}

func MergeExtraParams(jsonMap map[string]interface{}, extraParams map[string]interface{}) {
	for k, v := range extraParams {
		if existingVal, exists := jsonMap[k]; exists {
			if existingMap, ok := existingVal.(map[string]interface{}); ok {
				if newMap, ok := v.(map[string]interface{}); ok {
					MergeExtraParams(existingMap, newMap)
					continue
				}
			}
		}
		jsonMap[k] = v
	}
}

func MergeExtraParamsIntoJSON(jsonBody []byte, extraParams map[string]interface{}) ([]byte, error) {
	trimmed := bytes.TrimSpace(jsonBody)
	if len(trimmed) < 2 || trimmed[0] != '{' {
		return jsonBody, nil
	}

	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()

	if _, err := dec.Token(); err != nil {
		return jsonBody, nil
	}

	type kvPair struct {
		key string
		val json.RawMessage
	}
	var pairs []kvPair
	seen := make(map[string]int)

	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return jsonBody, nil
		}
		key, ok := tok.(string)
		if !ok {
			return jsonBody, nil
		}
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return jsonBody, nil
		}
		seen[key] = len(pairs)
		pairs = append(pairs, kvPair{key, val})
	}

	extraKeys := make([]string, 0, len(extraParams))
	for k := range extraParams {
		extraKeys = append(extraKeys, k)
	}
	sort.Strings(extraKeys)
	for _, k := range extraKeys {
		v := extraParams[k]
		newValBytes, err := MarshalSorted(v)
		if err != nil {
			continue
		}
		if idx, exists := seen[k]; exists {

			existingTrimmed := bytes.TrimSpace(pairs[idx].val)
			newTrimmed := bytes.TrimSpace(newValBytes)
			if len(existingTrimmed) > 0 && existingTrimmed[0] == '{' &&
				len(newTrimmed) > 0 && newTrimmed[0] == '{' {
				var existingMap, newMap map[string]interface{}
				existingDec := json.NewDecoder(bytes.NewReader(existingTrimmed))
				existingDec.UseNumber()
				newDec := json.NewDecoder(bytes.NewReader(newTrimmed))
				newDec.UseNumber()
				if existingDec.Decode(&existingMap) == nil {
					if newDec.Decode(&newMap) == nil {
						MergeExtraParams(existingMap, newMap)
						if merged, err := MarshalSorted(existingMap); err == nil {
							pairs[idx].val = merged
							continue
						}
					}
				}
			}

			pairs[idx].val = newValBytes
		} else {

			pairs = append(pairs, kvPair{k, newValBytes})
		}
	}

	var compact bytes.Buffer
	compact.WriteByte('{')
	for i, kv := range pairs {
		if i > 0 {
			compact.WriteByte(',')
		}
		keyBytes, err := sonic.Marshal(kv.key)
		if err != nil {
			return jsonBody, err
		}
		compact.Write(keyBytes)
		compact.WriteByte(':')

		compact.Write(bytes.TrimSpace(kv.val))
	}
	compact.WriteByte('}')

	return compact.Bytes(), nil
}

func startPhaseSpan(ctx context.Context, name string) (schemas.Tracer, schemas.SpanHandle) {
	t, ok := ctx.Value(schemas.BifrostContextKeyTracer).(schemas.Tracer)
	if !ok || t == nil {
		return nil, nil
	}
	_, h := t.StartSpanID(ctx, name, schemas.SpanKindInternal)
	return t, h
}

func StartResponseConvertorSpan(ctx context.Context) (schemas.Tracer, schemas.SpanHandle) {
	return startPhaseSpan(ctx, "convertor")
}

func StartResponseParseSpan(ctx context.Context) (schemas.Tracer, schemas.SpanHandle) {
	return startPhaseSpan(ctx, "response-parse")
}

func StartPhaseSpan(ctx context.Context, name string) (schemas.Tracer, schemas.SpanHandle) {
	return startPhaseSpan(ctx, name)
}

func CheckContextAndGetRequestBody(ctx context.Context, request RequestBodyGetter, requestConverter RequestBodyConverter) ([]byte, *schemas.BifrostError) {
	if IsLargePayloadPassthroughEnabled(ctx) {
		return nil, nil
	}

	rawBody, ok := CheckAndGetRawRequestBody(ctx, request)
	if !ok {

		ct, chdl := startPhaseSpan(ctx, "convertor")
		convertedBody, err := requestConverter()
		if err != nil {
			if ct != nil {
				ct.EndSpan(chdl, schemas.SpanStatusError, err.Error())
			}

			if badRequest, ok := AsBifrostBadRequestError(err); ok {
				return nil, badRequest
			}
			return nil, NewBifrostOperationError(schemas.ErrRequestBodyConversion, err)
		}
		if ct != nil {
			ct.EndSpan(chdl, schemas.SpanStatusOk, "")
		}
		if convertedBody == nil {
			return nil, NewBifrostOperationError("request body is not provided", nil)
		}

		mt, mhdl := startPhaseSpan(ctx, "request-marshal")

		jsonBody, err := MarshalProviderRequest(convertedBody)
		if err != nil {
			if mt != nil {
				mt.EndSpan(mhdl, schemas.SpanStatusError, err.Error())
			}
			return nil, NewBifrostOperationError(schemas.ErrProviderRequestMarshal, err)
		}

		if ctx.Value(schemas.BifrostContextKeyPassthroughExtraParams) != nil && ctx.Value(schemas.BifrostContextKeyPassthroughExtraParams) == true {
			extraParams := convertedBody.GetExtraParams()
			if len(extraParams) > 0 {

				jsonBody, err = MergeExtraParamsIntoJSON(jsonBody, extraParams)
				if err != nil {
					if mt != nil {
						mt.EndSpan(mhdl, schemas.SpanStatusError, err.Error())
					}
					return nil, NewBifrostOperationError(schemas.ErrProviderRequestMarshal, err)
				}
			}
		}
		if mt != nil {
			mt.EndSpan(mhdl, schemas.SpanStatusOk, "")
		}
		return jsonBody, nil
	} else {
		return rawBody, nil
	}
}

func rootErrorMessage(raw interface{}) string {
	body, ok := raw.(map[string]interface{})
	if !ok {
		return ""
	}
	message, _ := body["message"].(string)
	return strings.TrimSpace(message)
}

func HandleProviderAPIError(resp *fasthttp.Response, errorResp any) *schemas.BifrostError {
	bifrostErr := handleProviderAPIError(resp, errorResp)
	ApplyRetryAfter(bifrostErr, &resp.Header)
	return bifrostErr
}

func handleProviderAPIError(resp *fasthttp.Response, errorResp any) *schemas.BifrostError {
	statusCode := resp.StatusCode()

	decodedBody, err := CheckAndDecodeBody(resp)
	if err != nil {

		rawBody := resp.Body()
		var rawErrorResponse interface{}
		if len(rawBody) > 0 {

			if unmarshalErr := sonic.Unmarshal(rawBody, &rawErrorResponse); unmarshalErr != nil {
				rawErrorResponse = string(rawBody)
			}
		}

		return &schemas.BifrostError{
			IsBifrostError: false,
			StatusCode:     &statusCode,
			Error: &schemas.ErrorField{
				Message: err.Error(),
			},
			ExtraFields: schemas.BifrostErrorExtraFields{
				RawResponse: rawErrorResponse,
			},
		}
	}

	var rawErrorResponse interface{}
	if err := sonic.Unmarshal(decodedBody, &rawErrorResponse); err != nil {

		rawErrorResponse = string(decodedBody)
	}

	trimmed := strings.TrimSpace(string(decodedBody))
	if len(trimmed) == 0 {

		var errorMessage string
		switch statusCode {
		case 401:
			errorMessage = "authentication failed: unauthorized (401) - check your API key"
		case 403:
			errorMessage = "access forbidden (403) - your API key may not have permission for this operation"
		case 404:
			errorMessage = "resource not found (404)"
		case 429:
			errorMessage = "rate limit exceeded (429)"
		case 500, 502, 503, 504:
			errorMessage = fmt.Sprintf("provider server error (%d)", statusCode)
		default:
			errorMessage = fmt.Sprintf("%s (HTTP %d)", schemas.ErrProviderResponseEmpty, statusCode)
		}
		return &schemas.BifrostError{
			IsBifrostError: false,
			StatusCode:     &statusCode,
			Error: &schemas.ErrorField{
				Message: errorMessage,
			},
			ExtraFields: schemas.BifrostErrorExtraFields{
				RawResponse: rawErrorResponse,
			},
		}
	}

	if err := sonic.Unmarshal(decodedBody, errorResp); err == nil {

		return &schemas.BifrostError{
			IsBifrostError: false,
			StatusCode:     &statusCode,
			Error:          &schemas.ErrorField{Message: rootErrorMessage(rawErrorResponse)},
			ExtraFields: schemas.BifrostErrorExtraFields{
				RawResponse: rawErrorResponse,
			},
		}
	}

	if IsHTMLResponse(resp, decodedBody) {
		return &schemas.BifrostError{
			IsBifrostError: false,
			StatusCode:     &statusCode,
			Error: &schemas.ErrorField{
				Message: schemas.ErrProviderResponseHTML,
				Error:   errors.New(string(decodedBody)),
			},
			ExtraFields: schemas.BifrostErrorExtraFields{
				RawResponse: rawErrorResponse,
			},
		}
	}

	message := fmt.Sprintf("provider API error: %s", string(decodedBody))
	return &schemas.BifrostError{
		IsBifrostError: false,
		StatusCode:     &statusCode,
		Error: &schemas.ErrorField{
			Message: message,
		},
		ExtraFields: schemas.BifrostErrorExtraFields{
			RawResponse: rawErrorResponse,
		},
	}
}

func EnrichError(
	ctx *schemas.BifrostContext,
	bifrostErr *schemas.BifrostError,
	requestBody []byte,
	responseBody []byte,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	latency ...time.Duration,
) *schemas.BifrostError {
	if bifrostErr == nil {
		return bifrostErr
	}

	if len(latency) > 0 {
		SetErrorLatency(bifrostErr, latency[0])
	}

	if ShouldSendBackRawRequest(ctx, sendBackRawRequest) && len(requestBody) > 0 {

		bifrostErr.ExtraFields.RawRequest = compactRawJSON(requestBody)
	} else {
		bifrostErr.ExtraFields.RawRequest = nil
	}

	if ShouldSendBackRawResponse(ctx, sendBackRawResponse) {
		if len(responseBody) > 0 {
			bifrostErr.ExtraFields.RawResponse = compactRawJSON(responseBody)
		}
	} else {
		bifrostErr.ExtraFields.RawResponse = nil
	}

	return bifrostErr
}

func HandleProviderResponseCtx[T any](ctx context.Context, responseBody []byte, response *T, requestBody []byte, sendBackRawRequest bool, sendBackRawResponse bool) (rawRequest interface{}, rawResponse interface{}, bifrostErr *schemas.BifrostError) {
	if t, h := startPhaseSpan(ctx, "response-parse"); t != nil {

		defer func() {
			if bifrostErr != nil {
				msg := "response parse failed"
				if bifrostErr.Error != nil && bifrostErr.Error.Message != "" {
					msg = bifrostErr.Error.Message
				}
				t.EndSpan(h, schemas.SpanStatusError, msg)
				return
			}
			t.EndSpan(h, schemas.SpanStatusOk, "")
		}()
	}
	return HandleProviderResponse(responseBody, response, requestBody, sendBackRawRequest, sendBackRawResponse)
}

func HandleProviderResponse[T any](responseBody []byte, response *T, requestBody []byte, sendBackRawRequest bool, sendBackRawResponse bool) (rawRequest interface{}, rawResponse interface{}, bifrostErr *schemas.BifrostError) {

	if len(bytes.TrimSpace(responseBody)) == 0 {
		return nil, nil, &schemas.BifrostError{
			IsBifrostError: true,
			Error: &schemas.ErrorField{
				Message: schemas.ErrProviderResponseEmpty,
			},
		}
	}

	shouldCaptureRawRequest := sendBackRawRequest && requestBody != nil

	if shouldCaptureRawRequest {

		rawRequest = compactRawJSON(requestBody)
	}

	if sendBackRawResponse {
		rawResponse = compactRawJSON(responseBody)
	}

	structuredErr := sonic.Unmarshal(responseBody, response)
	if structuredErr != nil {

		if IsHTMLResponse(nil, responseBody) {
			return nil, nil, &schemas.BifrostError{
				IsBifrostError: false,
				Error: &schemas.ErrorField{
					Message: schemas.ErrProviderResponseHTML,
					Error:   errors.New(string(responseBody)),
				},
			}
		}

		return nil, nil, &schemas.BifrostError{
			IsBifrostError: true,
			Error: &schemas.ErrorField{
				Message: schemas.ErrProviderResponseUnmarshal,
				Error:   structuredErr,
			},
		}
	}

	if shouldCaptureRawRequest || sendBackRawResponse {
		return rawRequest, rawResponse, nil
	}

	return nil, nil, nil
}

func compactRawJSON(data []byte) json.RawMessage {
	var buf bytes.Buffer
	if err := schemas.Compact(&buf, data); err == nil {
		return json.RawMessage(buf.Bytes())
	}
	return json.RawMessage(data)
}

func ParseAndSetRawRequest(extraFields *schemas.BifrostResponseExtraFields, jsonBody []byte) {
	if len(jsonBody) > 0 {
		extraFields.RawRequest = compactRawJSON(jsonBody)
	}
}

func PassthroughJSONBody(fasthttpReq *fasthttp.Request, body []byte) []byte {
	if strings.Contains(strings.ToLower(string(fasthttpReq.Header.ContentType())), "application/json") {
		return body
	}
	return nil
}

func NewUnsupportedOperationError(requestType schemas.RequestType, providerName schemas.ModelProvider) *schemas.BifrostError {
	return &schemas.BifrostError{
		IsBifrostError: false,
		Error: &schemas.ErrorField{
			Message: fmt.Sprintf("%s is not supported by %s provider", requestType, providerName),
			Code:    schemas.Ptr("unsupported_operation"),
		},
		ExtraFields: schemas.BifrostErrorExtraFields{
			Provider:    providerName,
			RequestType: requestType,
		},
	}
}

func CheckOperationAllowed(defaultProvider schemas.ModelProvider, config *schemas.CustomProviderConfig, operation schemas.RequestType) *schemas.BifrostError {

	if config == nil || config.AllowedRequests == nil {
		return nil
	}

	if config.IsOperationAllowed(operation) {
		return nil
	}

	resolved := GetProviderName(defaultProvider, config)
	return NewUnsupportedOperationError(operation, resolved)
}

func CheckAndDecodeBody(resp *fasthttp.Response) ([]byte, error) {
	body := resp.Body()
	if len(body) == 0 {
		return nil, nil
	}

	result := append([]byte(nil), body...)
	contentEncoding := strings.ToLower(strings.TrimSpace(string(resp.Header.Peek("Content-Encoding"))))
	if contentEncoding == "" || contentEncoding == "identity" {
		return result, nil
	}

	encodings := strings.Split(contentEncoding, ",")
	for i := len(encodings) - 1; i >= 0; i-- {
		encoding := strings.TrimSpace(encodings[i])
		reader := bytes.NewReader(result)

		switch encoding {
		case "", "identity":
			continue
		case "gzip", "x-gzip":
			gz, err := AcquireGzipReader(reader)
			if err != nil {
				ReleaseGzipReader(gz)
				return nil, fmt.Errorf("decode %s response body: %w", encoding, err)
			}
			result, err = io.ReadAll(gz)
			ReleaseGzipReader(gz)
			if err != nil {
				return nil, fmt.Errorf("decode %s response body: %w", encoding, err)
			}
		case "deflate":
			fr, err := AcquireFlateReader(reader)
			if err != nil {
				ReleaseFlateReader(fr)
				return nil, fmt.Errorf("decode %s response body: %w", encoding, err)
			}
			result, err = io.ReadAll(fr)
			ReleaseFlateReader(fr)
			if err != nil {
				return nil, fmt.Errorf("decode %s response body: %w", encoding, err)
			}
		case "br":
			br := AcquireBrotliReader(reader)
			var err error
			result, err = io.ReadAll(br)
			ReleaseBrotliReader(br)
			if err != nil {
				return nil, fmt.Errorf("decode %s response body: %w", encoding, err)
			}
		case "zstd":
			dec, err := AcquireZstdDecoder(reader)
			if err != nil {
				ReleaseZstdDecoder(dec)
				return nil, fmt.Errorf("decode %s response body: %w", encoding, err)
			}
			result, err = io.ReadAll(dec)
			ReleaseZstdDecoder(dec)
			if err != nil {
				return nil, fmt.Errorf("decode %s response body: %w", encoding, err)
			}
		default:
			return nil, fmt.Errorf("unsupported Content-Encoding %q", encoding)
		}
	}

	return result, nil
}

func IsHTMLResponse(resp *fasthttp.Response, body []byte) bool {

	if resp != nil {
		contentType := strings.ToLower(string(resp.Header.Peek("Content-Type")))
		if strings.Contains(contentType, "text/html") {
			return true
		}
	}

	if len(body) < 20 {
		return false
	}

	bodyLower := strings.ToLower(string(body))

	htmlIndicators := []string{
		"<!doctype html",
		"<html",
		"<head",
		"<body",
		"<title>",
		"<h1>",
		"<h2>",
		"<h3>",
		"<p>",
		"<div",
	}

	for _, indicator := range htmlIndicators {
		if strings.Contains(bodyLower, indicator) {
			return true
		}
	}

	return false
}

const maxBodySize = 32 * 1024

func ParseJSONL(data []byte, parseLine func(line []byte) error) JSONLParseResult {
	result := JSONLParseResult{}

	lineNum := 0
	start := 0

	for i := 0; i <= len(data); i++ {

		if i == len(data) || data[i] == '\n' {
			lineNum++

			end := i
			if end > start {
				line := data[start:end]

				if len(line) > 0 && line[len(line)-1] == '\r' {
					line = line[:len(line)-1]
				}

				if len(line) > 0 {
					if err := parseLine(line); err != nil {
						lineNumCopy := lineNum
						result.Errors = append(result.Errors, schemas.BatchError{
							Code:    "parse_error",
							Message: err.Error(),
							Line:    &lineNumCopy,
						})
					}
				}
			}

			start = i + 1
		}
	}

	return result
}

func NewConfigurationError(message string) *schemas.BifrostError {
	return &schemas.BifrostError{
		IsBifrostError: false,
		Error: &schemas.ErrorField{
			Message: message,
		},
	}
}

func NewBifrostOperationError(message string, err error) *schemas.BifrostError {
	return &schemas.BifrostError{
		IsBifrostError: true,
		Error: &schemas.ErrorField{
			Message: message,
			Error:   err,
		},
	}
}

func NewBifrostTimeoutError(message string, err error) *schemas.BifrostError {
	statusCode := 504
	errorType := schemas.RequestTimedOut
	return &schemas.BifrostError{
		IsBifrostError: true,
		StatusCode:     &statusCode,
		Error: &schemas.ErrorField{
			Message: message,
			Type:    &errorType,
			Error:   err,
		},
	}
}

func NewBifrostBadRequestError(message string) *schemas.BifrostError {
	statusCode := 400
	errorType := "invalid_request_error"
	return &schemas.BifrostError{
		IsBifrostError: true,
		StatusCode:     &statusCode,
		Error: &schemas.ErrorField{
			Message: message,
			Type:    &errorType,
		},
		ExtraFields: schemas.BifrostErrorExtraFields{
			ErrorType: schemas.ErrorTypeCallerInvalidRequest,
		},
	}
}

func (e *invalidRequestError) Error() string { return e.msg }

func InvalidRequestErrorf(format string, args ...any) error {
	return &invalidRequestError{msg: fmt.Sprintf(format, args...)}
}

func AsBifrostBadRequestError(err error) (*schemas.BifrostError, bool) {
	var invalid *invalidRequestError
	if errors.As(err, &invalid) {
		return NewBifrostBadRequestError(invalid.msg), true
	}
	return nil, false
}

func NewBifrostUpstreamConnectionError(message string, err error) *schemas.BifrostError {
	statusCode := 502
	errorType := schemas.ProviderConnectionFailed
	return &schemas.BifrostError{
		IsBifrostError: false,
		StatusCode:     &statusCode,
		Error: &schemas.ErrorField{
			Message: message,
			Type:    &errorType,
			Error:   err,
		},
	}
}

func NewProviderAPIError(message string, err error, statusCode int, errorType *string, eventID *string) *schemas.BifrostError {
	return &schemas.BifrostError{
		IsBifrostError: false,
		StatusCode:     &statusCode,
		Type:           errorType,
		EventID:        eventID,
		Error: &schemas.ErrorField{
			Message: message,
			Error:   err,
			Type:    errorType,
		},
	}
}

func ShouldSendBackRawRequest(ctx context.Context, defaultSendBackRawRequest bool) bool {
	if capture, ok := ctx.Value(schemas.BifrostContextKeyCaptureRawRequest).(bool); ok {
		return capture
	}
	return defaultSendBackRawRequest
}

func ShouldSendBackRawResponse(ctx context.Context, defaultSendBackRawResponse bool) bool {
	if capture, ok := ctx.Value(schemas.BifrostContextKeyCaptureRawResponse).(bool); ok {
		return capture
	}
	return defaultSendBackRawResponse
}

func BuildClientStreamChunk(ctx context.Context, processedResponse *schemas.BifrostResponse, processedError *schemas.BifrostError) *schemas.BifrostStreamChunk {
	dropReq, _ := ctx.Value(schemas.BifrostContextKeyDropRawRequestFromClient).(bool)
	dropResp, _ := ctx.Value(schemas.BifrostContextKeyDropRawResponseFromClient).(bool)
	drop := dropReq || dropResp
	streamResponse := &schemas.BifrostStreamChunk{}
	if processedResponse != nil {
		streamResponse.BifrostTextCompletionResponse = processedResponse.TextCompletionResponse
		streamResponse.BifrostChatResponse = processedResponse.ChatResponse
		streamResponse.BifrostResponsesStreamResponse = processedResponse.ResponsesStreamResponse
		streamResponse.BifrostSpeechStreamResponse = processedResponse.SpeechStreamResponse
		streamResponse.BifrostTranscriptionStreamResponse = processedResponse.TranscriptionStreamResponse
		streamResponse.BifrostImageGenerationStreamResponse = processedResponse.ImageGenerationStreamResponse
		streamResponse.BifrostPassthroughResponse = processedResponse.PassthroughResponse

		if drop {
			if streamResponse.BifrostTextCompletionResponse != nil {
				cp := *streamResponse.BifrostTextCompletionResponse
				if dropReq {
					cp.ExtraFields.RawRequest = nil
				}
				if dropResp {
					cp.ExtraFields.RawResponse = nil
				}
				streamResponse.BifrostTextCompletionResponse = &cp
			}
			if streamResponse.BifrostChatResponse != nil {
				cp := *streamResponse.BifrostChatResponse
				if dropReq {
					cp.ExtraFields.RawRequest = nil
				}
				if dropResp {
					cp.ExtraFields.RawResponse = nil
				}
				streamResponse.BifrostChatResponse = &cp
			}
			if streamResponse.BifrostResponsesStreamResponse != nil {
				cp := *streamResponse.BifrostResponsesStreamResponse
				if dropReq {
					cp.ExtraFields.RawRequest = nil
				}
				if dropResp {
					cp.ExtraFields.RawResponse = nil
				}
				streamResponse.BifrostResponsesStreamResponse = &cp
			}
			if streamResponse.BifrostSpeechStreamResponse != nil {
				cp := *streamResponse.BifrostSpeechStreamResponse
				if dropReq {
					cp.ExtraFields.RawRequest = nil
				}
				if dropResp {
					cp.ExtraFields.RawResponse = nil
				}
				streamResponse.BifrostSpeechStreamResponse = &cp
			}
			if streamResponse.BifrostTranscriptionStreamResponse != nil {
				cp := *streamResponse.BifrostTranscriptionStreamResponse
				if dropReq {
					cp.ExtraFields.RawRequest = nil
				}
				if dropResp {
					cp.ExtraFields.RawResponse = nil
				}
				streamResponse.BifrostTranscriptionStreamResponse = &cp
			}
			if streamResponse.BifrostImageGenerationStreamResponse != nil {
				cp := *streamResponse.BifrostImageGenerationStreamResponse
				if dropReq {
					cp.ExtraFields.RawRequest = nil
				}
				if dropResp {
					cp.ExtraFields.RawResponse = nil
				}
				streamResponse.BifrostImageGenerationStreamResponse = &cp
			}
			if streamResponse.BifrostPassthroughResponse != nil {
				cp := *streamResponse.BifrostPassthroughResponse
				if dropReq {
					cp.ExtraFields.RawRequest = nil
				}
				if dropResp {
					cp.ExtraFields.RawResponse = nil
				}
				streamResponse.BifrostPassthroughResponse = &cp
			}
		}
	}
	if processedError != nil {
		if drop {

			errCopy := *processedError
			if dropReq {
				errCopy.ExtraFields.RawRequest = nil
			}
			if dropResp {
				errCopy.ExtraFields.RawResponse = nil
			}
			streamResponse.BifrostError = &errCopy
		} else {
			streamResponse.BifrostError = processedError
		}
	}
	return streamResponse
}

func GateSendChunk(ctx *schemas.BifrostContext, chunk *schemas.BifrostStreamChunk, responseChan chan *schemas.BifrostStreamChunk) (ok bool) {
	if gated, _ := ctx.Value(schemas.BifrostContextKeyStreamGated).(bool); gated {
		isFinal := false
		if v := ctx.Value(schemas.BifrostContextKeyStreamEndIndicator); v != nil {
			if b, ok := v.(bool); ok {
				isFinal = b
			}
		}
		isHardErr := chunk != nil && chunk.BifrostError != nil && chunk.BifrostError.IsBifrostError
		if tracer, ok := ctx.Value(schemas.BifrostContextKeyTracer).(schemas.Tracer); ok && tracer != nil {
			if traceID, ok := ctx.Value(schemas.BifrostContextKeyTraceID).(string); ok && traceID != "" {
				return tracer.GateSend(traceID, chunk, isFinal, isHardErr, responseChan, ctx)
			}
		}
	}
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	select {
	case responseChan <- chunk:
		return true
	case <-ctx.Done():
		return false
	}
}

func CloseStream(ctx *schemas.BifrostContext, ch chan *schemas.BifrostStreamChunk) {
	if gated, _ := ctx.Value(schemas.BifrostContextKeyStreamGated).(bool); gated {
		if tracer, ok := ctx.Value(schemas.BifrostContextKeyTracer).(schemas.Tracer); ok && tracer != nil {
			if traceID, ok := ctx.Value(schemas.BifrostContextKeyTraceID).(string); ok && traceID != "" {

				tracer.EndStream(traceID, nil)
				tracer.WaitForFlusher(traceID)
			}
		}
	}
	completeUnfinishedStreamSpan(ctx)
	close(ch)
}

func completeUnfinishedStreamSpan(ctx *schemas.BifrostContext) {
	var streamErr *schemas.BifrostError
	if ended, _ := ctx.Value(schemas.BifrostContextKeyStreamEndIndicator).(bool); !ended {
		tracer, _ := ctx.Value(schemas.BifrostContextKeyTracer).(schemas.Tracer)
		traceID, _ := ctx.Value(schemas.BifrostContextKeyTraceID).(string)
		if tracer == nil || traceID == "" {
			return
		}
		if _, _, chunks := tracer.GetAccumulatedChunks(traceID); chunks == 0 {
			return
		}
		streamErr = &schemas.BifrostError{
			Error: &schemas.ErrorField{Message: "stream ended before completion"},
		}
	}
	completeDeferredSpan(ctx, nil, streamErr, nil)
}

func ProcessAndSendResponse(
	ctx *schemas.BifrostContext,
	postHookRunner schemas.PostHookRunner,
	response *schemas.BifrostResponse,
	responseChan chan *schemas.BifrostStreamChunk,
	postHookSpanFinalizer func(context.Context),
) {

	if tracer, ok := ctx.Value(schemas.BifrostContextKeyTracer).(schemas.Tracer); ok && tracer != nil {
		if traceID, ok := ctx.Value(schemas.BifrostContextKeyTraceID).(string); ok && traceID != "" {
			tracer.AddStreamingChunk(traceID, response)
		}
	}

	if isFinalChunk := ctx.Value(schemas.BifrostContextKeyStreamEndIndicator); isFinalChunk != nil {
		if final, ok := isFinalChunk.(bool); ok && final && response != nil {
			response.PopulateUpstreamLatency(ctx)
			if ef := response.GetExtraFields(); ef != nil && ef.Latency > 0 {
				response.PopulateOverheadLatency(ctx, time.Duration(ef.Latency)*time.Millisecond)
			}
		}
	}

	processedResponse, processedError := postHookRunner(ctx, response, nil)

	if HandleStreamControlSkip(processedError) {

		if isFinalChunk := ctx.Value(schemas.BifrostContextKeyStreamEndIndicator); isFinalChunk != nil {
			if final, ok := isFinalChunk.(bool); ok && final {
				completeDeferredSpan(ctx, processedResponse, processedError, postHookSpanFinalizer)
			}
		}
		return
	}

	streamResponse := BuildClientStreamChunk(ctx, processedResponse, processedError)

	sendStart := time.Now()
	GateSendChunk(ctx, streamResponse, responseChan)
	schemas.AddStreamBackpressure(ctx, time.Since(sendStart))

	if isFinalChunk := ctx.Value(schemas.BifrostContextKeyStreamEndIndicator); isFinalChunk != nil {
		if final, ok := isFinalChunk.(bool); ok && final {
			completeDeferredSpan(ctx, processedResponse, processedError, postHookSpanFinalizer)
		}
	}
}

func ProcessAndSendBifrostError(
	ctx *schemas.BifrostContext,
	postHookRunner schemas.PostHookRunner,
	bifrostErr *schemas.BifrostError,
	responseChan chan *schemas.BifrostStreamChunk,
	logger schemas.Logger,
	postHookSpanFinalizer func(context.Context),
) {

	processedResponse, processedError := postHookRunner(ctx, nil, bifrostErr)

	if HandleStreamControlSkip(processedError) {

		if isFinalChunk := ctx.Value(schemas.BifrostContextKeyStreamEndIndicator); isFinalChunk != nil {
			if final, ok := isFinalChunk.(bool); ok && final {
				completeDeferredSpan(ctx, processedResponse, processedError, postHookSpanFinalizer)
			}
		}
		return
	}

	streamResponse := BuildClientStreamChunk(ctx, processedResponse, processedError)

	GateSendChunk(ctx, streamResponse, responseChan)

	if isFinalChunk := ctx.Value(schemas.BifrostContextKeyStreamEndIndicator); isFinalChunk != nil {
		if final, ok := isFinalChunk.(bool); ok && final {
			completeDeferredSpan(ctx, processedResponse, processedError, postHookSpanFinalizer)
		}
	}
}

func EnsureStreamFinalizerCalled(ctx context.Context, finalizer func(context.Context)) {
	defer func() {
		if r := recover(); r != nil {
			getLogger().Debug("recovered panic in deferred stream finalizer: %v", r)
		}
	}()

	if finalizer == nil {
		return
	}
	finalizer(ctx)
}

func SetupStreamCancellation(ctx *schemas.BifrostContext, bodyStream io.Reader, logger schemas.Logger) (cleanup func()) {
	done := make(chan struct{})
	closed := make(chan struct{})

	go func() {
		defer close(closed)

		defer func() {
			if rec := recover(); rec != nil {
				if logger == nil {
					logger = getLogger()
				}
				logger.Debug("recovered panic in stream cancellation closeBodyStream: %v", rec)
			}
		}()
		abort := AttemptAbortFromContext(ctx)
		abortDone, abortStopped := abort.Done(), abort.Stopped()
		for {
			select {
			case <-abortDone:

				if prev, _ := ctx.GetAndSetValue(schemas.BifrostContextKeyConnectionClosed, true).(bool); prev {
					return
				}
				closeBodyStream(bodyStream, ErrStreamFirstTokenTimeout)
			case <-abortStopped:

				abortDone, abortStopped = nil, nil
				continue
			case <-ctx.Done():

				if prev, _ := ctx.GetAndSetValue(schemas.BifrostContextKeyConnectionClosed, true).(bool); prev {
					return
				}

				closeBodyStream(bodyStream, ctx.Err())
			case <-done:

				if ctx.Err() != nil {
					if prev, _ := ctx.GetAndSetValue(schemas.BifrostContextKeyConnectionClosed, true).(bool); prev {
						return
					}
					closeBodyStream(bodyStream, ctx.Err())
				}
			}
			return
		}
	}()
	return func() {
		close(done)
		<-closed
	}
}

const DefaultStreamIdleTimeout = 120 * time.Second

func SetStreamIdleTimeoutIfEmpty(ctx *schemas.BifrostContext, configSeconds int) {
	if existing, ok := ctx.Value(schemas.BifrostContextKeyStreamIdleTimeout).(time.Duration); ok && existing > 0 {
		return
	}
	if configSeconds > 0 {
		ctx.SetValue(schemas.BifrostContextKeyStreamIdleTimeout, time.Duration(configSeconds)*time.Second)
	}
}

func GetStreamIdleTimeout(ctx *schemas.BifrostContext) time.Duration {
	if timeout, ok := ctx.Value(schemas.BifrostContextKeyStreamIdleTimeout).(time.Duration); ok && timeout > 0 {
		return timeout
	}
	return DefaultStreamIdleTimeout
}

func closeBodyStream(bodyStream io.Reader, err error) {
	if closer, ok := bodyStream.(io.Closer); ok {
		closer.Close()
	} else if wce, ok := bodyStream.(streamCloserWithError); ok {
		wce.CloseWithError(err)
	}
}

func NewIdleTimeoutReader(reader io.Reader, bodyStream io.Reader, timeout time.Duration, ctx *schemas.BifrostContext) (io.Reader, func()) {
	if timeout <= 0 {
		timeout = DefaultStreamIdleTimeout
	}

	if tb, ok := reader.(*upstreamTimingBody); ok {
		reader = tb.inner
	}
	if tb, ok := bodyStream.(*upstreamTimingBody); ok {
		bodyStream = tb.inner
	}
	r := &idleTimeoutReader{
		ctx:        ctx,
		reader:     reader,
		bodyStream: bodyStream,
		timeout:    timeout,
		timerDone:  make(chan struct{}),
	}
	r.timer = time.AfterFunc(timeout, func() {
		defer r.timerDoneOnce.Do(func() { close(r.timerDone) })
		r.once.Do(func() {

			if ctx != nil {
				if prev, _ := ctx.GetAndSetValue(schemas.BifrostContextKeyConnectionClosed, true).(bool); prev {
					return
				}
			}
			r.fired.Store(true)

			defer func() {
				if rec := recover(); rec != nil {
					getLogger().Debug("recovered panic in idle-timeout timer closeBodyStream: %v", rec)
				}
			}()
			closeBodyStream(r.bodyStream, ErrStreamIdleTimeout)
		})
	})
	return r, r.cleanup
}

func (r *idleTimeoutReader) cleanup() {
	r.cleanupOnce.Do(func() {
		if r.timer.Stop() {
			r.timerDoneOnce.Do(func() { close(r.timerDone) })
			return
		}
		<-r.timerDone
	})
}

func (r *idleTimeoutReader) connectionClosed() bool {
	if r.ctx == nil {
		return false
	}
	closed, ok := r.ctx.Value(schemas.BifrostContextKeyConnectionClosed).(bool)
	return ok && closed
}

func (r *idleTimeoutReader) closedReadError() error {
	if r.fired.Load() {
		return ErrStreamIdleTimeout
	}
	return ErrStreamClosed
}

func (r *idleTimeoutReader) Read(p []byte) (n int, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if r.fired.Load() || r.connectionClosed() {
				n = 0
				err = r.closedReadError()
				return
			}
		}
	}()

	if r.connectionClosed() {
		return 0, r.closedReadError()
	}
	readStart := time.Now()
	n, err = r.reader.Read(p)
	if r.ctx != nil {
		schemas.AddUpstreamLatency(r.ctx, time.Since(readStart))
	}
	if n > 0 {
		r.timer.Reset(r.timeout)
	}
	if err != nil && err != io.EOF && r.fired.Load() {
		return n, ErrStreamIdleTimeout
	}
	return n, err
}

func HandleStreamCancellation(
	ctx *schemas.BifrostContext,
	postHookRunner schemas.PostHookRunner,
	responseChan chan *schemas.BifrostStreamChunk,
	logger schemas.Logger,
	postHookSpanFinalizer func(context.Context),
	jsonBody []byte,
) {

	if indicator := ctx.GetAndSetValue(schemas.BifrostContextKeyStreamEndIndicator, true); indicator != nil {
		if set, ok := indicator.(bool); ok && set {
			return
		}
	}

	cancelErr := &schemas.BifrostError{
		StatusCode: new(499),
		Error: &schemas.ErrorField{
			Message: "Request cancelled: client disconnected",
			Type:    schemas.Ptr(schemas.RequestCancelled),
		},
	}

	if ShouldSendBackRawRequest(ctx, false) && len(jsonBody) > 0 {
		cancelErr.ExtraFields.RawRequest = compactRawJSON(jsonBody)
	}

	attachBilledUsageFromContext(ctx, cancelErr)

	ProcessAndSendBifrostError(ctx, postHookRunner, cancelErr, responseChan, logger, postHookSpanFinalizer)
}

func attachBilledUsageFromContext(ctx *schemas.BifrostContext, bifrostErr *schemas.BifrostError) {
	if ctx == nil || bifrostErr == nil {
		return
	}
	usage, ok := ctx.Value(schemas.BifrostContextKeyStreamAccumulatedUsage).(*schemas.BifrostLLMUsage)
	if !ok || usage == nil {
		return
	}
	if usage.PromptTokens == 0 &&
		usage.CompletionTokens == 0 &&
		usage.TotalTokens == 0 &&
		usage.PromptTokensDetails == nil &&
		usage.CompletionTokensDetails == nil &&
		usage.Cost == nil {
		return
	}

	usageCopy := *usage
	if usage.PromptTokensDetails != nil {
		promptDetailsCopy := *usage.PromptTokensDetails
		if usage.PromptTokensDetails.CachedWriteTokenDetails != nil {
			cachedWriteDetailsCopy := *usage.PromptTokensDetails.CachedWriteTokenDetails
			promptDetailsCopy.CachedWriteTokenDetails = &cachedWriteDetailsCopy
		}
		usageCopy.PromptTokensDetails = &promptDetailsCopy
	}
	if usage.CompletionTokensDetails != nil {
		completionDetailsCopy := *usage.CompletionTokensDetails
		usageCopy.CompletionTokensDetails = &completionDetailsCopy
	}
	if usage.Cost != nil {
		costCopy := *usage.Cost
		usageCopy.Cost = &costCopy
	}
	usageCopy.Iterations = slices.Clone(usage.Iterations)
	bifrostErr.ExtraFields.BilledUsage = &usageCopy
}

func HandleStreamTimeout(
	ctx *schemas.BifrostContext,
	postHookRunner schemas.PostHookRunner,
	responseChan chan *schemas.BifrostStreamChunk,
	logger schemas.Logger,
	postHookSpanFinalizer func(context.Context),
	jsonBody []byte,
) {

	if indicator := ctx.GetAndSetValue(schemas.BifrostContextKeyStreamEndIndicator, true); indicator != nil {
		if set, ok := indicator.(bool); ok && set {
			return
		}
	}

	timeoutErr := &schemas.BifrostError{
		StatusCode: schemas.Ptr(504),
		Error: &schemas.ErrorField{
			Message: "Request timed out: deadline exceeded",
			Type:    schemas.Ptr(schemas.RequestTimedOut),
		},
	}

	if ShouldSendBackRawRequest(ctx, false) && len(jsonBody) > 0 {
		timeoutErr.ExtraFields.RawRequest = compactRawJSON(jsonBody)
	}

	attachBilledUsageFromContext(ctx, timeoutErr)

	ProcessAndSendBifrostError(ctx, postHookRunner, timeoutErr, responseChan, logger, postHookSpanFinalizer)
}

func ProcessAndSendNonSSEStreamError(
	ctx *schemas.BifrostContext,
	postHookRunner schemas.PostHookRunner,
	info *NonSSEStreamBody,
	responseChan chan *schemas.BifrostStreamChunk,
	logger schemas.Logger,
	postHookSpanFinalizer func(context.Context),
) {
	err := info.Err()

	bifrostError := &schemas.BifrostError{
		IsBifrostError: false,
		StatusCode:     schemas.Ptr(fasthttp.StatusBadGateway),
		Error: &schemas.ErrorField{
			Message: err.Error(),
			Error:   err,
		},
	}
	if len(info.Sample) > 0 && ShouldSendBackRawResponse(ctx, false) {
		bifrostError.ExtraFields.RawResponse = string(info.Sample)
	}
	attachBilledUsageFromContext(ctx, bifrostError)
	ProcessAndSendBifrostError(ctx, postHookRunner, bifrostError, responseChan, logger, postHookSpanFinalizer)
}

func ProcessAndSendError(
	ctx *schemas.BifrostContext,
	postHookRunner schemas.PostHookRunner,
	err error,
	responseChan chan *schemas.BifrostStreamChunk,
	logger schemas.Logger,
	postHookSpanFinalizer func(context.Context),
) {
	var carrier BifrostErrorCarrier
	if errors.As(err, &carrier) {
		if typed := carrier.BifrostError(); typed != nil {
			ProcessAndSendBifrostError(ctx, postHookRunner, typed, responseChan, logger, postHookSpanFinalizer)
			return
		}
	}

	bifrostError := &schemas.BifrostError{
		IsBifrostError: true,
		Error: &schemas.ErrorField{
			Message: fmt.Sprintf("Error reading stream: %v", err),
			Error:   err,
		},
	}
	processedResponse, processedError := postHookRunner(ctx, nil, bifrostError)

	if HandleStreamControlSkip(processedError) {
		return
	}

	streamResponse := &schemas.BifrostStreamChunk{}
	if processedResponse != nil {
		streamResponse.BifrostTextCompletionResponse = processedResponse.TextCompletionResponse
		streamResponse.BifrostChatResponse = processedResponse.ChatResponse
		streamResponse.BifrostResponsesStreamResponse = processedResponse.ResponsesStreamResponse
		streamResponse.BifrostSpeechStreamResponse = processedResponse.SpeechStreamResponse
		streamResponse.BifrostTranscriptionStreamResponse = processedResponse.TranscriptionStreamResponse
	}
	if processedError != nil {
		streamResponse.BifrostError = processedError
	}

	GateSendChunk(ctx, streamResponse, responseChan)
}

func SendStreamTruncatedError(
	ctx *schemas.BifrostContext,
	postHookRunner schemas.PostHookRunner,
	responseChan chan *schemas.BifrostStreamChunk,
	logger schemas.Logger,
	postHookSpanFinalizer func(context.Context),
	jsonBody []byte,
) {
	if logger != nil {
		logger.Warn("Stream ended without a terminal marker; treating as truncated upstream stream")
	}

	truncatedErr := NewBifrostUpstreamConnectionError(schemas.ErrProviderStreamTruncated, io.ErrUnexpectedEOF)

	if ShouldSendBackRawRequest(ctx, false) && len(jsonBody) > 0 {
		truncatedErr.ExtraFields.RawRequest = compactRawJSON(jsonBody)
	}

	attachBilledUsageFromContext(ctx, truncatedErr)

	ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
	ProcessAndSendBifrostError(ctx, postHookRunner, truncatedErr, responseChan, logger, postHookSpanFinalizer)
}

func CreateBifrostTextCompletionChunkResponse(
	id string,
	usage *schemas.BifrostLLMUsage,
	finishReason *string,
	currentChunkIndex int,
	requestType schemas.RequestType,
	model string,
	created int,
) *schemas.BifrostTextCompletionResponse {
	response := &schemas.BifrostTextCompletionResponse{
		ID:      id,
		Model:   model,
		Created: created,
		Object:  "text_completion",
		Usage:   usage,
		Choices: []schemas.BifrostResponseChoice{
			{
				FinishReason:                 finishReason,
				TextCompletionResponseChoice: &schemas.TextCompletionResponseChoice{},
			},
		},
		ExtraFields: schemas.BifrostResponseExtraFields{
			ChunkIndex: currentChunkIndex + 1,
		},
	}
	return response
}

func CreateBifrostChatCompletionChunkResponse(
	id string,
	usage *schemas.BifrostLLMUsage,
	finishReason *string,
	currentChunkIndex int,
	model string,
	created int,
) *schemas.BifrostChatResponse {
	response := &schemas.BifrostChatResponse{
		ID:      id,
		Model:   model,
		Created: created,
		Object:  "chat.completion.chunk",
		Usage:   usage,
		Choices: []schemas.BifrostResponseChoice{
			{
				FinishReason: finishReason,
				ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
					Delta: &schemas.ChatStreamResponseChoiceDelta{},
				},
			},
		},
		ExtraFields: schemas.BifrostResponseExtraFields{
			ChunkIndex: currentChunkIndex + 1,
		},
	}
	return response
}

func HandleStreamControlSkip(bifrostErr *schemas.BifrostError) bool {
	if !isStreamControlSkip(bifrostErr) {
		return false
	}
	if bifrostErr.StreamControl.LogError != nil && *bifrostErr.StreamControl.LogError {
		getLogger().Warn("Error in stream: " + bifrostErr.Error.Message)
	}
	return true
}

func isStreamControlSkip(bifrostErr *schemas.BifrostError) bool {
	return bifrostErr != nil && bifrostErr.StreamControl != nil &&
		bifrostErr.StreamControl.SkipStream != nil && *bifrostErr.StreamControl.SkipStream
}

func GetProviderName(defaultProvider schemas.ModelProvider, customConfig *schemas.CustomProviderConfig) schemas.ModelProvider {
	if customConfig != nil {
		if key := strings.TrimSpace(customConfig.CustomProviderKey); key != "" {
			return schemas.ModelProvider(key)
		}
	}
	return defaultProvider
}

func ProviderSendsDoneMarker(ctx *schemas.BifrostContext, providerName schemas.ModelProvider) bool {
	if ctx != nil {
		if doesNotSendDoneMarker, ok := ctx.Value(schemas.BifrostContextKeyDoesNotSendDoneMarker).(bool); ok && doesNotSendDoneMarker {
			return false
		}
	}
	switch providerName {
	case schemas.Cerebras, schemas.Perplexity:

		return false
	default:

		return true
	}
}

func WaitForStreamUsage(ctx *schemas.BifrostContext) bool {
	if ctx == nil {
		return false
	}
	waitForUsage, ok := ctx.Value(schemas.BifrostContextKeyWaitForUsage).(bool)
	return ok && waitForUsage
}

func ReleaseStreamingResponse(ctx *schemas.BifrostContext, resp *fasthttp.Response) {
	if resp == nil {
		return
	}
	bodyStream := resp.BodyStream()
	if bodyStream == nil {
		fasthttp.ReleaseResponse(resp)
		return
	}

	if prev, _ := ctx.GetAndSetValue(schemas.BifrostContextKeyConnectionClosed, true).(bool); prev {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			getLogger().Debug("stream already closed before drain in ReleaseStreamingResponse: %v\n", r)
		}
	}()

	if ctx.Err() != nil {
		closeBodyStream(bodyStream, ctx.Err())
		return
	}

	if exhausted, _ := ctx.Value(schemas.BifrostContextKeyStreamBodyExhausted).(bool); !exhausted {

		parked, _ := ctx.Value(schemas.BifrostContextKeyStreamParkedAfterFinish).(bool)
		doesNotSendDoneMarker, _ := ctx.Value(schemas.BifrostContextKeyDoesNotSendDoneMarker).(bool)
		if parked || doesNotSendDoneMarker {
			closeBodyStream(bodyStream, errStreamParkedAfterFinish)
			return
		}
		if _, err := io.Copy(io.Discard, bodyStream); err != nil {
			getLogger().Warn("failed to drain streaming response body before release (may cause stale connection reuse): %v", err)
		}
	}

	resp.CloseBodyStream()
	fasthttp.ReleaseResponse(resp)
}

func GetBifrostResponseForStreamResponse(
	textCompletionResponse *schemas.BifrostTextCompletionResponse,
	chatResponse *schemas.BifrostChatResponse,
	responsesStreamResponse *schemas.BifrostResponsesStreamResponse,
	speechStreamResponse *schemas.BifrostSpeechStreamResponse,
	transcriptionStreamResponse *schemas.BifrostTranscriptionStreamResponse,
	imageGenerationStreamResponse *schemas.BifrostImageGenerationStreamResponse,
) *schemas.BifrostResponse {

	bifrostResponse := &schemas.BifrostResponse{}

	switch {
	case textCompletionResponse != nil:
		bifrostResponse.TextCompletionResponse = textCompletionResponse
		return bifrostResponse
	case chatResponse != nil:
		bifrostResponse.ChatResponse = chatResponse
		return bifrostResponse
	case responsesStreamResponse != nil:
		bifrostResponse.ResponsesStreamResponse = responsesStreamResponse
		return bifrostResponse
	case speechStreamResponse != nil:
		bifrostResponse.SpeechStreamResponse = speechStreamResponse
		return bifrostResponse
	case transcriptionStreamResponse != nil:
		bifrostResponse.TranscriptionStreamResponse = transcriptionStreamResponse
		return bifrostResponse
	case imageGenerationStreamResponse != nil:
		bifrostResponse.ImageGenerationStreamResponse = imageGenerationStreamResponse
		return bifrostResponse
	}
	return nil
}

func aggregateListModelsResponses(responses []*schemas.BifrostListModelsResponse) *schemas.BifrostListModelsResponse {
	if len(responses) == 0 {
		return &schemas.BifrostListModelsResponse{
			Data: []schemas.Model{},
		}
	}

	seenIDs := make(map[string]struct{})
	aggregated := &schemas.BifrostListModelsResponse{
		Data: make([]schemas.Model, 0),
	}

	var rawResponses []interface{}

	for _, response := range responses {
		if response == nil {
			continue
		}

		for _, model := range response.Data {
			if _, exists := seenIDs[model.ID]; !exists {
				seenIDs[model.ID] = struct{}{}
				aggregated.Data = append(aggregated.Data, model)
			}
		}

		if response.ExtraFields.RawResponse != nil {
			rawResponses = append(rawResponses, response.ExtraFields.RawResponse)
		}
	}

	sort.Slice(aggregated.Data, func(i, j int) bool {
		return aggregated.Data[i].ID < aggregated.Data[j].ID
	})

	if len(rawResponses) > 0 {
		aggregated.ExtraFields.RawResponse = rawResponses
	}

	return aggregated
}

func extractSuccessfulListModelsResponses(results chan schemas.ListModelsByKeyResult, provider schemas.ModelProvider) ([]*schemas.BifrostListModelsResponse, []schemas.KeyStatus, *schemas.BifrostError) {
	var successfulResponses []*schemas.BifrostListModelsResponse
	var keyStatuses []schemas.KeyStatus
	var lastError *schemas.BifrostError

	for result := range results {
		if result.Err != nil {
			errMsg := "unknown error"
			if errorField := result.Err.Error; errorField != nil {
				if errorField.Message != "" {
					errMsg = errorField.Message
				} else if errorField.Error != nil {
					errMsg = errorField.Error.Error()
				}
			}
			getLogger().Warn(fmt.Sprintf("failed to list models with key %s: %s", result.KeyID, errMsg))
			keyStatuses = append(keyStatuses, schemas.KeyStatus{
				KeyID:    result.KeyID,
				Provider: provider,
				Status:   schemas.KeyStatusListModelsFailed,
				Error:    result.Err,
			})
			lastError = result.Err
			continue
		}

		keyStatuses = append(keyStatuses, schemas.KeyStatus{
			KeyID:    result.KeyID,
			Provider: provider,
			Status:   schemas.KeyStatusSuccess,
		})
		successfulResponses = append(successfulResponses, result.Response)
	}

	if len(successfulResponses) == 0 {
		if lastError != nil {
			return nil, keyStatuses, lastError
		}
		return nil, keyStatuses, &schemas.BifrostError{
			IsBifrostError: false,
			Error: &schemas.ErrorField{
				Message: "all keys failed to list models",
			},
		}
	}

	return successfulResponses, keyStatuses, nil
}

func HandleKeylessListModelsRequest(
	provider schemas.ModelProvider,
	listFunc func() (*schemas.BifrostListModelsResponse, *schemas.BifrostError),
) (*schemas.BifrostListModelsResponse, *schemas.BifrostError) {
	resp, bifrostErr := listFunc()

	keyStatus := schemas.KeyStatus{
		KeyID:    "",
		Provider: provider,
	}

	if bifrostErr != nil {
		keyStatus.Status = schemas.KeyStatusListModelsFailed
		keyStatus.Error = bifrostErr
		bifrostErr.ExtraFields.KeyStatuses = []schemas.KeyStatus{keyStatus}
		return nil, bifrostErr
	}

	if resp != nil {
		keyStatus.Status = schemas.KeyStatusSuccess
		resp.KeyStatuses = []schemas.KeyStatus{keyStatus}
		return resp, nil
	}

	return resp, bifrostErr
}

func HandleMultipleListModelsRequests(
	ctx *schemas.BifrostContext,
	keys []schemas.Key,
	request *schemas.BifrostListModelsRequest,
	listModelsByKey func(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostListModelsRequest) (*schemas.BifrostListModelsResponse, *schemas.BifrostError),
) (*schemas.BifrostListModelsResponse, *schemas.BifrostError) {
	startTime := time.Now()

	results := make(chan schemas.ListModelsByKeyResult, len(keys))
	var wg sync.WaitGroup

	for _, key := range keys {
		wg.Add(1)
		go func(k schemas.Key) {
			defer wg.Done()

			defer func() {
				if r := recover(); r != nil {
					getLogger().Error("panic in listModelsByKey for key %s (%s): %v", k.Name, k.ID, r)
					results <- schemas.ListModelsByKeyResult{
						Err: &schemas.BifrostError{
							IsBifrostError: true,
							Error: &schemas.ErrorField{
								Message: "internal error while listing models for key",
							},
						},
						KeyID: k.ID,
					}
				}
			}()
			resp, bifrostErr := listModelsByKey(ctx, k, request)
			results <- schemas.ListModelsByKeyResult{Response: resp, Err: bifrostErr, KeyID: k.ID}
		}(key)
	}

	wg.Wait()
	close(results)

	successfulResponses, keyStatuses, err := extractSuccessfulListModelsResponses(results, request.Provider)
	if err != nil {

		err.ExtraFields.KeyStatuses = keyStatuses
		return nil, err
	}

	response := aggregateListModelsResponses(successfulResponses)
	response = response.ApplyPagination(request.PageSize, request.PageToken)

	response.KeyStatuses = keyStatuses

	latency := time.Since(startTime)
	response.ExtraFields.Latency = latency.Milliseconds()

	return response, nil
}

func GetReasoningEffortFromBudgetTokens(
	budgetTokens int,
	minBudgetTokens int,
	maxTokens int,
) string {
	if budgetTokens <= 0 {
		return "none"
	}

	if maxTokens <= 0 {
		return "medium"
	}

	if budgetTokens < minBudgetTokens {
		budgetTokens = minBudgetTokens
	}
	if budgetTokens > maxTokens {
		budgetTokens = maxTokens
	}

	if maxTokens <= minBudgetTokens {
		return "high"
	}

	ratio := float64(budgetTokens-minBudgetTokens) / float64(maxTokens-minBudgetTokens)

	switch {
	case ratio <= 0.25:
		return "low"
	case ratio <= 0.60:
		return "medium"
	default:
		return "high"
	}
}

func GetBudgetTokensFromReasoningEffort(
	effort string,
	minBudgetTokens int,
	maxTokens int,
) (int, error) {
	if effort == "none" {
		return 0, nil
	}

	if minBudgetTokens > maxTokens {
		return 0, fmt.Errorf("max_tokens must be greater than %d for reasoning", minBudgetTokens)
	}

	if maxTokens <= minBudgetTokens {
		return minBudgetTokens, nil
	}

	var ratio float64

	switch effort {
	case "minimal":
		ratio = 0.025
	case "low":
		ratio = 0.15
	case "medium":
		ratio = 0.425
	case "high":
		ratio = 0.80
	case "xhigh":
		ratio = 0.92
	case "max":
		ratio = 1.0
	default:

		ratio = 0.425
	}

	budget := minBudgetTokens + int(ratio*float64(maxTokens-minBudgetTokens))

	if budget >= maxTokens {
		budget = maxTokens - 1
	}

	return budget, nil
}

func completeDeferredSpan(ctx *schemas.BifrostContext, result *schemas.BifrostResponse, err *schemas.BifrostError, postHookSpanFinalizer func(context.Context)) {
	if ctx == nil {
		return
	}

	if pending, _ := ctx.Value(schemas.BifrostContextKeyStreamTurnPending).(bool); pending {
		return
	}

	traceID, ok := ctx.Value(schemas.BifrostContextKeyTraceID).(string)
	if !ok || traceID == "" {
		return
	}

	tracerVal := ctx.Value(schemas.BifrostContextKeyTracer)
	if tracerVal == nil {
		return
	}
	tracer, ok := tracerVal.(schemas.Tracer)
	if !ok || tracer == nil {
		return
	}

	ctx.StampUpstreamLatency()
	ctx.StampStreamOverhead()

	handle := tracer.GetDeferredSpanHandle(traceID)
	if handle == nil {
		return
	}

	if result != nil {
		extraFields := result.GetExtraFields()
		if extraFields.Latency > 0 {
			tracer.SetAttribute(handle, "gen_ai.response.total_latency_ms", extraFields.Latency)
		}
	}

	accumulatedResp, ttftNs, chunkCount := tracer.GetAccumulatedChunks(traceID)

	if ttftNs > 0 {
		tracer.SetAttribute(handle, schemas.AttrTimeToFirstChunk, float64(ttftNs)/1e9)
	}
	if chunkCount > 0 {
		tracer.SetAttribute(handle, schemas.AttrTotalChunks, chunkCount)
	}

	if accumulatedResp != nil {

		tracer.PopulateLLMResponseAttributes(ctx, handle, accumulatedResp, err)
	} else if result != nil {

		tracer.PopulateLLMResponseAttributes(ctx, handle, result, err)
	} else if err != nil {

		tracer.PopulateLLMResponseAttributes(ctx, handle, nil, err)
	}

	if postHookSpanFinalizer != nil {
		postHookSpanFinalizer(ctx)
	}

	if err != nil {
		tracer.EndSpan(handle, schemas.SpanStatusError, "streaming request failed")
	} else {
		tracer.EndSpan(handle, schemas.SpanStatusOk, "")
	}

	tracer.ClearDeferredSpan(traceID)
}

const jsonSchemaPatternKey = "pattern"

func NormalizeRegexNULEscape(pattern string) string {
	if !strings.Contains(pattern, `\0`) {
		return pattern
	}
	var b strings.Builder
	b.Grow(len(pattern) + 8)
	for i := 0; i < len(pattern); {
		if pattern[i] != '\\' || i+1 >= len(pattern) {
			b.WriteByte(pattern[i])
			i++
			continue
		}

		if pattern[i+1] == '0' && (i+2 >= len(pattern) || pattern[i+2] < '0' || pattern[i+2] > '7') {
			b.WriteString(`\x00`)
		} else {
			b.WriteByte(pattern[i])
			b.WriteByte(pattern[i+1])
		}
		i += 2
	}
	return b.String()
}

const jsonSchemaFormatKey = "format"

func (p ToolSchemaPolicy) IsZero() bool {
	return !p.NormalizeNULEscape && !p.StripLookaround && !p.DropPattern && p.StringFormats == nil
}

func (p ToolSchemaPolicy) rewritePattern(pattern string) string {
	if p.NormalizeNULEscape {
		pattern = NormalizeRegexNULEscape(pattern)
	}
	if p.StripLookaround {
		pattern = StripRegexLookaround(pattern)
	}
	return pattern
}

func (p ToolSchemaPolicy) dropsFormat(format string, typ any) bool {
	return p.StringFormats != nil && !slices.Contains(p.StringFormats, format) && !isNumericSchemaType(typ)
}

func isNumericSchemaType(typ any) bool {
	switch t := typ.(type) {
	case string:
		return t == "integer" || t == "number"
	case []any:
		numeric := false
		for _, v := range t {
			switch v {
			case "null":
			case "integer", "number":
				numeric = true
			default:
				return false
			}
		}
		return numeric
	}
	return false
}

func normalizeSchemaEntry(key string, value, typ any, p ToolSchemaPolicy, names bool) (normalized any, changed, remove bool) {
	if !names {
		switch key {
		case jsonSchemaPatternKey:
			if pattern, ok := value.(string); ok {
				if p.DropPattern {
					return value, false, true
				}
				if rewritten := p.rewritePattern(pattern); rewritten != pattern {
					return rewritten, true, false
				}
				return value, false, false
			}
		case jsonSchemaFormatKey:
			if format, ok := value.(string); ok {
				return value, false, p.dropsFormat(format, typ)
			}
		case "enum", "const", "default", "examples":

			return value, false, false
		case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas":
			normalized, changed = normalizeSchemaValue(value, p, true)
			return normalized, changed, false
		}
	}
	normalized, changed = normalizeSchemaValue(value, p, false)
	return normalized, changed, false
}

func normalizeSchemaValue(value any, p ToolSchemaPolicy, names bool) (any, bool) {
	switch typed := value.(type) {
	case *schemas.OrderedMap:
		return normalizeSchemaMap(typed, p, names)
	case map[string]any:

		var typ any
		if !names {
			typ = typed["type"]
		}
		var updated map[string]any
		for key, nested := range typed {
			normalized, changed, remove := normalizeSchemaEntry(key, nested, typ, p, names)
			if !changed && !remove {
				continue
			}
			if updated == nil {
				updated = make(map[string]any, len(typed))
				for k, v := range typed {
					updated[k] = v
				}
			}
			if remove {
				delete(updated, key)
			} else {
				updated[key] = normalized
			}
		}
		if updated == nil {
			return value, false
		}
		return updated, true
	case []any:
		var updated []any
		for i := range typed {
			normalized, changed := normalizeSchemaValue(typed[i], p, false)
			if !changed {
				continue
			}
			if updated == nil {
				updated = make([]any, len(typed))
				copy(updated, typed)
			}
			updated[i] = normalized
		}
		if updated == nil {
			return value, false
		}
		return updated, true
	}
	return value, false
}

func normalizeSchemaMap(schema *schemas.OrderedMap, p ToolSchemaPolicy, names bool) (*schemas.OrderedMap, bool) {
	if schema == nil || schema.Len() == 0 {
		return schema, false
	}
	var typ any
	if !names {
		typ, _ = schema.Get("type")
	}

	var updated *schemas.OrderedMap
	schema.Range(func(key string, value any) bool {
		normalized, changed, remove := normalizeSchemaEntry(key, value, typ, p, names)
		if !changed && !remove {
			return true
		}
		if updated == nil {
			updated = schema.Clone()
		}
		if remove {
			updated.Delete(key)
		} else {
			updated.Set(key, normalized)
		}
		return true
	})
	if updated == nil {
		return schema, false
	}
	return updated, true
}

func ApplyToolSchemaPolicy(params *schemas.ToolFunctionParameters, p ToolSchemaPolicy) (*schemas.ToolFunctionParameters, bool) {
	if params == nil {
		return params, false
	}

	var updated *schemas.ToolFunctionParameters
	edit := func() *schemas.ToolFunctionParameters {
		if updated == nil {
			cp := *params
			updated = &cp
		}
		return updated
	}

	if params.Pattern != nil {
		if p.DropPattern {
			edit().Pattern = nil
		} else if rewritten := p.rewritePattern(*params.Pattern); rewritten != *params.Pattern {
			edit().Pattern = &rewritten
		}
	}
	if params.Format != nil && p.dropsFormat(*params.Format, params.Type) {
		edit().Format = nil
	}

	for _, field := range []struct {
		value  *schemas.OrderedMap
		names  bool
		assign func(*schemas.ToolFunctionParameters, *schemas.OrderedMap)
	}{
		{params.Properties, true, func(d *schemas.ToolFunctionParameters, m *schemas.OrderedMap) { d.Properties = m }},
		{params.Items, false, func(d *schemas.ToolFunctionParameters, m *schemas.OrderedMap) { d.Items = m }},
		{params.Defs, true, func(d *schemas.ToolFunctionParameters, m *schemas.OrderedMap) { d.Defs = m }},
		{params.Definitions, true, func(d *schemas.ToolFunctionParameters, m *schemas.OrderedMap) { d.Definitions = m }},
	} {
		if normalized, fieldChanged := normalizeSchemaMap(field.value, p, field.names); fieldChanged {
			field.assign(edit(), normalized)
		}
	}

	if additional := params.AdditionalProperties; additional != nil && additional.AdditionalPropertiesMap != nil {
		if normalized, fieldChanged := normalizeSchemaMap(additional.AdditionalPropertiesMap, p, false); fieldChanged {
			additionalCopy := *additional
			additionalCopy.AdditionalPropertiesMap = normalized
			edit().AdditionalProperties = &additionalCopy
		}
	}

	for _, composition := range []struct {
		value  []schemas.OrderedMap
		assign func(*schemas.ToolFunctionParameters, []schemas.OrderedMap)
	}{
		{params.AnyOf, func(d *schemas.ToolFunctionParameters, s []schemas.OrderedMap) { d.AnyOf = s }},
		{params.OneOf, func(d *schemas.ToolFunctionParameters, s []schemas.OrderedMap) { d.OneOf = s }},
		{params.AllOf, func(d *schemas.ToolFunctionParameters, s []schemas.OrderedMap) { d.AllOf = s }},
	} {
		var rewritten []schemas.OrderedMap
		for i := range composition.value {
			normalized, elementChanged := normalizeSchemaMap(&composition.value[i], p, false)
			if !elementChanged {
				continue
			}
			if rewritten == nil {
				rewritten = make([]schemas.OrderedMap, len(composition.value))
				copy(rewritten, composition.value)
			}
			rewritten[i] = *normalized
		}
		if rewritten != nil {
			composition.assign(edit(), rewritten)
		}
	}

	if updated == nil {
		return params, false
	}
	return updated, true
}

func ApplyResponsesToolSchemaPolicy(tools []schemas.ResponsesTool, p ToolSchemaPolicy) ([]schemas.ResponsesTool, bool) {
	var updated []schemas.ResponsesTool
	for i := range tools {
		function, namespace := tools[i].ResponsesToolFunction, tools[i].ResponsesToolNamespace
		changed := false
		if function != nil && function.Parameters != nil {
			if normalized, ok := ApplyToolSchemaPolicy(function.Parameters, p); ok {
				cp := *function
				cp.Parameters = normalized
				function, changed = &cp, true
			}
		}
		if namespace != nil {
			if members, ok := ApplyResponsesToolSchemaPolicy(namespace.Tools, p); ok {
				cp := *namespace
				cp.Tools = members
				namespace, changed = &cp, true
			}
		}
		if !changed {
			continue
		}
		if updated == nil {
			updated = make([]schemas.ResponsesTool, len(tools))
			copy(updated, tools)
		}
		updated[i].ResponsesToolFunction, updated[i].ResponsesToolNamespace = function, namespace
	}
	if updated == nil {
		return tools, false
	}
	return updated, true
}

func ApplyChatToolSchemaPolicy(tools []schemas.ChatTool, p ToolSchemaPolicy) ([]schemas.ChatTool, bool) {
	var updated []schemas.ChatTool
	for i := range tools {
		if tools[i].Function == nil || tools[i].Function.Parameters == nil {
			continue
		}
		normalized, changed := ApplyToolSchemaPolicy(tools[i].Function.Parameters, p)
		if !changed {
			continue
		}
		if updated == nil {
			updated = make([]schemas.ChatTool, len(tools))
			copy(updated, tools)
		}
		function := *tools[i].Function
		function.Parameters = normalized
		updated[i].Function = &function
	}
	if updated == nil {
		return tools, false
	}
	return updated, true
}

func StripRegexLookaround(pattern string) string {
	if !strings.Contains(pattern, "(?") {
		return pattern
	}
	var b strings.Builder
	b.Grow(len(pattern))
	changed := false
	for i := 0; i < len(pattern); {
		c := pattern[i]
		switch {
		case c == '\\' && i+1 < len(pattern):
			b.WriteByte(c)
			b.WriteByte(pattern[i+1])
			i += 2
		case c == '[':
			end := regexClassEnd(pattern, i)
			b.WriteString(pattern[i:end])
			i = end
		case c == '(' && isRegexLookaroundStart(pattern, i):
			end := regexGroupEnd(pattern, i)
			if end < 0 {
				return pattern
			}
			changed = true

			i = regexQuantifierEnd(pattern, end)
		default:
			b.WriteByte(c)
			i++
		}
	}
	if !changed {
		return pattern
	}
	return b.String()
}

func regexQuantifierEnd(pattern string, start int) int {
	if start >= len(pattern) {
		return start
	}
	i := start
	switch pattern[i] {
	case '*', '+', '?':
		i++
	case '{':
		j := i + 1
		digits := func() bool {
			n := j
			for j < len(pattern) && pattern[j] >= '0' && pattern[j] <= '9' {
				j++
			}
			return j > n
		}
		if !digits() {
			return start
		}
		if j < len(pattern) && pattern[j] == ',' {
			j++
			digits()
		}
		if j >= len(pattern) || pattern[j] != '}' {
			return start
		}
		i = j + 1
	default:
		return start
	}
	if i < len(pattern) && pattern[i] == '?' {
		i++
	}
	return i
}

func isRegexLookaroundStart(pattern string, i int) bool {
	rest := pattern[i:]
	return strings.HasPrefix(rest, "(?=") || strings.HasPrefix(rest, "(?!") ||
		strings.HasPrefix(rest, "(?<=") || strings.HasPrefix(rest, "(?<!")
}

func regexClassEnd(pattern string, start int) int {
	j := start + 1
	if j < len(pattern) && pattern[j] == '^' {
		j++
	}
	if j < len(pattern) && pattern[j] == ']' {
		j++
	}
	for j < len(pattern) {
		switch pattern[j] {
		case '\\':
			j += 2
		case ']':
			return j + 1
		default:
			j++
		}
	}
	return len(pattern)
}

func regexGroupEnd(pattern string, start int) int {
	depth := 0
	for j := start; j < len(pattern); {
		switch pattern[j] {
		case '\\':
			j += 2
		case '[':
			j = regexClassEnd(pattern, j)
		case '(':
			depth++
			j++
		case ')':
			depth--
			j++
			if depth == 0 {
				return j
			}
		default:
			j++
		}
	}
	return -1
}
