package runtimebundle

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/releasetrust"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// PublisherRootOptions requires an existing public issuer key and explicitly
// approved metadata. It never generates a key, expiry, identifier or sequence.
type PublisherRootOptions struct {
	PublicKeyPath string
	Kid           string
	NotAfter      *timestamppb.Timestamp
	OutputPath    string
}

// WritePublisherRoot writes only the existing ArtifactSigningKey protobuf to a
// new file. The returned bytes are public metadata, not a signature or evidence
// that an installation trusts the key. Trust is still an independent input.
func WritePublisherRoot(o PublisherRootOptions) ([]byte, error) {
	raw, err := writePublisherRoot(o)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return raw, nil
}

func writePublisherRoot(o PublisherRootOptions) ([]byte, error) {
	public, err := publisherPublicKey(o.PublicKeyPath)
	if err != nil {
		return nil, err
	}
	key := &nodev1.ArtifactSigningKey{Kid: o.Kid, PublicKey: bytes.Clone(public)}
	if o.NotAfter != nil {
		key.NotAfter = proto.Clone(o.NotAfter).(*timestamppb.Timestamp)
	}
	// Use the exact production root shape/expiry policy, with no new validator.
	if _, err := releasetrust.Merge(releasetrust.Ring{}, []*nodev1.ArtifactSigningKey{key}, true, time.Now()); err != nil {
		return nil, err
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(key)
	if err != nil || len(raw) > MaxRootBytes {
		return nil, fmt.Errorf("publisher root cannot marshal within the root size limit")
	}
	absolute, err := absolutePath(o.OutputPath)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(absolute)
	if !filepath.IsLocal(name) || strings.ContainsAny(name, `\/:`) {
		return nil, fmt.Errorf("output must be an ordinary file name")
	}
	parent, err := openDirectory(filepath.Dir(absolute))
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()
	// The pinned parent and O_EXCL refuse existing files, directories and links.
	// Later failure keeps the new partial public file for diagnosis, never repair.
	f, err := parent.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, fmt.Errorf("publisher root output must be a new file: %w", err)
	}
	defer func() { _ = f.Close() }()
	created, err := f.Stat()
	if err != nil || !created.Mode().IsRegular() {
		return nil, fmt.Errorf("publisher root output is not an ordinary file")
	}
	if err := singleLink(f); err != nil {
		return nil, err
	}
	if n, err := f.Write(raw); err != nil || n != len(raw) {
		return nil, fmt.Errorf("cannot write complete publisher root")
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	// The original reader enforces canonical protobuf, OS file identity, root
	// shape and current expiry again before the command reports success.
	checked, parsed, _, observed, err := readRoot(absolute)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(created, observed) || !bytes.Equal(raw, checked) || !proto.Equal(key, parsed) {
		return nil, fmt.Errorf("publisher root output changed during verification")
	}
	return bytes.Clone(raw), nil
}

func publisherPublicKey(path string) (ed25519.PublicKey, error) {
	absolute, err := absolutePath(path)
	if err != nil {
		return nil, err
	}
	f, err := openOrdinary(absolute, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > MaxRootBytes {
		return nil, fmt.Errorf("public key must be a bounded ordinary SPKI PEM file")
	}
	if err := singleLink(f); err != nil {
		return nil, err
	}
	// Stop on the first non-public marker byte. In particular, a PRIVATE KEY
	// marker is rejected before its body, without a private-key parser or loader.
	var next [1]byte
	for _, expected := range []byte("-----BEGIN PUBLIC KEY-----") {
		if _, err := io.ReadFull(f, next[:]); err != nil || next[0] != expected {
			return nil, fmt.Errorf("expected a PUBLIC KEY SPKI PEM object")
		}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	raw, err := readBounded(f, MaxRootBytes)
	if err != nil {
		return nil, err
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PUBLIC KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("expected exactly one unencrypted PUBLIC KEY SPKI PEM object")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("invalid public SPKI")
	}
	public, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("publisher public key must be Ed25519")
	}
	canonical, err := x509.MarshalPKIXPublicKey(public)
	if err != nil || !bytes.Equal(canonical, block.Bytes) {
		return nil, fmt.Errorf("publisher public SPKI is not canonical")
	}
	return bytes.Clone(public), nil
}
