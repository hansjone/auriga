package bifrostbackend

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func TestValidatePoolSaveEffectiveAtSkew(t *testing.T) {
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
		name    string
		delta   time.Duration
		wantErr bool
	}{
		{"past", -2 * time.Second, false},
		{"near-past", -200 * time.Millisecond, false},
		{"now", 0, false},
		{"50ms-lead", 50 * time.Millisecond, false},
		{"1s-lead", time.Second, false},
		{"under-skew", priceEffectiveSkew - time.Second, false},
		{"at-skew", priceEffectiveSkew, false},
		{"over-skew", priceEffectiveSkew + time.Second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := base
			pool.Price.EffectiveAt = time.Now().Add(tc.delta)
			err := validatePoolSave(pool)
			if tc.wantErr {
				var problem validationProblem
				if !errors.As(err, &problem) || problem.field != "/price/effectiveAt" {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
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
