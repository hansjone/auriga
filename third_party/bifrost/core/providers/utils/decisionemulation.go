package utils

import (
	"strings"

	schemas "github.com/maximhq/bifrost/core/schemas"
)

func DecisionTextString(text *schemas.DecisionText) string {
	return renderStructuredText(text.Value())
}

func renderStructuredText(value any) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	if raw, err := MarshalSorted(value); err == nil {
		return string(raw)
	}
	return ""
}

func DecisionCriteriaText(criteria *schemas.DecisionCriteria) string {
	if criteria == nil {
		return ""
	}
	m := map[string]any{"true": criteria.True.Value(), "false": criteria.False.Value()}
	var out strings.Builder
	for _, key := range []string{"true", "false"} {
		desc := renderStructuredText(m[key])
		if desc == "" {
			continue
		}
		if out.Len() == 0 {
			out.WriteString(" Meaning (answer: meaning): ")
		} else {
			out.WriteString(", ")
		}
		out.WriteString(key)
		out.WriteByte('=')
		out.WriteString(desc)
	}
	return out.String()
}
