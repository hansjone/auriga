package schemas

import (
	"fmt"

	"path"
	"regexp"
	"strings"

	"golang.org/x/net/http/httpguts"
)

type RelayAWSAuthConfig struct {
	Service string `json:"service"`
}
type RelayAuthType string

var endpointAWSServiceRe = regexp.MustCompile(`^[a-z0-9-]+$`)

type RelayModelSource string

var endpointAuthTypes = map[RelayAuthType]struct{}{
	RelayAuthBearer: {}, RelayAuthHeader: {}, RelayAuthQuery: {}, RelayAuthBasic: {}, RelayAuthAWSSigV4: {}, RelayAuthNone: {},
}

type RelayUnitsMeasure string

var endpointRouteMethods = map[string]struct{}{
	"GET": {}, "POST": {}, "PUT": {}, "PATCH": {}, "DELETE": {}, "HEAD": {},
}

type RelayUnitsSource string
type RelayConfig struct {
	Name               string                `json:"name"`
	Description        string                `json:"description,omitempty"`
	BaseURL            string                `json:"base_url"`
	Auth               RelayAuthConfig       `json:"auth"`
	Credential         *RelayCredential      `json:"credential,omitempty"`
	ExtraHeaders       map[string]SecretVar  `json:"extra_headers,omitempty"`
	Network            *RelayNetworkConfig   `json:"network,omitempty"`
	ProxyConfig        *ProxyConfig          `json:"proxy_config,omitempty"`
	Usage              *RelayUsageConfig     `json:"usage,omitempty"`
	Streaming          *RelayStreamingConfig `json:"streaming,omitempty"`
	Routes             []RelayRouteConfig    `json:"routes,omitempty"`
	SkipRouteCheck     bool                  `json:"skip_route_check,omitempty"`
	IgnoreUpstreamCost bool                  `json:"ignore_upstream_cost,omitempty"`
}
type RelayJobPoll struct {
	Method      string            `json:"method,omitempty"`
	Path        string            `json:"path"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        string            `json:"body,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
}
type RelayStreamingConfig struct {
	Paths []string `json:"paths,omitempty"`
}
type RelayUsageConfig struct {
	ModelPath        string            `json:"model_path,omitempty"`
	ModelSource      RelayModelSource  `json:"model_source,omitempty"`
	InputTokensPath  string            `json:"input_tokens_path,omitempty"`
	OutputTokensPath string            `json:"output_tokens_path,omitempty"`
	TotalTokensPath  string            `json:"total_tokens_path,omitempty"`
	CostPath         string            `json:"cost_path,omitempty"`
	CostPerRequest   *float64          `json:"cost_per_request,omitempty"`
	UnitsPath        string            `json:"units_path,omitempty"`
	UnitsSource      RelayUnitsSource  `json:"units_source,omitempty"`
	UnitsMeasure     RelayUnitsMeasure `json:"units_measure,omitempty"`
	CostPerUnit      *float64          `json:"cost_per_unit,omitempty"`
	UnitsBlockSize   *float64          `json:"units_block_size,omitempty"`
	UnitsMinimum     *float64          `json:"units_minimum,omitempty"`
}
type RelayNetworkConfig struct {
	TimeoutSeconds             int    `json:"timeout_seconds,omitempty"`
	StreamIdleTimeoutInSeconds int    `json:"stream_idle_timeout_in_seconds,omitempty"`
	MaxConnsPerHost            int    `json:"max_conns_per_host,omitempty"`
	InsecureSkipVerify         bool   `json:"insecure_skip_verify,omitempty"`
	CACertPEM                  string `json:"ca_cert_pem,omitempty"`
}
type RelayAWSCredential struct {
	AccessKey    *SecretVar `json:"access_key,omitempty"`
	SecretKey    *SecretVar `json:"secret_key,omitempty"`
	SessionToken *SecretVar `json:"session_token,omitempty"`
	Region       *SecretVar `json:"region"`
	RoleARN      *SecretVar `json:"role_arn,omitempty"`
	ExternalID   *SecretVar `json:"external_id,omitempty"`
	SessionName  *SecretVar `json:"session_name,omitempty"`
}
type RelayAuthConfig struct {
	Type        RelayAuthType       `json:"type"`
	HeaderName  string              `json:"header_name,omitempty"`
	QueryParam  string              `json:"query_param,omitempty"`
	ValuePrefix string              `json:"value_prefix,omitempty"`
	AWS         *RelayAWSAuthConfig `json:"aws,omitempty"`
}
type RelayJobConfig struct {
	IDPath          string            `json:"id_path"`
	Poll            RelayJobPoll      `json:"poll"`
	StatusPath      string            `json:"status_path"`
	Succeeded       []string          `json:"succeeded"`
	Failed          []string          `json:"failed,omitempty"`
	IntervalSeconds int               `json:"interval_seconds,omitempty"`
	TimeoutSeconds  int               `json:"timeout_seconds,omitempty"`
	Usage           *RelayUsageConfig `json:"usage,omitempty"`
}
type RelayRouteConfig struct {
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	Headers  map[string]string `json:"headers,omitempty"`
	Enabled  *bool             `json:"enabled,omitempty"`
	Billable *bool             `json:"billable,omitempty"`
	Stream   *bool             `json:"stream,omitempty"`
	Usage    *RelayUsageConfig `json:"usage,omitempty"`
	Job      *RelayJobConfig   `json:"-"`
}
type RelayCredential struct {
	Value *SecretVar          `json:"value,omitempty"`
	AWS   *RelayAWSCredential `json:"aws,omitempty"`
}

const (
	RelayAuthBearer   RelayAuthType = "bearer"
	RelayAuthHeader   RelayAuthType = "header"
	RelayAuthQuery    RelayAuthType = "query"
	RelayAuthBasic    RelayAuthType = "basic"
	RelayAuthAWSSigV4 RelayAuthType = "aws_sigv4"
	RelayAuthNone     RelayAuthType = "none"
)

const (
	RelayModelFromBody  RelayModelSource = "body"
	RelayModelFromQuery RelayModelSource = "query"
)

const (
	RelayUnitsFromResponse RelayUnitsSource = "response"
	RelayUnitsFromRequest  RelayUnitsSource = "request"
)

const (
	RelayUnitsValue     RelayUnitsMeasure = "value"
	RelayUnitsChars     RelayUnitsMeasure = "chars"
	RelayUnitsBodyBytes RelayUnitsMeasure = "body_bytes"
	RelayUnitsBodyChars RelayUnitsMeasure = "body_chars"
)

func (c *RelayConfig) AuthType() RelayAuthType {
	if c == nil || c.Auth.Type == "" {
		return RelayAuthBearer
	}
	return c.Auth.Type
}

func (c *RelayConfig) Validate() error {
	if c == nil {
		return nil
	}
	if err := c.Auth.validate(); err != nil {
		return err
	}
	if err := c.Usage.validate("usage", nil); err != nil {
		return err
	}
	if c.Streaming != nil {
		for i, glob := range c.Streaming.Paths {
			if err := validateEndpointRouteGlob(glob); err != nil {
				return fmt.Errorf("streaming.paths[%d]: %w", i, err)
			}
		}
	}
	for i, route := range c.Routes {
		if _, ok := endpointRouteMethods[strings.ToUpper(route.Method)]; !ok {
			return fmt.Errorf("routes[%d]: unsupported method %q", i, route.Method)
		}
		if err := validateEndpointRouteGlob(route.Path); err != nil {
			return fmt.Errorf("routes[%d]: %w", i, err)
		}
		seenHeaders := make(map[string]struct{}, len(route.Headers))
		for name, glob := range route.Headers {
			if !httpguts.ValidHeaderFieldName(name) {
				return fmt.Errorf("routes[%d].headers: %q is not a valid header name", i, name)
			}

			if _, dup := seenHeaders[strings.ToLower(name)]; dup {
				return fmt.Errorf("routes[%d].headers: %q duplicates a header name that differs only by case", i, name)
			}
			seenHeaders[strings.ToLower(name)] = struct{}{}
			if strings.TrimSpace(glob) == "" {
				return fmt.Errorf("routes[%d].headers[%s]: a value glob is required", i, name)
			}
			if _, err := path.Match(glob, ""); err != nil {
				return fmt.Errorf("routes[%d].headers[%s]: %w", i, name, err)
			}
		}
		if err := route.Usage.validate(fmt.Sprintf("routes[%d].usage", i), c.Usage); err != nil {
			return err
		}
		if route.Job != nil {
			if err := route.Job.validate(fmt.Sprintf("routes[%d].job", i), route, c.Usage); err != nil {
				return err
			}
		}
	}
	return nil
}

func (j *RelayJobConfig) validate(field string, route RelayRouteConfig, base *RelayUsageConfig) error {
	if strings.TrimSpace(j.IDPath) == "" {
		return fmt.Errorf("%s.id_path is required", field)
	}
	if strings.TrimSpace(j.StatusPath) == "" {
		return fmt.Errorf("%s.status_path is required", field)
	}
	if len(j.Succeeded) == 0 {
		return fmt.Errorf("%s.succeeded must name at least one status", field)
	}
	if j.IntervalSeconds < 0 || j.TimeoutSeconds < 0 {
		return fmt.Errorf("%s: interval_seconds and timeout_seconds must not be negative", field)
	}
	if !strings.HasPrefix(j.Poll.Path, "/") {
		return fmt.Errorf("%s.poll.path %q must start with /", field, j.Poll.Path)
	}
	if j.Poll.Method != "" {
		if _, ok := endpointRouteMethods[strings.ToUpper(j.Poll.Method)]; !ok {
			return fmt.Errorf("%s.poll.method: unsupported method %q", field, j.Poll.Method)
		}
	}
	for name := range j.Poll.Headers {
		if !httpguts.ValidHeaderFieldName(name) {
			return fmt.Errorf("%s.poll.headers: %q is not a valid header name", field, name)
		}
	}

	if route.Stream != nil && *route.Stream {
		return fmt.Errorf("%s: a route that starts a job is served buffered; stream: true does not apply", field)
	}

	if route.Billable != nil && !*route.Billable {
		return fmt.Errorf("%s: a route that starts a job must be billable", field)
	}
	if route.Billable == nil && !strings.EqualFold(route.Method, "POST") {
		return fmt.Errorf("%s: a %s route that starts a job must set billable: true", field, strings.ToUpper(route.Method))
	}

	merged := &RelayUsageConfig{}
	for _, u := range []*RelayUsageConfig{base, route.Usage} {
		if u == nil {
			continue
		}
		if u.UnitsPath != "" {
			merged.UnitsPath = u.UnitsPath
		}
		if u.UnitsMeasure != "" {
			merged.UnitsMeasure = u.UnitsMeasure
		}
		if u.CostPerUnit != nil {
			merged.CostPerUnit = u.CostPerUnit
		}
	}

	var source RelayUnitsSource
	var rate *float64
	for _, u := range []*RelayUsageConfig{base, route.Usage, j.Usage} {
		if u == nil {
			continue
		}
		if u.UnitsSource != "" {
			source = u.UnitsSource
		}
		if u.CostPerUnit != nil {
			rate = u.CostPerUnit
		}
	}
	if source == RelayUnitsFromRequest && rate != nil && *rate > 0 {
		return fmt.Errorf("%s.usage: a job is priced from its terminal poll response, so request-side units are not billed at settlement; set units_source: response on the job", field)
	}
	return j.Usage.validate(field+".usage", merged)
}

func (a RelayAuthConfig) validate() error {
	if a.Type == "" {
		return nil
	}
	if _, ok := endpointAuthTypes[a.Type]; !ok {
		return fmt.Errorf("auth.type: unsupported value %q", a.Type)
	}
	switch a.Type {
	case RelayAuthHeader:
		if strings.TrimSpace(a.HeaderName) == "" {
			return fmt.Errorf("auth.header_name is required for auth.type header")
		}
		if !httpguts.ValidHeaderFieldName(a.HeaderName) {
			return fmt.Errorf("auth.header_name %q is not a valid header name", a.HeaderName)
		}
	case RelayAuthQuery:
		if strings.TrimSpace(a.QueryParam) == "" {
			return fmt.Errorf("auth.query_param is required for auth.type query")
		}
	case RelayAuthAWSSigV4:
		if a.AWS == nil || strings.TrimSpace(a.AWS.Service) == "" {
			return fmt.Errorf("auth.aws.service is required for auth.type aws_sigv4")
		}
		if !endpointAWSServiceRe.MatchString(a.AWS.Service) {
			return fmt.Errorf("auth.aws.service %q must match [a-z0-9-]+", a.AWS.Service)
		}
	}
	return nil
}

func (u *RelayUsageConfig) validate(field string, base *RelayUsageConfig) error {
	if u == nil {
		return nil
	}
	if u.CostPerRequest != nil && *u.CostPerRequest < 0 {
		return fmt.Errorf("%s.cost_per_request must not be negative", field)
	}
	if u.CostPerUnit != nil && *u.CostPerUnit < 0 {
		return fmt.Errorf("%s.cost_per_unit must not be negative", field)
	}
	if u.UnitsBlockSize != nil && *u.UnitsBlockSize <= 0 {
		return fmt.Errorf("%s.units_block_size must be above zero", field)
	}
	if u.UnitsMinimum != nil && *u.UnitsMinimum < 0 {
		return fmt.Errorf("%s.units_minimum must not be negative", field)
	}
	switch u.ModelSource {
	case "", RelayModelFromBody, RelayModelFromQuery:
	default:
		return fmt.Errorf("%s.model_source: unsupported value %q", field, u.ModelSource)
	}
	switch u.UnitsSource {
	case "", RelayUnitsFromResponse, RelayUnitsFromRequest:
	default:
		return fmt.Errorf("%s.units_source: unsupported value %q", field, u.UnitsSource)
	}
	switch u.UnitsMeasure {
	case "", RelayUnitsValue, RelayUnitsChars, RelayUnitsBodyBytes, RelayUnitsBodyChars:
	default:
		return fmt.Errorf("%s.units_measure: unsupported value %q", field, u.UnitsMeasure)
	}

	unitsPath, unitsMeasure := u.UnitsPath, u.UnitsMeasure
	if base != nil {
		if unitsPath == "" {
			unitsPath = base.UnitsPath
		}
		if unitsMeasure == "" {
			unitsMeasure = base.UnitsMeasure
		}
	}

	rate := u.CostPerUnit
	if rate == nil && base != nil {
		rate = base.CostPerUnit
	}
	if rate != nil && *rate > 0 && !unitsMeasureIsWholeBody(unitsMeasure) && unitsPath == "" {
		return fmt.Errorf("%s.units_path is required with cost_per_unit unless units_measure is body_bytes or body_chars", field)
	}
	return nil
}

func unitsMeasureIsWholeBody(m RelayUnitsMeasure) bool {
	return m == RelayUnitsBodyBytes || m == RelayUnitsBodyChars
}

func validateEndpointRouteGlob(glob string) error {
	if !strings.HasPrefix(glob, "/") {
		return fmt.Errorf("path %q must start with /", glob)
	}
	segments := strings.Split(strings.TrimPrefix(glob, "/"), "/")
	for i, segment := range segments {
		if segment == "**" {
			if i != len(segments)-1 {
				return fmt.Errorf("path %q: ** is only allowed as the last segment", glob)
			}
			continue
		}
		if _, err := path.Match(segment, ""); err != nil {
			return fmt.Errorf("path %q: %w", glob, err)
		}
	}
	return nil
}

func (u *RelayUsageConfig) clone() *RelayUsageConfig {
	if u == nil {
		return nil
	}
	out := *u
	if u.CostPerRequest != nil {
		out.CostPerRequest = Ptr(*u.CostPerRequest)
	}
	if u.CostPerUnit != nil {
		out.CostPerUnit = Ptr(*u.CostPerUnit)
	}
	if u.UnitsBlockSize != nil {
		out.UnitsBlockSize = Ptr(*u.UnitsBlockSize)
	}
	if u.UnitsMinimum != nil {
		out.UnitsMinimum = Ptr(*u.UnitsMinimum)
	}
	return &out
}

func (c *RelayConfig) Clone() *RelayConfig {
	if c == nil {
		return nil
	}
	out := *c

	if c.Credential != nil {
		cred := RelayCredential{Value: c.Credential.Value.Clone()}
		if c.Credential.AWS != nil {
			cred.AWS = &RelayAWSCredential{
				AccessKey: c.Credential.AWS.AccessKey.Clone(), SecretKey: c.Credential.AWS.SecretKey.Clone(), SessionToken: c.Credential.AWS.SessionToken.Clone(),
				Region: c.Credential.AWS.Region.Clone(), RoleARN: c.Credential.AWS.RoleARN.Clone(), ExternalID: c.Credential.AWS.ExternalID.Clone(), SessionName: c.Credential.AWS.SessionName.Clone(),
			}
		}
		out.Credential = &cred
	}
	if c.ExtraHeaders != nil {
		out.ExtraHeaders = make(map[string]SecretVar, len(c.ExtraHeaders))
		for k, v := range c.ExtraHeaders {
			out.ExtraHeaders[k] = *v.Clone()
		}
	}
	if c.Network != nil {
		network := *c.Network
		out.Network = &network
	}
	if c.ProxyConfig != nil {
		proxy := *c.ProxyConfig
		proxy.URL, proxy.Username, proxy.Password, proxy.CACertPEM = c.ProxyConfig.URL.Clone(), c.ProxyConfig.Username.Clone(), c.ProxyConfig.Password.Clone(), c.ProxyConfig.CACertPEM.Clone()
		out.ProxyConfig = &proxy
	}
	if c.Auth.AWS != nil {
		aws := *c.Auth.AWS
		out.Auth.AWS = &aws
	}
	out.Usage = c.Usage.clone()
	if c.Streaming != nil {
		streaming := *c.Streaming
		streaming.Paths = append([]string(nil), c.Streaming.Paths...)
		out.Streaming = &streaming
	}
	if c.Routes != nil {
		out.Routes = make([]RelayRouteConfig, len(c.Routes))
		for i, route := range c.Routes {
			out.Routes[i] = route
			if route.Headers != nil {
				out.Routes[i].Headers = make(map[string]string, len(route.Headers))
				for k, v := range route.Headers {
					out.Routes[i].Headers[k] = v
				}
			}
			if route.Billable != nil {
				out.Routes[i].Billable = Ptr(*route.Billable)
			}
			if route.Enabled != nil {
				out.Routes[i].Enabled = Ptr(*route.Enabled)
			}
			if route.Stream != nil {
				out.Routes[i].Stream = Ptr(*route.Stream)
			}
			if route.Job != nil {
				job := *route.Job
				job.Succeeded = append([]string(nil), route.Job.Succeeded...)
				job.Failed = append([]string(nil), route.Job.Failed...)
				if route.Job.Poll.Headers != nil {
					job.Poll.Headers = make(map[string]string, len(route.Job.Poll.Headers))
					for k, v := range route.Job.Poll.Headers {
						job.Poll.Headers[k] = v
					}
				}
				job.Usage = route.Job.Usage.clone()
				out.Routes[i].Job = &job
			}
			out.Routes[i].Usage = route.Usage.clone()
		}
	}
	return &out
}
