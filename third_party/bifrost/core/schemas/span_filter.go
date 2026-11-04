// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import (
	"fmt"

	"strings"
)

type PluginSpanFilterMode string
type PluginSpanFilter struct {
	Mode    PluginSpanFilterMode `json:"mode"`
	Plugins []string             `json:"plugins"`
}

const (
	PluginSpanFilterModeInclude PluginSpanFilterMode = "include"

	PluginSpanFilterModeExclude PluginSpanFilterMode = "exclude"
)

func (f *PluginSpanFilter) Validate() error {
	if f == nil {
		return nil
	}
	switch f.Mode {
	case PluginSpanFilterModeInclude, PluginSpanFilterModeExclude:
		return nil
	default:
		return fmt.Errorf("plugin_span_filter.mode %q is invalid: must be %q or %q",
			f.Mode, PluginSpanFilterModeInclude, PluginSpanFilterModeExclude)
	}
}

func SanitizePluginSpanName(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, " ", "-"))
}
