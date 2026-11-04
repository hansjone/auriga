// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

type BifrostGuardrailMetadata struct {
	JudgeCalls []BifrostGuardrailJudgeCall `json:"judge_calls,omitempty"`
	Detections []BifrostGuardrailDetection `json:"detections,omitempty"`

	Interventions []BifrostGuardrailIntervention `json:"interventions,omitempty"`
}
type BifrostGuardrailDetection struct {
	Phase             string   `json:"phase,omitempty"`
	RuleID            *uint    `json:"rule_id,omitempty"`
	RuleName          string   `json:"rule_name,omitempty"`
	GuardrailName     string   `json:"guardrail_name,omitempty"`
	GuardrailProvider string   `json:"guardrail_provider,omitempty"`
	Reason            string   `json:"reason,omitempty"`
	Assessments       []string `json:"assessments,omitempty"`
	Action            string   `json:"action,omitempty"`
	Enforced          *bool    `json:"enforced,omitempty"`
}
type BifrostGuardrailJudgeCall struct {
	Phase                   string                       `json:"phase,omitempty"`
	RuleID                  *uint                        `json:"rule_id,omitempty"`
	RuleName                string                       `json:"rule_name,omitempty"`
	GuardrailName           string                       `json:"guardrail_name,omitempty"`
	GuardrailProvider       string                       `json:"guardrail_provider,omitempty"`
	Action                  string                       `json:"action,omitempty"`
	Reason                  string                       `json:"reason,omitempty"`
	JudgeProvider           ModelProvider                `json:"judge_provider,omitempty"`
	JudgeModel              string                       `json:"judge_model,omitempty"`
	JudgeRequestType        RequestType                  `json:"judge_request_type,omitempty"`
	PromptTokens            int                          `json:"prompt_tokens,omitempty"`
	PromptTokensDetails     *ChatPromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokens        int                          `json:"completion_tokens,omitempty"`
	CompletionTokensDetails *ChatCompletionTokensDetails `json:"completion_tokens_details,omitempty"`
	TotalTokens             int                          `json:"total_tokens,omitempty"`
}
type BifrostGuardrailIntervention struct {
	Phase             string   `json:"phase,omitempty"`
	RuleID            *uint    `json:"rule_id,omitempty"`
	RuleName          string   `json:"rule_name,omitempty"`
	GuardrailName     string   `json:"guardrail_name,omitempty"`
	GuardrailProvider string   `json:"guardrail_provider,omitempty"`
	Reason            string   `json:"reason,omitempty"`
	Assessments       []string `json:"assessments,omitempty"`
}

func (d *BifrostGuardrailMetadata) isEmpty() bool {
	return d == nil || len(d.JudgeCalls) == 0 && len(d.Detections) == 0 && len(d.Interventions) == 0
}

func (d *BifrostGuardrailMetadata) Clone() *BifrostGuardrailMetadata {
	if d.isEmpty() {
		return nil
	}
	clone := &BifrostGuardrailMetadata{}
	if len(d.JudgeCalls) > 0 {
		clone.JudgeCalls = make([]BifrostGuardrailJudgeCall, len(d.JudgeCalls))
		for index, call := range d.JudgeCalls {
			clone.JudgeCalls[index] = call
			clone.JudgeCalls[index].RuleID = cloneUint(call.RuleID)
			clone.JudgeCalls[index].PromptTokensDetails = cloneChatPromptTokensDetails(call.PromptTokensDetails)
			clone.JudgeCalls[index].CompletionTokensDetails = cloneChatCompletionTokensDetails(call.CompletionTokensDetails)
		}
	}
	if len(d.Detections) > 0 {
		clone.Detections = make([]BifrostGuardrailDetection, len(d.Detections))
		for index, detection := range d.Detections {
			clone.Detections[index] = detection
			clone.Detections[index].RuleID = cloneUint(detection.RuleID)
			clone.Detections[index].Assessments = append([]string(nil), detection.Assessments...)
			if detection.Enforced != nil {
				clone.Detections[index].Enforced = Ptr(*detection.Enforced)
			}
		}
	}
	if len(d.Interventions) > 0 {
		clone.Interventions = make([]BifrostGuardrailIntervention, len(d.Interventions))
		for index, intervention := range d.Interventions {
			clone.Interventions[index] = intervention
			clone.Interventions[index].RuleID = cloneUint(intervention.RuleID)
			clone.Interventions[index].Assessments = append([]string(nil), intervention.Assessments...)
		}
	}
	return clone
}

func cloneChatPromptTokensDetails(details *ChatPromptTokensDetails) *ChatPromptTokensDetails {
	if details == nil {
		return nil
	}
	clone := *details
	if details.CachedWriteTokenDetails != nil {
		cachedWriteClone := *details.CachedWriteTokenDetails
		clone.CachedWriteTokenDetails = &cachedWriteClone
	}
	return &clone
}

func cloneChatCompletionTokensDetails(details *ChatCompletionTokensDetails) *ChatCompletionTokensDetails {
	if details == nil {
		return nil
	}
	clone := *details
	clone.CitationTokens = cloneInt(details.CitationTokens)
	clone.NumSearchQueries = cloneInt(details.NumSearchQueries)
	clone.ImageTokens = cloneInt(details.ImageTokens)
	return &clone
}

func cloneUint(value *uint) *uint {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
