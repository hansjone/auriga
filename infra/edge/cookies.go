package main

import (
	"net/http"
	"strings"
	"unicode"
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
		// Login binding cookies must reach IAM; edge does not terminate login_tx.
		if lname == "auriga_login_tx" || lname == "__host-auriga_login_tx" {
			kept = append(kept, p)
			continue
		}
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

// sanitizeCookieNameSuffix keeps cookie name characters safe (RFC 6265 token-ish).
func sanitizeCookieNameSuffix(id string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(id) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "x"
	}
	if len(out) > 48 {
		return out[:48]
	}
	return out
}

func openNonceCookieName(secure bool, domain, instanceID string) string {
	base := openNonceCookieBase()
	if instanceID != "" {
		base = base + "_" + sanitizeCookieNameSuffix(instanceID)
	}
	if domain == "" && secure {
		return "__Host-" + base
	}
	return base
}

func setOpenNonceCookie(w http.ResponseWriter, secure bool, domain, instanceID, value string, maxAge int) {
	c := &http.Cookie{
		Name:     openNonceCookieName(secure, domain, instanceID),
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

func clearOpenNonceCookie(w http.ResponseWriter, secure bool, domain, instanceID string) {
	setOpenNonceCookie(w, secure, domain, instanceID, "", -1)
}
