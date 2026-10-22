package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func reservedInstanceCookie(name string) bool {
	name = strings.ToLower(name)
	for strings.HasPrefix(name, "__host-") || strings.HasPrefix(name, "__secure-") {
		name = strings.TrimPrefix(strings.TrimPrefix(name, "__host-"), "__secure-")
	}
	return strings.HasPrefix(name, "auriga_")
}

func mapInstanceResponse(resp *http.Response, target InstanceTarget, requestPath, requestQuery string, secure bool) error {
	resp.Header.Del("Clear-Site-Data")
	cookies := resp.Header.Values("Set-Cookie")
	resp.Header.Del("Set-Cookie")
	for _, raw := range cookies {
		name, _, _ := strings.Cut(raw, "=")
		if reservedInstanceCookie(strings.TrimSpace(name)) {
			continue
		}
		mapped, err := mapInstanceCookie(raw, target.Mount, requestPath, secure)
		if err != nil {
			return err
		}
		resp.Header.Add("Set-Cookie", mapped)
	}
	for _, header := range []string{"Location", "Service-Worker-Allowed"} {
		values := resp.Header.Values(header)
		if len(values) == 0 {
			continue
		}
		if len(values) != 1 {
			return fmt.Errorf("ambiguous instance response header")
		}
		mapped, err := mapInstanceLocation(values[0], target, requestPath, requestQuery)
		if err != nil {
			return err
		}
		u, err := url.Parse(mapped)
		if err != nil || !strictInstancePath(u.Path, u.EscapedPath()) || !strings.HasPrefix(u.EscapedPath(), target.Mount) {
			return fmt.Errorf("instance response path escapes mount")
		}
		if header == "Service-Worker-Allowed" {
			if u.RawQuery != "" || u.Fragment != "" {
				return fmt.Errorf("invalid service worker scope")
			}
		}
		resp.Header.Set(header, mapped)
	}
	return nil
}

func mapInstanceCookie(raw, mount, requestPath string, secure bool) (string, error) {
	c, err := http.ParseSetCookie(raw)
	if err != nil || c == nil {
		return "", fmt.Errorf("invalid instance cookie")
	}
	if c.Valid() != nil || len(c.Unparsed) != 0 || strings.HasPrefix(strings.ToLower(c.Name), "__host-") || c.Secure && !secure {
		return "", fmt.Errorf("unsupported instance cookie semantics")
	}
	if strings.HasPrefix(strings.ToLower(c.Name), "__secure-") && (!c.Secure || !secure) {
		return "", fmt.Errorf("invalid secure instance cookie")
	}
	parts := strings.Split(raw, ";")
	seen := map[string]bool{}
	pathIndex := -1
	for i, part := range parts[1:] {
		key, _, _ := strings.Cut(strings.TrimSpace(part), "=")
		key = strings.ToLower(key)
		if key == "domain" || seen[key] {
			return "", fmt.Errorf("ambiguous instance cookie attributes")
		}
		seen[key] = true
		if key == "path" {
			pathIndex = i + 1
		}
	}
	p := c.Path
	if pathIndex < 0 {
		p = "/"
		if i := strings.LastIndexByte(requestPath, '/'); i > 0 {
			p = requestPath[:i]
		}
	} else if p == "" || !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("invalid instance cookie path")
	}
	// A cookie Path is already in the upstream namespace. Never reinterpret a
	// public mount as another upstream path and accidentally scope it twice.
	pathURL, err := url.Parse(p)
	if err != nil || !strictInstancePath(pathURL.Path, pathURL.EscapedPath()) || strings.HasPrefix(p, "/i/") || strings.ContainsAny(p, "?#") {
		return "", fmt.Errorf("unsafe instance cookie path")
	}
	mapped := strings.TrimSuffix(mount, "/") + p
	if pathIndex < 0 {
		parts = append(parts, " Path="+mapped)
	} else {
		parts[pathIndex] = " Path=" + mapped
	}
	return strings.Join(parts, ";"), nil
}

func mapInstanceLocation(raw string, target InstanceTarget, requestPath, requestQuery string) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\\\x00\r\n\t") {
		return "", fmt.Errorf("invalid instance redirect")
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" {
		return "", fmt.Errorf("invalid instance redirect")
	}
	if u.Host != "" || u.Scheme != "" {
		origin, err := url.Parse(target.Origin)
		if err != nil || u.Scheme != origin.Scheme {
			return "", fmt.Errorf("external instance redirect")
		}
		host, port, err := parseAuthority(u.Host, u.Scheme)
		wantHost, wantPort, wantErr := parseAuthority(origin.Host, origin.Scheme)
		if err != nil || wantErr != nil || host != wantHost || port != wantPort {
			return "", fmt.Errorf("external instance redirect")
		}
		// An absolute origin denotes its root even when URL.Path is empty.
		// Normalize before resolving so the mapped root retains its slash.
		if u.Path == "" {
			u.Path = "/"
		}
	}
	if strings.HasPrefix(u.Path, "/i/") {
		if !strings.HasPrefix(u.EscapedPath(), target.Mount) || !strictInstancePath(u.Path, u.EscapedPath()) {
			return "", fmt.Errorf("cross-instance redirect")
		}
		u.Scheme, u.Host = "", ""
		return u.String(), nil
	}
	// Check before resolving; ResolveReference normalizes dot segments, which
	// would otherwise hide encoded traversal or an attempt to climb above root.
	if hasEncodedSep(&http.Request{URL: u}) || strings.Contains(strings.ToLower(u.EscapedPath()), "%25") || strings.Contains(u.Path, "//") || strings.Contains(u.Path, ";") {
		return "", fmt.Errorf("ambiguous instance redirect")
	}
	depth := 0
	if i := strings.LastIndexByte(requestPath, '/'); i > 0 {
		depth = len(strings.Split(strings.Trim(requestPath[:i], "/"), "/"))
	}
	if strings.HasPrefix(u.Path, "/") {
		depth = 0
	}
	for _, part := range strings.Split(u.Path, "/") {
		if u.RawPath != "" && (part == "." || part == "..") {
			return "", fmt.Errorf("encoded redirect traversal")
		}
		switch part {
		case "..":
			depth--
			if depth < 0 {
				return "", fmt.Errorf("instance redirect escapes root")
			}
		case "", ".":
		default:
			depth++
		}
	}
	base, err := url.Parse(requestPath)
	if err != nil {
		return "", fmt.Errorf("invalid upstream request path")
	}
	base.RawQuery = requestQuery
	resolved := base.ResolveReference(u)
	if !strictInstancePath(resolved.Path, resolved.EscapedPath()) {
		return "", fmt.Errorf("unsafe instance redirect path")
	}
	resolved.Scheme, resolved.Host = "", ""
	resolved.Path = strings.TrimSuffix(target.Mount, "/") + resolved.Path
	if resolved.RawPath != "" {
		resolved.RawPath = strings.TrimSuffix(target.Mount, "/") + resolved.RawPath
	}
	return resolved.String(), nil
}
