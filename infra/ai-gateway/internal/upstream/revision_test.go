package upstream

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
)

// These addresses are only used behind injected dialers, never on the network.
func fixturePublicIP() netip.Addr { return netip.AddrFrom4([4]byte{11, 0, 0, 1}) }

func TestRevisionStoppedBeforeSubmission(t *testing.T) {
	for _, mode := range []string{"ssrf", "dns", "dial"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := New(Config{MaxResponseBytes: 4096, MaxEventBytes: 2048})
			c.lookup = func(context.Context, string) ([]netip.Addr, error) {
				if mode == "dns" {
					return nil, errors.New("resolution failed")
				}
				if mode == "ssrf" {
					return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
				}
				return []netip.Addr{fixturePublicIP()}, nil
			}
			c.dial = func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("connection failed") }
			for range 4 {
				x, err := c.Transport(t.Context(), submission("https://models.example.com", false), []byte(rand.Text()))
				if err == nil {
					t.Fatal("failure was accepted")
				}
				select {
				case <-x.Stopped:
				default:
					t.Fatal("never-submitted request leaked its stop proof")
				}
			}
		})
	}
}

func TestRevisionHTTPCompletionProof(t *testing.T) {
	for _, status := range []int{401, 404, 429, 500, 504} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			s := privateServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "rejected")
			}))
			defer s.Close()
			for range 4 {
				x, err := clientFor(t).Transport(t.Context(), submission(s.URL, false), []byte(rand.Text()))
				_ = readExchange(t, x, err)
				select {
				case <-x.Stopped:
				default:
					t.Fatal("fully drained HTTP error did not release stop proof")
				}
			}
		})
	}
	s := privateServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, strings.Repeat("x", 4097))
	}))
	defer s.Close()
	x, err := clientFor(t).Transport(t.Context(), submission(s.URL, false), []byte(rand.Text()))
	_ = readExchange(t, x, err)
	select {
	case <-x.Stopped:
		t.Fatal("truncated error body forged stop proof")
	default:
	}
}

type reportedRemote struct {
	net.Conn
	remote net.Addr
}

func (c *reportedRemote) RemoteAddr() net.Addr { return c.remote }

func TestRevisionActualPeerAndAddressFallback(t *testing.T) {
	for _, wrongPeer := range []bool{false, true} {
		t.Run(strconv.FormatBool(wrongPeer), func(t *testing.T) {
			s := privateServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, completion()) }))
			defer s.Close()
			c := clientFor(t)
			host := "models.example.com"
			c.allowed[host] = HostRule{Host: host, AllowPlaintext: true}
			ip, _ := netip.AddrFromSlice(privateIPForTest(t))
			ip = ip.Unmap()
			first := fixturePublicIP()
			c.lookup = func(context.Context, string) ([]netip.Addr, error) { return []netip.Addr{first, ip}, nil }
			var attempts atomic.Int64
			c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
				if attempts.Add(1) == 1 {
					return nil, errors.New("first address unavailable")
				}
				if !strings.HasPrefix(address, ip.String()+":") {
					t.Error("dial was not pinned to resolved numeric address")
				}
				conn, err := (&net.Dialer{}).DialContext(ctx, network, s.Listener.Addr().String())
				if err != nil {
					return nil, err
				}
				if wrongPeer {
					return &reportedRemote{Conn: conn, remote: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 443}}, nil
				}
				return conn, nil
			}
			base := strings.Replace(s.URL, ip.String(), host, 1)
			x, err := c.Transport(t.Context(), submission(base, false), []byte(rand.Text()))
			if attempts.Load() != 2 {
				t.Fatal("valid addresses were not tried in order")
			}
			if wrongPeer {
				if err == nil {
					if x.Body != nil {
						_ = x.Body.Close()
					}
					t.Fatal("actual loopback peer bypassed policy")
				}
				select {
				case <-x.Stopped:
				default:
					t.Fatal("peer rejected before submission leaked proof")
				}
			} else {
				_ = readExchange(t, x, err)
			}
		})
	}
}

func TestRevisionPrivateHostNeedsPlaintextOptIn(t *testing.T) {
	s := privateServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, completion()) }))
	defer s.Close()
	ip, _ := netip.AddrFromSlice(privateIPForTest(t))
	ip = ip.Unmap()
	for _, plaintext := range []bool{false, true} {
		c, _ := New(Config{AllowedHosts: []HostRule{{Host: "MoDeLs.ExAmPlE.CoM.", AllowPlaintext: plaintext}}, MaxResponseBytes: 4096, MaxEventBytes: 2048})
		c.lookup = func(context.Context, string) ([]netip.Addr, error) { return []netip.Addr{ip}, nil }
		x, err := c.Transport(t.Context(), submission(strings.Replace(s.URL, ip.String(), "models.example.com", 1), false), []byte(rand.Text()))
		if plaintext {
			_ = readExchange(t, x, err)
		} else if err == nil {
			t.Fatal("private host implicitly enabled plaintext")
		}
	}
}

func TestRevisionTLSFailureHasStopProof(t *testing.T) {
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, completion()) }))
	_ = s.Listener.Close()
	l, err := net.Listen("tcp", net.JoinHostPort(privateIPForTest(t).String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	s.Listener = l
	s.StartTLS()
	defer s.Close()
	x, err := clientFor(t).Transport(t.Context(), submission(s.URL, false), []byte(rand.Text()))
	if err == nil {
		t.Fatal("untrusted TLS certificate accepted")
	}
	select {
	case <-x.Stopped:
	default:
		t.Fatal("TLS failure leaked stop proof")
	}
}

func TestRevisionSSECommentsDoNotConsumeEventSize(t *testing.T) {
	s := privateServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, ":"+strings.Repeat("x", 2100)+"\n"+chunks())
	}))
	defer s.Close()
	x, err := clientFor(t).Transport(t.Context(), submission(s.URL, true), []byte(rand.Text()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = modelprotocol.Stream(t.Context(), x.Body, protocolRequest(t, true), modelprotocol.Limits{MaxBytes: 4096, MaxEvents: 16, MaxEventBytes: 2048}, "local-request", func(_ context.Context, b []byte) (int, error) { return len(b), nil })
	if err != nil {
		t.Fatal(err)
	}
}

func TestRevisionD2Projection(t *testing.T) {
	for _, provider := range []string{"openai", "minimax"} {
		for _, stream := range []bool{false, true} {
			t.Run(provider+"/"+map[bool]string{false: "json", true: "sse"}[stream], func(t *testing.T) {
				payload := completion()
				if stream {
					payload = chunks()
				}
				payload = strings.ReplaceAll(payload, `"provider-model"`, `"snapshot-model-2026"`)
				if provider == "openai" {
					payload = strings.ReplaceAll(payload, `"choices":`, `"obfuscation":"hidden","prompt_filter_results":[],"choices":`)
					payload = strings.ReplaceAll(payload, `"content":"OK"`, `"content":"OK","annotations":[],"refusal":null`)
					payload = strings.ReplaceAll(payload, `"prompt_tokens":1`, `"prompt_tokens_details":{"cached_tokens":0},"prompt_tokens":1`)
					if stream {
						payload = "data: " + `{"id":"provider-id","object":"chat.completion.chunk","created":1,"model":"snapshot-model-2026","choices":[],"prompt_filter_results":[]}` + "\n\n" + payload
					}
				} else {
					payload = strings.ReplaceAll(payload, `"choices":`, `"base_resp":{"status_code":0},"choices":`)
					payload = strings.ReplaceAll(payload, `"content":"OK"`, `"content":"OK","reasoning_content":"private reasoning","reasoning_details":[]`)
					payload = strings.ReplaceAll(payload, `"prompt_tokens":1`, `"prompt_cache_hit_tokens":0,"prompt_tokens":1`)
				}
				s := privateServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, payload) }))
				defer s.Close()
				x, err := clientFor(t).Transport(t.Context(), submission(s.URL, stream), []byte(rand.Text()))
				if err != nil {
					t.Fatal(err)
				}
				limits := modelprotocol.Limits{MaxBytes: 4096, MaxEvents: 32, MaxEventBytes: 2048}
				if stream {
					_, err = modelprotocol.Stream(t.Context(), x.Body, protocolRequest(t, true), limits, "local-request", func(_ context.Context, b []byte) (int, error) {
						if strings.Contains(string(b), "reasoning_content") || strings.Contains(string(b), "snapshot-model") {
							t.Error("provider extension escaped projection")
						}
						return len(b), nil
					})
				} else {
					_, err = modelprotocol.Complete(t.Context(), x.Body, protocolRequest(t, false), limits)
				}
				if err != nil {
					t.Fatal(err)
				}
				select {
				case <-x.Stopped:
				default:
					t.Fatal("valid provider response did not stop")
				}
			})
		}
	}
}

func TestRevisionSSRFNeverConnects(t *testing.T) {
	var accepted atomic.Int64
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) }))
	s.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			accepted.Add(1)
		}
	}
	s.Start()
	defer s.Close()
	privateOnly := map[string]bool{"10.0.0.1": true, "172.16.0.1": true, "192.168.0.1": true, "fc00::1": true, "100.64.0.1": true}                                                                                                                                                                                                                                                                           // gitleaks:allow
	for _, ip := range []string{"10.0.0.1", "172.16.0.1", "192.168.0.1", "fc00::1", "100.64.0.1", "127.0.0.1", "::1", "169.254.169.254", "169.254.1.1", "fe80::1", "fd00:ec2::254", "100.100.100.200", "0.0.0.0", "::", "224.0.0.1", "ff02::1", "198.18.0.1", "192.0.2.10", "198.51.100.10", "203.0.113.10", "2001:db8::1", "100::1", "::ffff:0:192.0.2.10", "64:ff9b::a00:1", "2002:7f00:1::", "2001::1"} { // gitleaks:allow
		for _, listed := range []bool{false, true} {
			if listed && privateOnly[ip] {
				continue
			}
			t.Run(ip+map[bool]string{false: "/default", true: "/listed"}[listed], func(t *testing.T) {
				cfg := Config{MaxResponseBytes: 4096, MaxEventBytes: 2048}
				if listed {
					cfg.AllowedHosts = []HostRule{{Host: "models.example.com", AllowPlaintext: true}}
				}
				c, _ := New(cfg)
				c.lookup = func(context.Context, string) ([]netip.Addr, error) { return []netip.Addr{netip.MustParseAddr(ip)}, nil }
				var dials atomic.Int64
				c.dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
					dials.Add(1)
					return (&net.Dialer{}).DialContext(ctx, "tcp", s.Listener.Addr().String())
				}
				x, err := c.Transport(t.Context(), submission("https://models.example.com", false), []byte(rand.Text()))
				if err == nil {
					if x.Body != nil {
						_ = x.Body.Close()
					}
					t.Fatal("forbidden address accepted")
				}
				if dials.Load() != 0 || accepted.Load() != 0 {
					t.Fatal("SSRF policy reached the real local listener")
				}
			})
		}
	}
}
