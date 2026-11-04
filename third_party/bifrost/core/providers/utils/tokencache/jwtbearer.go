// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package tokencache

import (
	"context"
	"crypto"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/golang-jwt/jwt/v5"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
)

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int64  `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}
type JWTBearerConfig struct {
	TokenURL      string
	PrivateKeyPEM string
	Issuer        string
	Subject       string
	Audience      string
	KeyID         string
	Scopes        []string
	Algorithm     string
	Lifetime      time.Duration
	ExtraParams   url.Values
	RefreshMargin time.Duration
}

const (
	jwtBearerGrantType = "urn:ietf:params:oauth:grant-type:jwt-bearer"

	DefaultAssertionLifetime = 5 * time.Minute

	assertionIssuedAtSkew = 30 * time.Second

	maxTokenResponseBytes = 64 * 1024
)

func JWTBearerMinter(cfg JWTBearerConfig, hc *http.Client) Minter[string] {
	margin := cfg.RefreshMargin
	if margin <= 0 {
		margin = DefaultRefreshMargin
	}

	hc = guardRedirects(exchangeClient(hc))
	return func(ctx context.Context, _ *Entry[string]) (*Entry[string], *schemas.BifrostError) {
		assertion, err := signAssertion(cfg, time.Now())
		if err != nil {
			bErr := configurationError("oauth jwt_bearer: " + err.Error())
			return nil, bErr
		}
		form := url.Values{}
		for k, vs := range cfg.ExtraParams {
			form[k] = append([]string(nil), vs...)
		}
		form.Set("grant_type", jwtBearerGrantType)
		form.Set("assertion", assertion)
		if len(cfg.Scopes) > 0 {
			form.Set("scope", strings.Join(cfg.Scopes, " "))
		}
		token, expiry, bErr := postTokenRequest(ctx, hc, cfg.TokenURL, form)
		if bErr != nil {
			return nil, bErr
		}
		return entryFor(token, expiry, margin), nil
	}
}

func signAssertion(cfg JWTBearerConfig, now time.Time) (string, error) {
	alg := cfg.Algorithm
	if alg == "" {
		alg = "RS256"
	}

	method := jwt.GetSigningMethod(alg)
	if method == nil || !slices.Contains(schemas.OAuthSigningAlgorithms, alg) {
		return "", fmt.Errorf("signing_algorithm %q is not supported", alg)
	}
	key, err := parsePrivateKey(NormalizePEM(cfg.PrivateKeyPEM), alg)
	if err != nil {
		return "", err
	}
	lifetime := cfg.Lifetime
	if lifetime <= 0 {
		lifetime = DefaultAssertionLifetime
	}
	subject := cfg.Subject
	if subject == "" {
		subject = cfg.Issuer
	}
	var jti [16]byte
	if _, err := io.ReadFull(rand.Reader, jti[:]); err != nil {
		return "", fmt.Errorf("could not generate jti: %w", err)
	}
	issuedAt := now.Add(-assertionIssuedAtSkew)

	token := jwt.NewWithClaims(method, jwt.MapClaims{
		"iss": cfg.Issuer,
		"sub": subject,
		"aud": cfg.Audience,
		"iat": issuedAt.Unix(),
		"nbf": issuedAt.Unix(),
		"exp": now.Add(lifetime).Unix(),
		"jti": hex.EncodeToString(jti[:]),
	})
	if cfg.KeyID != "" {
		token.Header["kid"] = cfg.KeyID
	}
	return token.SignedString(key)
}

func parsePrivateKey(pemData, alg string) (crypto.PrivateKey, error) {
	switch {
	case strings.HasPrefix(alg, "ES"):
		key, err := jwt.ParseECPrivateKeyFromPEM([]byte(pemData))
		if err != nil {
			return nil, fmt.Errorf("private_key could not be parsed as an EC PEM for %s: %w", alg, err)
		}

		if m, ok := jwt.GetSigningMethod(alg).(*jwt.SigningMethodECDSA); ok && key.Curve.Params().BitSize != m.CurveBits {
			return nil, fmt.Errorf("private_key is a %s key, which %s cannot sign with", key.Curve.Params().Name, alg)
		}
		return key, nil
	default:
		key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(pemData))
		if err != nil {
			return nil, fmt.Errorf("private_key could not be parsed as an RSA PEM (PKCS#1 or PKCS#8) for %s: %w", alg, err)
		}
		return key, nil
	}
}

func postTokenRequest(ctx context.Context, hc *http.Client, tokenURL string, form url.Values) (string, time.Time, *schemas.BifrostError) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, configurationError("oauth token request could not be built: " + err.Error())
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := hc.Do(req)
	if err != nil {
		return "", time.Time{}, transportError(tokenURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponseBytes))
	if err != nil {
		return "", time.Time{}, providerUtils.NewProviderAPIError(
			fmt.Sprintf("oauth token response from %s could not be read", redactURL(tokenURL)),
			sanitizeTransportError(tokenURL, err), 0, nil, nil)
	}

	var parsed tokenResponse
	_ = sonic.Unmarshal(body, &parsed)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {

		var submitted []string
		for k, vs := range form {
			if k != "grant_type" && k != "scope" {
				submitted = append(submitted, vs...)
			}
		}
		bErr := tokenEndpointError(tokenURL, resp.StatusCode, scrubSubmitted(strings.TrimSpace(parsed.Error+" "+parsed.ErrorDescription), submitted))
		providerUtils.ApplyRetryAfterHTTP(bErr, resp.Header)
		return "", time.Time{}, bErr
	}
	if parsed.AccessToken == "" {
		return "", time.Time{}, emptyTokenError(tokenURL, resp.StatusCode)
	}
	var expiry time.Time
	if parsed.ExpiresIn > 0 {
		expiry = time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second)
	}
	return parsed.AccessToken, expiry, nil
}
