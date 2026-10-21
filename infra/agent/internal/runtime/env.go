package runtime

import (
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// platformEnvKeys are the variables the agent itself renders for every
// instance (contracts/plugin/README.md §3). Values come from the renderer,
// never from InstanceSpec.Profile.Env.
var platformEnvKeys = map[string]struct{}{
	"AURIGA_MODE":                {},
	"AURIGA_PROTOCOL":            {},
	"AURIGA_INSTANCE_ID":         {},
	"AURIGA_OWNER_ID":            {},
	"AURIGA_TENANT_ID":           {},
	"AURIGA_PLATFORM_URL":        {},
	"AURIGA_IDENTITY_KEYS_FILE":  {},
	"AURIGA_INSTANCE_TOKEN_FILE": {},
	"DSH_HOME":                   {},
	"HTTPS_PROXY":                {},
	"HTTP_PROXY":                 {},
	"NO_PROXY":                   {},
}

// DefaultExtraEnvKeys are the only Profile.Env keys a spec may set unless the
// node overrides Preparing.ExtraEnvKeys. They are locale/timezone knobs that
// cannot change what code the instance loads or where it connects.
var DefaultExtraEnvKeys = []string{"TZ", "LANG", "LANGUAGE", "LC_ALL"}

var envKeyPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

// IsPlatformEnvKey reports whether key is rendered by the agent itself.
func IsPlatformEnvKey(key string) bool {
	_, ok := platformEnvKeys[key]
	return ok
}

// ContainerEnv builds the container environment from the rendered env file
// using an allowlist: platform keys, plus keys that the spec declares in
// Profile.Env and that are in extraAllowed. Every other key is dropped and its
// name (never its value) logged. Platform keys declared in Profile.Env are
// ignored: the rendered platform value always wins.
//
// Dropping (not rejecting) is deliberate: a dropped key only removes
// configuration, it can never grant the instance more than the allowlist, and
// one stale template key does not take every instance of that template down.
func ContainerEnv(rendered, specEnv map[string]string, extraAllowed []string, log *slog.Logger) (map[string]string, error) {
	if log == nil {
		log = slog.Default()
	}
	extra := make(map[string]struct{}, len(extraAllowed))
	for _, k := range extraAllowed {
		extra[k] = struct{}{}
	}
	out := make(map[string]string, len(rendered))
	var dropped []string
	for _, k := range sortedEnvKeys(rendered) {
		v := rendered[k]
		if err := validateEnvPair(k, v); err != nil {
			return nil, err
		}
		if IsPlatformEnvKey(k) {
			out[k] = v
			continue
		}
		if _, declared := specEnv[k]; declared {
			if _, ok := extra[k]; ok {
				out[k] = v
				continue
			}
		}
		dropped = append(dropped, k)
	}
	if len(dropped) > 0 {
		log.Warn("dropped container env keys not on allowlist", "keys", strings.Join(dropped, ","))
	}
	return out, nil
}

// validateEnvPair checks syntax only (allowlisting is ContainerEnv's job).
func validateEnvPair(k, v string) error {
	if !envKeyPattern.MatchString(k) {
		return fmt.Errorf("runtime: invalid env key %q", k)
	}
	for _, r := range v {
		if r == 0 || r == '\n' || r == '\r' || (unicode.IsControl(r) && r != '\t') {
			return fmt.Errorf("runtime: env %s has a control character in its value", k)
		}
	}
	return nil
}

func sortedEnvKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
