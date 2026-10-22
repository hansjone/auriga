// Package renderer produces deterministic instance config files from InstanceSpec.
package renderer

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/hansjone/auriga/infra/gen/deployment"
	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

// Container paths for files mounted read-only at /run/auriga.
const (
	ContainerMetaRoot       = "/run/auriga"
	ContainerIdentityKeys   = ContainerMetaRoot + "/identity-keys.json"
	ContainerInstanceToken  = ContainerMetaRoot + "/instance.token"
	ContainerAIGatewayToken = ContainerMetaRoot + "/ai-gateway.token"
	ContainerEnvFile        = ContainerMetaRoot + "/env"
	ContainerCordisPatch    = ContainerMetaRoot + "/cordis.patch.yml"
	ContainerPackageJSON    = ContainerMetaRoot + "/package.json"
	ContainerDSHHome        = "/home/dsh/.dsh"
)

// Result is the rendered digest and key file paths (never log secret contents).
type Result struct {
	Digest             string
	IdentityKeysFile   string
	InstanceTokenFile  string
	AIGatewayTokenFile string
	EnvFile            string
	CordisPatchFile    string
	PackageJSONFile    string
}

// Options controls where files are written.
type Options struct {
	MetaDir string
	HomeDir string
	// Access is the single validated installation boundary, loaded by the agent.
	Access         deploymentconfig.Config
	SealingPrivate [32]byte
	RestrictFile   func(path string) error
	// OwnerUID/GID set after write for secret files (0 = skip).
	OwnerUID int
	OwnerGID int
	Chown    func(path string, uid, gid int) error
}

// Render writes meta/ config from spec. Same spec → same digest.
func Render(spec *nodev1.InstanceSpec, opt Options) (Result, error) {
	if spec == nil || spec.InstanceId == "" {
		return Result{}, fmt.Errorf("renderer: missing instance")
	}
	if err := validateIdentity(spec.InstanceId, "instanceId"); err != nil {
		return Result{}, err
	}
	if err := validateIdentity(spec.OwnerId, "ownerId"); err != nil {
		return Result{}, err
	}
	if err := validateIdentity(spec.TenantId, "tenantId"); err != nil {
		return Result{}, err
	}
	if !opt.Access.Valid() {
		return Result{}, fmt.Errorf("renderer: validated deployment access required")
	}
	if _, err := BrowserOrigin(spec.InstanceId, opt.Access); err != nil {
		return Result{}, err
	}
	if opt.MetaDir == "" {
		return Result{}, fmt.Errorf("renderer: MetaDir required")
	}
	if err := os.MkdirAll(opt.MetaDir, 0o700); err != nil {
		return Result{}, err
	}
	restrict := opt.RestrictFile
	if restrict == nil {
		restrict = restrictSecretFile
	}

	instanceToken, err := openSecret(opt.SealingPrivate, spec, "instance_token", spec.GetSecrets().GetInstanceToken())
	if err != nil {
		return Result{}, err
	}
	aiGatewayToken, err := openSecret(opt.SealingPrivate, spec, "ai_gateway_token", spec.GetSecrets().GetAiGatewayToken())
	if err != nil {
		return Result{}, err
	}

	pkgPath := filepath.Join(opt.MetaDir, "package.json")
	if err := writeAtomic(pkgPath, buildPackageJSON(spec), 0o644, nil, opt); err != nil {
		return Result{}, err
	}
	cordisPath := filepath.Join(opt.MetaDir, "cordis.patch.yml")
	cordisRaw, err := buildCordisPatch(spec, opt)
	if err != nil {
		return Result{}, err
	}
	if err := writeAtomic(cordisPath, cordisRaw, 0o644, nil, opt); err != nil {
		return Result{}, err
	}
	keysPath := filepath.Join(opt.MetaDir, "identity-keys.json")
	if err := writeAtomic(keysPath, buildIdentityKeys(spec), 0o644, nil, opt); err != nil {
		return Result{}, err
	}
	tokenPath := filepath.Join(opt.MetaDir, "instance.token")
	if err := writeAtomic(tokenPath, instanceToken, 0o400, restrict, opt); err != nil {
		return Result{}, err
	}
	var gwPath string
	if len(aiGatewayToken) > 0 {
		gwPath = filepath.Join(opt.MetaDir, "ai-gateway.token")
		if err := writeAtomic(gwPath, aiGatewayToken, 0o400, restrict, opt); err != nil {
			return Result{}, err
		}
	}
	envPath := filepath.Join(opt.MetaDir, "env")
	envRaw, err := buildEnvFile(spec, opt)
	if err != nil {
		return Result{}, err
	}
	if err := writeAtomic(envPath, envRaw, 0o644, nil, opt); err != nil {
		return Result{}, err
	}

	digestPaths := []string{pkgPath, cordisPath, keysPath, envPath, tokenPath}
	if gwPath != "" {
		digestPaths = append(digestPaths, gwPath)
	}
	digest, err := digestFiles(digestPaths...)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Digest:             digest,
		IdentityKeysFile:   keysPath,
		InstanceTokenFile:  tokenPath,
		AIGatewayTokenFile: gwPath,
		EnvFile:            envPath,
		CordisPatchFile:    cordisPath,
		PackageJSONFile:    pkgPath,
	}, nil
}

func openSecret(priv [32]byte, spec *nodev1.InstanceSpec, name string, sealed *nodev1.SealedSecret) ([]byte, error) {
	if sealed == nil || (len(sealed.Enc) == 0 && len(sealed.Ciphertext) == 0) {
		return nil, nil
	}
	return Unseal(priv, spec.InstanceId, name, spec.Generation, sealed)
}

func buildPackageJSON(spec *nodev1.InstanceSpec) []byte {
	type bundle struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Enabled bool   `json:"enabled"`
	}
	bundles := make([]bundle, 0, len(spec.Plugins))
	for _, p := range spec.Plugins {
		if p == nil {
			continue
		}
		bundles = append(bundles, bundle{
			Name:    p.Name,
			Version: p.Version,
			Enabled: p.Enabled || p.Level == nodev1.PluginLevel_PLUGIN_LEVEL_L0,
		})
	}
	sort.Slice(bundles, func(i, j int) bool { return bundles[i].Name < bundles[j].Name })
	doc := map[string]any{
		"name":    "auriga-instance-profile",
		"private": true,
		"dsh": map[string]any{
			"profile": map[string]any{
				"bundles": bundles,
			},
		},
	}
	raw, _ := json.MarshalIndent(doc, "", "  ")
	return append(raw, '\n')
}

func buildIdentityKeys(spec *nodev1.InstanceSpec) []byte {
	type jwk struct {
		Kty string `json:"kty"`
		Crv string `json:"crv"`
		X   string `json:"x"`
		Kid string `json:"kid"`
		Alg string `json:"alg"`
		Use string `json:"use,omitempty"`
		Nbf *int64 `json:"nbf,omitempty"`
		Exp *int64 `json:"exp,omitempty"`
	}
	keys := make([]jwk, 0)
	if sec := spec.GetSecrets(); sec != nil {
		for _, k := range sec.IdentityKeys {
			if k == nil || len(k.PublicKey) != 32 {
				continue
			}
			item := jwk{
				Kty: "OKP",
				Crv: "Ed25519",
				X:   base64.RawURLEncoding.EncodeToString(k.PublicKey),
				Kid: k.Kid,
				Alg: "EdDSA",
				Use: "sig",
			}
			if k.NotBefore != nil {
				n := k.NotBefore.AsTime().Unix()
				item.Nbf = &n
			}
			if k.NotAfter != nil {
				n := k.NotAfter.AsTime().Unix()
				item.Exp = &n
			}
			keys = append(keys, item)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Kid < keys[j].Kid })
	raw, _ := json.MarshalIndent(map[string]any{"keys": keys}, "", "  ")
	return append(raw, '\n')
}

func buildEnvFile(spec *nodev1.InstanceSpec, opt Options) ([]byte, error) {
	lines := []string{
		"AURIGA_MODE=managed",
		"AURIGA_PROTOCOL=1",
		"AURIGA_INSTANCE_ID=" + spec.InstanceId,
		"AURIGA_OWNER_ID=" + spec.OwnerId,
		"AURIGA_TENANT_ID=" + spec.TenantId,
		"AURIGA_PLATFORM_URL=" + originURL(opt.Access.InstanceControl().Origin),
		"AURIGA_IDENTITY_KEYS_FILE=" + ContainerIdentityKeys,
		"AURIGA_INSTANCE_TOKEN_FILE=" + ContainerInstanceToken,
		"DSH_HOME=" + ContainerDSHHome,
	}
	if net := spec.GetNetwork(); net != nil {
		if net.EgressProxy != "" {
			if err := rejectUnsafeString("egressProxy", net.EgressProxy); err != nil {
				return nil, err
			}
			lines = append(lines,
				"HTTPS_PROXY="+net.EgressProxy,
				"HTTP_PROXY="+net.EgressProxy,
			)
		}
		if len(net.Allow) > 0 {
			for _, a := range net.Allow {
				if err := rejectUnsafeString("network.allow", a); err != nil {
					return nil, err
				}
			}
			lines = append(lines, "NO_PROXY="+strings.Join(net.Allow, ","))
		}
	}
	if prof := spec.GetProfile(); prof != nil {
		keys := sortedKeys(prof.Env)
		for _, k := range keys {
			if err := validateEnvKey(k); err != nil {
				return nil, err
			}
			v := prof.Env[k]
			if err := rejectUnsafeString("env."+k, v); err != nil {
				return nil, err
			}
			lines = append(lines, k+"="+v)
		}
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

func writeAtomic(path string, data []byte, mode os.FileMode, restrict func(string) error, opt Options) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Windows cannot rename over a read-only / ACL-restricted target.
	_ = os.Chmod(path, 0o600)
	_ = os.Remove(path)
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if restrict != nil {
		if err := restrict(path); err != nil {
			return err
		}
	}
	if opt.OwnerUID > 0 && opt.Chown != nil {
		if err := opt.Chown(path, opt.OwnerUID, opt.OwnerGID); err != nil {
			return err
		}
	}
	return nil
}

func digestFiles(paths ...string) (string, error) {
	h := sha256.New()
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		_, _ = h.Write([]byte(filepath.Base(p)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(raw)
		_, _ = h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysMap[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// BrowserOrigin derives only the browser boundary; the internal alias never enters it.
func BrowserOrigin(instanceID string, access deploymentconfig.Config) (deployment.Origin, error) {
	if !access.Valid() {
		return deployment.Origin{}, fmt.Errorf("renderer: validated deployment access required")
	}
	if sub, ok := access.InstanceSubdomain(); ok {
		if !hostLabelRe.MatchString(instanceID) || strings.Contains(instanceID, "_") || strings.HasPrefix(instanceID, "-") || strings.HasSuffix(instanceID, "-") || len(instanceID)+1+len(sub.ParentDomain) > 253 {
			return deployment.Origin{}, fmt.Errorf("renderer: instance id is not a canonical DNS label")
		}
		return deployment.Origin{Scheme: sub.Scheme, Host: instanceID + "." + sub.ParentDomain, Port: sub.Port}, nil
	}
	return access.PortalOrigin(), nil
}

func originURL(o deployment.Origin) string {
	authority := net.JoinHostPort(o.Host, strconv.Itoa(int(o.Port)))
	if o.Scheme == "http" && o.Port == 80 || o.Scheme == "https" && o.Port == 443 {
		authority = o.Host
	}
	return o.Scheme + "://" + authority
}
