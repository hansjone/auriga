// Signer modes use the existing installation key and publication lock. NAS can
// run hourly checks in an explicitly network-isolated long-lived sidecar.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hansjone/auriga/deploy/bootstrap/panel"
)

func main() {
	key := flag.String("key", "/run/panel.key", "installation private key")
	dist := flag.String("dist", "/srv/auriga/static/panel", "bundle embedded in edge image")
	out := flag.String("out", "/out", "public signed panel directory")
	check := flag.Bool("check", false, "check public manifest and renewal scheduler health without private key")
	bestEffort := flag.Bool("best-effort", false, "log startup publication failures without blocking other services")
	renew := flag.Bool("renew", false, "check immediately and hourly until terminated")
	listener := flag.String("probe-tcp", "", "check only a numeric loopback listener, independent of panel state")
	flag.Parse()
	if (*renew && (*check || *bestEffort || *listener != "")) || (*listener != "" && (*check || *bestEffort)) || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "incompatible panel operation flags")
		os.Exit(2)
	}
	if *renew {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := panel.Renew(ctx, time.Hour, func(now time.Time) error {
			return panel.PublishChecked(*key, *dist, *out, now)
		}, func(err error) {
			if errors.Is(err, panel.ErrPublicationBusy) {
				fmt.Fprintln(os.Stderr, "panel publication already in progress; skipped")
			} else if err != nil {
				fmt.Fprintln(os.Stderr, "panel renewal check failed:", err)
			} else {
				fmt.Fprintln(os.Stderr, "panel renewal check succeeded")
			}
		}); err != nil {
			fmt.Fprintln(os.Stderr, "panel renewal stopped:", err)
			os.Exit(1)
		}
		return
	}
	var err error
	if *listener != "" {
		err = panel.CheckListener(*listener)
	} else if *check {
		err = panel.CheckHealth(*out, time.Now())
	} else {
		err = panel.PublishChecked(*key, *dist, *out, time.Now())
	}
	if errors.Is(err, panel.ErrPublicationBusy) {
		fmt.Fprintln(os.Stderr, "panel publication already in progress; skipped")
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "panel publication failed:", err)
		if *check || !*bestEffort {
			os.Exit(1)
		}
		return
	}
	if !*check && *listener == "" {
		fmt.Fprintln(os.Stderr, "panel publication checked successfully")
	}
}
