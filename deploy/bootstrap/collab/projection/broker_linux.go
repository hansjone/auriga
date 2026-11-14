package projection

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// BrokerSupervisor has exactly one executable and a fixed argument shape. The
// socket cannot select an executable, configuration path or process identifier.
type BrokerSupervisor struct {
	mu                        sync.Mutex
	Binary, Template, Control string
	ExpectedSHA256            string
	Trust                     *Trust
	child                     *exec.Cmd
	done                      chan struct{}
	selected                  string
}

func (b *BrokerSupervisor) checkBinary() error {
	raw, e := readFile(b.Binary, 64*MaxFile)
	if e != nil {
		return e
	}
	if !hash.MatchString(b.ExpectedSHA256) || Digest(raw) != b.ExpectedSHA256 {
		return errors.New("projection: broker binary digest does not match installation pin")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	version, e := exec.CommandContext(ctx, b.Binary, "-v").Output()
	if e != nil || strings.TrimSpace(string(version)) != "nats-server: v2.12.6" {
		return errors.New("projection: broker must match repository-locked NATS 2.12.6")
	}
	return nil
}
func (b *BrokerSupervisor) stop() {
	if b.child == nil {
		return
	}
	select {
	case <-b.done:
	default:
		_ = b.child.Process.Signal(syscall.SIGTERM)
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-b.done:
		case <-timer.C:
			_ = b.child.Process.Kill()
			<-b.done
		}
	}
	b.child = nil
}
func (b *BrokerSupervisor) Close() { b.mu.Lock(); defer b.mu.Unlock(); b.stop() }
func (b *BrokerSupervisor) Active() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.child == nil {
		return false
	}
	select {
	case <-b.done:
		return false
	default:
		return true
	}
}
func (b *BrokerSupervisor) Prepare(g *Generation) (func() error, error) {
	if b.Trust == nil {
		return nil, Fault("invalid_chain")
	}
	if e := b.checkBinary(); e != nil {
		return nil, e
	}
	snapshot, e := b.Trust.Load(g)
	if e != nil {
		return nil, e
	}
	defer snapshot.Clear()
	leaf, ok := snapshot.TLS.Leaves["broker"]
	if !ok {
		return nil, Fault("invalid_chain")
	}
	template, e := readFile(b.Template, MaxFile)
	if e != nil {
		return nil, e
	}
	dir := filepath.Join(b.Control, "broker-tls")
	if e = privateDir(dir); e != nil {
		return nil, e
	}
	var roots []byte
	// Only public CA material is written beside the broker's own leaf.
	var bundle map[string]any
	if e = Decode(g.Files["bundle.json"], &bundle, true); e != nil {
		return nil, e
	}
	payload, ok := bundle["payload"].(map[string]any)
	if !ok {
		return nil, Fault("invalid_chain")
	}
	cas, ok := payload["cas"].([]any)
	if !ok {
		return nil, Fault("invalid_chain")
	}
	for _, entry := range cas {
		ca, ok := entry.(map[string]any)
		if !ok {
			return nil, Fault("invalid_chain")
		}
		encoded, ok := ca["certificate_der"].(string)
		if !ok {
			return nil, Fault("invalid_chain")
		}
		raw, e := binaryText(encoded)
		if e != nil {
			return nil, e
		}
		roots = append(roots, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})...)
	}
	cert, key := []byte(nil), []byte(nil)
	for j := 0; j < 126; j++ {
		raw, exists := g.Files[fmt.Sprintf("leaf-%03d.cert.pem", j)]
		if !exists {
			break
		}
		der, e := DecodePEM(raw, "CERTIFICATE")
		if e != nil {
			return nil, e
		}
		if string(der) == string(leaf.Certificate[0]) {
			cert = append([]byte(nil), raw...)
			key = append([]byte(nil), g.Files[fmt.Sprintf("leaf-%03d.key.pem", j)]...)
			break
		}
	}
	if len(key) == 0 {
		return nil, Fault("invalid_chain")
	}
	config := string(template)
	for token, path := range map[string]string{"{{MSGNET_CERT}}": filepath.Join(dir, "broker.cert.pem"), "{{MSGNET_KEY}}": filepath.Join(dir, "broker.key.pem"), "{{MSGNET_CA}}": filepath.Join(dir, "ca.cert.pem")} {
		if !strings.Contains(config, token) {
			clear(key)
			return nil, Fault("invalid_chain")
		}
		config = strings.ReplaceAll(config, token, path)
	}
	if strings.Contains(config, "{{MSGNET_") {
		clear(key)
		return nil, Fault("invalid_chain")
	}
	selector := g.Selector.ManifestDigest
	return func() error {
		b.mu.Lock()
		defer b.mu.Unlock()
		defer clear(key)
		if b.child != nil && b.selected == selector {
			select {
			case <-b.done:
			default:
				return nil
			}
		}
		// The supported fallback is an owned-child graceful restart, never a Docker
		// restart or a signal sent to an externally supplied PID.
		b.stop()
		for name, raw := range map[string][]byte{"broker.cert.pem": cert, "broker.key.pem": key, "ca.cert.pem": roots} {
			if e := atomicFile(filepath.Join(dir, name), raw); e != nil {
				return e
			}
		}
		path := filepath.Join(b.Control, "broker-runtime.conf")
		if e := atomicFile(path, []byte(config)); e != nil {
			return e
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		check := exec.CommandContext(ctx, b.Binary, "-t", "-c", path)
		check.Stdout = io.Discard
		check.Stderr = io.Discard
		if e := check.Run(); e != nil {
			return Fault("projection_pending")
		}
		child := exec.Command(b.Binary, "-c", path)
		child.Stdout = io.Discard
		child.Stderr = io.Discard
		child.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
		if e := child.Start(); e != nil {
			return Fault("projection_pending")
		}
		b.child = child
		b.done = make(chan struct{})
		done := b.done
		go func() { _ = child.Wait(); close(done) }()
		b.selected = selector
		return b.Trust.RecordLoaded(g.Selector, false)
	}, nil
}
