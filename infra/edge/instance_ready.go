package main

import (
	"mime"
	"net/http"
	"strconv"
	"strings"
)

// Only document GETs may wait. API calls, writes and upgrades retain Problem responses.
func instanceNavigation(r *http.Request) bool {
	if r.Method != http.MethodGet || r.Header.Get("Upgrade") != "" {
		return false
	}
	if mode := r.Header.Get("Sec-Fetch-Mode"); mode != "" && mode != "navigate" {
		return false
	}
	if dest := r.Header.Get("Sec-Fetch-Dest"); dest != "" && dest != "document" {
		return false
	}
	for _, accept := range strings.Split(r.Header.Get("Accept"), ",") {
		media, params, err := mime.ParseMediaType(strings.TrimSpace(accept))
		if err != nil || media != "text/html" {
			continue
		}
		if raw, ok := params["q"]; ok {
			q, err := strconv.ParseFloat(raw, 64)
			if err != nil || !(q > 0 && q <= 1) {
				continue
			}
		}
		return true
	}
	return false
}

func instanceNotRunning(w http.ResponseWriter, r *http.Request, state, rid string, navigation bool) {
	w.Header().Set("Cache-Control", "no-store")
	if state == "starting" {
		w.Header().Set("Retry-After", "1")
		if navigation && instanceNavigation(r) {
			// Refresh the same authenticated GET. Each attempt rechecks the current
			// KV revision; elapsed time alone can never grant access or issue a ticket.
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "no-referrer")
			_, _ = w.Write([]byte(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="1"><title>实例准备中 / Preparing your instance</title></head>
<body><main><h1>实例准备中</h1><p role="status">正在等待实例入口就绪，页面会自动重试。</p>
<p lang="en">Preparing your instance. This page will retry automatically.</p>
<p><a href="">立即重试 / Retry now</a></p></main></body></html>`))
			return
		}
	}
	writeProblem(w, problem(http.StatusServiceUnavailable, "edge.instance_not_running", "Service Unavailable", "instance state="+state, rid))
}
