package schemas

import "maps"

type RedactionPhase string
type RedactionMapsByPhase struct {
	Input  map[string]string `json:"input,omitempty"`
	Output map[string]string `json:"output,omitempty"`
}
type RedactionData struct {
	LiteralReplacements RedactionMapsByPhase `json:"literal_replacements,omitempty"`
	ReversibleMappings  RedactionMapsByPhase `json:"reversible_mappings,omitempty"`
}

func (m RedactionMapsByPhase) Clone() RedactionMapsByPhase {
	return RedactionMapsByPhase{
		Input:  maps.Clone(m.Input),
		Output: maps.Clone(m.Output),
	}
}

func (d RedactionData) Clone() RedactionData {
	return RedactionData{
		LiteralReplacements: d.LiteralReplacements.Clone(),
		ReversibleMappings:  d.ReversibleMappings.Clone(),
	}
}
