package main

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
)

var opsBackupDownloadPath = regexp.MustCompile(`^/api/workspace/v1/admin/ops/backups/[0-9a-fA-F-]{36}/download$`)

// Native browser downloads stream to disk without JS buffering or ticket URLs.
// Only this exact form can supply a CSRF nonce in the body; the existing domain
// cookie or IP session-bound verifier still performs all CSRF checks below.
func opsDownloadFormCSRF(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost || !opsBackupDownloadPath.MatchString(r.URL.Path) {
		return true
	}
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || len(r.Header.Values("Content-Type")) != 1 || kind != "application/x-www-form-urlencoded" || r.URL.RawQuery != "" || len(r.Header.Values("X-Auriga-CSRF")) != 0 {
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		return false
	}
	form, err := url.ParseQuery(string(body))
	if err != nil || len(form) != 2 || len(form["csrf"]) != 1 || len(form["ticket"]) != 1 || form.Get("csrf") == "" || form.Get("ticket") == "" {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("X-Auriga-CSRF", form.Get("csrf"))
	return true
}
