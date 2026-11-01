// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import (
	"context"

	"strings"
)

var VaultResolveHook func(ctx context.Context, value *string) error

func LookupVault(ref string) (string, bool) {
	if !strings.HasPrefix(ref, "vault.") || VaultResolveHook == nil {
		return "", false
	}
	val := ref
	if err := VaultResolveHook(context.Background(), &val); err != nil {
		return "", false
	}
	return val, true
}
