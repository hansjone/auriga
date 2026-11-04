// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import "context"

func SpanEnrichmentFromContext(ctx context.Context) *SpanEnrichment {
	if ctx == nil {
		return nil
	}
	e := &SpanEnrichment{}
	str := func(key BifrostContextKey) string {
		v, _ := ctx.Value(key).(string)
		return v
	}
	strs := func(key BifrostContextKey) []string {
		v, _ := ctx.Value(key).([]string)
		return v
	}

	e.VirtualKeyID = str(BifrostContextKeyGovernanceVirtualKeyID)
	e.VirtualKeyName = str(BifrostContextKeyGovernanceVirtualKeyName)
	e.SelectedKeyID = str(BifrostContextKeySelectedKeyID)
	e.SelectedKeyName = str(BifrostContextKeySelectedKeyName)
	e.RoutingRuleID = str(BifrostContextKeyGovernanceRoutingRuleID)
	e.RoutingRuleName = str(BifrostContextKeyGovernanceRoutingRuleName)
	e.TeamID = str(BifrostContextKeyGovernanceTeamID)
	e.TeamName = str(BifrostContextKeyGovernanceTeamName)
	e.CustomerID = str(BifrostContextKeyGovernanceCustomerID)
	e.CustomerName = str(BifrostContextKeyGovernanceCustomerName)
	e.BusinessUnitID = str(BifrostContextKeyGovernanceBusinessUnitID)
	e.BusinessUnitName = str(BifrostContextKeyGovernanceBusinessUnitName)
	e.ProjectID = str(BifrostContextKeyGovernanceProjectID)
	e.ProjectName = str(BifrostContextKeyGovernanceProjectName)
	e.PromptID = str(BifrostContextKeySelectedPromptID)
	e.PromptName = str(BifrostContextKeySelectedPromptName)
	e.PromptVersion = str(BifrostContextKeySelectedPromptVersion)
	e.UserID = str(BifrostContextKeyUserID)
	e.UserName = str(BifrostContextKeyUserName)
	e.UserEmail = str(BifrostContextKeyUserEmail)

	e.App = DetectAppFromUserAgent(str(BifrostContextKeyUserAgent))

	e.TeamIDs = strs(BifrostContextKeyGovernanceTeamIDs)
	e.TeamNames = strs(BifrostContextKeyGovernanceTeamNames)
	e.CustomerIDs = strs(BifrostContextKeyGovernanceCustomerIDs)
	e.CustomerNames = strs(BifrostContextKeyGovernanceCustomerNames)
	e.BusinessUnitIDs = strs(BifrostContextKeyGovernanceBusinessUnitIDs)
	e.BusinessUnitNames = strs(BifrostContextKeyGovernanceBusinessUnitNames)

	if fallbackIndex, ok := ctx.Value(BifrostContextKeyFallbackIndex).(int); ok {
		e.FallbackIndex = &fallbackIndex
	}
	return e
}

func (e *SpanEnrichment) ApplyToSpan(span *Span) {
	if e == nil || span == nil {
		return
	}
	span.Enrichment = e

	if e.VirtualKeyID != "" {
		span.SetAttribute(AttrBifrostVirtualKeyID, e.VirtualKeyID)
		span.SetAttribute(AttrBifrostVirtualKeyName, e.VirtualKeyName)
	}
	if e.SelectedKeyID != "" {
		span.SetAttribute(AttrBifrostSelectedKeyID, e.SelectedKeyID)
		span.SetAttribute(AttrBifrostSelectedKeyName, e.SelectedKeyName)
	}
	if e.RoutingRuleID != "" {
		span.SetAttribute(AttrBifrostRoutingRuleID, e.RoutingRuleID)
		span.SetAttribute(AttrBifrostRoutingRuleName, e.RoutingRuleName)
	}
	setSpanStr(span, AttrBifrostTeamID, e.TeamID)
	setSpanStr(span, AttrBifrostTeamName, e.TeamName)
	setSpanStr(span, AttrBifrostCustomerID, e.CustomerID)
	setSpanStr(span, AttrBifrostCustomerName, e.CustomerName)
	setSpanStr(span, AttrBifrostBusinessUnitID, e.BusinessUnitID)
	setSpanStr(span, AttrBifrostBusinessUnitName, e.BusinessUnitName)
	setSpanStr(span, AttrBifrostProjectID, e.ProjectID)
	setSpanStr(span, AttrBifrostProjectName, e.ProjectName)
	setSpanStr(span, AttrBifrostPromptID, e.PromptID)
	setSpanStr(span, AttrBifrostPromptName, e.PromptName)
	setSpanStr(span, AttrBifrostPromptVersion, e.PromptVersion)
	setSpanStr(span, AttrBifrostUserID, e.UserID)
	setSpanStr(span, AttrBifrostUserName, e.UserName)
	setSpanStr(span, AttrBifrostUserEmail, e.UserEmail)
	setSpanStr(span, AttrBifrostApp, e.App)

	setSpanStrs(span, AttrBifrostTeamIDs, e.TeamIDs)
	setSpanStrs(span, AttrBifrostTeamNames, e.TeamNames)
	setSpanStrs(span, AttrBifrostCustomerIDs, e.CustomerIDs)
	setSpanStrs(span, AttrBifrostCustomerNames, e.CustomerNames)
	setSpanStrs(span, AttrBifrostBusinessUnitIDs, e.BusinessUnitIDs)
	setSpanStrs(span, AttrBifrostBusinessUnitNames, e.BusinessUnitNames)

	if e.FallbackIndex != nil {
		span.SetAttribute(AttrBifrostFallbackIndex, *e.FallbackIndex)
	}
}

func (s *Span) SetRetries(attempts int) {
	if s == nil {
		return
	}
	if s.Enrichment == nil {
		s.Enrichment = &SpanEnrichment{}
	}
	s.Enrichment.Retries = attempts
	s.SetAttribute(AttrBifrostRetries, attempts)
}

func setSpanStr(span *Span, key, value string) {
	if value != "" {
		span.SetAttribute(key, value)
	}
}

func setSpanStrs(span *Span, key string, value []string) {
	if len(value) > 0 {
		span.SetAttribute(key, value)
	}
}
