// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package bifrost

import (
	"context"

	schemas "github.com/maximhq/bifrost/core/schemas"
)

func applyContextSpanAttributes(span *schemas.Span, ctx context.Context) {
	schemas.SpanEnrichmentFromContext(ctx).ApplyToSpan(span)
}
