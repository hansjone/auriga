package admin

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type Principal struct {
	jwt.RegisteredClaims
	Tenant      string   `json:"tid"`
	Context     string   `json:"ctx"`
	Type        string   `json:"ptyp"`
	Session     string   `json:"sid"`
	Permissions []string `json:"perms"`
	StepUp      int64    `json:"stepup_time"`
}
type Authenticate func(context.Context, string) (*Principal, error)

// JWTAuthenticator uses the same jwt/v5 dependency as Team. IAM public JWKS are
// supplied by deployment; reading each time observes atomic file rotations.
func JWTAuthenticator(issuer, jwksFile string) (Authenticate, error) {
	if issuer == "" || jwksFile == "" {
		return nil, errors.New("AI admin issuer and IAM JWKS file are required")
	}
	if _, err := readKeys(jwksFile); err != nil {
		return nil, err
	}
	return func(_ context.Context, raw string) (*Principal, error) {
		keys, err := readKeys(jwksFile)
		if err != nil {
			return nil, failure("common.unavailable")
		}
		p := new(Principal)
		token, err := jwt.ParseWithClaims(raw, p, func(t *jwt.Token) (any, error) {
			if t.Header["typ"] != "at+jwt" {
				return nil, errors.New("invalid token type")
			}
			kid, ok := t.Header["kid"].(string)
			if !ok || keys[kid] == nil {
				return nil, errors.New("unknown signing key")
			}
			return keys[kid], nil
		}, jwt.WithValidMethods([]string{"ES256"}), jwt.WithIssuer(issuer), jwt.WithAudience("auriga"), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(30*time.Second))
		if err != nil || !token.Valid || p.IssuedAt == nil || p.ExpiresAt == nil || p.ID == "" || !uuidPattern.MatchString(p.Tenant) || !uuidPattern.MatchString(p.Subject) || p.Type != "user" || len(p.Audience) != 1 || p.Audience[0] != "auriga" || p.ExpiresAt.Sub(p.IssuedAt.Time) > 300*time.Second || !p.ExpiresAt.After(p.IssuedAt.Time) {
			return nil, failure("common.unauthenticated")
		}
		if p.Context != "admin" {
			return nil, failure("common.forbidden")
		}
		if !uuidPattern.MatchString(p.Session) {
			return nil, failure("common.unauthenticated")
		}
		return p, nil
	}, nil
}

func readKeys(path string) (map[string]*ecdsa.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read IAM public JWKS file")
	}
	var document struct {
		Keys []struct{ Kid, Kty, Crv, Alg, Use, X, Y string } `json:"keys"`
	}
	if len(data) > 1<<20 || json.Unmarshal(data, &document) != nil {
		return nil, errors.New("invalid IAM JWKS file")
	}
	keys := make(map[string]*ecdsa.PublicKey)
	for _, j := range document.Keys {
		if j.Kty != "EC" || j.Crv != "P-256" || (j.Alg != "" && j.Alg != "ES256") || (j.Use != "" && j.Use != "sig") {
			continue
		}
		x, e1 := base64.RawURLEncoding.DecodeString(j.X)
		y, e2 := base64.RawURLEncoding.DecodeString(j.Y)
		if e1 != nil || e2 != nil || len(x) != 32 || len(y) != 32 || j.Kid == "" || keys[j.Kid] != nil {
			return nil, errors.New("invalid IAM EC signing key")
		}
		point := append([]byte{4}, x...)
		point = append(point, y...)
		key, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
		if err != nil {
			return nil, errors.New("invalid IAM EC point")
		}
		keys[j.Kid] = key
	}
	if len(keys) == 0 {
		return nil, errors.New("IAM JWKS contains no ES256 signing key")
	}
	return keys, nil
}
