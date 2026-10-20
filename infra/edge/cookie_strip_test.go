package main

import "testing"

func TestLoginTxCookieOnlyReachesPlatformAPI(t *testing.T) {
	in := "auriga_session=s; auriga_login_tx=tx; theme=dark"
	if got := stripAurigaCookies(in, true); got != "auriga_login_tx=tx; theme=dark" {
		t.Fatalf("API direction: %q", got)
	}
	if got := stripAurigaCookies(in, false); got != "theme=dark" {
		t.Fatalf("instance direction must drop login_tx: %q", got)
	}
}
