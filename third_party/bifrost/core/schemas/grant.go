package schemas

type Access interface {
	Bases() []Permit

	Scoping() Permit

	Mode() string

	IsScoped() bool

	IsProviderAllowed(provider string) bool

	IsModelAllowed(provider string, model string) bool

	AllowsEveryModel(provider string) bool

	IsMCPToolAllowed(toolPattern string) bool

	IsAgentAllowed(agentName string) bool

	IsRelayAllowed(endpointName string) bool

	PermitsForModel(provider string, model string) []Permit

	KeysForModel(provider string, model string) (keyIDs []string, restricted bool)

	ProvidersForModel(model string) []ProviderCandidate

	GrantedProvidersForModel(model string) []string

	MCPToolIncludeList() []string

	NarrowMCPToolIncludeList(requested []string) []string

	DeniedPermitsForModel(provider string, model string) []Permit

	DeniedPermitsForAgent(agentName string) []Permit

	DeniedPermitsForRelay(endpointName string) []Permit

	DeniedPermitsForMCPTool(toolPattern string) []Permit
}
type Credential struct {
	Kind  string
	Value string
}
type Identity interface {
	Credential() Credential

	Presented() bool

	User() *UserRef

	VirtualKey() *EntityRef

	Teams() []EntityRef
	Customers() []EntityRef
	BusinessUnits() []EntityRef

	Project() *EntityRef
}
type Limit struct {
	ID string

	HolderKind string
	HolderID   string
	HolderName string

	Provider string
	Model    string
}
type Grant interface {
	Identity() Identity

	Access() Access

	Limits() Limits

	SetIdentity(identity Identity) bool

	SetAccess(access Access) bool

	SetLimits(limits Limits) bool
}
type Permit interface {
	Type() string
	ID() string
	Name() string
	IsActive() bool
	IsExpired() bool

	ProviderPermits() []ProviderPermit

	MCPPermits() []MCPPermit

	AgentPermits() []string

	RelayPermits() []string

	AllowsAllProviders() bool
}
type EntityRef struct {
	ID   string
	Name string
}
type MCPPermit struct {
	Client string

	ClientName string
	Tools      WhiteList
}
type Limits interface {
	Budgets() []Limit
	RateLimits() []Limit
}
type UserRef struct {
	ID    string
	Name  string
	Email string
}
type ProviderCandidate struct {
	Provider string
	Weight   *float64
	KeyIDs   WhiteList
}
type ProviderPermit struct {
	Provider          string
	AllowedModels     WhiteList
	BlacklistedModels BlackList
	KeyIDs            WhiteList

	Weight *float64
}
