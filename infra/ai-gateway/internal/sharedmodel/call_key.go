package sharedmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
)

func CallKey(binding sharedauth.Binding, callID string) string {
	key, _ := json.Marshal(struct {
		TenantID string
		MemberID string
		CallID   string
	}{binding.TenantID, binding.MemberID, callID})
	digest := sha256.Sum256(key)
	return "shared-" + hex.EncodeToString(digest[:])
}
