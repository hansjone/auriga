package main

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
)

func TestIPTicketBrowserKeepsAllowedProxyPresentation(t *testing.T) {
	b := newIPBrowser(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	upstream, _ := url.Parse(b.root)
	var presentation *url.URL
	proxy := &httputil.ReverseProxy{Rewrite: func(request *httputil.ProxyRequest) {
		request.SetURL(upstream)
		request.Out.Host = upstream.Host
		request.Out.Header.Set("X-Forwarded-Host", presentation.Host)
		request.Out.Header.Set("X-Forwarded-Proto", presentation.Scheme)
	}}
	front := httptest.NewServer(proxy)
	t.Cleanup(front.Close)
	presentation, _ = url.Parse(strings.Replace(front.URL, "127.0.0.1", "localhost", 1))
	b.g.Cfg.LinkOrigins = []string{presentation.String()}
	b.g.Cfg.LinkTrustedProxies = []string{"127.0.0.1"}
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(presentation, []*http.Cookie{{Name: "auriga_session", Value: "portal", Path: "/"}})
	client := &http.Client{Jar: jar}
	t.Cleanup(client.CloseIdleConnections)
	response, err := client.Get(presentation.String() + "/open/" + pathInstanceA)
	if err != nil {
		t.Fatal("proxy browser request failed")
	}
	readIPResponse(t, response, 204, "")
	if response.Request.URL.Host != presentation.Host {
		t.Fatal("ticket navigation left the browser origin")
	}
	cookieURL, _ := url.Parse(presentation.String() + instanceMount(pathInstanceA))
	var binding *InstBinding
	for _, cookie := range jar.Cookies(cookieURL) {
		if cookie.Name == "auriga_inst" {
			binding, err = b.g.Tickets.LookupInstance(cookie.Value)
		}
	}
	if err != nil || binding == nil || binding.Target.Origin != b.root {
		t.Fatal("presentation changed internal ticket binding")
	}
}
