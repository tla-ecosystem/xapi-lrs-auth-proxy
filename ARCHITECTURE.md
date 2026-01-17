# Architecture Documentation

## Overview

The xAPI LRS Auth Proxy is a security and authentication layer that sits between Learning Management Systems (LMS) and Learning Record Stores (LRS) to provide proper session-scoped authentication for cmi5 and xAPI learning content.

## Problem Statement

Traditional xAPI/cmi5 implementations use static API keys that:
- Never expire (or have very long expiration)
- Grant access to write statements as any actor
- Cannot be scoped to specific sessions
- Violate privacy principles (FERPA, GDPR)
- Create security vulnerabilities

## Solution

This proxy implements:
1. **JWT-based authentication** with short-lived, session-scoped tokens
2. **cmi5 permission enforcement** (actor, activity, registration scoping)
3. **Multi-tenant support** for SaaS deployments
4. **LRS abstraction** allowing any xAPI-compliant LRS backend

## Architecture Diagram

```
┌─────────┐                                    ┌──────────────┐
│   LMS   │────── POST /auth/token ──────────►│              │
│         │  (LMS API Key)                     │    Token     │
└─────────┘                                    │   Service    │
                                               │              │
                                               │  (validates  │
     ┌─────────────── JWT Token ──────────────┤  LMS API key │
     │                                         │  issues JWT) │
     │                                         │              │
     │                                         └──────────────┘
     │                                                │
     ▼                                                │
┌──────────┐                                          │
│ Content  │────── POST /xapi/statements ─────────────┤
│ (SCORM/  │  (JWT token in Authorization)            │
│  cmi5)   │                                           ▼
└──────────┘                                    ┌──────────────┐
                                                │              │
                                                │   Proxy      │
                                                │  Validator   │
                                                │              │
                                                │ (validates   │
                                                │  JWT token,  │
                                                │  enforces    │
                                                │  permissions)│
                                                │              │
                                                └──────┬───────┘
                                                       │
                                            Forwards   │
                                            with LRS   │
                                            credentials│
                                                       │
                                                       ▼
                                                ┌─────────────┐
                                                │     LRS     │
                                                │  (any xAPI  │
                                                │  compliant) │
                                                └─────────────┘
```

## Component Details

### 1. Token Service

**Purpose:** Issues JWT tokens to LMS when content is launched

**Inputs:**
- LMS API key (validates LMS identity)
- Actor (learner identity)
- Registration (session ID)
- Activity ID (content being launched)
- Course ID (optional, for course-wide permissions)
- Permissions (read/write scopes)

**Outputs:**
- JWT token (signed with tenant's secret)
- Expiration timestamp

**Key Functions:**
```go
func IssueToken(req TokenRequest) TokenResponse
- Validates LMS API key
- Creates JWT claims with actor, registration, activity
- Encodes permissions into token
- Signs with tenant's JWT secret
- Returns token + expiration
```

### 2. Permission Validator

**Purpose:** Enforces cmi5 permission model on xAPI statements

**Permission Scopes Supported:**

| Scope | Description | Use Case |
|-------|-------------|----------|
| `actor-activity-registration-scoped` | Default isolation | Standard content |
| `actor-course-registration-scoped` | Course-wide read | Analytics, competency |
| `actor-activity-all-registrations` | Historical data | Adaptive learning |
| `group-activity-registration-scoped` | Team training | Collaborative simulations |
| `actor-cross-course-certification` | Multi-course | Certification tracking |

**Validation Rules:**

**For Write Operations:**
```
IF permission.write == "actor-activity-registration-scoped":
    REQUIRE statement.actor == token.actor
    REQUIRE statement.object.id == token.activity_id
    REQUIRE statement.context.registration == token.registration
```

**For Read Operations:**
```
IF permission.read == "actor-course-registration-scoped":
    REQUIRE query.agent == token.actor (if specified)
    REQUIRE query.registration == token.registration (if specified)
    ALLOW query.activity IN token.course (any activity in course)
```

### 3. xAPI Proxy

**Purpose:** Forwards validated requests to LRS backend

**Flow:**
1. Receive xAPI request from content
2. Extract JWT from Authorization header
3. Validate JWT signature and expiration
4. Parse statement(s) from request body
5. Validate each statement against permissions
6. Forward to LRS with master credentials
7. Return LRS response to content

**Key Features:**
- Connection pooling to LRS
- Transparent passthrough of xAPI responses
- Preserves all xAPI headers
- Supports all xAPI endpoints (statements, state, profiles, about)

### 4. Tenant Store

**Purpose:** Manages tenant configurations (single or multi-tenant)

**Single-Tenant Mode:**
- Configuration from YAML file
- One tenant serves all requests
- Simple deployment for on-premises

**Multi-Tenant Mode:**
- Configuration from PostgreSQL database
- Host header routing to tenant
- Separate JWT secrets per tenant
- Separate LRS backends per tenant

**Tenant Configuration:**
```go
type TenantConfig struct {
    TenantID         string
    Hosts            []string           // DNS names
    LRSEndpoint      string            // Backend LRS
    LRSUsername      string
    LRSPassword      string
    JWTSecret        []byte            // For signing tokens
    JWTTTLSeconds    int
    LMSAPIKeys       map[string]bool   // Authorized LMS keys
    PermissionPolicy string            // "strict" or "permissive"
}
```

## Security Model

### JWT Token Structure

```json
{
  "tenant_id": "acme-corp",
  "actor": {
    "objectType": "Agent",
    "mbox": "mailto:learner@example.com"
  },
  "registration": "550e8400-e29b-41d4-a716-446655440000",
  "activity_id": "https://example.com/activity/lesson1",
  "course_id": "safety-training-101",
  "permissions": {
    "write": "actor-activity-registration-scoped",
    "read": "actor-course-registration-scoped"
  },
  "exp": 1705518000,  // Expires in 1 hour
  "iat": 1705514400,  // Issued at
  "iss": "xapi-lrs-auth-proxy"
}
```

**Signed with:** HMAC-SHA256 using tenant's JWT secret

### Security Properties

1. **Non-forgeable:** Cannot create valid token without JWT secret
2. **Tamper-evident:** Modifying token breaks signature
3. **Time-limited:** Expires after configurable TTL (default 1 hour)
4. **Session-bound:** Tied to specific registration (session ID)
5. **Actor-bound:** Tied to specific learner
6. **Activity-scoped:** Limited to specific activity or course

### Threat Model

**Threats Mitigated:**
- ✅ Actor impersonation (token tied to specific actor)
- ✅ Session hijacking (short expiration window)
- ✅ Cross-session access (registration isolation)
- ✅ Privilege escalation (permission enforcement)
- ✅ Token replay (expiration check)
- ✅ Static key exposure (dynamic JWTs replace static keys)

**Remaining Threats:**
- ⚠️ Token interception (mitigated by TLS/HTTPS)
- ⚠️ Compromised JWT secret (rotate secrets periodically)
- ⚠️ LRS compromise (proxy can't protect against this)

## Performance Characteristics

### Latency Overhead

- JWT validation: ~0.05ms (cryptographic signature check)
- Permission validation: ~0.01ms (rule checking)
- Total proxy overhead: ~0.1ms

**LRS remains the bottleneck:**
- Network latency: 10-50ms
- LRS processing: 10-100ms
- Total request time: 20-150ms

**Proxy overhead is <1% of total request time**

### Throughput

**Single Instance Capacity:**
- Token issuance: 10,000+ tokens/second
- Statement validation: 50,000+ statements/second
- End-to-end proxy: 10,000+ requests/second

**Limiting Factor:** LRS backend capacity (100-1000 writes/second typical)

### Scalability

**Horizontal Scaling:**
- Stateless design (JWT validation requires no database lookup)
- Add proxy instances behind load balancer
- Linear scaling up to LRS capacity

**Multi-Tenant Scaling:**
- 1,000+ tenants per instance
- ~2KB memory per tenant
- Database caching reduces lookup overhead

## Data Flow Examples

### Example 1: Standard Content Launch

```
1. LMS: User clicks "Launch Lesson"
   └─> LMS calls POST /auth/token
       Headers: Authorization: Bearer lms-api-key-123
       Body: {actor, registration, activity_id, permissions}

2. Proxy: Validates LMS API key against tenant config
   └─> Creates JWT with actor/registration/activity/permissions
   └─> Signs JWT with tenant's secret
   └─> Returns {token: "eyJ...", expires_at: "..."}

3. LMS: Launches content with cmi5 parameters
   └─> endpoint: https://proxy.example.com/xapi/
   └─> fetch: (content gets token via cmi5 fetch API)

4. Content: Sends statement
   └─> POST /xapi/statements
       Headers: Authorization: Bearer eyJ...
       Body: [{actor, verb, object, context: {registration}}]

5. Proxy: Validates JWT
   └─> Checks signature with tenant's secret
   └─> Checks expiration
   └─> Validates statement.actor == token.actor
   └─> Validates statement.object.id == token.activity_id
   └─> Validates statement.context.registration == token.registration
   └─> Forwards to LRS with master credentials

6. LRS: Processes statement
   └─> Returns response to proxy

7. Proxy: Returns LRS response to content
```

### Example 2: Competency Evaluation AU

```
1. LMS: Launches competency evaluator AU
   └─> Requests token with elevated permissions
       permissions: {
         write: "actor-activity-registration-scoped",
         read: "actor-course-registration-scoped"  ← Elevated
       }

2. Content: Reads statements from multiple AUs
   └─> GET /xapi/statements?registration=123&activity=course/module1
   └─> GET /xapi/statements?registration=123&activity=course/module2
   └─> GET /xapi/statements?registration=123&activity=course/module3

3. Proxy: Allows reads because permission is "actor-course-registration-scoped"
   └─> Validates actor matches (if agent filter used)
   └─> Validates registration matches
   └─> Allows different activities (course-wide scope)

4. Content: Evaluates competency
   └─> Writes competency assertion
       POST /xapi/statements
       Body: [{actor, verb: "asserted-competency", object: "competency-id"}]

5. Proxy: Validates write (default scope still applies)
   └─> Ensures actor/registration match
   └─> Activity can be different (competency ID)
```

## Multi-Tenant Architecture

### Host-Based Routing

```
Request: Host: acme.proxy.example.com
         ↓
    Extract: "acme"
         ↓
    Database Query: SELECT * FROM tenants WHERE host = 'acme.proxy.example.com'
         ↓
    Load Config: {tenant_id, lrs_endpoint, jwt_secret, ...}
         ↓
    Add to Context: request.Context().Value("tenant")
         ↓
    All downstream operations use this tenant config
```

### Tenant Isolation

**Critical Isolation Points:**
1. JWT secrets unique per tenant
2. LRS backends separate per tenant (or separate credentials)
3. API keys scoped to tenant
4. Audit logs tagged with tenant_id
5. No cross-tenant data leakage

## Database Schema

### Tenants Table
```sql
tenants
├── tenant_id (PK)
├── status ('active', 'suspended', 'deleted')
├── created_at
└── updated_at
```

### Tenant Configuration Tables
```sql
tenant_hosts
├── tenant_id (FK)
├── host (unique)
└── created_at

tenant_lrs_config
├── tenant_id (PK, FK)
├── endpoint
├── username
├── password (encrypted)
├── connection_timeout
└── max_retries

tenant_auth_config
├── tenant_id (PK, FK)
├── jwt_secret (encrypted)
├── jwt_ttl_seconds
└── permission_policy

tenant_lms_api_keys
├── id (PK)
├── tenant_id (FK)
├── api_key_hash
├── description
├── expires_at
└── revoked
```

### Audit Table
```sql
audit_log
├── id (PK)
├── tenant_id (FK)
├── timestamp
├── operation
├── actor_mbox
├── registration
├── activity_id
├── permission_write
├── permission_read
├── success
├── error_message
├── ip_address
└── user_agent
```

## Deployment Models

### Model 1: On-Premises Single-Tenant

```
[LMS] ──┐
         ├──► [Proxy] ──► [LRS]
[Users]─┘    (single     (on-premises)
             instance)
```

**Use Case:** University or company with own infrastructure
**Configuration:** YAML file, no database needed
**Scaling:** Single instance sufficient for most use cases

### Model 2: SaaS Multi-Tenant

```
[Org A LMS] ──┐
               ├──► [Load Balancer] ──┐
[Org B LMS] ──┘                       │
                                      ├──► [Proxy 1] ──┐
[Org A Users] ────────────────────────┘   [Proxy 2]    ├──► [LRS A]
[Org B Users] ────────────────────────────[Proxy 3]    ├──► [LRS B]
                                                        └──► [LRS C]
                    ▲                        │
                    │                        ▼
                [PostgreSQL] ◄───────── [Cache (Redis)]
```

**Use Case:** SaaS LRS provider or proxy service
**Configuration:** Database-backed tenant configs
**Scaling:** Horizontal scaling behind load balancer

### Model 3: Hybrid

```
[Small Orgs] ──► [Shared Proxy Pool] ──► [Shared LRS]
                                      
[Enterprise] ──► [Dedicated Proxy]   ──► [Dedicated LRS]
```

**Use Case:** Mixed customer base
**Configuration:** Both models supported
**Scaling:** Per-customer decision

## Future Enhancements

### Planned Features
1. **Rate Limiting:** Per-tenant request limits
2. **Metrics:** Prometheus endpoint for monitoring
3. **Token Revocation:** Blacklist/revoke specific tokens
4. **OAuth2 Support:** Standard OAuth2 token flow
5. **Admin UI:** Web interface for tenant management
6. **Redis Caching:** Cache tenant configs for performance
7. **TLS Autocert:** Automatic Let's Encrypt certificates

### Extension Points
1. **Custom Validators:** Pluggable permission validators
2. **Webhook Support:** Notify on token issuance/revocation
3. **Policy Engine:** Configurable permission policies
4. **Audit Exports:** Export audit logs to external systems

## References

- [cmi5 Specification](https://github.com/AICC/CMI-5_Spec_Current)
- [xAPI Specification](https://github.com/adlnet/xAPI-Spec)
- [JWT RFC 7519](https://tools.ietf.org/html/rfc7519)
- [OAuth 2.0 RFC 6749](https://tools.ietf.org/html/rfc6749)
