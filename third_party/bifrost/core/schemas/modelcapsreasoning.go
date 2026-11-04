// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import "slices"

var effortLadder = []string{
	ReasoningEffortMinimal, ReasoningEffortLow, ReasoningEffortMedium,
	ReasoningEffortHigh, ReasoningEffortXHigh, ReasoningEffortMax,
}
var baseReasoningContexts = []string{ReasoningContextAuto, ReasoningContextCurrentTurn}
var baseEffortLevels = []string{ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh}

const (
	ReasoningEffortNone    = "none"
	ReasoningEffortMinimal = "minimal"
	ReasoningEffortLow     = "low"
	ReasoningEffortMedium  = "medium"
	ReasoningEffortHigh    = "high"
	ReasoningEffortXHigh   = "xhigh"
	ReasoningEffortMax     = "max"
)

const (
	ReasoningContextAuto        = "auto"
	ReasoningContextCurrentTurn = "current_turn"
	ReasoningContextAllTurns    = "all_turns"
)

const DefaultReasoningBudgetMin = 1024

func (c ModelCaps) SupportsReasoning(fallback bool) bool {
	if c.record != nil && c.record.SupportsReasoning != nil {
		return *c.record.SupportsReasoning
	}
	return fallback
}

func (c ModelCaps) ReasoningEffortLevels(fallback *EffortControl) []string {
	if c.record != nil && len(c.record.ReasoningEffortLevels) > 0 {
		return c.record.ReasoningEffortLevels
	}
	if fallback != nil && len(fallback.Levels) > 0 {
		return fallback.Levels
	}
	return slices.Clone(baseEffortLevels)
}

func (c ModelCaps) SupportsAdaptiveThinking(fallback bool) bool {
	if c.record != nil && c.record.SupportsAdaptiveThinking != nil {
		return *c.record.SupportsAdaptiveThinking
	}
	return fallback
}

func (c ModelCaps) AdaptiveOnlyThinking(fallback bool) bool {
	if c.record != nil && c.record.SupportsSamplingParams != nil {
		return !*c.record.SupportsSamplingParams
	}
	return fallback
}

func (c ModelCaps) SupportsReasoningContentBlocks(fallback bool) bool {
	if c.record != nil && c.record.SupportsReasoningContentBlocks != nil {
		return *c.record.SupportsReasoningContentBlocks
	}
	return fallback
}

func (c ModelCaps) SupportedReasoningContexts(fallback []string) []string {
	if c.record != nil && len(c.record.SupportedReasoningContexts) > 0 {
		return c.record.SupportedReasoningContexts
	}
	if len(fallback) > 0 {
		return fallback
	}
	return slices.Clone(baseReasoningContexts)
}

func (c ModelCaps) SupportsReasoningEffort(fallback bool) bool {
	if c.record == nil {
		return fallback
	}
	if c.record.SupportsReasoningEffort != nil {
		return *c.record.SupportsReasoningEffort
	}
	if len(c.record.ReasoningEffortLevels) > 0 {
		return true
	}
	return fallback
}

func (c ModelCaps) NormalizeReasoningEffort(effort string, fallback *EffortControl) string {
	var renames map[string]string
	switch {
	case c.record != nil && c.record.ReasoningEffortRenames != nil:
		renames = c.record.ReasoningEffortRenames
	case fallback != nil:
		renames = fallback.Renames
	}
	if renamed, ok := renames[effort]; ok {
		return renamed
	}
	levels := c.ReasoningEffortLevels(fallback)
	if slices.Contains(levels, effort) {
		return effort
	}

	rank := slices.Index(effortLadder, effort)
	if rank < 0 {
		return effort
	}
	best, bestDistance := "", 0
	for _, candidate := range levels {
		idx := slices.Index(effortLadder, candidate)
		if idx < 0 {
			continue
		}
		distance := idx - rank
		if distance < 0 {
			distance = -distance
		}
		if best == "" || distance < bestDistance || (distance == bestDistance && idx > rank) {
			best, bestDistance = candidate, distance
		}
	}
	if best == "" {
		return effort
	}
	return best
}

func (c ModelCaps) ReasoningBudgetRange(defaultMax int, fallback *BudgetControl) (min, max int, ok bool) {
	var b *BudgetControl
	if c.record != nil {
		b = c.record.ReasoningBudget
	}
	if b == nil || (b.Min == nil && b.Max == nil) {
		b = fallback
	}
	if b != nil && (b.Min != nil || b.Max != nil) {
		min, max = DefaultReasoningBudgetMin, defaultMax
		if b.Min != nil {
			min = *b.Min
		}
		if b.Max != nil {
			max = *b.Max
		}
		return min, max, true
	}
	return DefaultReasoningBudgetMin, defaultMax, false
}

func (c ModelCaps) SupportsReasoningBudget(fallback bool) bool {
	if c.record != nil && c.record.SupportsReasoningBudget != nil {
		return *c.record.SupportsReasoningBudget
	}
	return fallback
}

func (c ModelCaps) CanDisableReasoning(fallback bool) bool {
	if c.record != nil && c.record.SupportsReasoningDisable != nil {
		return *c.record.SupportsReasoningDisable
	}
	return fallback
}

func (c ModelCaps) DefaultReasoningEffort(fallback string) string {
	if c.record != nil && c.record.DefaultReasoningEffort != nil {
		return *c.record.DefaultReasoningEffort
	}
	return fallback
}

func (c ModelCaps) SupportsBetweenToolsThinking(fallback bool) bool {
	if c.record != nil && c.record.SupportsBetweenToolsThinking != nil {
		return *c.record.SupportsBetweenToolsThinking
	}
	return fallback
}

func (c ModelCaps) SupportsThinkingDisplayUpdates(fallback bool) bool {
	if c.record != nil && c.record.SupportsThinkingDisplayUpdates != nil {
		return *c.record.SupportsThinkingDisplayUpdates
	}
	return fallback
}

func (c ModelCaps) SupportsDynamicReasoningBudget(fallback bool) bool {
	if c.record != nil && c.record.SupportsDynamicReasoningBudget != nil {
		return *c.record.SupportsDynamicReasoningBudget
	}
	return fallback
}
