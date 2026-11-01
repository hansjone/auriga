// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package tokencache

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

const maxTokenRedirects = 10

func guardRedirects(hc *http.Client) *http.Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	guarded := *hc
	inner := hc.CheckRedirect
	guarded.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := checkTokenRedirect(req, via); err != nil {
			return err
		}
		if inner != nil {
			return inner(req, via)
		}
		if len(via) >= maxTokenRedirects {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &guarded
}

func checkTokenRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 || req == nil || req.URL == nil {
		return nil
	}
	if !httpsOrLoopbackURL(req.URL.String()) {
		return errors.New("oauth token endpoint redirect to a non-https, non-loopback url refused")
	}
	prev := via[len(via)-1].URL
	if prev != nil && strings.EqualFold(prev.Scheme, "https") && !strings.EqualFold(req.URL.Scheme, "https") {
		return errors.New("oauth token endpoint redirect from https to http refused")
	}
	if first := via[0].URL; first != nil && origin(first) != origin(req.URL) {
		return errors.New("oauth token endpoint redirect to another origin refused")
	}
	return nil
}

func origin(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	if port == "" {
		switch scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return scheme + "://" + strings.ToLower(u.Hostname()) + ":" + port
}
