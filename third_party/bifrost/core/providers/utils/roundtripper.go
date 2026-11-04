// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package utils

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http/httputil"
	"sync"
	"sync/atomic"
	"time"

	"github.com/valyala/fasthttp"
)

type chunkedBody struct {
	chunked io.Reader
	br      *bufio.Reader
	resp    *fasthttp.Response
	done    bool
}

var testHookBeforeConnRelease func()
var errTooManyInterimResponses = errors.New("too many 1xx interim responses")
var requestContexts sync.Map

type cancelWatcher struct {
	ctx       context.Context
	cancelled atomic.Bool
	aborted   atomic.Bool
	stopCh    chan struct{}
	done      chan struct{}
	stopOnce  sync.Once
}
type contextTransport struct{}
type eofReader struct{}
type contentLengthBody struct {
	reader io.LimitedReader
}
type streamBody struct {
	reader    io.Reader
	conn      net.Conn
	release   func(discard bool)
	closed    atomic.Bool
	fullyRead bool
	readLock  sync.Mutex
	closeOnce sync.Once
}

func bindRequestContext(req *fasthttp.Request, ctx context.Context) (unbind func()) {
	if req == nil || ctx == nil || ctx.Done() == nil {
		return func() {}
	}
	requestContexts.Store(req, ctx)
	var once sync.Once
	return func() {
		once.Do(func() {
			requestContexts.CompareAndDelete(req, ctx)
		})
	}
}

func lookupRequestContext(req *fasthttp.Request) context.Context {
	if v, ok := requestContexts.Load(req); ok {
		if ctx, ok := v.(context.Context); ok {
			return ctx
		}
	}
	return context.Background()
}

func NewContextTransport() fasthttp.RoundTripper {
	return &contextTransport{}
}

const maxInterimResponses = 10

func (t *contextTransport) RoundTrip(hc *fasthttp.HostClient, req *fasthttp.Request, resp *fasthttp.Response) (retry bool, err error) {
	ctx := lookupRequestContext(req)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, ctxErr
	}

	abort := AttemptAbortFromContext(ctx)
	if abort.Fired() {
		return false, ErrStreamFirstTokenTimeout
	}

	customSkipBody := resp.SkipBody

	cc, err := hc.AcquireConn(0, req.ConnectionClose())
	if err != nil {
		return false, err
	}
	conn := cc.Conn()
	resp.ParseNetConn(conn)

	reused := !cc.LastUseTime().IsZero()
	retryOn := func(err error) bool {
		return reused && !errors.Is(err, fasthttp.ErrBodyTooLarge)
	}

	watcher := startCancelWatcher(ctx, abort, conn)
	defer watcher.stop()

	if err = conn.SetWriteDeadline(deadlineFor(hc.WriteTimeout, ctx)); err != nil {
		hc.CloseConn(cc)
		return retryOn(err), watcher.classify(err)
	}

	resetConnection := false
	if hc.MaxConnDuration > 0 && time.Since(cc.CreatedTime()) > hc.MaxConnDuration && !req.ConnectionClose() {
		req.SetConnectionClose()
		resetConnection = true
	}

	bw := hc.AcquireWriter(conn)
	err = req.Write(bw)
	if resetConnection {
		req.Header.ResetConnectionClose()
	}
	if err == nil {
		err = bw.Flush()
	}
	hc.ReleaseWriter(bw)
	if err != nil {
		hc.CloseConn(cc)
		return retryOn(err), watcher.classify(err)
	}

	if err = conn.SetReadDeadline(deadlineFor(hc.ReadTimeout, ctx)); err != nil {
		hc.CloseConn(cc)
		return retryOn(err), watcher.classify(err)
	}

	if customSkipBody || req.Header.IsHead() {
		resp.SkipBody = true
	}
	if hc.DisableHeaderNamesNormalizing {
		resp.Header.DisableNormalizing()
	}

	br := hc.AcquireReader(conn)

	if !resp.StreamBody {

		resp.ResetBody()
		if err = readResponseHeaders(resp, br); err != nil {
			hc.ReleaseReader(br)
			hc.CloseConn(cc)
			return retryOn(err), watcher.classify(err)
		}
		err = readBufferedBody(resp, br, hc.MaxResponseBodySize)
		hc.ReleaseReader(br)
		if err != nil {
			hc.CloseConn(cc)
			return false, watcher.classify(err)
		}

		watcher.stop()
		if watcher.wasCancelled() {
			hc.CloseConn(cc)
			return false, watcher.cancelErr()
		}
		if testHookBeforeConnRelease != nil {
			testHookBeforeConnRelease()
		}
		if resetConnection || req.ConnectionClose() || resp.ConnectionClose() {
			hc.CloseConn(cc)
		} else {
			hc.ReleaseConn(cc)
		}
		return false, nil
	}

	resp.ResetBody()
	if err = readResponseHeaders(resp, br); err != nil {
		hc.ReleaseReader(br)
		hc.CloseConn(cc)
		return retryOn(err), watcher.classify(err)
	}

	watcher.stop()
	if watcher.wasCancelled() {
		hc.ReleaseReader(br)
		hc.CloseConn(cc)
		return false, watcher.cancelErr()
	}
	if err = conn.SetDeadline(time.Time{}); err != nil {
		hc.ReleaseReader(br)
		hc.CloseConn(cc)
		return false, err
	}

	closeConn := resetConnection || req.ConnectionClose() || resp.ConnectionClose()
	body := &streamBody{
		reader: newStreamBodyReader(resp, br),
		conn:   conn,
		release: func(discard bool) {
			hc.ReleaseReader(br)
			if discard || closeConn || resp.ConnectionClose() {
				hc.CloseConn(cc)
			} else {
				hc.ReleaseConn(cc)
			}
		},
	}
	resp.SetBodyStream(body, resp.Header.ContentLength())
	return false, nil
}

func deadlineFor(timeout time.Duration, ctx context.Context) time.Time {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	if ctxDeadline, ok := ctx.Deadline(); ok && (deadline.IsZero() || ctxDeadline.Before(deadline)) {
		deadline = ctxDeadline
	}
	return deadline
}

func readResponseHeaders(resp *fasthttp.Response, br *bufio.Reader) error {
	if err := resp.Header.Read(br); err != nil {
		return err
	}
	for n := 0; ; n++ {
		status := resp.Header.StatusCode()
		if status < 100 || status > 199 || status == fasthttp.StatusSwitchingProtocols {
			return nil
		}
		if n >= maxInterimResponses {
			return errTooManyInterimResponses
		}
		if err := resp.Header.Read(br); err != nil {
			return err
		}
	}
}

func readBufferedBody(resp *fasthttp.Response, br *bufio.Reader, maxBodySize int) error {
	status := resp.Header.StatusCode()
	if resp.SkipBody || status == fasthttp.StatusNoContent || status == fasthttp.StatusNotModified || (status >= 100 && status <= 199) {
		return nil
	}
	if err := resp.ReadBody(br, maxBodySize); err != nil {
		return err
	}
	if resp.Header.ContentLength() == -1 {
		if err := resp.Header.ReadTrailer(br); err != nil {
			if errors.Is(err, io.EOF) {
				return io.ErrUnexpectedEOF
			}
			return err
		}
	}
	return nil
}

func newStreamBodyReader(resp *fasthttp.Response, br *bufio.Reader) io.Reader {
	status := resp.Header.StatusCode()
	if resp.SkipBody || status == fasthttp.StatusNoContent || status == fasthttp.StatusNotModified || (status >= 100 && status <= 199) {
		return eofReader{}
	}
	switch contentLength := resp.Header.ContentLength(); {
	case contentLength == -1:
		return &chunkedBody{chunked: httputil.NewChunkedReader(br), br: br, resp: resp}
	case contentLength >= 0:
		return &contentLengthBody{reader: io.LimitedReader{R: br, N: int64(contentLength)}}
	default:

		return br
	}
}

func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }

func (c *contentLengthBody) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	if errors.Is(err, io.EOF) && c.reader.N > 0 {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func (c *chunkedBody) Read(p []byte) (int, error) {
	if c.done {
		return 0, io.EOF
	}
	n, err := c.chunked.Read(p)
	if errors.Is(err, io.EOF) {
		if trailerErr := c.resp.Header.ReadTrailer(c.br); trailerErr != nil && !errors.Is(trailerErr, io.EOF) {
			return n, trailerErr
		}
		c.done = true
	}
	return n, err
}

func (s *streamBody) Read(p []byte) (int, error) {
	if s.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	s.readLock.Lock()
	defer s.readLock.Unlock()
	if s.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	n, err := s.reader.Read(p)
	if errors.Is(err, io.EOF) {
		s.fullyRead = true
	} else if errors.Is(err, io.ErrUnexpectedEOF) {

		if _, chunked := s.reader.(*chunkedBody); chunked {
			err = io.EOF
		}
	}
	return n, err
}

func (s *streamBody) CloseWithError(err error) error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		locked := s.readLock.TryLock()
		discard := err != nil || !locked
		if discard || !s.fullyRead {
			discard = true
			_ = s.conn.Close()
		}
		if !locked {
			s.readLock.Lock()
		}
		defer s.readLock.Unlock()
		s.release(discard)
	})
	return nil
}

func startCancelWatcher(ctx context.Context, abort *AttemptAbort, conn net.Conn) *cancelWatcher {
	if ctx.Done() == nil && abort == nil {
		return nil
	}
	w := &cancelWatcher{ctx: ctx, stopCh: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		abortDone, abortStopped := abort.Done(), abort.Stopped()
		for {
			select {
			case <-ctx.Done():
				w.cancelled.Store(true)
				_ = conn.Close()
				return
			case <-abortDone:
				w.aborted.Store(true)
				w.cancelled.Store(true)
				_ = conn.Close()
				return
			case <-abortStopped:

				abortDone, abortStopped = nil, nil
			case <-w.stopCh:
				return
			}
		}
	}()
	return w
}

func (w *cancelWatcher) stop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() {
		close(w.stopCh)
		<-w.done
	})
}

func (w *cancelWatcher) wasCancelled() bool {
	return w != nil && w.cancelled.Load()
}

func (w *cancelWatcher) cancelErr() error {
	if w != nil && w.aborted.Load() {
		return ErrStreamFirstTokenTimeout
	}
	return w.ctx.Err()
}

func (w *cancelWatcher) classify(err error) error {
	if w != nil && w.aborted.Load() {
		return ErrStreamFirstTokenTimeout
	}
	if w != nil && w.cancelled.Load() {
		if ctxErr := w.ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fasthttp.ErrTimeout
	}
	return err
}
