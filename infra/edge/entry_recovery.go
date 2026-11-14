package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html"
	"net/http"
)

// This private browser-entry marker is removed from every workload response.
// API callers still receive the original Problem; it is not an auth grant.
const instanceEntryHeader = "X-Auriga-Instance-Entry"

func (g *Gateway) instanceEntryRequired(w http.ResponseWriter, r *http.Request, instanceID, instPath, rid string) {
	w.Header().Set("Cache-Control", "no-store")
	if !g.Cfg.Access.Valid() {
		writeProblem(w, problem(401, "edge.unauthorized", "Unauthorized", "instance cookie required", rid))
		return
	}
	w.Header().Set(instanceEntryHeader, "required")
	if instPath != "/" || !instanceNavigation(r) || r.Header.Get("Sec-Fetch-Mode") != "navigate" || r.Header.Get("Sec-Fetch-Dest") != "document" {
		writeProblem(w, problem(401, "edge.unauthorized", "Unauthorized", "instance cookie required", rid))
		return
	}
	// Access is the installation authority. Only IP/path mode may follow the
	// already checked presentation alias, exactly as ticketRedirectURL does.
	origin := g.Cfg.Access.PortalURL()
	if g.Cfg.Access.Mode() == "ip" {
		if alias, _ := r.Context().Value(linkOriginKey{}).(string); alias != "" {
			origin = alias
		}
	}
	target := origin + "/open/" + instanceID
	mount := g.instanceTarget(r, instanceID).Mount
	key, _ := json.Marshal("auriga.entry-recovery:" + mount)
	url, _ := json.Marshal(target)
	// The tab marker survives the normal open/ticket redirects. A successfully
	// connected Auriga browser client clears it for a later independent outage.
	// Storage unavailable or a repeated failure leaves an explicit manual link.
	script := `try{if(window.top===window&&sessionStorage.getItem(` + string(key) + `)===null){sessionStorage.setItem(` + string(key) + `,"1");window.location.replace(` + string(url) + `)}}catch{}`
	digest := sha256.Sum256([]byte(script))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'sha256-"+base64.StdEncoding.EncodeToString(digest[:])+"'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Auriga DSH · 恢复实例入口 / Restore instance entry</title></head><body><main><h1>正在恢复实例入口</h1><p>将通过门户重新验证当前会话。如果无法自动恢复，请点击下方入口；实例不会因此自动启动。</p><p lang="en">Revalidating your session through the portal. If recovery stops, use the link below. This does not start the instance.</p><p><a href="` + html.EscapeString(target) + `">重新进入实例 / Re-enter instance</a></p></main><script>` + script + `</script></body></html>`))
}
