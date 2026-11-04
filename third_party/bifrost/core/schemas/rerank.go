// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import "github.com/bytedance/sonic"

type RerankDocument struct {
	Text string                 `json:"text"`
	Data map[string]interface{} `json:"data,omitempty"`
	ID   *string                `json:"id,omitempty"`
	Meta map[string]interface{} `json:"meta,omitempty"`
}
type BifrostRerankResponse struct {
	ID          string                     `json:"id,omitempty"`
	Results     []RerankResult             `json:"results"`
	Model       string                     `json:"model"`
	Usage       *BifrostLLMUsage           `json:"usage,omitempty"`
	NextToken   *string                    `json:"next_token,omitempty"`
	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostRerankRequest struct {
	Provider       ModelProvider     `json:"provider"`
	Model          string            `json:"model"`
	Query          string            `json:"query"`
	Documents      []RerankDocument  `json:"documents"`
	Params         *RerankParameters `json:"params,omitempty"`
	Fallbacks      []Fallback        `json:"fallbacks,omitempty"`
	RawRequestBody []byte            `json:"-"`
}
type RerankResult struct {
	Index          int             `json:"index"`
	RelevanceScore float64         `json:"relevance_score"`
	ID             *string         `json:"id,omitempty"`
	Document       *RerankDocument `json:"document,omitempty"`
}
type RerankParameters struct {
	TopN            *int                   `json:"top_n,omitempty"`
	MaxTokensPerDoc *int                   `json:"max_tokens_per_doc,omitempty"`
	Priority        *int                   `json:"priority,omitempty"`
	ReturnDocuments *bool                  `json:"return_documents,omitempty"`
	NextToken       *string                `json:"next_token,omitempty"`
	ExtraParams     map[string]interface{} `json:"-"`
}
type rerankDocumentFields struct {
	Text string                 `json:"text"`
	Data map[string]interface{} `json:"data,omitempty"`
	ID   *string                `json:"id,omitempty"`
	Meta map[string]interface{} `json:"meta,omitempty"`
}

func (d *RerankDocument) UnmarshalJSON(data []byte) error {
	var text string
	if err := sonic.Unmarshal(data, &text); err == nil {
		*d = RerankDocument{Text: text}
		return nil
	}

	var fields rerankDocumentFields
	if err := sonic.Unmarshal(data, &fields); err != nil {
		return err
	}
	*d = RerankDocument(fields)
	return nil
}

func (r *BifrostRerankRequest) GetRawRequestBody() []byte {
	return r.RawRequestBody
}
