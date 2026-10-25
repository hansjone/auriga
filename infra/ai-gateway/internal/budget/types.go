// Package budget owns durable, tenant-scoped model admission and accounting.
// Its inputs are trusted internal ports, never data-plane JSON request DTOs.
package budget

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	ai "github.com/hansjone/auriga/infra/gen/auriga/ai/v1"
)

const maxInteger int64 = 9007199254740991

// Error carries catalog codes; adapters may map these to the existing envelope.
type Error struct {
	Code              string
	OriginalRequestID string
	Outcome           string
}

func (e *Error) Error() string  { return e.Code }
func failure(code string) error { return &Error{Code: code} }

// TrustedSnapshot must come from online modelgate authorization/quota checks and
// a conservative tokenizer/context bound, not caller-selected quota or pricing.
// Admission's request ID, periods, reserved values and policy are computed here.
type TrustedSnapshot struct {
	Admission  *ai.BudgetAdmission
	Price      api.Price
	ValidUntil time.Time
}

// Request contains only a digest of the canonical validated request (including
// model and generation parameters). Neither body nor key is persisted in clear.
type Request struct {
	Method      string
	Path        string
	Key         string
	Fingerprint [32]byte
}

type Ref struct{ TenantID, AccountID, RequestID string }
type Reservation struct {
	Admission *ai.BudgetAdmission
	State     string
	Revision  int64
}

// FinalUsage is provider-confirmed final usage priced with the pinned price.
// Price/currency conflicts or invalid counts are durably quarantined as unknown.
type FinalUsage struct {
	InputTokens, OutputTokens int64
	Price                     api.Price
}

type Finish struct {
	Outcome          string
	Usage            *FinalUsage
	ExecutionStopped bool
}

// RecoveryEvidence is asserted only by a trusted execution supervisor. Time or
// lease expiry alone is not evidence that execution stopped or never submitted.
type RecoveryEvidence struct{ NeverSubmitted, ExecutionStopped bool }

type Ledger struct {
	store    *store.Store
	currency string
	policy   api.AdmissionPolicy
}

func New(s *store.Store, currency string, policy *api.AdmissionPolicy) (*Ledger, error) {
	p := api.AdmissionPolicy{MaxActiveRequestsPerSubject: 2, MaxInFlightPercent: 25}
	if policy != nil {
		p = *policy
	}
	if s == nil || !currencyPattern.MatchString(currency) || len(p.AdditionalProperties) != 0 || p.MaxActiveRequestsPerSubject < 1 || p.MaxActiveRequestsPerSubject > 100 || p.MaxInFlightPercent < 1 || p.MaxInFlightPercent > 100 || !positiveOptional(p.MaxInFlightTokens) || !positiveOptional(p.MaxInFlightMicros) {
		return nil, failure("ai.budget_unavailable")
	}
	// Detach pointer fields so configuration cannot change beneath admissions.
	if p.MaxInFlightTokens != nil {
		n := *p.MaxInFlightTokens
		p.MaxInFlightTokens = &n
	}
	if p.MaxInFlightMicros != nil {
		n := *p.MaxInFlightMicros
		p.MaxInFlightMicros = &n
	}
	return &Ledger{store: s, currency: currency, policy: p}, nil
}

func positiveOptional(n *int64) bool { return n == nil || (*n > 0 && *n <= maxInteger) }

func newID(now time.Time) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	ms := uint64(now.UnixMilli())
	for i := 5; i >= 0; i-- {
		b[i] = byte(ms)
		ms >>= 8
	}
	b[6] = b[6]&15 | 0x70
	b[8] = b[8]&63 | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
