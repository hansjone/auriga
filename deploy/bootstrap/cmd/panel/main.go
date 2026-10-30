// The short-lived, network-isolated signer is the only container receiving the
// installation panel private key. Long-lived services get public files only.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hansjone/auriga/deploy/bootstrap/panel"
)

func main() {
	key := flag.String("key", "/run/panel.key", "installation private key")
	dist := flag.String("dist", "/srv/auriga/static/panel", "bundle embedded in edge image")
	out := flag.String("out", "/out", "public signed panel directory")
	versionFile := flag.String("version-file", "/srv/auriga/static/panel/version.txt", "release version embedded in image")
	flag.Parse()
	raw, err := os.ReadFile(*versionFile)
	if err == nil {
		err = panel.Publish(*key, *dist, *out, strings.TrimSpace(string(raw)), time.Now())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "panel publication failed:", err)
		os.Exit(1)
	}
}
