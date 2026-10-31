package observability

import (
	"context"
	"testing"
)

func TestJoinTraceVectorPreservesUniqueRequestIdentity(t *testing.T) {
	const trace = "1234567890abcdef1234567890abcdef"
	for _, id := range []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"} {
		raw := []byte(`{"request_id":"join-` + trace + `-` + id + `","type":"local_register"}`)
		ctx := NATSContext(context.Background(), raw)
		if Parent(ctx)[3:35] != trace {
			t.Fatal("join trace diverged")
		}
	}
}
