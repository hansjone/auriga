package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const maxCompressedStaticBytes = 8 << 20

// Only public build assets are compressed. Login pages and authenticated API
// bodies may combine secrets with caller input and must not enter this path.
func compressibleStatic(r *http.Request, contentType string) bool {
	if r.Method != http.MethodGet || r.Header.Get("Range") != "" ||
		(!strings.HasPrefix(r.URL.Path, "/assets/") && !strings.HasPrefix(r.URL.Path, "/ai/_next/static/")) {
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
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Encoding") != "" ||
		!compressibleStatic(resp.Request, resp.Header.Get("Content-Type")) {
		return nil
	}
	resp.Header.Add("Vary", "Accept-Encoding")
	if !acceptsStaticGzip(resp.Request) || resp.ContentLength > maxCompressedStaticBytes {
		return nil
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
	var encoded bytes.Buffer
	writer := gzip.NewWriter(&encoded)
	if _, err := writer.Write(raw); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	resp.Body = io.NopCloser(bytes.NewReader(encoded.Bytes()))
	resp.ContentLength = int64(encoded.Len())
	resp.Header.Set("Content-Length", strconv.Itoa(encoded.Len()))
	resp.Header.Set("Content-Encoding", "gzip")
	resp.Header.Del("ETag")
	resp.Header.Del("Accept-Ranges")
	return nil
}

func serveGzipStaticFile(w http.ResponseWriter, r *http.Request, filename string, info os.FileInfo) bool {
	contentType := mime.TypeByExtension(filepath.Ext(filename))
	if !compressibleStatic(r, contentType) {
		return false
	}
	w.Header().Add("Vary", "Accept-Encoding")
	if !acceptsStaticGzip(r) || info.Size() > maxCompressedStaticBytes {
		return false
	}
	file, err := os.Open(filename)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	resp := &http.Response{StatusCode: http.StatusOK, Request: r, Header: make(http.Header), Body: file, ContentLength: info.Size()}
	resp.Header.Set("Content-Type", contentType)
	if err := gzipStaticResponse(resp); err != nil {
		writeProblem(w, problem(503, "common.unavailable", "Service Unavailable", "static asset unavailable", requestID(r)))
		return true
	}
	defer func() { _ = resp.Body.Close() }()
	for name, values := range resp.Header {
		w.Header()[name] = values
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, resp.Body)
	return true
}
