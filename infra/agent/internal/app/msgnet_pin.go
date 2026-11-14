package app

import (
	"crypto/x509"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/hansjone/auriga/infra/agent/internal/config"
	"github.com/hansjone/auriga/infra/agent/internal/identity"
	trust "github.com/hansjone/auriga/infra/edge/pkg/msgnet"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// msgnet-pin emits public enrollment input for D's independent installation
// channel. It neither uploads the pin nor trusts a replacement identity.
func msgnetPublicPin(args []string, out, logs io.Writer) error {
	flags := flag.NewFlagSet("msgnet-pin", flag.ContinueOnError)
	flags.SetOutput(logs)
	configPath := flags.String("config", "", "optional node config path")
	dataRoot := flags.String("data-root", "", "existing enrolled node data root")
	target := flags.String("target-id", "", "D's fixed installation target")
	identityID := flags.String("identity-id", "", "D's independent confirmation identity")
	revision := flags.String("identity-revision", "", "D's monotonic identity revision")
	if e := flags.Parse(args); e != nil {
		return e
	}
	if flags.NArg() != 0 || *target == "" || len(*target) > 128 || *identityID == "" || len(*identityID) > 128 {
		return fmt.Errorf("target and independent identity required")
	}
	n, e := strconv.ParseUint(*revision, 10, 64)
	if e != nil || n == 0 || strconv.FormatUint(n, 10) != *revision {
		return fmt.Errorf("canonical identity revision required")
	}
	cfg, e := config.Load(*configPath)
	if e != nil {
		return e
	}
	if *dataRoot != "" {
		cfg.DataRoot = *dataRoot
	}
	// The public enrollment certificate supplies node_id. Do not read node.key.
	path := filepath.Join(cfg.AgentDir(), identity.CertFileName)
	info, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return fmt.Errorf("invalid enrolled node certificate")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	certs, e := identity.ParseCertPEM(raw)
	if e != nil || len(certs) == 0 {
		return fmt.Errorf("enrolled node certificate required")
	}
	node, e := identity.NodeIDFromCert(certs[0])
	if e != nil {
		return e
	}
	keyDir, e := identity.MsgnetIdentityDir(cfg.AgentDir(), *identityID)
	if e != nil {
		return e
	}
	if e = identity.EnsureDir(keyDir); e != nil {
		return e
	}
	key, e := identity.MsgnetConfirmationKey(keyDir)
	if e != nil {
		return e
	}
	defer clear(key)
	der, e := x509.MarshalPKIXPublicKey(key.Public())
	if e != nil {
		return e
	}
	pin := api.NodeControlPin{TargetId: *target, NodeId: node, IdentityId: *identityID, IdentityRevision: *revision, SigningSpki: base64.RawURLEncoding.EncodeToString(der)}
	public, e := trust.Bytes(pin)
	if e != nil {
		return e
	}
	_, e = fmt.Fprintln(out, string(public))
	return e
}
