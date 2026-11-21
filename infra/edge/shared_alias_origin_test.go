package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTrustedPathAliasOriginOnlyForVerifiedAliasAndPathMount(t *testing.T) {
	g := &Gateway{}
	path := InstanceTarget{Origin: "http://10.0.0.133:8080", Mount: "/i/" + sharedTestID + "/"}
	request := func(alias string, origins ...string) *http.Request {
		r := httptest.NewRequest("POST", "http://10.0.0.133:8080"+path.Mount+"_auriga/placement-ticket", nil)
		for _, o := range origins {
			r.Header.Add("Origin", o)
		}
		if alias != "" {
			r = r.WithContext(context.WithValue(r.Context(), linkOriginKey{}, alias))
		}
		return r
	}
	const alias = "https://ai.example.test"
	if !g.trustedPathAliasOrigin(request(alias, alias), path) {
		t.Fatal("verified public alias must share the path-mounted instance origin")
	}
	for name, c := range map[string]struct {
		r      *http.Request
		target InstanceTarget
	}{
		"no verified alias": {request("", alias), path},
		"different origin":  {request(alias, "https://evil.example.test"), path},
		"duplicate origin":  {request(alias, alias, alias), path},
		"missing origin":    {request(alias), path},
		"subdomain mount":   {request(alias, alias), InstanceTarget{Origin: path.Origin, Mount: "/"}},
		"unresolved mount":  {request(alias, alias), InstanceTarget{}},
	} {
		if g.trustedPathAliasOrigin(c.r, c.target) {
			t.Fatal(name)
		}
	}
}
