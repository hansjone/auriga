// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package utils

import (
	"sort"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type aliasMatch struct {
	key   string
	value string
}
type ListModelsPipeline struct {
	AllowedModels     schemas.WhiteList
	BlacklistedModels schemas.BlackList

	Aliases     schemas.KeyAliases
	Unfiltered  bool
	ProviderKey schemas.ModelProvider

	MatchFns []MatchFn
}
type FilterResult struct {
	ResolvedID string

	AliasValue string
}
type MatchFn func(a, b string) bool

func ToDisplayName(id string) string {
	caser := cases.Title(language.English)
	parts := strings.FieldsFunc(id, func(r rune) bool {
		return r == '-' || r == '_'
	})
	if len(parts) == 0 {
		return ""
	}
	for i, part := range parts {
		if part != "" {
			parts[i] = caser.String(strings.ToLower(part))
		}
	}
	return strings.Join(parts, " ")
}

func DefaultMatchFns() []MatchFn {
	return []MatchFn{
		func(a, b string) bool { return strings.EqualFold(a, b) },
	}
}

func matches(a, b string, fns []MatchFn) bool {
	if schemas.IsRegexEntry(b) {
		return schemas.MatchEntry(b, a)
	}
	for _, fn := range fns {
		if fn(a, b) {
			return true
		}
	}
	return false
}

func (p *ListModelsPipeline) ShouldEarlyExit() bool {
	if p.Unfiltered {
		return false
	}
	if p.BlacklistedModels.IsBlockAll() {
		return true
	}
	if p.AllowedModels.IsEmpty() && len(p.Aliases) == 0 {
		return true
	}
	return false
}

func (p *ListModelsPipeline) resolveModelID(modelID string) []aliasMatch {
	var candidates []aliasMatch
	for aliasKey, alias := range p.Aliases {
		if matches(modelID, alias.ModelID, p.MatchFns) {
			candidates = append(candidates, aliasMatch{key: aliasKey, value: alias.ModelID})
		}
	}
	if len(candidates) == 0 {
		return []aliasMatch{{key: modelID, value: ""}}
	}

	candidates = append(candidates, aliasMatch{key: modelID, value: ""})
	sort.Slice(candidates, func(i, j int) bool {
		return strings.ToLower(candidates[i].key) < strings.ToLower(candidates[j].key)
	})
	return candidates
}

func (p *ListModelsPipeline) FilterModel(modelID string) []FilterResult {

	candidates := p.resolveModelID(modelID)

	var results []FilterResult
	for _, candidate := range candidates {
		resolvedName := candidate.key

		if !p.Unfiltered && p.AllowedModels.IsRestricted() {
			allowed := false
			for _, entry := range p.AllowedModels {
				if matches(resolvedName, entry, p.MatchFns) {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}
		}

		if !p.Unfiltered {
			blacklisted := false
			for _, entry := range p.BlacklistedModels {
				if matches(resolvedName, entry, p.MatchFns) {
					blacklisted = true
					break
				}
			}
			if blacklisted {
				continue
			}
		}

		results = append(results, FilterResult{
			ResolvedID: resolvedName,
			AliasValue: candidate.value,
		})
	}
	return results
}

func (p *ListModelsPipeline) BackfillModels(included map[string]bool) []schemas.Model {
	var result []schemas.Model

	if !p.Unfiltered && p.AllowedModels.IsRestricted() {

		for _, entry := range p.AllowedModels {
			if schemas.IsRegexEntry(entry) || included[strings.ToLower(entry)] {
				continue
			}

			blacklisted := false
			for _, bl := range p.BlacklistedModels {
				if matches(entry, bl, p.MatchFns) {
					blacklisted = true
					break
				}
			}
			if blacklisted {
				continue
			}
			m := schemas.Model{
				ID:   string(p.ProviderKey) + "/" + entry,
				Name: schemas.Ptr(ToDisplayName(entry)),
			}

			for aliasKey, alias := range p.Aliases {
				if matches(entry, aliasKey, p.MatchFns) {
					m.Alias = schemas.Ptr(alias.ModelID)
					break
				}
			}
			result = append(result, m)
		}

		for aliasKey, alias := range p.Aliases {
			if included[strings.ToLower(aliasKey)] || !p.allowedOnlyByRegex(aliasKey) {
				continue
			}

			blacklisted := false
			for _, bl := range p.BlacklistedModels {
				if matches(aliasKey, bl, p.MatchFns) {
					blacklisted = true
					break
				}
			}
			if blacklisted {
				continue
			}
			result = append(result, schemas.Model{
				ID:    string(p.ProviderKey) + "/" + aliasKey,
				Name:  schemas.Ptr(ToDisplayName(aliasKey)),
				Alias: schemas.Ptr(alias.ModelID),
			})
		}
		return result
	}

	if !p.Unfiltered && len(p.Aliases) > 0 {
		for aliasKey, alias := range p.Aliases {
			if included[strings.ToLower(aliasKey)] {
				continue
			}

			blacklisted := false
			for _, bl := range p.BlacklistedModels {
				if matches(aliasKey, bl, p.MatchFns) {
					blacklisted = true
					break
				}
			}
			if blacklisted {
				continue
			}
			result = append(result, schemas.Model{
				ID:    string(p.ProviderKey) + "/" + aliasKey,
				Name:  schemas.Ptr(ToDisplayName(aliasKey)),
				Alias: schemas.Ptr(alias.ModelID),
			})
		}
	}

	return result
}

func (p *ListModelsPipeline) allowedOnlyByRegex(name string) bool {
	byRegex := false
	for _, entry := range p.AllowedModels {
		if !matches(name, entry, p.MatchFns) {
			continue
		}
		if !schemas.IsRegexEntry(entry) {
			return false
		}
		byRegex = true
	}
	return byRegex
}
