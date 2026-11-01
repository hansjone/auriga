package tokencache

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

type ClientCredentialsConfig struct {
	TokenURL      string
	ClientID      string
	ClientSecret  string
	Scopes        []string
	Audience      string
	AuthStyle     oauth2.AuthStyle
	ExtraParams   url.Values
	RefreshMargin time.Duration
}

const fallbackTokenLifetime = 5 * time.Minute

func ClientCredentialsMinter(cfg ClientCredentialsConfig, hc *http.Client) Minter[string] {
	authStyle := cfg.AuthStyle
	if authStyle == oauth2.AuthStyleAutoDetect {

		authStyle = oauth2.AuthStyleInHeader
	}
	conf := &clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     cfg.TokenURL,
		Scopes:       cfg.Scopes,
		AuthStyle:    authStyle,
	}
	if cfg.Audience != "" || len(cfg.ExtraParams) > 0 {
		params := url.Values{}
		for k, vs := range cfg.ExtraParams {
			params[k] = append([]string(nil), vs...)
		}
		if cfg.Audience != "" {
			params.Set("audience", cfg.Audience)
		}
		conf.EndpointParams = params
	}
	margin := cfg.RefreshMargin
	if margin <= 0 {
		margin = DefaultRefreshMargin
	}

	hc = guardRedirects(exchangeClient(hc))
	return func(ctx context.Context, _ *Entry[string]) (*Entry[string], *schemas.BifrostError) {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, hc)
		token, err := conf.Token(ctx)
		if err != nil {
			return nil, tokenRequestError(cfg.TokenURL, err, submittedValues(cfg.ClientID, cfg.ClientSecret, cfg.ExtraParams))
		}
		return entryFor(token.AccessToken, token.Expiry, margin), nil
	}
}

func entryFor(token string, expiry time.Time, margin time.Duration) *Entry[string] {
	if expiry.IsZero() {
		expiry = time.Now().Add(fallbackTokenLifetime)
	}
	refreshAt := expiry.Add(-margin)
	if !refreshAt.After(time.Now()) {

		refreshAt = time.Now().Add(time.Until(expiry) / 2)
	}
	return &Entry[string]{Value: token, ExpiresAt: expiry, RefreshAt: refreshAt}
}

func tokenRequestError(tokenURL string, err error, submitted []string) *schemas.BifrostError {
	var rerr *oauth2.RetrieveError
	if errors.As(err, &rerr) && rerr.Response != nil {
		detail := scrubSubmitted(strings.TrimSpace(strings.Join([]string{rerr.ErrorCode, rerr.ErrorDescription}, " ")), submitted)
		bErr := tokenEndpointError(tokenURL, rerr.Response.StatusCode, detail)
		providerUtils.ApplyRetryAfterHTTP(bErr, rerr.Response.Header)
		return bErr
	}
	if strings.Contains(err.Error(), "missing access_token") {

		return emptyTokenError(tokenURL, http.StatusOK)
	}
	return transportError(tokenURL, err)
}

func submittedValues(clientID, clientSecret string, extra url.Values) []string {
	out := []string{clientID, clientSecret}
	for _, vs := range extra {
		out = append(out, vs...)
	}
	return out
}

func scrubSubmitted(description string, submitted []string) string {
	for _, v := range submitted {
		switch {
		case v == "":
		case len(v) >= 4:
			description = strings.ReplaceAll(description, v, "[redacted]")
		case containsWord(description, v):
			return "(details withheld: the response quoted a submitted credential)"
		}
	}
	return description
}

func containsWord(s, w string) bool {
	isWordByte := func(b byte) bool {
		return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
	}
	for i := 0; ; {
		j := strings.Index(s[i:], w)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(w)
		if (start == 0 || !isWordByte(s[start-1])) && (end == len(s) || !isWordByte(s[end])) {
			return true
		}
		i = start + 1
	}
}

func tokenEndpointError(tokenURL string, statusCode int, detail string) *schemas.BifrostError {
	if detail == "" {
		detail = "(no detail)"
	}
	bErr := providerUtils.NewProviderAPIError(fmt.Sprintf("oauth token request to %s failed (%d): %s",
		redactURL(tokenURL), statusCode, detail), nil, statusCode, nil, nil)
	if IsPermanentError(bErr) {
		bErr.AllowFallbacks = schemas.Ptr(false)
	}
	return bErr
}

func emptyTokenError(tokenURL string, statusCode int) *schemas.BifrostError {
	bErr := providerUtils.NewProviderAPIError(
		fmt.Sprintf("oauth token endpoint %s returned no access_token (%d)", redactURL(tokenURL), statusCode),
		nil, http.StatusBadGateway, nil, nil)
	bErr.AllowFallbacks = schemas.Ptr(false)
	return bErr
}

func transportError(tokenURL string, err error) *schemas.BifrostError {
	return providerUtils.NewProviderAPIError(
		fmt.Sprintf("oauth token request to %s could not be completed", redactURL(tokenURL)),
		sanitizeTransportError(tokenURL, err), 0, nil, nil)
}

func sanitizeTransportError(tokenURL string, err error) error {
	if err == nil {
		return nil
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = fmt.Errorf("%s %s: %w", ue.Op, redactURL(tokenURL), ue.Err)
	}

	if msg := err.Error(); strings.Contains(msg, tokenURL) {
		err = errors.New(strings.ReplaceAll(msg, tokenURL, redactURL(tokenURL)))
	}
	return err
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "the token endpoint"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
