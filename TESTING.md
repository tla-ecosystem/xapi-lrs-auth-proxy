# Testing and Deployment Guide

## Quick Start Testing

### 1. Single-Tenant Local Testing

```bash
# Copy example config
cp config.example.yaml config.yaml

# Edit config.yaml with your LRS details
# Set environment variables
export LRS_PASSWORD="your-lrs-password"
export JWT_SECRET=$(openssl rand -base64 32)
export LMS_API_KEY_1="test-api-key-12345"

# Run the proxy
make run

# Test health endpoint
curl http://localhost:8080/health
```

### 2. Request a Token (LMS perspective)

```bash
# Request JWT token
curl -X POST http://localhost:8080/auth/token \
  -H "Authorization: Bearer test-api-key-12345" \
  -H "Content-Type: application/json" \
  -d '{
    "actor": {
      "objectType": "Agent",
      "mbox": "mailto:learner@example.com",
      "name": "Test Learner"
    },
    "registration": "550e8400-e29b-41d4-a716-446655440000",
    "activity_id": "https://example.com/activity/test-lesson",
    "course_id": "test-course-101",
    "permissions": {
      "write": "actor-activity-registration-scoped",
      "read": "actor-activity-registration-scoped"
    }
  }'

# Response contains JWT token:
# {
#   "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
#   "expires_at": "2026-01-17T15:30:00Z"
# }
```

### 3. Send xAPI Statement (Content perspective)

```bash
# Use the JWT token from step 2
TOKEN="eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."

# Post a statement
curl -X POST http://localhost:8080/xapi/statements \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Experience-API-Version: 1.0.3" \
  -d '[{
    "actor": {
      "objectType": "Agent",
      "mbox": "mailto:learner@example.com",
      "name": "Test Learner"
    },
    "verb": {
      "id": "http://adlnet.gov/expapi/verbs/completed",
      "display": {"en-US": "completed"}
    },
    "object": {
      "id": "https://example.com/activity/test-lesson",
      "objectType": "Activity"
    },
    "context": {
      "registration": "550e8400-e29b-41d4-a716-446655440000"
    }
  }]'
```

### 4. Permission Validation Testing

**Test 1: Valid statement (should succeed)**
```bash
# Statement matches token actor, activity, and registration
curl -X POST http://localhost:8080/xapi/statements \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '[{
    "actor": {"mbox": "mailto:learner@example.com"},
    "verb": {"id": "http://adlnet.gov/expapi/verbs/passed"},
    "object": {"id": "https://example.com/activity/test-lesson"},
    "context": {"registration": "550e8400-e29b-41d4-a716-446655440000"}
  }]'
# Expected: 200 OK
```

**Test 2: Wrong actor (should fail)**
```bash
curl -X POST http://localhost:8080/xapi/statements \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '[{
    "actor": {"mbox": "mailto:different-learner@example.com"},
    "verb": {"id": "http://adlnet.gov/expapi/verbs/passed"},
    "object": {"id": "https://example.com/activity/test-lesson"},
    "context": {"registration": "550e8400-e29b-41d4-a716-446655440000"}
  }]'
# Expected: 403 Forbidden - "actor mismatch"
```

**Test 3: Wrong activity (should fail)**
```bash
curl -X POST http://localhost:8080/xapi/statements \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '[{
    "actor": {"mbox": "mailto:learner@example.com"},
    "verb": {"id": "http://adlnet.gov/expapi/verbs/passed"},
    "object": {"id": "https://example.com/activity/different-lesson"},
    "context": {"registration": "550e8400-e29b-41d4-a716-446655440000"}
  }]'
# Expected: 403 Forbidden - "activity mismatch"
```

**Test 4: Wrong registration (should fail)**
```bash
curl -X POST http://localhost:8080/xapi/statements \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '[{
    "actor": {"mbox": "mailto:learner@example.com"},
    "verb": {"id": "http://adlnet.gov/expapi/verbs/passed"},
    "object": {"id": "https://example.com/activity/test-lesson"},
    "context": {"registration": "different-registration-uuid"}
  }]'
# Expected: 403 Forbidden - "registration mismatch"
```

## Multi-Tenant Testing

### 1. Start Multi-Tenant Stack

```bash
# Start PostgreSQL, Redis, and proxy
docker-compose up -d proxy-multi postgres redis

# Verify database is ready
docker-compose exec postgres psql -U xapi_proxy -d xapi_proxy -c "SELECT 1;"
```

### 2. Create a Tenant

```bash
# Create tenant via Admin API
curl -X POST http://localhost:8081/admin/tenants \
  -H "Authorization: Bearer admin-token" \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_id": "acme-corp",
    "hosts": ["acme.localhost", "localhost:8081"],
    "lrs": {
      "endpoint": "https://lrs.acme.com/xapi/",
      "username": "acme_user",
      "password": "acme_password"
    },
    "auth": {
      "jwt_secret": "acme-jwt-secret-32-bytes-minimum",
      "jwt_ttl_seconds": 3600,
      "lms_api_keys": ["acme-lms-key-12345"],
      "permission_policy": "strict"
    }
  }'
```

### 3. Test Tenant Isolation

```bash
# Request token for tenant (use Host header or subdomain)
curl -X POST http://localhost:8081/auth/token \
  -H "Host: acme.localhost" \
  -H "Authorization: Bearer acme-lms-key-12345" \
  -H "Content-Type: application/json" \
  -d '{
    "actor": {"mbox": "mailto:user@acme.com"},
    "registration": "acme-session-123",
    "activity_id": "https://acme.com/lesson1",
    "permissions": {
      "write": "actor-activity-registration-scoped",
      "read": "actor-activity-registration-scoped"
    }
  }'

# Token will only work for this tenant
# Verify by trying to use it with wrong Host header (should fail)
```

## Performance Testing

### Load Testing with Apache Bench

```bash
# Generate test token first
TOKEN=$(curl -s -X POST http://localhost:8080/auth/token \
  -H "Authorization: Bearer test-api-key-12345" \
  -H "Content-Type: application/json" \
  -d '{"actor":{"mbox":"mailto:test@example.com"},"registration":"test-123","activity_id":"https://example.com/test","permissions":{"write":"actor-activity-registration-scoped","read":"actor-activity-registration-scoped"}}' \
  | jq -r .token)

# Create test statement file
cat > statement.json <<EOF
[{
  "actor": {"mbox": "mailto:test@example.com"},
  "verb": {"id": "http://adlnet.gov/expapi/verbs/experienced"},
  "object": {"id": "https://example.com/test"},
  "context": {"registration": "test-123"}
}]
EOF

# Run load test (1000 requests, 10 concurrent)
ab -n 1000 -c 10 -p statement.json -T "application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -H "X-Experience-API-Version: 1.0.3" \
  http://localhost:8080/xapi/statements
```

## Production Deployment

### AWS Deployment Example

```bash
# Build for Linux
GOOS=linux GOARCH=amd64 go build -o xapi-proxy cmd/proxy/main.go

# Create systemd service
sudo tee /etc/systemd/system/xapi-proxy.service > /dev/null <<EOF
[Unit]
Description=xAPI LRS Auth Proxy
After=network.target

[Service]
Type=simple
User=xapi
WorkingDirectory=/opt/xapi-proxy
ExecStart=/opt/xapi-proxy/xapi-proxy --config /etc/xapi-proxy/config.yaml
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

# Start service
sudo systemctl enable xapi-proxy
sudo systemctl start xapi-proxy
sudo systemctl status xapi-proxy
```

### Docker Production Deployment

```bash
# Build production image
docker build -t xapi-proxy:1.0.0 .

# Run with production config
docker run -d \
  --name xapi-proxy \
  -p 8080:8080 \
  -v /etc/xapi-proxy/config.yaml:/app/config.yaml:ro \
  -e LRS_PASSWORD=${LRS_PASSWORD} \
  -e JWT_SECRET=${JWT_SECRET} \
  --restart unless-stopped \
  xapi-proxy:1.0.0
```

### Kubernetes Deployment

```yaml
# deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: xapi-proxy
spec:
  replicas: 3
  selector:
    matchLabels:
      app: xapi-proxy
  template:
    metadata:
      labels:
        app: xapi-proxy
    spec:
      containers:
      - name: xapi-proxy
        image: xapi-proxy:1.0.0
        ports:
        - containerPort: 8080
        env:
        - name: LRS_PASSWORD
          valueFrom:
            secretKeyRef:
              name: xapi-secrets
              key: lrs-password
        - name: JWT_SECRET
          valueFrom:
            secretKeyRef:
              name: xapi-secrets
              key: jwt-secret
```

## Monitoring and Logging

### View Logs

```bash
# Docker
docker-compose logs -f proxy-single

# Systemd
sudo journalctl -u xapi-proxy -f

# JSON log output includes:
# - tenant_id
# - method, path, status
# - duration (milliseconds)
# - actor, registration
# - permissions
```

### Prometheus Metrics (TODO)

Future enhancement: Add Prometheus metrics endpoint at `/metrics`

## Security Checklist

- [ ] Use strong, random JWT secrets (32+ bytes)
- [ ] Store secrets in environment variables, not config files
- [ ] Use HTTPS/TLS in production (terminate at proxy or load balancer)
- [ ] Rotate JWT secrets periodically
- [ ] Monitor audit logs for suspicious activity
- [ ] Use PostgreSQL with proper user permissions
- [ ] Enable database connection encryption (SSL)
- [ ] Implement rate limiting (TODO: add to proxy)
- [ ] Regular security updates for dependencies

## Troubleshooting

### Common Issues

**Problem: "Tenant not found"**
- Check Host header matches configured tenant
- Verify database connection in multi-tenant mode

**Problem: "Invalid token"**
- Token may be expired (check expires_at)
- JWT secret mismatch between token issuance and validation
- Verify tenant_id in token matches request

**Problem: "Permission denied"**
- Statement actor/activity/registration doesn't match token
- Check permission scope in token request

**Problem: LRS connection fails**
- Verify LRS endpoint is accessible
- Check LRS credentials
- Review LRS logs for authentication errors
