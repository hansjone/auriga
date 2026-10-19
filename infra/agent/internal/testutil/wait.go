package testutil

import (
	"context"
	"fmt"
	"time"
)

// WaitFor polls cond until it returns true or timeout elapses.
func WaitFor(timeout time.Duration, cond func() bool) error {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("WaitFor: timed out after %s", timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// WaitForCtx is WaitFor with context cancellation.
func WaitForCtx(ctx context.Context, timeout time.Duration, cond func() bool) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if cond() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("WaitForCtx: timed out after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
