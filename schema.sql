-- PostgreSQL schema for xAPI LRS Auth Proxy multi-tenant mode

-- Tenants table
CREATE TABLE tenants (
    tenant_id VARCHAR(100) PRIMARY KEY,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    status VARCHAR(20) DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'deleted'))
);

-- Tenant host mappings
CREATE TABLE tenant_hosts (
    tenant_id VARCHAR(100) REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    host VARCHAR(255) NOT NULL UNIQUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, host)
);

CREATE INDEX idx_tenant_hosts_host ON tenant_hosts(host);

-- Tenant LRS configuration
CREATE TABLE tenant_lrs_config (
    tenant_id VARCHAR(100) PRIMARY KEY REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    endpoint VARCHAR(512) NOT NULL,
    username VARCHAR(255) NOT NULL,
    password TEXT NOT NULL,  -- Encrypted in production
    connection_timeout INT DEFAULT 30,
    max_retries INT DEFAULT 3,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Tenant authentication configuration
CREATE TABLE tenant_auth_config (
    tenant_id VARCHAR(100) PRIMARY KEY REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    jwt_secret TEXT NOT NULL,  -- Base64 encoded, encrypted in production
    jwt_ttl_seconds INT DEFAULT 3600,
    permission_policy VARCHAR(20) DEFAULT 'strict' CHECK (permission_policy IN ('strict', 'permissive')),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Tenant LMS API keys
CREATE TABLE tenant_lms_api_keys (
    id SERIAL PRIMARY KEY,
    tenant_id VARCHAR(100) REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    api_key_hash VARCHAR(255) NOT NULL,  -- Store hash, not plaintext
    description TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP,
    revoked BOOLEAN DEFAULT FALSE,
    revoked_at TIMESTAMP
);

CREATE INDEX idx_tenant_lms_api_keys_tenant ON tenant_lms_api_keys(tenant_id);
CREATE INDEX idx_tenant_lms_api_keys_hash ON tenant_lms_api_keys(api_key_hash) WHERE revoked = FALSE;

-- Audit log for all proxy operations
CREATE TABLE audit_log (
    id BIGSERIAL PRIMARY KEY,
    tenant_id VARCHAR(100) REFERENCES tenants(tenant_id) ON DELETE SET NULL,
    timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    operation VARCHAR(50) NOT NULL,  -- 'token_issued', 'statement_write', 'statement_read', etc.
    actor_mbox VARCHAR(255),
    registration VARCHAR(255),
    activity_id VARCHAR(512),
    permission_write VARCHAR(100),
    permission_read VARCHAR(100),
    success BOOLEAN DEFAULT TRUE,
    error_message TEXT,
    ip_address INET,
    user_agent TEXT
);

CREATE INDEX idx_audit_log_tenant ON audit_log(tenant_id, timestamp DESC);
CREATE INDEX idx_audit_log_actor ON audit_log(actor_mbox, timestamp DESC);
CREATE INDEX idx_audit_log_registration ON audit_log(registration, timestamp DESC);

-- Permission approval tracking (for LMS admin approval workflow)
CREATE TABLE permission_approvals (
    id SERIAL PRIMARY KEY,
    tenant_id VARCHAR(100) REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    course_id VARCHAR(255) NOT NULL,
    au_id VARCHAR(255) NOT NULL,
    permission_type VARCHAR(50) NOT NULL,  -- 'statements_read', 'statements_write', 'state_read', etc.
    permission_scope VARCHAR(100) NOT NULL,
    approved BOOLEAN DEFAULT FALSE,
    approved_by VARCHAR(255),
    approved_at TIMESTAMP,
    justification TEXT,
    revoked BOOLEAN DEFAULT FALSE,
    revoked_at TIMESTAMP,
    revoked_by VARCHAR(255),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, course_id, au_id, permission_type)
);

CREATE INDEX idx_permission_approvals_tenant_course ON permission_approvals(tenant_id, course_id);
CREATE INDEX idx_permission_approvals_approved ON permission_approvals(tenant_id, approved) WHERE approved = TRUE AND revoked = FALSE;

-- Function to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Triggers for updated_at
CREATE TRIGGER update_tenants_updated_at BEFORE UPDATE ON tenants
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_tenant_lrs_config_updated_at BEFORE UPDATE ON tenant_lrs_config
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_tenant_auth_config_updated_at BEFORE UPDATE ON tenant_auth_config
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Sample data for testing (remove in production)
-- INSERT INTO tenants (tenant_id, status) VALUES ('demo-tenant', 'active');
-- INSERT INTO tenant_hosts (tenant_id, host) VALUES ('demo-tenant', 'demo.proxy.example.com');
-- INSERT INTO tenant_lrs_config (tenant_id, endpoint, username, password) 
--   VALUES ('demo-tenant', 'https://lrs.demo.com/xapi/', 'admin', 'change-me');
-- INSERT INTO tenant_auth_config (tenant_id, jwt_secret, jwt_ttl_seconds, permission_policy)
--   VALUES ('demo-tenant', 'change-this-to-a-secure-random-secret', 3600, 'strict');
-- INSERT INTO tenant_lms_api_keys (tenant_id, api_key_hash, description)
--   VALUES ('demo-tenant', 'demo-lms-api-key', 'Demo LMS API key');
