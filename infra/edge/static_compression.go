package main

import (
	"bytes"
	"compress/gzip"
	"container/list"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxCompressedStaticBytes = 8 << 20

const staticBodyTimeout = 15 * time.Second
const defaultStaticCacheBytes = 12 << 20

type staticCacheKey struct {
	path  string
	size  int64
	mtime string
}

type staticCacheItem struct {
	key  staticCacheKey
	data []byte
}

// Each server owns its cache. Limits cover both bytes and tiny-file metadata.
type staticGzipCache struct {
	mu       sync.Mutex
	items    map[staticCacheKey]*list.Element
	lru      list.List
	bytes    int
	maxBytes int
}

func (c *staticGzipCache) get(key staticCacheKey) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.items[key]; e != nil {
		c.lru.MoveToFront(e)
		return e.Value.(staticCacheItem).data
	}
	return nil
}

func (c *staticGzipCache) encode(key staticCacheKey, raw []byte) ([]byte, error) {
	if data := c.get(key); data != nil {
		return data, nil
	}
	data, err := compressStaticBytes(raw)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// A concurrent miss may have inserted the same representation while gzip
	// ran outside the lock. Keep the first immutable entry and its accounting.
	if e := c.items[key]; e != nil {
		c.lru.MoveToFront(e)
		return e.Value.(staticCacheItem).data, nil
	}
	limit := c.maxBytes
	if limit == 0 {
		limit = defaultStaticCacheBytes
	}
	if len(data) > limit {
		return data, nil
	}
	if c.items == nil {
		c.items = make(map[staticCacheKey]*list.Element)
	}
	for c.bytes+len(data) > limit || c.lru.Len() >= 128 {
		e := c.lru.Back()
		item := e.Value.(staticCacheItem)
		delete(c.items, item.key)
		c.bytes -= len(item.data)
		c.lru.Remove(e)
	}
	c.items[key] = c.lru.PushFront(staticCacheItem{key, data})
	c.bytes += len(data)
	return data, nil
}

func compressStaticBytes(raw []byte) ([]byte, error) {
	var encoded bytes.Buffer
	writer := gzip.NewWriter(&encoded)
	if _, err := writer.Write(raw); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func varyStaticEncoding(h http.Header) {
	for _, value := range h.Values("Vary") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "Accept-Encoding") || strings.TrimSpace(token) == "*" {
				return
			}
		}
	}
	h.Add("Vary", "Accept-Encoding")
}

func staticAssetPath(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/assets/") || strings.HasPrefix(r.URL.Path, "/ai/_next/static/")
}

// Only public build assets are compressed. Login pages and authenticated API
// bodies may combine secrets with caller input and must not enter this path.
func compressibleStatic(r *http.Request, contentType string) bool {
	if (r.Method != http.MethodGet && r.Method != http.MethodHead) || r.Header.Get("Range") != "" || !staticAssetPath(r) {
		return false
	}
	typeName, _, err := mime.ParseMediaType(contentType)
	return err == nil && (typeName == "text/javascript" || typeName == "application/javascript" || typeName == "text/css")
}

func acceptsStaticGzip(r *http.Request) bool {
	for _, value := range r.Header.Values("Accept-Encoding") {
		for _, item := range strings.Split(value, ",") {
			parts := strings.Split(item, ";")
			if !strings.EqualFold(strings.TrimSpace(parts[0]), "gzip") {
				continue
			}
			quality := 1.0
			for _, parameter := range parts[1:] {
				name, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
				if !ok || !strings.EqualFold(name, "q") {
					return false
				}
				q, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
				if err != nil || !(q > 0 && q <= 1) {
					return false
				}
				quality = q
			}
			return quality > 0
		}
	}
	return false
}

func gzipStaticResponse(resp *http.Response) error {
	return gzipStaticResponseCached(resp, nil)
}

func gzipStaticResponseCached(resp *http.Response, cache *staticGzipCache) error {
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Encoding") != "" ||
		!compressibleStatic(resp.Request, resp.Header.Get("Content-Type")) {
		return nil
	}
	varyStaticEncoding(resp.Header)
	// A bare upstream HEAD has no body to compress. Proxy HEADs use GET on the
	// private hop, then discard the body after computing the same representation.
	if resp.Request.Method == http.MethodHead || !acceptsStaticGzip(resp.Request) || resp.ContentLength > maxCompressedStaticBytes {
		return nil
	}
	key := staticCacheKey{resp.Request.URL.String(), resp.ContentLength, resp.Header.Get("Last-Modified") + "\n" + resp.Header.Get("ETag")}
	// Vary now includes Accept-Encoding; only that dimension is safe to reuse.
	_, modifiedErr := http.ParseTime(resp.Header.Get("Last-Modified"))
	cacheable := cache != nil && key.size >= 0 && modifiedErr == nil && len(resp.Header.Values("Vary")) == 1 && strings.EqualFold(resp.Header.Get("Vary"), "Accept-Encoding") && resp.Header.Get("Set-Cookie") == ""
	if cacheable {
		if encoded := cache.get(key); encoded != nil {
			if err := resp.Body.Close(); err != nil {
				return err
			}
			setGzipStaticBody(resp, encoded)
			return nil
		}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxCompressedStaticBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxCompressedStaticBytes {
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(raw), resp.Body), resp.Body}
		return nil
	}
	if err := resp.Body.Close(); err != nil {
		return err
	}
	var encoded []byte
	if cacheable {
		encoded, err = cache.encode(key, raw)
	} else {
		encoded, err = compressStaticBytes(raw)
	}
	if err != nil {
		return err
	}
	setGzipStaticBody(resp, encoded)
	return nil
}

func setGzipStaticBody(resp *http.Response, encoded []byte) {
	resp.Body = io.NopCloser(bytes.NewReader(encoded))
	resp.ContentLength = int64(len(encoded))
	resp.Header.Set("Content-Length", strconv.Itoa(len(encoded)))
	resp.Header.Set("Content-Encoding", "gzip")
	resp.Header.Del("ETag")
	resp.Header.Del("Accept-Ranges")
}

func (c *staticGzipCache) serveFile(w http.ResponseWriter, r *http.Request, filename string, info os.FileInfo) bool {
	contentType := mime.TypeByExtension(filepath.Ext(filename))
	if !compressibleStatic(r, contentType) {
		return false
	}
	varyStaticEncoding(w.Header())
	if !acceptsStaticGzip(r) || info.Size() > maxCompressedStaticBytes {
		return false
	}
	key := staticCacheKey{filename, info.Size(), info.ModTime().UTC().Format(time.RFC3339Nano)}
	encoded := c.get(key)
	if encoded == nil {
		file, err := os.Open(filename)
		if err != nil {
			return false
		}
		raw, err := io.ReadAll(io.LimitReader(file, maxCompressedStaticBytes+1))
		after, statErr := file.Stat()
		_ = file.Close()
		if err == nil && statErr == nil && int64(len(raw)) == info.Size() && after.Size() == info.Size() && after.ModTime().Equal(info.ModTime()) && len(raw) <= maxCompressedStaticBytes {
			encoded, err = c.encode(key, raw)
		}
		if err != nil || encoded == nil {
			writeProblem(w, problem(503, "common.unavailable", "Service Unavailable", "static asset unavailable", requestID(r)))
			return true
		}
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
	http.ServeContent(w, r, info.Name(), info.ModTime(), bytes.NewReader(encoded))
	return true
}
