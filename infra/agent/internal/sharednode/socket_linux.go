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
	"sync"
	"time"
)

// The Host sees only the read-only public relay directory, never node ledgers.
func (r *Relay) Start(ctx context.Context, path string, uid int) (func(), error) {
	if !filepath.IsAbs(path) || filepath.Base(path) != "shared-node.sock" || uid <= 0 {
		return nil, fmt.Errorf("invalid pool socket")
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
			return context.WithValue(ctx, peerKey{}, peerUID(conn) == uint32(uid))
		}}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Context().Value(peerKey{}) != true {
			replyError(w, "common.forbidden")
			return
		}
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

func peerUID(conn net.Conn) uint32 {
	c, ok := conn.(*net.UnixConn)
	if !ok {
		return ^uint32(0)
	}
	raw, e := c.SyscallConn()
	if e != nil {
		return ^uint32(0)
	}
	var cred *unix.Ucred
	var err error
	if e = raw.Control(func(fd uintptr) { cred, err = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); e != nil || err != nil || cred == nil {
		return ^uint32(0)
	}
	return cred.Uid
}
