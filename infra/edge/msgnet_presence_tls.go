package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/nats-io/nats.go"
)

// NATS invokes certificate then root callbacks under its connection lock on
// initial connect and every reconnect. Keep a single resolved generation for
// both callbacks, including across an atomic projector selector switch.
func reloadingPresenceTLS(cfg DevicePresenceConfig) (nats.Option, error) {
	var mu sync.Mutex
	var roots *x509.CertPool
	var lastPair tls.Certificate
	load := func() (tls.Certificate, error) {
		cert, key, ca := cfg.CertFile, cfg.KeyFile, cfg.CAFile
		if filepath.Dir(cert) == filepath.Dir(key) && filepath.Dir(cert) == filepath.Dir(ca) {
			dir, err := filepath.EvalSymlinks(filepath.Dir(cert))
			if err != nil {
				return tls.Certificate{}, errDevicePresence
			}
			cert, key, ca = filepath.Join(dir, filepath.Base(cert)), filepath.Join(dir, filepath.Base(key)), filepath.Join(dir, filepath.Base(ca))
		}
		pair, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			return tls.Certificate{}, errDevicePresence
		}
		raw, err := os.ReadFile(ca)
		if err != nil {
			return tls.Certificate{}, errDevicePresence
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(raw) {
			return tls.Certificate{}, errDevicePresence
		}
		mu.Lock()
		roots = pool
		lastPair = pair
		mu.Unlock()
		return pair, nil
	}
	if _, err := load(); err != nil {
		return nil, err
	}
	return func(o *nats.Options) error {
		if o.TLSConfig != nil {
			return fmt.Errorf("presence TLS must be component scoped")
		}
		o.Secure = true
		o.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.ServerName}
		o.TLSCertCB = func() (tls.Certificate, error) {
			pair, err := load()
			if err == nil {
				return pair, nil
			}
			mu.Lock()
			defer mu.Unlock()
			return lastPair, nil
		}
		o.RootCAsCB = func() (*x509.CertPool, error) { mu.Lock(); defer mu.Unlock(); return roots, nil }
		return nil
	}, nil
}
