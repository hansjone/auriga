package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStaticReadIdleAllowsProgressAndSlowClient(t *testing.T) {
	for _, mode := range []string{"progress", "slow-client", "stalled"} {
		t.Run(mode, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "5")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				if mode == "stalled" {
					<-r.Context().Done()
					return
				}
				for range 5 {
					if _, err := w.Write([]byte("a")); err != nil {
						return
					}
					w.(http.Flusher).Flush()
					if mode == "progress" {
						time.Sleep(60 * time.Millisecond)
					}
				}
			}))
			defer upstream.Close()
			client := http.Client{Transport: staticIdleTransport{base: http.DefaultTransport, timeout: 200 * time.Millisecond}}
			resp, err := client.Get(upstream.URL + "/ai/_next/static/app.js")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if mode == "slow-client" {
				time.Sleep(300 * time.Millisecond)
			}
			raw, err := io.ReadAll(resp.Body)
			if mode == "stalled" {
				if err == nil {
					t.Fatal("stalled upstream was not canceled")
				}
				return
			}
			if err != nil || string(raw) != "aaaaa" {
				t.Fatal("active stream or slow client was cut off", err)
			}
		})
	}
}
