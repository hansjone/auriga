package main

import (
	"net/http"
	"strings"
)

func getCookie(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil || c == nil {
		return ""
	}
	return c.Value
}

func requestHost(r *http.Request) string {
	h := r.Host
	if h == "" {
		h = r.Header.Get("Host")
	}
	if i := strings.IndexByte(h, ':'); i >= 0 {
		// strip port for Host matching (IPv6 in brackets handled loosely)
		if !strings.HasPrefix(h, "[") {
			h = h[:i]
		}
	}
	return strings.ToLower(h)
}

func isWriteMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func stripAurigaCookies(cookieHeader string) string {
	if cookieHeader == "" {
		return ""
	}
	parts := strings.Split(cookieHeader, ";")
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		name := p
		if i := strings.IndexByte(p, '='); i >= 0 {
			name = p[:i]
		}
		lname := strings.ToLower(name)
		if strings.Contains(lname, "auriga_") {
			continue
		}
		kept = append(kept, p)
	}
	return strings.Join(kept, "; ")
}

func setInstanceCookie(w http.ResponseWriter, secure bool, value string, maxAge int) {
	name := cookieName(secure, instCookieBase())
	c := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
		Secure:   secure,
	}
	http.SetCookie(w, c)
}

func openNonceCookieBase() string { return "auriga_open_nonce" }

// openNonceCookieDomain returns a Domain that covers portal + instance hosts when using
// subdomain routing (e.g. i.example.com → example.com). Empty means host-only cookie.
func openNonceCookieDomain(instanceParentDomain string) string {
	parent := strings.ToLower(strings.TrimSpace(instanceParentDomain))
	if parent == "" {
		return ""
	}
	if i := strings.IndexByte(parent, '.'); i > 0 && i+1 < len(parent) {
		return parent[i+1:]
	}
	return parent
}

func openNonceCookieName(secure bool, domain string) string {
	if domain == "" && secure {
		return "__Host-" + openNonceCookieBase()
	}
	return openNonceCookieBase()
}

func setOpenNonceCookie(w http.ResponseWriter, secure bool, domain, value string, maxAge int) {
	c := &http.Cookie{
		Name:     openNonceCookieName(secure, domain),
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
		Secure:   secure,
	}
	if domain != "" {
		c.Domain = domain
	}
	http.SetCookie(w, c)
}

func clearOpenNonceCookie(w http.ResponseWriter, secure bool, domain string) {
	setOpenNonceCookie(w, secure, domain, "", -1)
}
