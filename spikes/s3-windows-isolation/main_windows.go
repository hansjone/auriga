//go:build windows && amd64

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"golang.org/x/sys/windows"
)

type Check struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Evidence any    `json:"evidence,omitempty"`
}
type Result struct {
	Experiment string  `json:"experiment"`
	Status     string  `json:"status"`
	Checks     []Check `json:"checks"`
	Error      string  `json:"error,omitempty"`
	DurationMS int64   `json:"durationMs"`
}

func (r *Result) check(name string, ok bool, evidence any) {
	s := "fail"
	if ok {
		s = "pass"
	}
	r.Checks = append(r.Checks, Check{name, s, evidence})
}
func (r *Result) observe(name string, evidence any) {
	r.Checks = append(r.Checks, Check{name, "observed", evidence})
}
func (r *Result) unsupported(name string, evidence any) {
	r.Checks = append(r.Checks, Check{name, "unsupported", evidence})
}
func (r *Result) finish(err error) {
	r.Status = aggregate(r.Checks, err)
	if err != nil {
		r.Error = err.Error()
	}
}
func aggregate(checks []Check, err error) string {
	if err != nil || len(checks) == 0 {
		return "error"
	}
	status := "pass"
	for _, c := range checks {
		if c.Status == "fail" {
			return "fail"
		}
		if c.Status == "unsupported" {
			status = "unsupported"
		}
	}
	return status
}
func emit(v any) { _ = json.NewEncoder(os.Stdout).Encode(v) }

var validID = regexp.MustCompile(`^[a-f0-9]{10}$`)

type options struct{ Root, ID, Node string }

func validateOptions(o options) error {
	if !validID.MatchString(o.ID) {
		return errors.New("id must contain exactly ten lowercase hex digits")
	}
	expected := filepath.Join(os.Getenv("ProgramData"), "auriga-s3-"+o.ID)
	if !filepath.IsAbs(o.Root) || filepath.Clean(o.Root) != filepath.Clean(expected) {
		return errors.New("root must be ProgramData/auriga-s3-<id>")
	}
	return nil
}
func gate(ack bool, elevated bool) error {
	if !ack || os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" {
		return errors.New("privileged experiments require --ci-disposable-vm on a GitHub-hosted disposable runner")
	}
	if elevated && !windows.GetCurrentProcessToken().IsElevated() {
		return errors.New("elevated administrator or SYSTEM token required")
	}
	return nil
}
func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "s3probe identity|job|shim|wfp|acl|quota|appcontainer|cleanup --ci-disposable-vm --id <10 hex> --root <ProgramData/auriga-s3-id> [--node <node.exe>]")
		os.Exit(2)
	}
	mode := os.Args[1]
	// Internal workloads inherit the CI environment, but never need administrator rights.
	if mode == "_work" || mode == "_shim" || mode == "_controller" || mode == "_wfp-observer" || mode == "_wfp-install" {
		if err := gate(true, false); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		switch mode {
		case "_work":
			worker(os.Args[2:])
		case "_shim":
			shimWorker(os.Args[2:])
		case "_controller":
			controllerWorker(os.Args[2:])
		case "_wfp-observer":
			wfpObserver(os.Args[2:])
		case "_wfp-install":
			wfpInstaller()
		}
		return
	}
	fs := flag.NewFlagSet(mode, flag.ExitOnError)
	var o options
	ack := fs.Bool("ci-disposable-vm", false, "only disposable GitHub-hosted Windows VM")
	fs.StringVar(&o.Root, "root", "", "run root")
	fs.StringVar(&o.ID, "id", "", "run ID")
	fs.StringVar(&o.Node, "node", "", "Node executable")
	_ = fs.Parse(os.Args[2:])
	r := Result{Experiment: mode, Checks: []Check{}}
	start := time.Now()
	err := gate(*ack, true)
	if err == nil {
		err = validateOptions(o)
	}
	if err == nil {
		switch mode {
		case "identity":
			err = identityExperiment(o, &r)
		case "job":
			err = jobExperiment(o, &r)
		case "shim":
			err = shimExperiment(o, &r)
		case "wfp":
			err = wfpExperiment(o, &r)
		case "acl":
			err = aclExperiment(o, &r)
		case "quota":
			err = quotaExperiment(o, &r)
		case "appcontainer":
			err = appContainerExperiment(o, &r)
		case "cleanup":
			err = cleanupExperiment(o, &r)
		default:
			err = fmt.Errorf("unknown experiment %q", mode)
		}
	}
	r.DurationMS = time.Since(start).Milliseconds()
	r.finish(err)
	emit(r)
	if r.Status == "error" || r.Status == "fail" {
		os.Exit(1)
	}
}
