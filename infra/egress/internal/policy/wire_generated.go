// Code generated from contracts/deployment/egress.schema.json; DO NOT EDIT.
package policy

type Value struct {
	Categories   []string `json:"categories"`
	AllowDomains []string `json:"allowDomains"`
	DenyDomains  []string `json:"denyDomains"`
	Mode         string   `json:"mode"`
}

type Snapshot struct {
	SchemaVersion int64  `json:"schemaVersion"`
	TenantId      string `json:"tenantId"`
	Version       int64  `json:"version"`
	Value         Value  `json:"value"`
}

type Report struct {
	TenantId       string `json:"tenantId"`
	Version        int64  `json:"version"`
	NodeId         string `json:"nodeId"`
	Digest         string `json:"digest"`
	Result         string `json:"result"`
	AppliedVersion int64  `json:"appliedVersion"`
	ReportedAt     string `json:"reportedAt"`
}
