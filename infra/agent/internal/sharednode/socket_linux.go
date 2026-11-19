//go:build linux

package sharednode

import (
	"context"
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The Host sees only the read-only public relay directory, never node ledgers.
func (r *Relay) Start(ctx context.Context, path string, uid int) (func(), error) {
	if !filepath.IsAbs(path) || filepath.Base(path) != "shared-node.sock" || uid <= 0 {
		return nil, fmt.Errorf("invalid pool socket")
	}
	if r.VerifyPeer == nil || r.Admission == nil {
		return nil, fmt.Errorf("signed pool process verifier and task admission required")
	}
	if info, e := os.Lstat(path); e == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("unexpected socket path")
		}
		if e = os.Remove(path); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	// A held directory fd avoids sockaddr_un limits on long NAS data roots;
	// the same actual socket inode is mounted at the Host's fixed short path.
	listener, directory, e := listenPoolSocket(path)
	if e != nil {
		return nil, e
	}
	fail := func(e error) (func(), error) { _ = listener.Close(); _ = directory.Close(); return nil, e }
	if e = os.Chown(path, uid, uid); e != nil {
		return fail(e)
	}
	if e = os.Chmod(path, 0600); e != nil {
		return fail(e)
	}
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second,
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			return context.WithValue(ctx, peerKey{}, socketPeer(conn))
		}}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		peer, ok := req.Context().Value(peerKey{}).(poolPeer)
		if !ok || peer.uid != uint32(uid) || peer.pid <= 0 || peer.start == "" {
			replyError(w, "common.forbidden")
			return
		}
		identity, err := r.VerifyPeer(req.Context(), peer.pid, peer.start)
		if err != nil || identity == "" {
			replyError(w, "shared.revoked")
			return
		}
		req = req.WithContext(context.WithValue(req.Context(), executionPeerKey{}, identity))
		callContext := req.Context()
		req = req.WithContext(context.WithValue(callContext, executionFenceKey{}, func() error {
			current, err := r.VerifyPeer(callContext, peer.pid, peer.start)
			if err != nil || current != identity {
				return fmt.Errorf("shared.revoked")
			}
			return nil
		}))
		r.ServeHTTP(w, req)
	})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = srv.Close()
			_ = listener.Close()
			_ = directory.Close()
			r.mu.Lock()
			tasks := make(map[string]execution, len(r.tasks))
			for id, t := range r.tasks {
				tasks[id] = t
			}
			r.mu.Unlock()
			for id, t := range tasks {
				_ = r.finish(t.binding, id)
			}
		})
	}
	go func() { _ = srv.Serve(listener) }()
	go func() { <-ctx.Done(); stop() }()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.RetryFinishes()
			}
		}
	}()
	return stop, nil
}

func listenPoolSocket(path string) (*net.UnixListener, *os.File, error) {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, nil, err
	}
	short := fmt.Sprintf("/proc/self/fd/%d/%s", directory.Fd(), filepath.Base(path))
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: short, Net: "unix"})
	if err != nil {
		_ = directory.Close()
		return nil, nil, err
	}
	return listener, directory, nil
}

type peerKey struct{}

type poolPeer struct {
	uid   uint32
	pid   int
	start string
}

func socketPeer(conn net.Conn) poolPeer {
	cred := peerCredentials(conn)
	if cred == nil {
		return poolPeer{uid: ^uint32(0)}
	}
	p := poolPeer{uid: cred.Uid, pid: int(cred.Pid)}
	if raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", p.pid)); err == nil {
		fields := strings.Fields(string(raw)[strings.LastIndex(string(raw), ")")+1:])
		if len(fields) > 19 {
			if _, err := strconv.ParseUint(fields[19], 10, 64); err == nil {
				p.start = fields[19]
			}
		}
	}
	return p
}

func peerUID(conn net.Conn) uint32 {
	if cred := peerCredentials(conn); cred != nil {
		return cred.Uid
	}
	return ^uint32(0)
}

func peerCredentials(conn net.Conn) *unix.Ucred {
	c, ok := conn.(*net.UnixConn)
	if !ok {
		return nil
	}
	raw, e := c.SyscallConn()
	if e != nil {
		return nil
	}
	var cred *unix.Ucred
	var err error
	if e = raw.Control(func(fd uintptr) { cred, err = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); e != nil || err != nil || cred == nil {
		return nil
	}
	return cred
}
