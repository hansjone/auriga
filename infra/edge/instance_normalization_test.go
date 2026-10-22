package main

import (
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestIPEmptyAbsoluteResponseRoot(t *testing.T) {
	var origin string
	b := newIPBrowser(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", origin+r.URL.Query().Get("suffix"))
		if r.URL.Query().Get("scope") != "" {
			w.Header().Set("Service-Worker-Allowed", origin)
		}
		w.WriteHeader(http.StatusFound)
	}))
	origin = b.root
	b.open(t, pathInstanceA)
	mount := instanceMount(pathInstanceA)
	for _, tc := range []struct{ name, suffix, scope string }{
		{"empty", "", "yes"}, {"query", "?q=one%2Ftwo", ""}, {"fragment", "#part", ""},
		{"query-fragment", "?q=one%2Ftwo#part", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := b.get(t, mount+"prefs/page?suffix="+url.QueryEscape(tc.suffix)+"&scope="+tc.scope)
			readIPResponse(t, resp, http.StatusFound, "")
			if got := resp.Header.Get("Location"); got != mount+tc.suffix {
				t.Errorf("Location=%q want=%q", got, mount+tc.suffix)
			}
			if tc.scope != "" && resp.Header.Get("Service-Worker-Allowed") != mount {
				t.Errorf("scope=%q want=%q", resp.Header.Get("Service-Worker-Allowed"), mount)
			}
		})
	}
}

func TestIPRejectsInvalidUTF8AtEveryPathEntry(t *testing.T) {
	var calls atomic.Int32
	b := newIPBrowser(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		p := "/" + r.URL.Query().Get("bytes")
		switch r.URL.Query().Get("entry") {
		case "location":
			w.Header().Set("Location", p)
		case "cookie":
			w.Header().Set("Set-Cookie", "app=x; Path="+p)
		case "scope":
			w.Header().Set("Service-Worker-Allowed", p)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	b.open(t, pathInstanceA)
	mount := instanceMount(pathInstanceA)
	for _, invalid := range []string{"%ff", "%c0%afapi", "%ed%a0%80"} {
		for _, entry := range []string{"request", "location", "cookie", "scope"} {
			t.Run(entry+"/"+invalid, func(t *testing.T) {
				before := calls.Load()
				path := mount + "page?entry=" + entry + "&bytes=" + url.QueryEscape(invalid)
				status, code := http.StatusBadGateway, "edge.upstream_error"
				if entry == "request" {
					path, status, code = mount+invalid, http.StatusBadRequest, "edge.bad_path"
				}
				resp := b.get(t, path)
				readIPResponse(t, resp, status, code)
				if entry == "request" && calls.Load() != before {
					t.Error("invalid request reached upstream")
				}
				if len(resp.Cookies()) != 0 || resp.Header.Get("Location") != "" || resp.Header.Get("Service-Worker-Allowed") != "" {
					t.Error("rejected response leaked browser state")
				}
			})
		}
	}
}

func TestIPPreservesValidUTF8AndEscaping(t *testing.T) {
	seen := make(chan string, 1)
	const escaped = "%E4%B8%AD%E6%96%87/%F0%9F%8C%9F/a%20b"
	b := newIPBrowser(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.RequestURI
		w.Header().Set("Location", "/"+escaped+"?q=1%2F2#part")
		w.Header().Set("Set-Cookie", "app=x; Path=/"+escaped)
		w.Header().Set("Service-Worker-Allowed", "/"+escaped)
		w.WriteHeader(http.StatusFound)
	}))
	b.open(t, pathInstanceA)
	mount := instanceMount(pathInstanceA)
	for _, path := range []string{escaped, "中文/🌟/a%20b"} {
		resp := b.get(t, mount+path+"?q=1%2F2")
		readIPResponse(t, resp, http.StatusFound, "")
		if got := <-seen; got != "/i/"+pathInstanceA+"/"+escaped+"?q=1%2F2" {
			t.Errorf("upstream URI=%q", got)
		}
		if resp.Header.Get("Location") != mount+escaped+"?q=1%2F2#part" || resp.Header.Get("Service-Worker-Allowed") != mount+escaped || !strings.Contains(resp.Header.Get("Set-Cookie"), "Path="+mount+escaped) {
			t.Fatal("valid UTF-8 or escaping changed")
		}
	}
}

func TestIPServiceWorkerScopeMountBoundary(t *testing.T) {
	b := newIPBrowser(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Service-Worker-Allowed", r.URL.Query().Get("scope"))
		w.WriteHeader(http.StatusNoContent)
	}))
	b.open(t, pathInstanceA)
	mount := instanceMount(pathInstanceA)
	for _, tc := range []struct {
		name, scope, want string
	}{
		{"root", "/", mount}, {"own-subpath", mount + "assets/", mount + "assets/"},
		{"relative", "assets/", mount + "prefs/assets/"},
		{"missing-slash", strings.TrimSuffix(mount, "/"), ""},
		{"prefix-sibling", strings.TrimSuffix(mount, "/") + "extra/", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := b.get(t, mount+"prefs/page?scope="+url.QueryEscape(tc.scope))
			if tc.want == "" {
				readIPResponse(t, resp, http.StatusBadGateway, "edge.upstream_error")
				if resp.Header.Get("Service-Worker-Allowed") != "" {
					t.Fatal("rejected scope leaked")
				}
				return
			}
			readIPResponse(t, resp, http.StatusNoContent, "")
			if got := resp.Header.Get("Service-Worker-Allowed"); got != tc.want {
				t.Errorf("scope=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestInstanceAbsoluteRootEffectivePorts(t *testing.T) {
	for _, tc := range []struct{ origin, absolute string }{
		{"http://example.com", "http://example.com:80"},
		{"http://example.com:80", "http://example.com"},
		{"https://example.com", "https://example.com:443"},
		{"https://example.com:443", "https://example.com"},
	} {
		t.Run(tc.absolute+"-to-"+tc.origin, func(t *testing.T) {
			for _, header := range []string{"Location", "Service-Worker-Allowed"} {
				resp := &http.Response{Header: http.Header{header: {tc.absolute}}}
				target := InstanceTarget{Origin: tc.origin, Mount: instanceMount(pathInstanceA)}
				if err := mapInstanceResponse(resp, target, "/prefs/page", "old=1", true); err != nil {
					t.Fatal(err)
				}
				if got := resp.Header.Get(header); got != target.Mount {
					t.Errorf("%s=%q want=%q", header, got, target.Mount)
				}
			}
		})
	}
}
