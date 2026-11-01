package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLiteLLMUIConfigUsesPresentationOrigin(t *testing.T) {
	for _, origin := range []string{"https://relay.example.com", "http://192.0.2.10:8086"} {
		r := httptest.NewRequest("GET", "/ai/.well-known/litellm-ui-config", nil)
		resp := &http.Response{StatusCode: 200, Request: r, Header: http.Header{"Content-Type": {"application/json"}, "ETag": {`"internal"`}}, Body: io.NopCloser(strings.NewReader(`{"proxy_base_url":"http://192.0.2.20:8086/ai","server_root_path":"/ai","other":{"keep":true}}`))}
		if err := rewriteLiteLLMUIConfig(resp, origin); err != nil {
			t.Fatal(err)
		}
		var config map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if config["proxy_base_url"] != origin+"/ai" || config["server_root_path"] != "/ai" || config["other"].(map[string]any)["keep"] != true || resp.Header.Get("ETag") != "" {
			t.Fatal("UI config rewrite changed other settings or retained internal URL")
		}
	}
	for _, body := range []string{`null`, `[]`, `{}`, `{"proxy_base_url":1}`, `{"proxy_base_url":null} {}`, `{"proxy_base_url":null,"proxy_base_url":"https://other.example.com"}`, `{"proxy_base_url":null}` + strings.Repeat(" ", maxAPIBodyBytes)} {
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
		if rewriteLiteLLMUIConfig(resp, "https://relay.example.com") == nil {
			t.Fatal("malformed UI config accepted")
		}
		_ = resp.Body.Close()
	}
	for _, mode := range []string{"encoding", "content-type", "status", "origin"} {
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"proxy_base_url":null}`))}
		origin := "https://relay.example.com"
		switch mode {
		case "encoding":
			resp.Header.Set("Content-Encoding", "gzip")
		case "content-type":
			resp.Header.Set("Content-Type", "text/plain")
		case "status":
			resp.StatusCode = 503
		case "origin":
			origin = ""
		}
		if rewriteLiteLLMUIConfig(resp, origin) == nil {
			t.Fatal("unsafe upstream configuration accepted", mode)
		}
		_ = resp.Body.Close()
	}
}
