package observer

import "time"

// Clock is injectable for deterministic tests.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// FixedClock returns a constant time (tests).
type FixedClock struct{ T time.Time }

func (c FixedClock) Now() time.Time { return c.T }

// MutableClock allows advancing time in tests.
type MutableClock struct{ T time.Time }

func (c *MutableClock) Now() time.Time { return c.T }

func (c *MutableClock) Advance(d time.Duration) { c.T = c.T.Add(d) }
