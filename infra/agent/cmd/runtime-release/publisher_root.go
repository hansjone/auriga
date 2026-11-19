package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/hansjone/auriga/infra/agent/runtimebundle"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type publisherRootString struct {
	name, value string
	present     bool
}

func (s *publisherRootString) String() string { return s.value }

func (s *publisherRootString) Set(value string) error {
	if s.present {
		return fmt.Errorf("duplicate --%s", s.name)
	}
	s.present, s.value = true, value
	return nil
}

func publisherRootCommand(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runtime-release publisher-root", flag.ContinueOnError)
	fs.SetOutput(stderr)
	public := &publisherRootString{name: "public-key"}
	kid := &publisherRootString{name: "kid"}
	notAfter := &publisherRootString{name: "not-after"}
	out := &publisherRootString{name: "out"}
	fs.Var(public, public.name, "existing independently approved Ed25519 SPKI PUBLIC KEY PEM file; no private key")
	fs.Var(kid, kid.name, "explicit publisher-approved key identifier")
	fs.Var(notAfter, notAfter.name, "explicit approved future canonical UTC RFC3339 timestamp")
	fs.Var(out, out.name, "new external root.pb file; parent must already exist")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "usage: runtime-release publisher-root --public-key <SPKI.pem> --kid <approved-kid> --not-after <UTC-Z> --out <new-root.pb>")
		_, _ = fmt.Fprintln(stderr, "Prepares public metadata only; does not generate keys, sign, or install trust.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	for _, option := range []*publisherRootString{public, kid, notAfter, out} {
		if !option.present || option.value == "" {
			return fmt.Errorf("explicit --%s is required", option.name)
		}
	}
	expiry, err := time.Parse(time.RFC3339Nano, notAfter.value)
	if err != nil || expiry.UTC().Format(time.RFC3339Nano) != notAfter.value {
		return fmt.Errorf("not-after must be canonical UTC RFC3339 with Z and shortest fractional seconds")
	}
	raw, err := runtimebundle.WritePublisherRoot(runtimebundle.PublisherRootOptions{
		PublicKeyPath: public.value, Kid: kid.value, NotAfter: timestamppb.New(expiry), OutputPath: out.value,
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "prepared publisher-root kid=%s not_after=%s sha256=%x\n", kid.value, notAfter.value, sha256.Sum256(raw))
	return err
}
