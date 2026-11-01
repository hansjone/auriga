package budget

import (
	"math/big"
	"regexp"
	"time"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
	ai "github.com/hansjone/auriga/infra/gen/auriga/ai/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var modelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Use arbitrary precision intermediate arithmetic; only persisted bounded
// integers are int64. Round the total price up once to whole currency micros.
func cost(input, output int64, p api.Price) (int64, error) {
	for _, n := range []int64{input, output, p.InputMicrosPerMillionTokens, p.OutputMicrosPerMillionTokens} {
		if n < 0 || n > maxInteger {
			return 0, failure("ai.budget_unavailable")
		}
	}
	n := new(big.Int).Mul(big.NewInt(input), big.NewInt(p.InputMicrosPerMillionTokens))
	n.Add(n, new(big.Int).Mul(big.NewInt(output), big.NewInt(p.OutputMicrosPerMillionTokens)))
	n.Add(n, big.NewInt(999999)).Quo(n, big.NewInt(1000000))
	if !n.IsInt64() || n.Int64() > maxInteger {
		return 0, failure("ai.budget_unavailable")
	}
	return n.Int64(), nil
}

func flightCap(quota *int64, percent int64, absolute *int64) (int64, error) {
	if !positiveOptional(absolute) || percent < 1 || percent > 100 || (quota != nil && (*quota < 0 || *quota > maxInteger)) || (quota == nil && absolute == nil) {
		return 0, failure("ai.budget_unavailable")
	}
	n := maxInteger
	if quota != nil {
		n = *quota/100*percent + (*quota % 100 * percent / 100)
	}
	if absolute != nil && *absolute < n {
		n = *absolute
	}
	return n, nil
}

func add(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > maxInteger-b {
		return 0, failure("ai.budget_unavailable")
	}
	return a + b, nil
}

func (l *Ledger) snapshot(s TrustedSnapshot, now time.Time) (*ai.BudgetAdmission, error) {
	if s.Admission == nil {
		return nil, failure("ai.budget_unavailable")
	}
	a := proto.Clone(s.Admission).(*ai.BudgetAdmission)
	if !uuidPattern.MatchString(a.TenantId) || !uuidPattern.MatchString(a.AccountId) || !uuidPattern.MatchString(a.ProviderId) || !uuidPattern.MatchString(a.CredentialId) || !modelPattern.MatchString(a.ModelId) || a.Currency != l.currency || a.Currency != s.Price.Currency || a.PriceVersion != s.Price.Version || len(s.Price.Version) < 1 || len(s.Price.Version) > 128 || s.Price.EffectiveAt.IsZero() || s.Price.EffectiveAt.After(now) || !s.ValidUntil.After(now) || a.TokenExpiresAt == nil || a.TokenExpiresAt.CheckValid() != nil || !a.TokenExpiresAt.AsTime().After(now) || a.InputTokenUpperBound <= 0 || a.MaxOutputTokens <= 0 {
		return nil, failure("ai.budget_unavailable")
	}
	if (a.GetInstanceId() == "") == (a.GetDeviceId() == "") || !uuidPattern.MatchString(a.GetInstanceId()+a.GetDeviceId()) {
		return nil, failure("ai.budget_unavailable")
	}
	var err error
	a.ReservedTokens, err = add(a.InputTokenUpperBound, a.MaxOutputTokens)
	if err != nil {
		return nil, err
	}
	a.ReservedMicros, err = cost(a.InputTokenUpperBound, a.MaxOutputTokens, s.Price)
	if err != nil {
		return nil, err
	}
	if _, err = flightCap(a.DailyTokens, l.policy.MaxInFlightPercent, l.policy.MaxInFlightTokens); err != nil {
		return nil, err
	}
	if _, err = flightCap(a.MonthlyBudgetMicros, l.policy.MaxInFlightPercent, l.policy.MaxInFlightMicros); err != nil {
		return nil, err
	}
	a.RequestId, err = newID(now)
	if err != nil {
		return nil, err
	}
	a.DayPeriodStart = now.UTC().Format("2006-01-02") + "T00:00:00Z"
	a.MonthPeriodStart = now.UTC().Format("2006-01") + "-01T00:00:00Z"
	a.AdmittedAt = timestamppb.New(now)
	a.MaxActiveRequestsPerSubject = uint32(l.policy.MaxActiveRequestsPerSubject)
	a.MaxInFlightPercent = uint32(l.policy.MaxInFlightPercent)
	if l.policy.MaxInFlightMicros != nil {
		n := *l.policy.MaxInFlightMicros
		a.MaxInFlightMicros = &n
	}
	if l.policy.MaxInFlightTokens != nil {
		n := *l.policy.MaxInFlightTokens
		a.MaxInFlightTokens = &n
	}
	return a, nil
}

func samePrice(a, b api.Price) bool {
	return a.Currency == b.Currency && a.Version == b.Version && a.InputMicrosPerMillionTokens == b.InputMicrosPerMillionTokens && a.OutputMicrosPerMillionTokens == b.OutputMicrosPerMillionTokens && a.EffectiveAt.Equal(b.EffectiveAt)
}
