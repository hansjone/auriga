package main

import (
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
)

// reloadingServerTLS reads only the configured leaf paths, never paths supplied
// by a request. A selector shared by both files is resolved once per handshake
// so an atomic generation switch cannot mix the old certificate and new key.
// The projector is responsible for root-chain, scope and monotonicity checks
// before committing this component's private projection.
func reloadingServerTLS(certFile, keyFile string) (*tls.Config, error) {
	load := func() (*tls.Config, error) {
		cert, key := certFile, keyFile
		if filepath.Dir(certFile) == filepath.Dir(keyFile) {
			dir, err := filepath.EvalSymlinks(filepath.Dir(certFile))
			if err != nil {
				return nil, fmt.Errorf("TLS projection directory unavailable")
			}
			cert, key = filepath.Join(dir, filepath.Base(certFile)), filepath.Join(dir, filepath.Base(keyFile))
		}
		for _, file := range []string{cert, key} {
			info, err := os.Lstat(file)
			if err != nil || !info.Mode().IsRegular() {
				return nil, fmt.Errorf("TLS projection requires regular files")
			}
		}
		pair, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			return nil, fmt.Errorf("TLS projection certificate/key invalid")
		}
		return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}, nil
	}
	initial, err := load()
	if err != nil {
		return nil, err
	}
	var committed atomic.Pointer[tls.Config]
	committed.Store(initial.Clone())
	initial.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
		current, err := load()
		if err != nil {
			return committed.Load().Clone(), nil
		}
		committed.Store(current.Clone())
		return current, nil
	}
	return initial, nil
}
