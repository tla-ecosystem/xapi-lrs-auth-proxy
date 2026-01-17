# xAPI LRS Auth Proxy

A reference implementation of a secure authentication proxy for cmi5 and xAPI learning systems.

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

### Single Tenant (On-Premises)

1. **Create configuration:**

```yaml
# config.yaml
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

2. **Run the proxy:**

```bash
./xapi-proxy --config config.yaml
```

3. **Configure LMS:**
   - Token API: `http://localhost:8080/auth/token`
   - xAPI Endpoint: `http://localhost:8080/xapi/`

### Multi-Tenant (SaaS)

1. **Setup database:**

```bash
psql -U postgres -f schema.sql
```

2. **Run in multi-tenant mode:**

```bash
./xapi-proxy --multi-tenant --db "postgresql://user:pass@localhost/xapi_proxy"
```

3. **Create tenants via Admin API:**

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

[
  {
    "actor": {...},
    "verb": {...},
    "object": {...},
    "context": {
      "registration": "uuid-here"
    }
  }
]
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

## Building

```bash
go build -o xapi-proxy cmd/proxy/main.go
```

## Testing

```bash
go test ./...
```

## Docker

```bash
docker build -t xapi-proxy .
docker run -p 8080:8080 -v $(pwd)/config.yaml:/config.yaml xapi-proxy
```

## License

MIT License - see LICENSE file

## Contributing

This is a reference implementation for the cmi5/xAPI community. Contributions welcome!

## Support

For issues and questions, please use GitHub Issues or contact the IEEE LTSC cmi5 working group.
