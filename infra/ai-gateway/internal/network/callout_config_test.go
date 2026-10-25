package network

import (
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nkeys"
)

func TestNewCallout_RedeemTimeoutVsAuthTimeout(t *testing.T) {
	issuer, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	base := CalloutConfig{
		Issuer:   issuer,
		Account:  clientAccount,
		Redeemer: newMemTickets(),
	}

	t.Run("ok-redeem-strictly-less", func(t *testing.T) {
		cfg := base
		cfg.RedeemTimeout = time.Second
		cfg.AuthTimeout = 2 * time.Second
		if _, err := NewCallout(cfg); err != nil {
			t.Fatalf("expected ok: %v", err)
		}
	})

	t.Run("reject-redeem-ge-auth", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			redeem time.Duration
			auth   time.Duration
		}{
			{"equal", time.Second, time.Second},
			{"greater", 2 * time.Second, time.Second},
			{"default-redeem-vs-short-auth", 0, 500 * time.Millisecond}, // defaults to 1s
		} {
			t.Run(tc.name, func(t *testing.T) {
				cfg := base
				cfg.RedeemTimeout = tc.redeem
				cfg.AuthTimeout = tc.auth
				_, err := NewCallout(cfg)
				if err == nil {
					t.Fatal("expected RedeemTimeout vs authorization.timeout error")
				}
				if !strings.Contains(err.Error(), "must be less than authorization.timeout") {
					t.Fatalf("err = %v", err)
				}
			})
		}
	})
}
