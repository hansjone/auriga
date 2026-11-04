// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package proxytest

import (
	"net"

	"sync"
)

type Recorder struct {
	Name     string
	CAPEM    string
	listener net.Listener
	mu       sync.Mutex
	hits     []Hit
}
type Hit struct {
	Target string
	Auth   string
}
type Set struct {
	Config    *Recorder
	Config6   *Recorder
	TLS       *Recorder
	Socks     *Recorder
	EnvHTTPS  *Recorder
	EnvHTTPS6 *Recorder
	EnvHTTP   *Recorder
	All       []*Recorder
}
type Target struct {
	Name   string
	Scheme string
	Port   string
}

func (r *Recorder) record(hit Hit) {
	r.mu.Lock()
	r.hits = append(r.hits, hit)
	r.mu.Unlock()
}

func (r *Recorder) Reset() {
	r.mu.Lock()
	r.hits = nil
	r.mu.Unlock()
}

func (r *Recorder) Port() string {
	_, port, _ := net.SplitHostPort(r.listener.Addr().String())
	return port
}

func (s *Set) Reset() {
	for _, r := range s.All {
		r.Reset()
	}
}
