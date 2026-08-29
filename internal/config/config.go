package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config represents the application configuration
type Config struct {
	Mode                string                     `yaml:"mode"` // "single-tenant" or "multi-tenant"
	Server              ServerConfig               `yaml:"server"`
	LRS                 LRSConfig                  `yaml:"lrs,omitempty"`      // Single-tenant only
	Auth                AuthConfig                 `yaml:"auth,omitempty"`     // Single-tenant only
	Database            DatabaseConfig             `yaml:"database,omitempty"` // Multi-tenant only
	Redis               RedisConfig                `yaml:"redis,omitempty"`    // Optional caching
	StatementForwarding StatementForwardingConfig  `yaml:"statement_forwarding,omitempty"`
}

// StatementForwardingConfig controls the proxy's statement fan-out sink: after
// a statement write is accepted by the backend LRS, if its verb is in Verbs,
// the statement is also POSTed to every configured Destination. This is how
// HazReady gets cmi5 statements into its own SQL for reporting without the
// LRS choice mattering — the forwarding logic lives here, not in the LRS.
//
// Delivery is asynchronous, in-memory, best-effort-with-retry: it never blocks
// or fails the original statement write, but it is NOT durable — statements
// still queued (not yet delivered) when the proxy process stops are lost.
// There's no persistent retry queue (e.g. backed by Postgres) yet; that's a
// known gap, worth revisiting if a listener outage causing gaps in reporting
// data becomes a real problem.
type StatementForwardingConfig struct {
	Enabled bool `yaml:"enabled"`
	// Verbs is the allowlist of xAPI verb IDs (full IRIs) that get forwarded.
	// Everything else (e.g. plain "experienced" statements a course might send)
	// is ignored. If Enabled is true and Verbs is left empty, Load() fills in
	// the standard cmi5 lifecycle verbs plus answered/interacted as a sensible
	// default - see config.example.yaml for the full list and how to override it.
	Verbs        []string             `yaml:"verbs,omitempty"`
	Destinations []ForwardDestination `yaml:"destinations,omitempty"`
	// QueueDBPath is where the durable delivery queue's SQLite file lives
	// (see internal/forwarder/store.go). Relative paths resolve against the
	// process's working directory, same as CONFIG_FILE - defaults to
	// "forward_queue.db" if left blank while Enabled is true. Only ever
	// created/opened when Enabled is true and at least one Destination is
	// configured, so enabling this feature is what actually creates the file.
	QueueDBPath string `yaml:"queue_db_path,omitempty"`
}

// ForwardDestination is one listener endpoint a matching statement gets POSTed
// to, as JSON: {"tenant_id": "...", "verb_id": "...", "statement": { ...the
// original xAPI statement, byte-for-byte as received... }}.
type ForwardDestination struct {
	URL string `yaml:"url"`
	// SharedSecret, if set, is sent as the "X-Cmi5-Forward-Secret" header so
	// the listener can confirm the call actually came from this proxy and not
	// an arbitrary POST from the internet. Set the same value on both ends.
	SharedSecret        string `yaml:"shared_secret,omitempty"`
	TimeoutSeconds      int    `yaml:"timeout_seconds,omitempty"`
	MaxRetries          int    `yaml:"max_retries,omitempty"`
	RetryBackoffSeconds int    `yaml:"retry_backoff_seconds,omitempty"`
}

// DefaultCmi5ForwardingVerbs is the standard cmi5 lifecycle vocabulary plus
// answered/interacted - the verbs actually needed for cmi5 completion/scoring
// reporting. Used as the default when statement_forwarding.enabled is true
// but no explicit verb list was configured.
var DefaultCmi5ForwardingVerbs = []string{
	"http://adlnet.gov/expapi/verbs/launched",
	"http://adlnet.gov/expapi/verbs/initialized",
	"http://adlnet.gov/expapi/verbs/completed",
	"http://adlnet.gov/expapi/verbs/passed",
	"http://adlnet.gov/expapi/verbs/failed",
	"http://adlnet.gov/expapi/verbs/abandoned",
	"https://w3id.org/xapi/adl/verbs/waived",
	"https://w3id.org/xapi/adl/verbs/satisfied",
	"http://adlnet.gov/expapi/verbs/terminated",
	"http://adlnet.gov/expapi/verbs/answered",
	"http://adlnet.gov/expapi/verbs/interacted",
}

// ServerConfig contains server settings
type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

// LRSConfig contains LRS connection settings
type LRSConfig struct {
	Endpoint        string `yaml:"endpoint"`
	Username        string `yaml:"username"`
	Password        string `yaml:"password"`
	ConnectionTimeout int  `yaml:"connection_timeout"` // seconds
	MaxRetries      int    `yaml:"max_retries"`
}

// AuthConfig contains authentication settings
type AuthConfig struct {
	JWTSecret      string   `yaml:"jwt_secret"`
	JWTTTLSeconds  int      `yaml:"jwt_ttl_seconds"`
	LMSAPIKeys     []string `yaml:"lms_api_keys"`
	PermissionPolicy string `yaml:"permission_policy"` // "strict" or "permissive"
	// PassthroughKeys are full-access, unscoped Basic Auth credentials for
	// testing/admin tools (e.g. xAPI conformance suites) that need to talk to
	// the proxy exactly like they'd talk to the raw LRS. Requests authenticated
	// this way skip actor/activity/registration scoping entirely and are
	// forwarded straight to the backend LRS. Same blast radius as the raw LRS
	// credential — do not hand these out to real course content.
	PassthroughKeys []PassthroughCredential `yaml:"passthrough_keys,omitempty"`
}

// PassthroughCredential is one static username/password pair accepted as
// full-access HTTP Basic Auth on the /xapi/* routes.
type PassthroughCredential struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// DatabaseConfig contains database settings
type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Database string `yaml:"database"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	SSLMode  string `yaml:"ssl_mode"`
}

// RedisConfig contains Redis cache settings
type RedisConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
	CacheTTL int    `yaml:"cache_ttl"` // seconds
}

// Load reads configuration from a YAML file
func Load(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Set defaults
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.LRS.ConnectionTimeout == 0 {
		cfg.LRS.ConnectionTimeout = 30
	}
	if cfg.LRS.MaxRetries == 0 {
		cfg.LRS.MaxRetries = 3
	}
	if cfg.Auth.JWTTTLSeconds == 0 {
		cfg.Auth.JWTTTLSeconds = 14400 // 4 hours
	}
	if cfg.Auth.PermissionPolicy == "" {
		cfg.Auth.PermissionPolicy = "strict"
	}
	if cfg.Database.Port == 0 {
		cfg.Database.Port = 5432
	}
	if cfg.Database.SSLMode == "" {
		cfg.Database.SSLMode = "require"
	}
	if cfg.Redis.Port == 0 {
		cfg.Redis.Port = 6379
	}
	if cfg.Redis.CacheTTL == 0 {
		cfg.Redis.CacheTTL = 300 // 5 minutes
	}
	if cfg.StatementForwarding.Enabled && len(cfg.StatementForwarding.Verbs) == 0 {
		cfg.StatementForwarding.Verbs = DefaultCmi5ForwardingVerbs
	}
	if cfg.StatementForwarding.Enabled && cfg.StatementForwarding.QueueDBPath == "" {
		cfg.StatementForwarding.QueueDBPath = "forward_queue.db"
	}
	for i := range cfg.StatementForwarding.Destinations {
		d := &cfg.StatementForwarding.Destinations[i]
		if d.TimeoutSeconds == 0 {
			d.TimeoutSeconds = 10
		}
		if d.MaxRetries == 0 {
			d.MaxRetries = 5
		}
		if d.RetryBackoffSeconds == 0 {
			d.RetryBackoffSeconds = 2
		}
		d.SharedSecret = expandEnv(d.SharedSecret)
	}

	// Expand environment variables
	cfg.LRS.Password = expandEnv(cfg.LRS.Password)
	cfg.Auth.JWTSecret = expandEnv(cfg.Auth.JWTSecret)
	cfg.Database.Password = expandEnv(cfg.Database.Password)
	cfg.Redis.Password = expandEnv(cfg.Redis.Password)
	for i := range cfg.Auth.PassthroughKeys {
		cfg.Auth.PassthroughKeys[i].Password = expandEnv(cfg.Auth.PassthroughKeys[i].Password)
	}

	return &cfg, nil
}

// expandEnv expands environment variables in format ${VAR} or $VAR
func expandEnv(s string) string {
	return os.ExpandEnv(s)
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.Mode == "single-tenant" {
		if c.LRS.Endpoint == "" {
			return fmt.Errorf("LRS endpoint is required in single-tenant mode")
		}
		if c.Auth.JWTSecret == "" {
			return fmt.Errorf("JWT secret is required")
		}
		if len(c.Auth.LMSAPIKeys) == 0 {
			return fmt.Errorf("at least one LMS API key is required")
		}
	}
	return nil
}
