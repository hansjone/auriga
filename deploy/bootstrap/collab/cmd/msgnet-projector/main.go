package main

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/hansjone/auriga/deploy/bootstrap/collab/projection"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func enc(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
func main() {
	if e := run(); e != nil {
		_, _ = fmt.Fprintln(os.Stderr, "msgnet-projector: stopped; projection_pending")
		os.Exit(1)
	}
}
func private(path string) (any, error) {
	st, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > 16384 {
		return nil, errors.New("private installation file rejected")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	defer clear(b)
	if block, rest := pem.Decode(b); block != nil {
		if block.Type != "PRIVATE KEY" || len(rest) != 0 {
			return nil, errors.New("invalid installation key")
		}
		defer clear(block.Bytes)
		return x509.ParsePKCS8PrivateKey(block.Bytes)
	}
	return x509.ParsePKCS8PrivateKey(b)
}
func run() error {
	mode := flag.String("mode", "projector", "projector, broker or control-import")
	controlPackage := flag.String("control-package", "", "public ControlPackage file; control-import mode only")
	controlSnapshot := flag.String("control-snapshot", "", "public root-control snapshot path; defaults to private control volume")
	schema := flag.String("schema", "", "frozen auto-renewal schema file")
	pinsPath := flag.String("pins", "", "installation pins file")
	rootPath := flag.String("root-spki", "", "installed root SPKI base64url file")
	identity := flag.String("identity", "", "installed projection identity")
	materials := flag.String("materials", "", "private component material volume")
	control := flag.String("control", "", "private component control volume")
	readerState := flag.String("reader-state", "", "reader-only durable state volume; required in broker mode")
	origin := flag.String("origin", "", "fixed HTTPS collab origin")
	signing := flag.String("signing-key", "", "installation signing PKCS8 file")
	sealing := flag.String("sealing-key", "", "installation sealing PKCS8 file")
	ca := flag.String("https-ca", "", "independent HTTPS CA file; empty uses system roots")
	serviceToken := flag.String("service-token-file", "", "independent projector service JWT file for protected receipts")
	clientCert := flag.String("https-client-cert", "", "independent projector mTLS client certificate for protected receipts")
	clientKey := flag.String("https-client-key", "", "independent projector mTLS client key for protected receipts")
	broker := flag.String("broker", "", "repository-locked NATS 2.12.6 binary; broker mode only")
	brokerDigest := flag.String("broker-sha256", "", "installation-pinned sha256 digest; broker mode only")
	template := flag.String("broker-template", "", "trusted NATS config template; broker mode only")
	flag.Parse()
	if flag.NArg() != 0 || (*mode != "projector" && *mode != "broker" && *mode != "control-import") {
		return errors.New("invalid mode")
	}
	schemas, e := projection.LoadSchemas(*schema)
	if e != nil {
		return e
	}
	b, e := os.ReadFile(*pinsPath)
	if e != nil {
		return e
	}
	var pins api.InstallationPins
	if e = schemas.Decode("InstallationPins", b, &pins); e != nil {
		return e
	}
	if e = projection.ValidatePins(pins); e != nil {
		return e
	}
	var pin *api.InstallationPin
	for j := range pins.Pins {
		if pins.Pins[j].IdentityId == *identity {
			pin = &pins.Pins[j]
		}
	}
	if pin == nil {
		return errors.New("installation pin missing")
	}
	if e = projection.RequireProjectorUID(pin.Component); e != nil {
		return e
	}
	b, e = os.ReadFile(*rootPath)
	if e != nil {
		return e
	}
	root := strings.TrimSpace(string(b))
	role := "projector"
	if *controlSnapshot == "" {
		*controlSnapshot = filepath.Join(*control, "root-control.json")
	}
	if *mode == "control-import" {
		role = "control-import"
	}
	if *mode == "broker" {
		if pin.Component != "broker" {
			return errors.New("broker scope required")
		}
		role = "broker"
	}
	stateDir := filepath.Join(*control, role+"-state")
	if *mode == "broker" {
		if *readerState == "" || filepath.Clean(*readerState) == filepath.Clean(*control) || filepath.Clean(*readerState) == filepath.Clean(*materials) {
			return errors.New("separate reader state required")
		}
		stateDir = *readerState
	}
	if e = os.MkdirAll(stateDir, 0700); e != nil {
		return e
	}
	trust, e := projection.NewTrust(schemas, root, pins.TenantId, pins.Realm, stateDir, *pin)
	if e != nil {
		return e
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *mode != "control-import" {
		request, err := projection.NewTransportHTTP(*origin, *ca, *clientCert, *clientKey, *serviceToken)
		if err != nil {
			return err
		}
		source := &projection.TransportSource{Schemas: schemas, Root: root, Tenant: pins.TenantId, Realm: pins.Realm, Target: pin.TargetId, Request: request}
		source.Bind(ctx, trust)
	}
	if *mode == "broker" {
		supervisor := &projection.BrokerSupervisor{Binary: *broker, ExpectedSHA256: *brokerDigest, Template: *template, Control: *control, Trust: trust}
		defer supervisor.Close()
		reader, e := projection.NewReader(*materials, *control, *readerState, supervisor.Prepare)
		if e != nil {
			return e
		}
		reader.Active = supervisor.Active
		reader.Health = func() error {
			raw, e := projection.ReadControlSnapshot(*controlSnapshot)
			if e == nil {
				e = trust.ObserveControl(raw)
			}
			if e == nil {
				e = trust.CheckLoadedCurrent()
			}
			if e != nil {
				supervisor.Close()
			}
			return e
		}
		done := make(chan struct{})
		go func() { defer close(done); reader.Watch(ctx) }()
		e = reader.Serve(ctx, "broker")
		cancel()
		<-done
		return e
	}
	k, e := private(*signing)
	if e != nil {
		return e
	}
	signKey, ok := k.(ed25519.PrivateKey)
	if !ok {
		return errors.New("Ed25519 signing key required")
	}
	defer clear(signKey)
	publicDER, e := x509.MarshalPKIXPublicKey(signKey.Public())
	if e != nil {
		return e
	}
	if enc(publicDER) != pin.SigningSpki {
		return errors.New("installation signing pin mismatch")
	}
	if *mode == "control-import" {
		raw, e := os.ReadFile(*controlPackage)
		if e != nil {
			return e
		}
		var q api.ControlPackage
		if e = schemas.Decode("ControlPackage", raw, &q); e != nil {
			return e
		}
		receipt, e := trust.InstallControlPackage(q, signKey, *controlSnapshot, filepath.Join(*control, "control-receipts"))
		if e != nil {
			return e
		}
		raw, e = projection.Bytes(receipt)
		if e != nil {
			return e
		}
		_, e = os.Stdout.Write(append(raw, '\n'))
		return e
	}
	k, e = private(*sealing)
	if e != nil {
		return e
	}
	sealKey, ok := k.(*ecdh.PrivateKey)
	if !ok || sealKey.Curve() != ecdh.X25519() {
		return errors.New("X25519 sealing key required")
	}
	publicDER, e = x509.MarshalPKIXPublicKey(sealKey.PublicKey())
	if e != nil || enc(publicDER) != pin.SealingSpki {
		return errors.New("installation sealing pin mismatch")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if *ca != "" {
		b, e = os.ReadFile(*ca)
		if e != nil {
			return e
		}
		tlsConfig.RootCAs = x509.NewCertPool()
		if !tlsConfig.RootCAs.AppendCertsFromPEM(b) {
			return errors.New("invalid HTTPS CA")
		}
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsConfig}}
	p := &projection.Projector{Root: *materials, Control: *control, Origin: *origin, Schemas: schemas, Trust: trust, Pin: *pin, Signing: signKey, Sealing: sealKey, Client: client}
	p.ServiceTokenFile = *serviceToken
	if (*clientCert == "") != (*clientKey == "") {
		return errors.New("receipt mTLS keypair required")
	}
	if *clientCert != "" {
		receiptTLS := tlsConfig.Clone()
		receiptTLS.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			pair, e := tls.LoadX509KeyPair(*clientCert, *clientKey)
			if e != nil {
				return nil, errors.New("receipt mTLS identity unavailable")
			}
			return &pair, nil
		}
		p.ReceiptClient = &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: receiptTLS, DisableKeepAlives: true}}
	}
	p.ControlSnapshot = *controlSnapshot
	p.OnFailure = func(e error, age time.Duration) {
		code := "projection_pending"
		if e == nil {
			code = "loaded"
		} else {
			var f projection.Fault
			if errors.As(e, &f) {
				switch f {
				case "invalid_chain", "version_rollback", "same_version_conflict", "authority_revoked", "authority_unknown", "scope_denied", "control_required", "control_rollback", "invalid_transition":
					code = string(f)
				}
			}
		}
		_, _ = fmt.Fprintf(os.Stderr, "msgnet-projector: state=%s age_seconds=%d unavailable_60s=%t\n", code, int64(age.Seconds()), e != nil && age >= time.Minute)
	}
	e = p.Run(ctx)
	if errors.Is(e, context.Canceled) {
		return nil
	}
	return e
}
