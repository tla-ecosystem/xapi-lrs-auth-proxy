package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	_ "github.com/lib/pq"
	log "github.com/sirupsen/logrus"

	"github.com/inxsol/xapi-lrs-auth-proxy/internal/config"
)

// TenantConfig represents a tenant's configuration
type TenantConfig struct {
	TenantID         string
	Hosts            []string
	LRSEndpoint      string
	LRSUsername      string
	LRSPassword      string
	JWTSecret        []byte
	JWTTTLSeconds    int
	LMSAPIKeys       map[string]bool // API key -> enabled
	PermissionPolicy string          // "strict" or "permissive"
}

// TenantStore provides access to tenant configurations
type TenantStore interface {
	GetByHost(ctx context.Context, host string) (*TenantConfig, error)
	GetByID(ctx context.Context, tenantID string) (*TenantConfig, error)
}

// SingleTenantStore implements TenantStore for single-tenant deployments
type SingleTenantStore struct {
	config *TenantConfig
	mu     sync.RWMutex
}

// NewSingleTenantStore creates a single-tenant store from configuration
func NewSingleTenantStore(cfg *config.Config) (*SingleTenantStore, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	apiKeys := make(map[string]bool)
	for _, key := range cfg.Auth.LMSAPIKeys {
		apiKeys[key] = true
	}

	tenantCfg := &TenantConfig{
		TenantID:         "default",
		Hosts:            []string{"*"}, // Accept any host
		LRSEndpoint:      cfg.LRS.Endpoint,
		LRSUsername:      cfg.LRS.Username,
		LRSPassword:      cfg.LRS.Password,
		JWTSecret:        []byte(cfg.Auth.JWTSecret),
		JWTTTLSeconds:    cfg.Auth.JWTTTLSeconds,
		LMSAPIKeys:       apiKeys,
		PermissionPolicy: cfg.Auth.PermissionPolicy,
	}

	return &SingleTenantStore{
		config: tenantCfg,
	}, nil
}

// GetByHost returns the single tenant config (ignores host)
func (s *SingleTenantStore) GetByHost(ctx context.Context, host string) (*TenantConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config, nil
}

// GetByID returns the single tenant config (ignores ID)
func (s *SingleTenantStore) GetByID(ctx context.Context, tenantID string) (*TenantConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config, nil
}

// DatabaseTenantStore implements TenantStore using PostgreSQL
type DatabaseTenantStore struct {
	db *sql.DB
	mu sync.RWMutex
	// In-memory cache (optional - could use Redis)
	cache map[string]*TenantConfig
}

// NewDatabaseTenantStore creates a database-backed tenant store
func NewDatabaseTenantStore(connStr string) (*DatabaseTenantStore, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	log.Info("Connected to tenant database")

	return &DatabaseTenantStore{
		db:    db,
		cache: make(map[string]*TenantConfig),
	}, nil
}

// GetByHost looks up tenant by host header
func (s *DatabaseTenantStore) GetByHost(ctx context.Context, host string) (*TenantConfig, error) {
	// Check cache first
	s.mu.RLock()
	if cached, ok := s.cache[host]; ok {
		s.mu.RUnlock()
		return cached, nil
	}
	s.mu.RUnlock()

	// Query database
	var tenantID string
	err := s.db.QueryRowContext(ctx, `
		SELECT tenant_id 
		FROM tenant_hosts 
		WHERE host = $1
	`, host).Scan(&tenantID)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("tenant not found for host: %s", host)
	}
	if err != nil {
		return nil, fmt.Errorf("database error: %w", err)
	}

	// Load full tenant config
	config, err := s.loadTenantConfig(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	// Cache it
	s.mu.Lock()
	s.cache[host] = config
	s.mu.Unlock()

	return config, nil
}

// GetByID looks up tenant by ID
func (s *DatabaseTenantStore) GetByID(ctx context.Context, tenantID string) (*TenantConfig, error) {
	return s.loadTenantConfig(ctx, tenantID)
}

// loadTenantConfig loads complete tenant configuration from database
func (s *DatabaseTenantStore) loadTenantConfig(ctx context.Context, tenantID string) (*TenantConfig, error) {
	config := &TenantConfig{
		TenantID:   tenantID,
		LMSAPIKeys: make(map[string]bool),
	}

	// Load LRS config
	err := s.db.QueryRowContext(ctx, `
		SELECT endpoint, username, password
		FROM tenant_lrs_config
		WHERE tenant_id = $1
	`, tenantID).Scan(&config.LRSEndpoint, &config.LRSUsername, &config.LRSPassword)

	if err != nil {
		return nil, fmt.Errorf("failed to load LRS config: %w", err)
	}

	// Load auth config
	var jwtSecretStr string
	err = s.db.QueryRowContext(ctx, `
		SELECT jwt_secret, jwt_ttl_seconds, permission_policy
		FROM tenant_auth_config
		WHERE tenant_id = $1
	`, tenantID).Scan(&jwtSecretStr, &config.JWTTTLSeconds, &config.PermissionPolicy)

	if err != nil {
		return nil, fmt.Errorf("failed to load auth config: %w", err)
	}

	config.JWTSecret = []byte(jwtSecretStr)

	// Load hosts
	rows, err := s.db.QueryContext(ctx, `
		SELECT host
		FROM tenant_hosts
		WHERE tenant_id = $1
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to load hosts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var host string
		if err := rows.Scan(&host); err != nil {
			return nil, err
		}
		config.Hosts = append(config.Hosts, host)
	}

	// Load API keys
	rows, err = s.db.QueryContext(ctx, `
		SELECT api_key_hash
		FROM tenant_lms_api_keys
		WHERE tenant_id = $1 AND revoked = false
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to load API keys: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		config.LMSAPIKeys[key] = true
	}

	return config, nil
}

// CreateTenant creates a new tenant
func (s *DatabaseTenantStore) CreateTenant(ctx context.Context, req *CreateTenantRequest) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Insert tenant
	_, err = tx.ExecContext(ctx, `
		INSERT INTO tenants (tenant_id, status)
		VALUES ($1, 'active')
	`, req.TenantID)
	if err != nil {
		return fmt.Errorf("failed to create tenant: %w", err)
	}

	// Insert LRS config
	_, err = tx.ExecContext(ctx, `
		INSERT INTO tenant_lrs_config (tenant_id, endpoint, username, password)
		VALUES ($1, $2, $3, $4)
	`, req.TenantID, req.LRS.Endpoint, req.LRS.Username, req.LRS.Password)
	if err != nil {
		return fmt.Errorf("failed to create LRS config: %w", err)
	}

	// Insert auth config
	_, err = tx.ExecContext(ctx, `
		INSERT INTO tenant_auth_config (tenant_id, jwt_secret, jwt_ttl_seconds, permission_policy)
		VALUES ($1, $2, $3, $4)
	`, req.TenantID, req.Auth.JWTSecret, req.Auth.JWTTTLSeconds, req.Auth.PermissionPolicy)
	if err != nil {
		return fmt.Errorf("failed to create auth config: %w", err)
	}

	// Insert hosts
	for _, host := range req.Hosts {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO tenant_hosts (tenant_id, host)
			VALUES ($1, $2)
		`, req.TenantID, host)
		if err != nil {
			return fmt.Errorf("failed to create host mapping: %w", err)
		}
	}

	// Insert API keys
	for _, key := range req.Auth.LMSAPIKeys {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO tenant_lms_api_keys (tenant_id, api_key_hash, description)
			VALUES ($1, $2, $3)
		`, req.TenantID, key, "Initial API key")
		if err != nil {
			return fmt.Errorf("failed to create API key: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Invalidate cache
	s.mu.Lock()
	for _, host := range req.Hosts {
		delete(s.cache, host)
	}
	s.mu.Unlock()

	log.WithField("tenant_id", req.TenantID).Info("Tenant created")

	return nil
}

// CreateTenantRequest represents a request to create a tenant
type CreateTenantRequest struct {
	TenantID string              `json:"tenant_id"`
	Hosts    []string            `json:"hosts"`
	LRS      LRSConfigRequest    `json:"lrs"`
	Auth     AuthConfigRequest   `json:"auth"`
}

type LRSConfigRequest struct {
	Endpoint string `json:"endpoint"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type AuthConfigRequest struct {
	JWTSecret        string   `json:"jwt_secret"`
	JWTTTLSeconds    int      `json:"jwt_ttl_seconds"`
	LMSAPIKeys       []string `json:"lms_api_keys"`
	PermissionPolicy string   `json:"permission_policy"`
}

// ListTenants returns all tenants
func (s *DatabaseTenantStore) ListTenants(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tenant_id FROM tenants WHERE status = 'active' ORDER BY tenant_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tenants []string
	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			return nil, err
		}
		tenants = append(tenants, tenantID)
	}

	return tenants, nil
}

// DeleteTenant deletes a tenant
func (s *DatabaseTenantStore) DeleteTenant(ctx context.Context, tenantID string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE tenants SET status = 'deleted' WHERE tenant_id = $1
	`, tenantID)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("tenant not found: %s", tenantID)
	}

	// Invalidate cache
	s.mu.Lock()
	for host := range s.cache {
		if s.cache[host].TenantID == tenantID {
			delete(s.cache, host)
		}
	}
	s.mu.Unlock()

	log.WithField("tenant_id", tenantID).Info("Tenant deleted")

	return nil
}

// MarshalJSON implements json.Marshaler for TenantConfig
func (t *TenantConfig) MarshalJSON() ([]byte, error) {
	// Don't include secrets in JSON output
	return json.Marshal(struct {
		TenantID         string   `json:"tenant_id"`
		Hosts            []string `json:"hosts"`
		LRSEndpoint      string   `json:"lrs_endpoint"`
		PermissionPolicy string   `json:"permission_policy"`
	}{
		TenantID:         t.TenantID,
		Hosts:            t.Hosts,
		LRSEndpoint:      t.LRSEndpoint,
		PermissionPolicy: t.PermissionPolicy,
	})
}
