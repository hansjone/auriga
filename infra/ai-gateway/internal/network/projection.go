package network

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hansjone/auriga/deploy/bootstrap/collab/projection"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

type ProjectionConfig struct {
	MaterialsDir        string `json:"materialsDir"`
	ControlDir          string `json:"controlDir"`
	StateDir            string `json:"stateDir"`
	SchemaFile          string `json:"schemaFile"`
	RootSPKI            string `json:"rootSpki"`
	TenantID            string `json:"tenantId"`
	Realm               string `json:"realm"`
	PinsFile            string `json:"pinsFile"`
	InstallationID      string `json:"installationId"`
	FenceOrigin         string `json:"fenceOrigin"`
	FenceCAFile         string `json:"fenceCAFile"`
	ServiceTokenFile    string `json:"serviceTokenFile"`
	FenceClientCertFile string `json:"fenceClientCertFile,omitempty"`
	FenceClientKeyFile  string `json:"fenceClientKeyFile,omitempty"`
	ControlSnapshotFile string `json:"controlSnapshotFile,omitempty"`
}
type projectionRuntime struct {
	reader           *projection.Reader
	trust            *projection.Trust
	snapshot         atomic.Pointer[projection.Snapshot]
	onSwap           func()
	handshakes       chan handshakeObservation
	handshakeConns   sync.Map
	localControlFile string
}
type handshakeObservation struct {
	Selector api.LocalProjectionSelector
	Role     string
}

func startProjection(ctx context.Context, c ProjectionConfig, signer *errorSigner) (*projectionRuntime, error) {
	schemas, e := projection.LoadSchemas(c.SchemaFile)
	if e != nil {
		return nil, e
	}
	raw, e := os.ReadFile(c.PinsFile)
	if e != nil {
		return nil, e
	}
	var pins api.InstallationPins
	if e = schemas.Decode("InstallationPins", raw, &pins); e != nil {
		return nil, e
	}
	if e = projection.ValidatePins(pins); e != nil {
		return nil, e
	}
	if pins.TenantId != c.TenantID || pins.Realm != c.Realm {
		return nil, errors.New("network: installation scope mismatch")
	}
	var pin *api.InstallationPin
	for j := range pins.Pins {
		if pins.Pins[j].IdentityId == c.InstallationID && pins.Pins[j].Component == "network" {
			pin = &pins.Pins[j]
		}
	}
	if pin == nil {
		return nil, errors.New("network: network installation pin required")
	}
	if c.StateDir == "" || filepath.Clean(c.StateDir) == filepath.Clean(c.ControlDir) || filepath.Clean(c.StateDir) == filepath.Clean(c.MaterialsDir) {
		return nil, errors.New("network: separate reader state required")
	}
	trust, e := projection.NewTrust(schemas, c.RootSPKI, c.TenantID, c.Realm, c.StateDir, *pin)
	if e != nil {
		return nil, e
	}
	r := &projectionRuntime{trust: trust, handshakes: make(chan handshakeObservation, 8), localControlFile: c.ControlSnapshotFile}
	request, e := projection.NewTransportHTTP(c.FenceOrigin, c.FenceCAFile, c.FenceClientCertFile, c.FenceClientKeyFile, c.ServiceTokenFile)
	if e != nil {
		return nil, e
	}
	source := &projection.TransportSource{Schemas: schemas, Root: c.RootSPKI, Tenant: c.TenantID, Realm: c.Realm, Target: pin.TargetId, Request: request}
	source.Bind(ctx, trust)
	fence, e := publicationFence(ctx, c, schemas, trust, func() *projection.Snapshot { return r.snapshot.Load() })
	if e != nil {
		return nil, e
	}
	reader, e := projection.NewReader(c.MaterialsDir, c.ControlDir, c.StateDir, func(g *projection.Generation) (func() error, error) {
		snap, e := trust.Load(g)
		if e != nil {
			return nil, e
		}
		if snap.Protocol == nil {
			return nil, errors.New("network: protocol material required")
		}
		var key api.PublicKey
		for _, k := range snap.Publication.Keyset.Payload.Keys {
			if k.Kid == snap.Protocol.ActiveKid {
				key = k
			}
		}
		if time.Now().Before(parseInstant(key.NotBefore)) || !time.Now().Before(parseInstant(key.NotAfter)) {
			snap.Clear()
			return nil, errors.New("network: active protocol key expired")
		}
		next := &errorSigner{key: snap.Key, tenant: snap.Publication.Keyset.Payload.TenantId, realm: snap.Publication.Keyset.Payload.Realm, cfg: ProtocolSigningConfig{KeyID: key.Kid, KeysetVersion: snap.Selector.KeysetVersion, NotBefore: key.NotBefore, NotAfter: key.NotAfter}, begin: parseInstant(key.NotBefore), end: parseInstant(key.NotAfter)}
		return func() error {
			signer.mu.Lock()
			defer signer.mu.Unlock()
			old := signer.current
			r.snapshot.Store(snap)
			signer.current = next
			signer.fence = func() error {
				err := fence()
				r.reader.RecordHealth(err)
				if err != nil {
					return err
				}
				if r.reader.Withdrawn(time.Now()) {
					return errors.New("network: projection unavailable")
				}
				return nil
			}
			if old != nil {
				clear(old.key)
			}
			if r.onSwap != nil {
				r.onSwap()
			}
			return trust.RecordLoaded(snap.Selector, false)
		}, nil
	})
	if e != nil {
		return nil, e
	}
	r.reader = reader
	r.reader.Health = fence
	// Initial failure is an explicit startup failure; the supervisor may retry.
	g, e := projection.ReadGeneration(c.MaterialsDir)
	if e != nil {
		return nil, e
	}
	defer g.Clear()
	reply := reader.Handle(api.LocalProjectionRequest{Operation: "reload", Selector: g.Selector})
	_, phase, _, e := projection.ResponseFields(reply)
	if e != nil || phase == "failed" {
		return nil, errors.New("network: initial projection rejected")
	}
	return r, nil
}
func (r *projectionRuntime) checkLocalSnapshot(snap *projection.Snapshot) error {
	if snap == nil {
		return errors.New("network: projection pending")
	}
	if r.localControlFile != "" {
		raw, e := projection.ReadControlSnapshot(r.localControlFile)
		if e != nil {
			return e
		}
		if e = r.trust.ObserveControl(raw); e != nil {
			return e
		}
	}
	return r.trust.CheckCurrent(snap.Publication)
}
func parseInstant(s string) time.Time { v, _ := time.Parse(time.RFC3339, s); return v }
func publicationFence(parent context.Context, c ProjectionConfig, schemas *projection.Schemas, trust *projection.Trust, current func() *projection.Snapshot) (func() error, error) {
	origin, e := url.Parse(c.FenceOrigin)
	if e != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") {
		return nil, errors.New("network: fixed HTTPS collab origin required")
	}
	request, e := projection.NewTransportHTTP(c.FenceOrigin, c.FenceCAFile, c.FenceClientCertFile, c.FenceClientKeyFile, c.ServiceTokenFile)
	if e != nil {
		return nil, e
	}
	get := func(ctx context.Context, path string) ([]byte, error) { return request(ctx, "GET", path, nil) }
	return func() error {
		snap := current()
		if snap == nil {
			return errors.New("network: projection pending")
		}
		if c.ControlSnapshotFile != "" {
			raw, e := projection.ReadControlSnapshot(c.ControlSnapshotFile)
			if e != nil {
				return errors.New("network: independent root control unavailable")
			}
			if e = trust.ObserveControl(raw); e != nil {
				return e
			}
		}
		if e := trust.CheckCurrent(snap.Publication); e != nil {
			return e
		}
		ctx, cancel := context.WithTimeout(parent, 5*time.Second)
		defer cancel()
		b, e := get(ctx, "/api/collab/v2/network/bootstrap/controls")
		if e != nil {
			return e
		}
		if e = trust.ObserveControl(b); e != nil {
			return e
		}
		if e = trust.CheckCurrent(snap.Publication); e != nil {
			return e
		}
		b, e = get(ctx, "/api/collab/v2/network/keys/status")
		if e != nil {
			return e
		}
		var output api.RenewalStatusOutput
		if e = schemas.Decode("RenewalStatusOutput", b, &output); e != nil || output.RenewalStatus == nil {
			return errors.New("network: unpublished renewal status")
		}
		status := output.RenewalStatus
		if status.TenantId != c.TenantID || status.Realm != c.Realm || (status.State != "ready" && status.State != "renewing" && status.State != "projection_pending") || status.Generation != snap.Selector.Generation || status.KeysetVersion != snap.Selector.KeysetVersion {
			return errors.New("network: stale protocol snapshot")
		}
		b, e = get(ctx, "/api/collab/v2/network/bootstrap/keys")
		if e != nil {
			return e
		}
		var p api.Publication
		if e = schemas.Decode("Publication", b, &p); e != nil {
			return e
		}
		controlBytes, e := projection.Bytes(p.Control)
		if e != nil {
			return e
		}
		if e = trust.ObserveControl(controlBytes); e != nil {
			return e
		}
		if e = trust.CheckCurrent(snap.Publication); e != nil {
			return e
		}
		d, e := projection.ObjectDigest(p.Keyset)
		if e != nil || d != snap.Selector.PublicationDigest {
			return errors.New("network: committed publication changed")
		}
		return nil
	}, nil
}
