package admin

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testUUID = "00000000-0000-7000-8000-000000000011"

func signer(t *testing.T) (*ecdsa.PrivateKey, Authenticate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enc := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	point, err := key.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"keys": []any{map[string]any{"kty": "EC", "crv": "P-256", "alg": "ES256", "use": "sig", "kid": "test", "x": enc(point[1:33]), "y": enc(point[33:])}}}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "jwks.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	auth, err := JWTAuthenticator("https://iam.example.com", path)
	if err != nil {
		t.Fatal(err)
	}
	return key, auth
}
func validClaims() jwt.MapClaims {
	now := time.Now().Unix()
	return jwt.MapClaims{"iss": "https://iam.example.com", "aud": []string{"auriga"}, "sub": testUUID, "tid": testUUID, "ctx": "admin", "ptyp": "user", "sid": testUUID, "jti": "test", "iat": now, "exp": now + 300, "stepup_time": now, "perms": []string{"ai.providers.read", "ai.providers.write", "ai.credentials.rotate", "ai.models.grant", "ai.models.invoke"}}
}
func sign(t *testing.T, key *ecdsa.PrivateKey, claims jwt.MapClaims, typ string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["kid"] = "test"
	token.Header["typ"] = typ
	raw, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestJWTAuthenticator(t *testing.T) {
	key, auth := signer(t)
	if _, err := auth(context.Background(), sign(t, key, validClaims(), "at+jwt")); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"aud", "iss", "ctx", "ptyp", "tid", "sid", "sub", "jti", "iat", "exp"} {
		t.Run(field, func(t *testing.T) {
			claims := validClaims()
			switch field {
			case "aud":
				claims[field] = []string{"auriga.ai-gateway"}
			case "ctx":
				claims[field] = "portal"
			case "ptyp":
				claims[field] = "internal_service"
			case "exp":
				claims[field] = time.Now().Add(-time.Hour).Unix()
			default:
				delete(claims, field)
			}
			if _, err := auth(context.Background(), sign(t, key, claims, "at+jwt")); err == nil {
				t.Fatal("invalid claim accepted")
			}
		})
	}
	if _, err := auth(context.Background(), sign(t, key, validClaims(), "JWT")); err == nil {
		t.Fatal("ID token accepted")
	}
	wrong, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = auth(context.Background(), sign(t, wrong, validClaims(), "at+jwt")); err == nil {
		t.Fatal("forged signature accepted")
	}
	claims := validClaims()
	claims["aud"] = []string{"auriga", "auriga.internal"}
	if _, err = auth(context.Background(), sign(t, key, claims, "at+jwt")); err == nil {
		t.Fatal("internal audience accepted")
	}
}
