package schemas

type WebSocketConfig struct {
	MaxConnections       int           `json:"max_connections_per_user"`
	TranscriptBufferSize int           `json:"transcript_buffer_size"`
	Pool                 *WSPoolConfig `json:"pool,omitempty"`
}
type WSPoolConfig struct {
	MaxIdlePerKey                int `json:"max_idle_per_key"`
	MaxTotalConnections          int `json:"max_total_connections"`
	IdleTimeoutSeconds           int `json:"idle_timeout_seconds"`
	MaxConnectionLifetimeSeconds int `json:"max_connection_lifetime_seconds"`
}

const (
	DefaultWSMaxIdlePerKey                = 50
	DefaultWSMaxTotalConnections          = 1000
	DefaultWSIdleTimeoutSeconds           = 600
	DefaultWSMaxConnectionLifetimeSeconds = 7200
	DefaultWSMaxConnections               = 100
	DefaultWSTranscriptBufferSize         = 100
)

func (c *WebSocketConfig) CheckAndSetDefaults() {
	if c.MaxConnections <= 0 {
		c.MaxConnections = DefaultWSMaxConnections
	}
	if c.TranscriptBufferSize <= 0 {
		c.TranscriptBufferSize = DefaultWSTranscriptBufferSize
	}
	if c.Pool == nil {
		c.Pool = &WSPoolConfig{}
	}
	c.Pool.CheckAndSetDefaults()
}

func (c *WSPoolConfig) CheckAndSetDefaults() {
	if c.MaxIdlePerKey <= 0 {
		c.MaxIdlePerKey = DefaultWSMaxIdlePerKey
	}
	if c.MaxTotalConnections <= 0 {
		c.MaxTotalConnections = DefaultWSMaxTotalConnections
	}
	if c.IdleTimeoutSeconds <= 0 {
		c.IdleTimeoutSeconds = DefaultWSIdleTimeoutSeconds
	}
	if c.MaxConnectionLifetimeSeconds <= 0 {
		c.MaxConnectionLifetimeSeconds = DefaultWSMaxConnectionLifetimeSeconds
	}
}
