// The short-lived, network-isolated signer is the only container receiving the
// installation panel private key. Long-lived services get public files only.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/hansjone/auriga/deploy/bootstrap/panel"
)

func main() {
	key := flag.String("key", "/run/panel.key", "installation private key")
	dist := flag.String("dist", "/srv/auriga/static/panel", "bundle embedded in edge image")
	out := flag.String("out", "/out", "public signed panel directory")
	check := flag.Bool("check", false, "check public manifest and renewal scheduler health without private key")
	bestEffort := flag.Bool("best-effort", false, "log startup publication failures without blocking other services")
	flag.Parse()
	var err error
	if *check {
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
	if !*check {
		fmt.Fprintln(os.Stderr, "panel publication checked successfully")
	}
}
