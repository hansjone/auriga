package bifrost

import (
	"context"

	schemas "github.com/maximhq/bifrost/core/schemas"
)

func applyContextSpanAttributes(span *schemas.Span, ctx context.Context) {
	schemas.SpanEnrichmentFromContext(ctx).ApplyToSpan(span)
}
