package renderer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

var credentialUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var credentialSecret = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type managedModel struct {
	Tenant     string `json:"tenantId"`
	Owner      string `json:"ownerId"`
	Instance   string `json:"instanceId"`
	Version    uint64 `json:"credentialVersion"`
	Credential string `json:"credential"`
	BaseURL    string `json:"baseUrl"`
	Models     []struct {
		ID              string          `json:"id"`
		ContextWindow   json.RawMessage `json:"contextWindow,omitempty"`
		MaxOutputTokens json.RawMessage `json:"maxOutputTokens,omitempty"`
	} `json:"models"`
}

// Only HPKE-authenticated material can create the renderer-owned native route.
func decodeManagedModel(raw []byte, spec *nodev1.InstanceSpec) (*managedModel, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	fail := func() (*managedModel, error) { return nil, fmt.Errorf("renderer: invalid managed model material") }
	if len(raw) > 1<<20 {
		return fail()
	}
	var m managedModel
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil || d.Decode(new(any)) != io.EOF {
		return fail()
	}
	if m.Tenant != spec.TenantId || m.Owner != spec.OwnerId || m.Instance != spec.InstanceId || m.Version < 1 || m.Version > 9007199254740991 {
		return fail()
	}
	prefix := "amc." + m.Tenant + "." + m.Owner + "." + m.Instance + "."
	if !strings.HasPrefix(m.Credential, prefix) || strings.ContainsAny(m.Credential, " \r\n\t") || len(m.Credential) > 512 {
		return fail()
	}
	tail := strings.Split(strings.TrimPrefix(m.Credential, prefix), ".")
	if len(tail) != 2 || !credentialUUID.MatchString(tail[0]) || !credentialSecret.MatchString(tail[1]) {
		return fail()
	}
	u, err := url.Parse(m.BaseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.Path != "/ai/v1" {
		return fail()
	}
	if len(m.Models) == 0 || len(m.Models) > 256 {
		return fail()
	}
	seen := map[string]bool{}
	for _, model := range m.Models {
		context, validContext := managedCapability(model.ContextWindow, 2)
		output, validOutput := managedCapability(model.MaxOutputTokens, 1)
		if strings.TrimSpace(model.ID) == "" || len(model.ID) > 200 || strings.ContainsFunc(model.ID, unicode.IsControl) || seen[model.ID] || !validContext || !validOutput || (context > 0 && output > 0 && output >= context) {
			return fail()
		}
		seen[model.ID] = true
	}
	return &m, nil
}

func managedCapability(raw json.RawMessage, minimum int64) (int64, bool) {
	if len(raw) == 0 {
		return 0, true
	}
	var value int64
	if json.Unmarshal(raw, &value) != nil || value < minimum || value > 9007199254740991 {
		return 0, false
	}
	return value, true
}

func managedModelRow(_ *managedModel) any {
	// The plugin initializes its provider through the public configuration editor.
	// A command-line config overlay would veto every subsequent native model save.
	return map[string]any{"id": "llm-pi-ai"}
}
