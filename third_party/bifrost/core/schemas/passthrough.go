// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import (
	"fmt"
	"net/http"
	"path"
	"strings"
)

type PassthroughPolicy struct {
	AllowedRoutes []PassthroughRouteRule `json:"allowed_routes"`
}
type BifrostPassthroughResponse struct {
	StatusCode       int
	Headers          map[string]string
	Body             []byte
	ExtraFields      BifrostResponseExtraFields
	Path             string
	PassthroughUsage *BifrostPassthroughUsage
}
type PassthroughRouteRule struct {
	Methods []string `json:"methods,omitempty"`
	Path    string   `json:"path"`
}
type BifrostPassthroughRequest struct {
	Provider    ModelProvider
	Model       string
	Method      string
	Path        string
	APIVersion  string
	RawQuery    string
	UpstreamURL string
	Body        []byte
	SafeHeaders map[string]string
}
type BifrostPassthroughUsage struct {
	LLMUsage     *BifrostLLMUsage
	ServiceTier  *BifrostServiceTier
	Speed        *string
	InferenceGeo *string

	Iterations []ResponsesResponseUsage

	ImageUsage   *ImageUsage
	ImageSize    string
	ImageQuality string

	AudioInputChars int

	AudioSeconds      *int
	AudioTokenDetails *TranscriptionUsageInputTokenDetails

	VideoSeconds *int

	ContainerIdentifier string

	Cost *BifrostCost
}

func (p *PassthroughPolicy) Validate() error {
	if p == nil {
		return nil
	}
	for i, rule := range p.AllowedRoutes {
		if err := rule.validate(); err != nil {
			return fmt.Errorf("allowed_routes[%d]: %w", i, err)
		}
	}
	return nil
}

func (r *PassthroughRouteRule) validate() error {
	for _, method := range r.Methods {
		switch method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions:
		default:
			return fmt.Errorf("unsupported method %q; use GET, POST, PUT, PATCH, DELETE, HEAD or OPTIONS", method)
		}
	}
	if !strings.HasPrefix(r.Path, "/") {
		return fmt.Errorf("path must start with /")
	}
	if strings.ContainsAny(r.Path, "?#%\\[]") {
		return fmt.Errorf("path must not contain ?, #, %%, \\, [ or ]")
	}
	if r.Path == "/" {
		return nil
	}
	segments := strings.Split(r.Path[1:], "/")
	for i, segment := range segments {
		switch {
		case segment == "" || segment == "." || segment == "..":
			return fmt.Errorf("path must not contain empty, . or .. segments")
		case strings.Contains(segment, "**") && (segment != "**" || i != len(segments)-1):
			return fmt.Errorf("** is only allowed as the last path segment")
		}
	}
	return nil
}

func (r *PassthroughRouteRule) Matches(method, apiVersion, requestPath string) bool {
	if r.Path == "" {
		return false
	}
	if len(r.Methods) > 0 {
		allowed := false
		for _, m := range r.Methods {
			if strings.EqualFold(m, method) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	pattern := r.Path
	if apiVersion != "" {
		if pattern == "/**" {
			return true
		}
		segment, rest := cutPassthroughSegment(pattern[1:])
		if !matchPassthroughSegment(segment, apiVersion) {
			return false
		}
		pattern = rest
	}
	return matchPassthroughPath(pattern, requestPath)
}

func matchPassthroughPath(pattern, p string) bool {
	for {
		if pattern == "/**" {
			return true
		}
		if pattern == "" || p == "" {
			return pattern == p
		}
		patternSegment, patternRest := cutPassthroughSegment(pattern[1:])
		pathSegment, pathRest := cutPassthroughSegment(p[1:])
		if !matchPassthroughSegment(patternSegment, pathSegment) {
			return false
		}
		pattern, p = patternRest, pathRest
	}
}

func cutPassthroughSegment(s string) (string, string) {
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return s[:i], s[i:]
	}
	return s, ""
}

func matchPassthroughSegment(pattern, segment string) bool {
	if strings.IndexByte(pattern, '*') < 0 {
		return pattern == segment
	}
	if segment == "" {
		return false
	}
	ok, _ := path.Match(pattern, segment)
	return ok
}
