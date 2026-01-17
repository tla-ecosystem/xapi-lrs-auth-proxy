package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config represents the application configuration
type Config struct {
	Mode     string         `yaml:"mode"` // "single-tenant" or "multi-tenant"
	Server   ServerConfig   `yaml:"server"`
	LRS      LRSConfig      `yaml:"lrs,omitempty"`      // Single-tenant only
	Auth     AuthConfig     `yaml:"auth,omitempty"`     // Single-tenant only
	Database DatabaseConfig `yaml:"database,omitempty"` // Multi-tenant only
	Redis    RedisConfig    `yaml:"redis,omitempty"`    // Optional caching
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
		cfg.Auth.JWTTTLSeconds = 3600 // 1 hour
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

	// Expand environment variables
	cfg.LRS.Password = expandEnv(cfg.LRS.Password)
	cfg.Auth.JWTSecret = expandEnv(cfg.Auth.JWTSecret)
	cfg.Database.Password = expandEnv(cfg.Database.Password)
	cfg.Redis.Password = expandEnv(cfg.Redis.Password)

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
