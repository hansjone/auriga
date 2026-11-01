package schemas

type TokenExchangeRejectedError struct {
	Detail string

	SubjectRejected bool
}
type OAuth2Config struct {
	ID              string     `json:"id"`
	ClientID        *SecretVar `json:"client_id,omitempty"`
	ClientSecret    *SecretVar `json:"client_secret,omitempty"`
	AuthorizeURL    string     `json:"authorize_url,omitempty"`
	TokenURL        string     `json:"token_url,omitempty"`
	RegistrationURL *string    `json:"registration_url,omitempty"`
	RedirectURI     string     `json:"redirect_uri"`
	Scopes          []string   `json:"scopes,omitempty"`
	ServerURL       string     `json:"server_url"`
	Resource        string     `json:"resource,omitempty"`
	UseDiscovery    bool       `json:"use_discovery,omitempty"`

	UseClientMetadataDocument bool `json:"use_client_metadata_document,omitempty"`

	RequirePublicTarget bool   `json:"-"`
	ClientMetadataURL   string `json:"-"`
}

func (e *TokenExchangeRejectedError) Error() string {
	return "identity provider rejected the token exchange: " + e.Detail
}
