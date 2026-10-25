package budget

import (
	"testing"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func TestCostAndPolicy(t *testing.T) {
	p := api.Price{InputMicrosPerMillionTokens: 1, OutputMicrosPerMillionTokens: 1}
	if n, err := cost(1, 1, p); err != nil || n != 1 {
		t.Fatalf("ceil combined micros: %d %v", n, err)
	}
	if _, err := cost(maxInteger, maxInteger, api.Price{InputMicrosPerMillionTokens: maxInteger, OutputMicrosPerMillionTokens: maxInteger}); err == nil {
		t.Fatal("overflow accepted")
	}
	if _, err := cost(-1, 0, p); err == nil {
		t.Fatal("negative accepted")
	}
	limit := int64(100)
	abs := int64(20)
	if n, err := flightCap(&limit, 25, &abs); err != nil || n != 20 {
		t.Fatalf("minimum cap: %d %v", n, err)
	}
	if _, err := flightCap(nil, 25, nil); err == nil {
		t.Fatal("unbounded unlimited quota")
	}
	limit = 0
	if n, err := flightCap(&limit, 25, &abs); err != nil || n != 0 {
		t.Fatalf("zero quota: %d %v", n, err)
	}
}
