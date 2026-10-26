package relayhttp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func socket(t *testing.T, s *httptest.Server) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", s.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err = c.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSocketReadDeadline(t *testing.T) {
	h, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(h)
	defer s.Close()
	c := socket(t, s)
	started := time.Now()
	_, err = fmt.Fprintf(c, "POST %s HTTP/1.1\r\nHost: example.com\r\nAuthorization: Bearer original\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{", chatPath)
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Body.Close() }()
	if r.StatusCode != 400 || time.Since(started) > time.Second {
		t.Fatalf("read deadline not bounded: %d %s", r.StatusCode, time.Since(started))
	}
	b, err := io.ReadAll(r.Body)
	if err != nil || !strings.Contains(string(b), "common.validation_failed") {
		t.Fatalf("%s %v", b, err)
	}
}

func TestSocketWriteDeadline(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ended := make(chan error, 1)
			writing := make(chan struct{})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				r := response{w: w, rc: http.NewResponseController(w), timeout: 100 * time.Millisecond, max: 64 << 20}
				close(writing)
				b := []byte(strings.Repeat("x", 128<<10))
				for n := 0; n < 512; n++ {
					if _, err := r.sink(ctx, b); err != nil {
						ended <- err
						return
					}
				}
				ended <- nil
			}))
			defer s.Close()
			c := socket(t, s)
			if tcp, ok := c.(*net.TCPConn); ok {
				if err := tcp.SetReadBuffer(1024); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := io.WriteString(c, "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"); err != nil {
				t.Fatal(err)
			}
			<-writing
			if mode == "cancel" {
				cancel()
			}
			select {
			case err := <-ended:
				if err == nil {
					t.Fatal("slow peer did not block bounded writer")
				}
			case <-time.After(time.Second):
				t.Fatal("real socket write did not stop")
			}
		})
	}
}

func TestActualOversizeBodyAndDuplicateHeaders(t *testing.T) {
	h, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(h)
	defer s.Close()
	for _, wire := range []string{
		fmt.Sprintf("POST %s HTTP/1.1\r\nHost: example.com\r\nAuthorization: Bearer original\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\n%x\r\n%s\r\n0\r\n\r\n", chatPath, (1<<20)+1, strings.Repeat("x", (1<<20)+1)),
		"GET /ai/v1/models HTTP/1.1\r\nHost: example.com\r\nAuthorization: Bearer a\r\nAuthorization: Bearer b\r\n\r\n",
		"POST /ai/v1/chat/completions HTTP/1.1\r\nHost: example.com\r\nAuthorization: Bearer a\r\nContent-Type: application/json\r\nContent-Length: 0\r\nIdempotency-Key: a\r\nIdempotency-Key: b\r\n\r\n",
	} {
		c := socket(t, s)
		done := make(chan struct{})
		go func() { defer close(done); _, _ = io.WriteString(c, wire) }()
		r, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		_ = c.Close()
		<-done // A rejected peer may close while the oversized writer finishes.
		if err != nil || (r.StatusCode != 400 && r.StatusCode != 401) {
			t.Fatalf("%d %s %v", r.StatusCode, b, err)
		}
	}
}
