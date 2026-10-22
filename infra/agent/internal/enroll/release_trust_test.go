package enroll_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/enroll"
	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/testutil"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func runtimeRoot(t *testing.T) (*nodev1.ArtifactSigningKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &nodev1.ArtifactSigningKey{Kid: "root", PublicKey: pub, NotAfter: timestamppb.New(time.Now().Add(time.Hour))}, priv
}
func runtimeChild(t *testing.T, id string, parent *nodev1.ArtifactSigningKey, signer ed25519.PrivateKey) *nodev1.ArtifactSigningKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &nodev1.ArtifactSigningKey{Kid: id, PublicKey: pub, NotAfter: timestamppb.New(time.Now().Add(2 * time.Hour)), EndorsedByKid: parent.Kid, Endorsement: ed25519.Sign(signer, append([]byte(id), pub...))}
}
func startTLS(t *testing.T, keys []*nodev1.ArtifactSigningKey) (*testutil.FakeWorkspace, enroll.Options) {
	t.Helper()
	fw, err := testutil.StartFakeWorkspace(testutil.FakeWorkspaceOpts{TLS: true, ArtifactSigningKeys: keys})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fw.Close)
	return fw, enroll.Options{Server: fw.URL(), Token: fw.Token(), CAFingerprint: fw.CA.Fingerprint(), AgentDir: filepath.Join(t.TempDir(), "agent"), AllowInsecure: true}
}
func enrollmentContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestTLSJoinRenewTrustAndRestart(t *testing.T) {
	root, priv := runtimeRoot(t)
	fw, opts := startTLS(t, []*nodev1.ArtifactSigningKey{root})
	ctx := enrollmentContext(t)
	mat, err := enroll.Join(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(opts.AgentDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	opts.Store = st
	ring, err := st.GetReleaseTrust()
	if err != nil || len(ring.Records) != 1 {
		t.Fatalf("TLS with AllowInsecure flag did not establish root: %v", err)
	}
	old := bytes.Clone(mat.CertificatePEM)
	child := runtimeChild(t, "child", root, priv)
	bad := proto.Clone(child).(*nodev1.ArtifactSigningKey)
	bad.Endorsement[0] ^= 1
	fw.SetArtifactSigningKeys([]*nodev1.ArtifactSigningKey{bad})
	renewed, err := enroll.RenewCertificate(ctx, opts, true)
	var trustErr *enroll.TrustUpdateError
	if renewed == nil || !errors.As(err, &trustErr) {
		t.Fatalf("missing split renewal result: %v", err)
	}
	if bytes.Equal(old, renewed.CertificatePEM) {
		t.Fatal("valid certificate not replaced")
	}
	disk, err := identity.Load(opts.AgentDir)
	if err != nil || !bytes.Equal(disk.CertificatePEM, renewed.CertificatePEM) {
		t.Fatal("renewed certificate not persisted")
	}
	ring, err = st.GetReleaseTrust()
	if err != nil || len(ring.Records) != 1 {
		t.Fatal("bad keys changed old ring")
	}
	fw.SetArtifactSigningKeys([]*nodev1.ArtifactSigningKey{child, root})
	if _, err := enroll.RenewCertificate(ctx, opts, true); err != nil {
		t.Fatal(err)
	}
	// Omission, order, and file mtimes cannot revoke or replace accepted keys.
	fw.SetArtifactSigningKeys([]*nodev1.ArtifactSigningKey{root})
	if _, err := enroll.RenewCertificate(ctx, opts, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(opts.AgentDir, "state.db"), time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = state.Open(opts.AgentDir)
	if err != nil {
		t.Fatal(err)
	}
	opts.Store = st
	ring, err = st.GetReleaseTrust()
	if err != nil || len(ring.Records) != 2 {
		t.Fatalf("restart/omission lost ring: %v", err)
	}
	conflict := proto.Clone(child).(*nodev1.ArtifactSigningKey)
	conflict.NotAfter.Nanos = (conflict.NotAfter.Nanos + 1) % 1000000000
	fw.SetArtifactSigningKeys([]*nodev1.ArtifactSigningKey{conflict})
	if mat, err := enroll.RenewCertificate(ctx, opts, true); mat == nil || !errors.As(err, &trustErr) {
		t.Fatalf("expiry conflict accepted: %v", err)
	}
}

func TestSecureJoinRejectsEmptyBadRootsAndPin(t *testing.T) {
	for _, kind := range []string{"empty", "bad public key", "expired", "unknown protobuf", "wrong pin"} {
		t.Run(kind, func(t *testing.T) {
			root, _ := runtimeRoot(t)
			keys := []*nodev1.ArtifactSigningKey{root}
			switch kind {
			case "empty":
				keys = nil
			case "bad public key":
				root.PublicKey = root.PublicKey[:31]
			case "expired":
				root.NotAfter = timestamppb.New(time.Now().Add(-time.Second))
			case "unknown protobuf":
				root.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 1})
			}
			_, opts := startTLS(t, keys)
			if kind == "wrong pin" {
				opts.CAFingerprint = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if m, err := enroll.Join(ctx, opts); err == nil || m != nil {
				t.Fatal("invalid secure bootstrap succeeded")
			}
			for _, name := range []string{identity.KeyFileName, identity.CertFileName, identity.TrustGuardFileName, "state.db"} {
				if _, err := os.Lstat(filepath.Join(opts.AgentDir, name)); !os.IsNotExist(err) {
					t.Fatalf("failed bootstrap wrote %s: %v", name, err)
				}
			}
		})
	}
}

func TestPlaintextNeverBootstrapsAndCannotUpgrade(t *testing.T) {
	root, _ := runtimeRoot(t)
	fw, err := testutil.StartFakeWorkspace(testutil.FakeWorkspaceOpts{ArtifactSigningKeys: []*nodev1.ArtifactSigningKey{root}})
	if err != nil {
		t.Fatal(err)
	}
	defer fw.Close()
	opts := enroll.Options{Server: fw.URL(), Token: fw.Token(), CAFingerprint: fw.CA.Fingerprint(), AgentDir: filepath.Join(t.TempDir(), "agent"), AllowInsecure: true}
	ctx := enrollmentContext(t)
	if _, err := enroll.Join(ctx, opts); err != nil {
		t.Fatal(err)
	}
	guard, err := identity.ReadTrustGuard(opts.AgentDir)
	if err != nil || guard.Trusted || guard.Roots != "" {
		t.Fatalf("plaintext acquired roots: %+v %v", guard, err)
	}
	st, err := state.Open(opts.AgentDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	opts.Store = st
	if _, err := st.GetReleaseTrust(); err == nil {
		t.Fatal("plaintext trusted")
	}
	var split *enroll.TrustUpdateError
	if m, err := enroll.RenewCertificate(ctx, opts, true); m == nil || !errors.As(err, &split) {
		t.Fatalf("plaintext renewal bootstrapped root or lost certificate: %v", err)
	}
	opts.Force = true
	if _, err := enroll.Join(ctx, opts); err == nil {
		t.Fatal("force upgraded plaintext trust")
	}
}

func TestJoinRejectsHistoricalTracesBeforeRPC(t *testing.T) {
	for _, name := range []string{identity.KeyFileName, identity.CertFileName, identity.CAFileName, identity.SealingFileName, identity.TrustGuardFileName, "state.db", ".node.key.tmp.crashed"} {
		for _, force := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/normal", true: "/force"}[force], func(t *testing.T) {
				root, _ := runtimeRoot(t)
				fw, opts := startTLS(t, []*nodev1.ArtifactSigningKey{root})
				opts.Force = force
				if err := os.MkdirAll(opts.AgentDir, 0o700); err != nil {
					t.Fatal(err)
				}
				p := filepath.Join(opts.AgentDir, name)
				before := []byte("historical evidence")
				if err := os.WriteFile(p, before, 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := enroll.Join(enrollmentContext(t), opts); err == nil {
					t.Fatal("historical evidence ignored")
				}
				after, err := os.ReadFile(p)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("evidence changed")
				}
				fresh := opts
				fresh.AgentDir = filepath.Join(t.TempDir(), "fresh")
				fresh.Token = fw.Token()
				if _, err := enroll.Join(enrollmentContext(t), fresh); err != nil {
					t.Fatalf("locally rejected attempt consumed token: %v", err)
				}
			})
		}
	}
}

func TestMissingDatabaseAndLegacyIdentityCannotTOFU(t *testing.T) {
	for _, missing := range []string{"state.db", identity.TrustGuardFileName} {
		t.Run(missing, func(t *testing.T) {
			root, _ := runtimeRoot(t)
			_, opts := startTLS(t, []*nodev1.ArtifactSigningKey{root})
			ctx := enrollmentContext(t)
			if _, err := enroll.Join(ctx, opts); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(opts.AgentDir, missing)); err != nil {
				t.Fatal(err)
			}
			opts.Force = true
			if _, err := enroll.Join(ctx, opts); err == nil {
				t.Fatal("force recovered by TOFU")
			}
			m, err := enroll.RenewCertificate(ctx, opts, true)
			if err == nil {
				t.Fatal("lost state silently recreated trust")
			}
			if missing == identity.TrustGuardFileName && m == nil {
				t.Fatal("legacy identity lost valid certificate renewal")
			}
			if missing == "state.db" {
				if _, err := os.Stat(filepath.Join(opts.AgentDir, missing)); !os.IsNotExist(err) {
					t.Fatal("missing state recreated")
				}
			}
		})
	}
}

func TestTwoAuthenticatedRenewConnectionsMerge(t *testing.T) {
	root, priv := runtimeRoot(t)
	a, b := runtimeChild(t, "a", root, priv), runtimeChild(t, "b", root, priv)
	var mu sync.Mutex
	var requests int
	ready := make(chan struct{})
	var fw *testutil.FakeWorkspace
	var err error
	fw, err = testutil.StartFakeWorkspace(testutil.FakeWorkspaceOpts{TLS: true, ArtifactSigningKeys: []*nodev1.ArtifactSigningKey{root}, RenewResponse: func(resp *nodev1.RenewCertificateResponse) {
		mu.Lock()
		requests++
		n := requests
		if n == 2 {
			close(ready)
		}
		mu.Unlock()
		<-ready
		certs, e := identity.ParseCertPEM(resp.CertificateChain)
		if e != nil {
			t.Error(e)
			return
		}
		expiry := time.Now().Truncate(time.Second).Add(time.Duration(n+2) * time.Hour)
		cert, e := fw.CA.IssueNode("n_test01", certs[0].PublicKey.(*ecdsa.PublicKey), expiry)
		if e != nil {
			t.Error(e)
			return
		}
		resp.CertificateChain = cert
		resp.CertificateExpiresAt = timestamppb.New(expiry)
		if n == 1 {
			resp.ArtifactSigningKeys = []*nodev1.ArtifactSigningKey{a}
		} else {
			resp.ArtifactSigningKeys = []*nodev1.ArtifactSigningKey{b}
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer fw.Close()
	ctx := enrollmentContext(t)
	opts := enroll.Options{Server: fw.URL(), Token: fw.Token(), CAFingerprint: fw.CA.Fingerprint(), AgentDir: filepath.Join(t.TempDir(), "agent")}
	if _, err := enroll.Join(ctx, opts); err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(opts.AgentDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	opts.Store = st
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := enroll.RenewCertificate(ctx, opts, true); errs <- e }()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent TLS renewal persistence deadlocked")
	}
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	ring, err := st.GetReleaseTrust()
	if err != nil || len(ring.Records) != 3 {
		t.Fatalf("two authenticated connections lost merged keys: %v", err)
	}
	disk, err := identity.Load(opts.AgentDir)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := st.GetNodeMeta()
	if err != nil {
		t.Fatal(err)
	}
	if !disk.CertificateExpiresAt.Equal(meta.ExpiresAt) || disk.NodeID != meta.NodeID {
		t.Fatalf("final certificate/metadata mismatch: %s vs %s", disk.CertificateExpiresAt, meta.ExpiresAt)
	}
}
func TestTLSJoinRejectsTemporaryHistoryInLiteralParentBeforeRPC(t *testing.T) {
	parents := []string{"legacy[1]"}
	if runtime.GOOS != "windows" {
		parents = append(parents, "legacy*?[1]")
	}
	for _, parent := range parents {
		for _, force := range []bool{false, true} {
			t.Run(parent+map[bool]string{false: "/normal", true: "/force"}[force], func(t *testing.T) {
				root, _ := runtimeRoot(t)
				fw, opts := startTLS(t, []*nodev1.ArtifactSigningKey{root})
				opts.AgentDir = filepath.Join(t.TempDir(), parent, "agent")
				opts.Force = force
				if err := os.MkdirAll(opts.AgentDir, 0o700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(opts.AgentDir, ".node.key.tmp.crashed")
				want := []byte("historical partial identity")
				if err := os.WriteFile(path, want, 0o600); err != nil {
					t.Fatal(err)
				}
				before, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if mat, err := enroll.Join(enrollmentContext(t), opts); err == nil || mat != nil {
					t.Fatal("real CA-pin TLS Join accepted temporary historical identity")
				}
				entries, err := os.ReadDir(opts.AgentDir)
				if err != nil || len(entries) != 1 || entries[0].Name() != ".node.key.tmp.crashed" {
					t.Fatal("rejected join wrote identity/guard/state")
				}
				after, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) || after.Mode() != before.Mode() {
					t.Fatal("rejected join changed old evidence")
				}
				// The real service consumes one-time tokens only on Join. Success in a
				// separate empty directory proves the rejected call never reached its RPC.
				fresh := opts
				fresh.AgentDir = filepath.Join(t.TempDir(), "fresh")
				fresh.Token = fw.Token()
				if _, err := enroll.Join(enrollmentContext(t), fresh); err != nil {
					t.Fatalf("local rejection consumed token before RPC: %v", err)
				}
			})
		}
	}
}
func TestTLSJoinSymlinkTargetsStayConsistent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows tests do not request privilege to create symlinks")
	}
	for _, ambiguous := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal alias succeeds", true: "symlink parent rejected before RPC"}[ambiguous], func(t *testing.T) {
			root, _ := runtimeRoot(t)
			fw, opts := startTLS(t, []*nodev1.ArtifactSigningKey{root})
			base := t.TempDir()
			target := filepath.Join(base, "actual", "inside")
			if err := os.MkdirAll(target, 0o700); err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(base, "alias[1]")
			if err := os.Symlink(target, alias); err != nil {
				t.Fatal(err)
			}
			opts.AgentDir = alias + "/new?/deep*/agent"
			if ambiguous {
				opts.AgentDir = alias + "/../new?/deep*/agent"
			}
			mat, err := enroll.Join(enrollmentContext(t), opts)
			if ambiguous {
				if err == nil || mat != nil || !strings.Contains(err.Error(), "ambiguous symlink/parent traversal") {
					t.Fatalf("ambiguous initialization did not fail before writes: %v", err)
				}
				for _, path := range []string{filepath.Join(base, "actual", "new?"), filepath.Join(base, "new?")} {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatalf("rejected path created directory %s: %v", path, err)
					}
				}
				fresh := opts
				fresh.AgentDir = filepath.Join(t.TempDir(), "fresh")
				fresh.Token = fw.Token()
				if _, err := enroll.Join(enrollmentContext(t), fresh); err != nil {
					t.Fatalf("ambiguous path consumed token before RPC: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			physical := filepath.Join(target, "new?", "deep*", "agent")
			for _, name := range []string{identity.KeyFileName, identity.CertFileName, identity.CAFileName, identity.SealingFileName, identity.TrustGuardFileName, "state.db"} {
				if _, err := os.Stat(filepath.Join(physical, name)); err != nil {
					t.Fatalf("alias file escaped physical identity directory: %s: %v", name, err)
				}
			}
			st, err := state.Open(opts.AgentDir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = st.Close() }()
			ring, err := st.GetReleaseTrust()
			if err != nil || len(ring.Records) != 1 {
				t.Fatalf("Join guard/DB/identity paths diverged: %v", err)
			}
			meta, err := st.GetNodeMeta()
			if err != nil || meta.NodeID != mat.NodeID {
				t.Fatal("alias meta identity mismatch")
			}
		})
	}
}
