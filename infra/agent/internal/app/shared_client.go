package app

import (
	"encoding/json"
	"fmt"
	"github.com/hansjone/auriga/infra/agent/internal/config"
	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/sharednode"
	"github.com/hansjone/auriga/infra/agent/internal/tlsutil"
	"os"
	"path/filepath"
)

func openSharedClient(cfg config.Config) (*sharednode.Client, error) {
	if cfg.SharedDshRuntime == nil {
		return nil, fmt.Errorf("shared runtime not installed")
	}
	path := cfg.SharedDshRuntime.ServicesFile
	info, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !filepath.IsAbs(path) || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > 256*1024 {
		return nil, fmt.Errorf("protected shared services file required")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var s sharednode.Settings
	if e = json.Unmarshal(raw, &s); e != nil {
		return nil, e
	}
	mat, e := identity.Load(cfg.AgentDir())
	if e != nil {
		return nil, e
	}
	key, e := marshalECDSAKeyPEM(mat.PrivateKey)
	if e != nil {
		return nil, e
	}
	cert, e := tlsutil.LoadTLSCertificate(mat.CertificatePEM, key)
	if e != nil {
		return nil, e
	}
	return sharednode.Open(s, cert)
}
