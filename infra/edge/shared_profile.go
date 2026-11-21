package main

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"time"
)

const sharedProfilePath = "/_auriga/shared-profile"
const dedicatedProfilePath = "/_auriga/dedicated-profile"
const sharedProfileHeader = "Auriga-Shared-Profile-Probe"

var sharedProfileMount = regexp.MustCompile(`^/i/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/_auriga/(?:shared|dedicated)-profile$`)

func profileMode(path string) string {
	switch path {
	case sharedProfilePath:
		return "shared"
	case dedicatedProfilePath:
		return "dedicated"
	default:
		return ""
	}
}

func (g *Gateway) sharedProfileRequest(r *http.Request) bool {
	if r.Method != http.MethodGet || r.URL.EscapedPath() != r.URL.Path || r.URL.RawQuery != "" || r.URL.ForceQuery {
		return false
	}
	if _, ok := g.instanceIDFromHost(requestHost(r)); ok {
		return profileMode(r.URL.Path) != ""
	}
	return sharedProfileMount.MatchString(r.URL.Path)
}

// This proof authorizes one bounded static profile read. It is a distinct
// purpose from K's normal-page mode-switch challenge and from member tickets.
func (g *Gateway) serveSharedProfile(w http.ResponseWriter, r *http.Request, id, path string) {
	fail := func(err error) { sharedError(w, r, err) }
	mode := profileMode(path)
	if mode == "" || r.Method != http.MethodGet || r.URL.EscapedPath() != r.URL.Path || r.URL.RawQuery != "" || r.URL.ForceQuery || isUpgradeRequest(r) ||
		r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || len(r.Header.Values(sharedProfileHeader)) != 1 {
		fail(errors.New("shared.binding_mismatch"))
		return
	}
	claims, err := sharedVerifyPayload(r.Header.Get(sharedProfileHeader), g.Shared.TicketKeys, "auriga-shared-profile-probe+jwt")
	if err != nil {
		fail(err)
		return
	}
	if _, ok := sharedObject(claims, "schemaVersion audience binding issuedAt expiresAt jti"); !ok ||
		!sharedNumber(claims["schemaVersion"], 1) || sharedInt(claims["schemaVersion"]) != 1 || claims["audience"] != "auriga.shared.profile-probe" ||
		!sharedNumber(claims["issuedAt"], 0) || !sharedNumber(claims["expiresAt"], 0) ||
		!sharedUUID.MatchString(sharedString(claims["jti"])) {
		fail(errors.New("shared.binding_mismatch"))
		return
	}
	binding, ok := claims["binding"].(map[string]any)
	if !ok || !sharedBinding(binding) || binding["physicalInstanceId"] != id || binding["mode"] != mode {
		fail(errors.New("shared.binding_mismatch"))
		return
	}
	now, issued, expires := g.Now().Unix(), sharedInt(claims["issuedAt"]), sharedInt(claims["expiresAt"])
	if issued > now || expires <= now || expires <= issued || expires-issued > 10 {
		fail(errors.New("shared.expired"))
		return
	}
	route, err := g.Routes.Get(r.Context(), id)
	if err != nil || route == nil || route.InstanceID != id || route.TenantID != binding["tenantId"] || route.State != "running" {
		fail(nil)
		return
	}
	upstream, transport, err := g.Ingress.target(route)
	if err != nil {
		fail(nil)
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), time.Unix(expires, 0))
	defer cancel()
	upstream.Path = "/i/" + id + path
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream.String(), nil)
	if err != nil {
		fail(nil)
		return
	}
	// The node accepts only its existing authenticated Edge mTLS peer and this
	// fixed read. No browser cookie, bearer or user identity enters the hop.
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Encoding", "identity")
	response, err := transport.RoundTrip(request)
	if err != nil {
		fail(nil)
		return
	}
	defer func() { _ = response.Body.Close() }()
	contentType, _, parseErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || parseErr != nil || contentType != "application/json" ||
		len(response.Header.Values("Set-Cookie")) != 0 || len(response.Header.Values("Location")) != 0 {
		fail(nil)
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil || len(body) > 16384 {
		fail(nil)
		return
	}
	profile, err := sharedDecode(body)
	if err != nil || !validRuntimeProfile(profile, id, mode) {
		fail(nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

func validRuntimeProfile(profile map[string]any, id, mode string) bool {
	if _, ok := sharedObject(profile, "schemaVersion physicalInstanceId profile capabilities"); !ok ||
		!sharedNumber(profile["schemaVersion"], 1) || sharedInt(profile["schemaVersion"]) != 1 || profile["physicalInstanceId"] != id ||
		(mode != "shared" && mode != "dedicated") || profile["profile"] != "auriga."+mode+".v1" {
		return false
	}
	capabilities, ok := profile["capabilities"].(map[string]any)
	if !ok {
		return false
	}
	if mode == "dedicated" {
		_, exact := sharedObject(capabilities, "pluginInstall privateSkills includeDefaultRoots toolPresentation")
		return exact && capabilities["pluginInstall"] == true && capabilities["privateSkills"] == true && capabilities["includeDefaultRoots"] == true && capabilities["toolPresentation"] == "native"
	}
	if _, ok := sharedObject(capabilities, "shell terminal jobs subagent stdioMcp pluginInstall privateSkills personalKeys includeDefaultRoots toolPresentation"); !ok || capabilities["toolPresentation"] != "native" {
		return false
	}
	for name, value := range capabilities {
		if name != "toolPresentation" && value != false {
			return false
		}
	}
	return true
}
