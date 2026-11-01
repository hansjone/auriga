// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package utils

import (
	"strconv"

	schemas "github.com/maximhq/bifrost/core/schemas"
)

func DecisionLevelDescription(index int, level schemas.DecisionLevel) interface{} {
	description := level.Description.Value()
	if level.Label == "" || level.Label == strconv.Itoa(index) {
		return description
	}
	if description == nil {
		return level.Label
	}
	if text, ok := description.(string); ok {
		return level.Label + ": " + text
	}
	return map[string]interface{}{"label": level.Label, "description": description}
}

func DecisionScoreLegend(question schemas.DecisionQuestion) map[string]any {
	if len(question.Levels) == 0 {
		return nil
	}
	legend := make(map[string]any, len(question.Levels))
	for i, level := range question.Levels {
		legend[strconv.Itoa(i)] = DecisionLevelDescription(i, level)
	}
	return legend
}
