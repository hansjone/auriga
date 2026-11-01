package utils

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/network"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
	"golang.org/x/net/http/httpproxy"
)

var fetchResolveTarget = network.ResolvePublicTarget
var proxyEnvVars = []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy", "REQUEST_METHOD"}

type fetchClientKey struct {
	proxyType schemas.ProxyType
	url       string
	username  string
	password  string
	caCertPEM string
	noProxy   string

	skipTLSVerify bool

	env string
}

var fetchClients sync.Map

func RedactURLForError(resourceURL string) string {
	parsed, err := url.Parse(resourceURL)
	if err != nil || parsed.Host == "" {

		return "[redacted url]"
	}
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: parsed.Path}).String()
}

func sanitizeFetchError(err error, redacted string) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return &url.Error{Op: urlErr.Op, URL: redacted, Err: urlErr.Err}
	}
	return err
}

func FetchAndEncodeURL(ctx context.Context, resourceURL string) (mediaType string, encoded string, err error) {
	const maxBytes = maxFetchBytes

	redacted := RedactURLForError(resourceURL)

	parsed, err := url.Parse(resourceURL)
	if err != nil {
		return "", "", fmt.Errorf("invalid resource URL %q: %w", redacted, sanitizeFetchError(err, redacted))
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", fmt.Errorf("unsupported URL scheme %q (only http/https allowed)", parsed.Scheme)
	}

	client, err := fetchClientFor(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to fetch from %q: %w", redacted, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("invalid resource URL %q: %w", redacted, sanitizeFetchError(err, redacted))
	}
	req.Header.Set("User-Agent", "bifrost-fetch/1")

	resp, err := DoHTTPRequest(client, req)
	if err != nil {
		if errors.Is(err, fasthttp.ErrBodyTooLarge) {
			return "", "", fmt.Errorf("resource at %q exceeds %d-byte limit", redacted, maxBytes)
		}
		return "", "", fmt.Errorf("failed to fetch from %q: %w", redacted, sanitizeFetchError(err, redacted))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("fetch %q returned non-2xx status %d", redacted, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return "", "", fmt.Errorf("failed to read body from %q: %w", redacted, sanitizeFetchError(err, redacted))
	}
	if int64(len(body)) > maxBytes {
		return "", "", fmt.Errorf("resource at %q exceeds %d-byte limit", redacted, maxBytes)
	}

	mediaType = resp.Header.Get("Content-Type")
	if i := strings.Index(mediaType, ";"); i != -1 {
		mediaType = strings.TrimSpace(mediaType[:i])
	}

	return mediaType, base64.StdEncoding.EncodeToString(body), nil
}

const maxFetchBytes int64 = 25 * 1024 * 1024

func fetchClientFor(ctx context.Context) (*http.Client, error) {
	proxyConfig, _ := ctx.Value(schemas.BifrostContextKeyProviderProxyConfig).(*schemas.ProxyConfig)
	key := fetchClientKeyFor(proxyConfig)
	if cached, ok := fetchClients.Load(key); ok {
		return cached.(*http.Client), nil
	}
	actual, _ := fetchClients.LoadOrStore(key, newFetchClient(proxyConfig))
	return actual.(*http.Client), nil
}

func proxyBypassFunc(proxyConfig *schemas.ProxyConfig) func(host, port string) bool {
	noProxy := proxyConfig.NoProxy
	var envProxy func(*url.URL) (*url.URL, error)
	if proxyConfig.Type == schemas.EnvProxy {
		envProxy = httpproxy.FromEnvironment().ProxyFunc()
	}
	return func(host, port string) bool {
		if noProxy != "" && network.MatchesNoProxy(host, noProxy) {
			return true
		}
		if envProxy == nil {
			return false
		}
		scheme := "http"
		if port == "443" {
			scheme = "https"
		}
		proxyURL, err := envProxy(&url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port)})
		return err == nil && proxyURL == nil
	}
}

func newFetchClient(proxyConfig *schemas.ProxyConfig) *http.Client {
	ssrfDial := network.SSRFSafeDialContext(10 * time.Second)
	direct := func(addr string) (net.Conn, error) {
		return ssrfDial(context.Background(), "tcp", addr)
	}
	client := &fasthttp.Client{
		ReadTimeout:              20 * time.Second,
		WriteTimeout:             20 * time.Second,
		MaxIdleConnDuration:      30 * time.Second,
		MaxResponseBodySize:      int(maxFetchBytes) + 1,
		NoDefaultUserAgentHeader: true,
	}
	proxied := false
	if namesProxy(proxyConfig) {
		client = ConfigureProxy(client, proxyConfig, getLogger())
		if proxyDial := client.Dial; proxyDial != nil {
			proxied = true
			bypass := proxyBypassFunc(proxyConfig)
			client.Dial = func(addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				if bypass(host, port) {
					return direct(addr)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				ips, err := fetchResolveTarget(ctx, host)
				if err != nil {
					return nil, err
				}
				var lastErr error
				for _, ip := range ips {
					pinned := net.JoinHostPort(ip.String(), port)
					conn, err := proxyDial(pinned)
					if errors.Is(err, errBypassProxy) {

						conn, err = direct(pinned)
					}
					if err == nil {
						return conn, nil
					}
					lastErr = err
				}
				return nil, lastErr
			}
		}
	}
	if !proxied {
		client.Dial = direct
	}
	client.Transport = NewContextTransport()

	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: &fasthttpRoundTripper{client: client},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("blocked redirect to unsupported scheme %q", req.URL.Scheme)
			}
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			return nil
		},
	}
}

func fetchClientKeyFor(proxyConfig *schemas.ProxyConfig) fetchClientKey {
	if proxyConfig == nil || proxyConfig.Type == "" || proxyConfig.Type == schemas.NoProxy {
		return fetchClientKey{}
	}
	key := fetchClientKey{
		proxyType:     proxyConfig.Type,
		url:           proxyConfig.URL.GetValue(),
		username:      proxyConfig.Username.GetValue(),
		password:      proxyConfig.Password.GetValue(),
		caCertPEM:     proxyConfig.CACertPEM.GetValue(),
		noProxy:       proxyConfig.NoProxy,
		skipTLSVerify: proxyConfig.SkipTLSVerify,
	}
	if proxyConfig.Type == schemas.EnvProxy {
		var env strings.Builder
		for _, name := range proxyEnvVars {
			env.WriteString(os.Getenv(name))
			env.WriteByte(0)
		}
		key.env = env.String()
	}
	return key
}
