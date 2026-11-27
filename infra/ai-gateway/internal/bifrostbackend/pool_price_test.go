package bifrostbackend

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func TestNormalizePoolSaveEffectiveAtSkew(t *testing.T) {
	base := api.RelayPool{
		Id:              "skew-model",
		Enabled:         true,
		ContextWindow:   0,
		MaxOutputTokens: 0,
		Routes:          []api.RelayRoute{{ProviderId: "019a0000-0000-7000-8000-000000000001", Model: "upstream", Weight: 1}},
		Price: api.Price{
			Currency:                     "USD",
			Version:                      "v1",
			InputMicrosPerMillionTokens:  1,
			OutputMicrosPerMillionTokens: 1,
		},
	}
	for _, tc := range []struct {
		name      string
		delta     time.Duration
		wantErr   bool
		wantClamp bool
	}{
		{"past", -2 * time.Second, false, false},
		{"near-past", -200 * time.Millisecond, false, false},
		{"now", 0, false, false},
		{"50ms-lead", 50 * time.Millisecond, false, true},
		{"1s-lead", time.Second, false, true},
		{"under-skew", priceEffectiveSkew - time.Second, false, true},
		{"at-skew", priceEffectiveSkew, false, true},
		{"over-skew", priceEffectiveSkew + time.Second, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := base
			submitted := time.Now().Add(tc.delta)
			pool.Price.EffectiveAt = submitted
			before := time.Now()
			err := normalizePoolSave(&pool)
			after := time.Now()
			if tc.wantErr {
				var problem validationProblem
				if !errors.As(err, &problem) || problem.field != "/price/effectiveAt" {
					t.Fatalf("err=%v", err)
				}
				if !pool.Price.EffectiveAt.Equal(submitted) {
					t.Fatal("rejected effectiveAt was mutated")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantClamp {
				if pool.Price.EffectiveAt.Before(before) || pool.Price.EffectiveAt.After(after) {
					t.Fatalf("lead was not clamped to server now: %v", pool.Price.EffectiveAt)
				}
				return
			}
			if !pool.Price.EffectiveAt.Equal(submitted) {
				t.Fatalf("past effectiveAt changed: %v", pool.Price.EffectiveAt)
			}
		})
	}
}

func TestRejectIncludesValidationField(t *testing.T) {
	w := httptest.NewRecorder()
	reject(w, invalidField("/price/effectiveAt"))
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"common.validation_failed"`) || !strings.Contains(w.Body.String(), `"/price/effectiveAt"`) {
		t.Fatalf("body=%s", w.Body.String())
	}
}
