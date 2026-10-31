package panel

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"
)

// Renew checks immediately and periodically with the original publication lock.
// A failed check remains observable and does not stop subsequent renewal checks.
func Renew(ctx context.Context, interval time.Duration, check func(time.Time) error, report func(error)) error {
	if interval <= 0 {
		return fmt.Errorf("renewal interval must be positive")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		report(check(time.Now()))
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// CheckListener is edge process liveness, independent of panel publication or
// scheduler status. It never resolves a hostname or connects off loopback.
func CheckListener(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("health listener must use a numeric loopback address")
	}
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return fmt.Errorf("invalid health listener port")
	}
	connection, err := net.DialTimeout("tcp", address, 4*time.Second)
	if err != nil {
		return fmt.Errorf("edge listener unavailable: %w", err)
	}
	return connection.Close()
}
