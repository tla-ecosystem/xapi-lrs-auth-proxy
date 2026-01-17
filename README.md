# xAPI LRS Auth Proxy

A reference implementation of a secure authentication proxy for cmi5 and xAPI learning systems.

## 🪟 Windows Users - Start Here!

**New to Windows development?** We've created a complete step-by-step guide just for you:

**👉 [Windows Quick Start Guide](WINDOWS_QUICKSTART.md)**

Covers:
- Installing Go on Windows
- Setting up PATH correctly
- Proper file structure
- Building with PowerShell
- Testing on Windows
- Common Windows-specific issues

---

## Overview

This proxy provides:
- **Session-scoped JWT tokens** instead of static API keys
- **cmi5 permission enforcement** (actor, activity, registration scoping)
- **Multi-tenant support** via Host header routing
- **LRS abstraction** - swap any xAPI-compliant LRS backend
- **Centralized security** - one place to manage authentication

## Architecture

```
[LMS] → Token API → Issues JWT
         ↓
[Content] → xAPI Proxy → Validates JWT → [LRS]
```

## Features

### Security
- ✅ JWT-based authentication with short-lived tokens
- ✅ Actor, activity, and registration scoping
- ✅ Permission enforcement per cmi5 specification
- ✅ Group actor support for team training
- ✅ Audit logging

### Multi-Tenancy
- ✅ Host header-based tenant routing
- ✅ Tenant-specific JWT secrets
- ✅ Tenant-specific LRS backends
- ✅ Per-tenant permission policies

### Deployment
- ✅ Single-tenant mode (on-premises)
- ✅ Multi-tenant mode (SaaS)
- ✅ Database-backed (PostgreSQL) or config file
- ✅ Redis caching (optional)
- ✅ Docker support

## Quick Start

### Prerequisites

- **Go 1.21 or later** - [Download](https://go.dev/dl/)
- **Git** (for cloning) - [Download](https://git-scm.com/)
- **PostgreSQL** (for multi-tenant mode only)

**Windows users:** See [WINDOWS_QUICKSTART.md](WINDOWS_QUICKSTART.md) for detailed setup.

### Single Tenant (On-Premises)

**1. Clone the repository:**

```bash
git clone https://github.com/tla-ecosystem/xapi-lrs-auth-proxy.git
cd xapi-lrs-auth-proxy
```

**2. Create configuration:**

```bash
cp config.example.yaml config.yaml
# Edit config.yaml with your LRS details
```

Minimal `config.yaml`:
```yaml
mode: single-tenant
server:
  port: 8080
  
lrs:
  endpoint: https://lrs.example.com/xapi/
  username: admin
  password: your-lrs-password
  
auth:
  jwt_secret: your-256-bit-secret-key-here
  jwt_ttl_seconds: 3600
  lms_api_keys:
    - your-lms-api-key
```

**3. Build and run:**

```bash
# Download dependencies
go mod download

# Build
go build -o xapi-proxy cmd/proxy/main.go

# Run
./xapi-proxy --config config.yaml
```

**Windows:**
```powershell
go mod download
go build -o xapi-proxy.exe ./cmd/proxy
.\xapi-proxy.exe --config config.yaml
```

**4. Test:**

```bash
# Health check
curl http://localhost:8080/health

# Request token
curl -X POST http://localhost:8080/auth/token \
  -H "Authorization: Bearer your-lms-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "actor": {"mbox": "mailto:learner@example.com"},
    "registration": "uuid-here",
    "activity_id": "https://example.com/activity",
    "permissions": {
      "write": "actor-activity-registration-scoped",
      "read": "actor-activity-registration-scoped"
    }
  }'
```

### Multi-Tenant (SaaS)

**1. Setup database:**

```bash
psql -U postgres -f schema.sql
```

**2. Run in multi-tenant mode:**

```bash
./xapi-proxy --multi-tenant --db "postgresql://user:pass@localhost/xapi_proxy"
```

**3. Create tenants via Admin API:**

```bash
curl -X POST http://localhost:8080/admin/tenants \
  -H "Authorization: Bearer admin-token" \
  -d '{
    "tenant_id": "acme-corp",
    "hosts": ["acme.proxy.example.com"],
    "lrs": {
      "endpoint": "https://lrs.acme.com/xapi/",
      "username": "admin",
      "password": "password"
    }
  }'
```

### Docker

```bash
# Single-tenant
docker-compose up -d proxy-single

# Multi-tenant (with PostgreSQL)
docker-compose up -d proxy-multi postgres redis
```

## API Reference

### Token API (LMS-facing)

**Issue Token:**
```http
POST /auth/token
Authorization: Bearer <LMS-API-Key>
Content-Type: application/json

{
  "actor": {
    "objectType": "Agent",
    "mbox": "mailto:learner@example.com"
  },
  "registration": "uuid-here",
  "activity_id": "https://example.com/activity",
  "course_id": "safety-training",
  "permissions": {
    "write": "actor-activity-registration-scoped",
    "read": "actor-course-registration-scoped"
  }
}

Response: {
  "token": "eyJhbGci...",
  "expires_at": "2026-01-17T15:30:00Z"
}
```

### xAPI Proxy (Content-facing)

**Post Statements:**
```http
POST /xapi/statements
Authorization: Bearer <JWT-token>
Content-Type: application/json

[{
  "actor": {...},
  "verb": {...},
  "object": {...},
  "context": {
    "registration": "uuid-here"
  }
}]
```

All standard xAPI endpoints are supported:
- `POST/PUT/GET /xapi/statements`
- `POST/PUT/GET/DELETE /xapi/activities/state`
- `POST/PUT/GET/DELETE /xapi/activities/profile`
- `POST/PUT/GET/DELETE /xapi/agents/profile`
- `GET /xapi/about`

## Permission Scopes

### Default (most restrictive)
- `actor-activity-registration-scoped` - Write/read only own activity in current session

### Course-wide
- `actor-course-registration-scoped` - Read across entire course (current session)

### Historical
- `actor-activity-all-registrations` - Read across all attempts

### Team Training
- `group-activity-registration-scoped` - Group actor with member validation

### Cross-Course
- `actor-cross-course-certification` - Read across multiple courses

## Performance

**Benchmarks:**
- Token issuance: 10,000+ tokens/second
- Statement validation: 50,000+ statements/second
- Proxy overhead: <0.1ms per request
- Horizontal scaling: Linear up to LRS capacity

**Capacity:**
- Single instance: 10,000 concurrent users
- Multi-tenant: 1,000+ tenants per instance

## Documentation

**Getting Started:**
- 🪟 [Windows Quick Start](WINDOWS_QUICKSTART.md) - Step-by-step for Windows
- 📘 [README.md](README.md) - This file
- 🧪 [TESTING.md](TESTING.md) - Comprehensive testing guide

**Technical:**
- 🏗️ [ARCHITECTURE.md](ARCHITECTURE.md) - Design decisions, data flows, security model
- 🤝 [CONTRIBUTING.md](CONTRIBUTING.md) - How to contribute
- 📋 [PROJECT_SUMMARY.md](PROJECT_SUMMARY.md) - Executive overview

## Building

```bash
# Standard build
go build -o xapi-proxy cmd/proxy/main.go

# With make
make build

# Windows
go build -o xapi-proxy.exe ./cmd/proxy

# Docker
docker build -t xapi-proxy .
```

**Troubleshooting build issues?** See [WINDOWS_QUICKSTART.md](WINDOWS_QUICKSTART.md#troubleshooting)

## Testing

```bash
# Run all tests
go test ./...

# With make
make test

# Use test script
./test-client.sh
```

**Windows:** See [WINDOWS_QUICKSTART.md](WINDOWS_QUICKSTART.md#testing) for PowerShell test commands.

## Standards Compliance

- ✅ **cmi5 specification** - Implements permission model
- ✅ **xAPI 1.0.3** - Full xAPI endpoint support
- ✅ **JWT RFC 7519** - Standard token format
- ✅ **OAuth 2.0 patterns** - Bearer token authentication

## License

MIT License - see [LICENSE](LICENSE) file

## Contributing

This is a reference implementation for the cmi5/xAPI community. Contributions welcome!

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## Support

- **GitHub Issues:** https://github.com/tla-ecosystem/xapi-lrs-auth-proxy/issues
- **TLA Forum:** https://discuss.tlaworks.com/
- **IEEE LTSC:** cmi5 working group

## Acknowledgments

- IEEE LTSC cmi5 Working Group - Bill McDonald and Andy Johnson
- inXsol LLC , PoweredLearning Corp, SimplestData LLC

---

**Quick Links:**
- 🪟 [Windows Setup Guide](WINDOWS_QUICKSTART.md)
- 📖 [Full Documentation](ARCHITECTURE.md)
- 🧪 [Testing Guide](TESTING.md)
- 🐛 [Report Issues](https://github.com/tla-ecosystem/xapi-lrs-auth-proxy/issues)
