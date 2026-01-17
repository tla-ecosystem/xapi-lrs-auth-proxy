# Contributing to xAPI LRS Auth Proxy

Thank you for your interest in contributing! This project is a reference implementation for secure cmi5/xAPI authentication.

## Project Goals

1. **Standards Compliance:** Implement cmi5 and xAPI specifications correctly
2. **Security First:** Demonstrate proper authentication patterns
3. **Reference Quality:** Clear, well-documented code for others to learn from
4. **Production Ready:** Usable in real deployments
5. **Community Driven:** Accept contributions from the learning technology community

## How to Contribute

### Reporting Issues

Found a bug or security issue? Please report it:

1. Check existing issues first
2. Create new issue with:
   - Clear description
   - Steps to reproduce
   - Expected vs actual behavior
   - Environment details (Go version, OS, deployment model)

**Security Issues:** Email security@inxsol.com instead of creating public issue

### Suggesting Features

Have an idea? Great! Please:

1. Check if it aligns with project goals
2. Create an issue describing:
   - Use case / problem being solved
   - Proposed solution
   - Standards alignment (if applicable)
3. Discuss before implementing

### Contributing Code

#### Development Setup

```bash
# Clone repository
git clone https://github.com/inxsol/xapi-lrs-auth-proxy.git
cd xapi-lrs-auth-proxy

# Install dependencies
make deps

# Run tests
make test

# Run locally
make run
```

#### Code Standards

**Go Style:**
- Follow [Effective Go](https://golang.org/doc/effective_go.html)
- Use `gofmt` for formatting
- Run `golangci-lint` before committing

**Documentation:**
- Add godoc comments for exported functions
- Update README.md if adding features
- Update ARCHITECTURE.md for design changes
- Add examples in TESTING.md

**Testing:**
- Add unit tests for new functionality
- Ensure existing tests pass
- Add integration tests for complex features

#### Pull Request Process

1. **Fork and Branch**
   ```bash
   git checkout -b feature/your-feature-name
   ```

2. **Make Changes**
   - Write code
   - Add tests
   - Update documentation

3. **Test Thoroughly**
   ```bash
   make test
   make build
   # Manual testing
   ```

4. **Commit**
   - Use clear commit messages
   - Reference issue numbers
   - Sign commits (optional but appreciated)

5. **Push and Create PR**
   - Push to your fork
   - Create pull request
   - Fill out PR template
   - Link related issues

6. **Review Process**
   - Maintainers will review
   - Address feedback
   - PR will be merged when approved

## Areas for Contribution

### High Priority

1. **Permission Validators**
   - Implement additional permission scopes
   - Add validation for group actors
   - Support for aggregate-only scopes

2. **Testing**
   - Integration tests with real LRS
   - Load testing scripts
   - cmi5 conformance tests

3. **Documentation**
   - Deployment guides for cloud providers
   - Integration examples for popular LMS
   - Security best practices

### Medium Priority

1. **Performance**
   - Redis caching implementation
   - Connection pooling optimization
   - Metrics/monitoring

2. **Admin UI**
   - Web interface for tenant management
   - Permission approval workflow UI
   - Audit log viewer

3. **Features**
   - Token revocation API
   - Rate limiting
   - OAuth2 support

### Nice to Have

1. **Integrations**
   - Moodle plugin
   - Canvas integration
   - Totara connector

2. **Tools**
   - CLI for tenant management
   - Migration scripts
   - Testing utilities

## Code Review Checklist

Before submitting PR, verify:

- [ ] Code follows Go style guidelines
- [ ] All tests pass (`make test`)
- [ ] New features have tests
- [ ] Documentation updated
- [ ] No hardcoded secrets
- [ ] Error handling is comprehensive
- [ ] Logging is appropriate (not excessive)
- [ ] Changes are backwards compatible (or documented)
- [ ] Performance impact considered
- [ ] Security implications reviewed

## Testing Guidelines

### Unit Tests

```go
// Good test example
func TestValidateWrite_ActorMismatch(t *testing.T) {
    validator := NewPermissionValidator("strict")
    
    claims := &models.Claims{
        Actor: models.Actor{Mbox: "mailto:learner@example.com"},
        ActivityID: "https://example.com/activity",
        Registration: "reg-123",
        Permissions: models.Permissions{
            Write: "actor-activity-registration-scoped",
        },
    }
    
    stmt := &models.Statement{
        Actor: models.Actor{Mbox: "mailto:different@example.com"},
        Object: models.Object{ID: "https://example.com/activity"},
        Context: &models.Context{Registration: "reg-123"},
    }
    
    err := validator.ValidateWrite(claims, stmt)
    
    if err == nil {
        t.Error("Expected actor mismatch error, got nil")
    }
}
```

### Integration Tests

```go
// Good integration test example
func TestEndToEnd_TokenIssuanceAndValidation(t *testing.T) {
    // Start test server
    srv := setupTestServer(t)
    defer srv.Close()
    
    // Request token
    token := requestToken(t, srv.URL, validTokenRequest)
    
    // Use token to post statement
    resp := postStatement(t, srv.URL, token, validStatement)
    
    // Verify response
    if resp.StatusCode != http.StatusOK {
        t.Errorf("Expected 200, got %d", resp.StatusCode)
    }
}
```

## Documentation Standards

### Code Comments

```go
// Good: Clear, concise, explains why
// ValidateWrite checks if a statement write is allowed based on JWT claims.
// It enforces the cmi5 permission model by validating actor, activity,
// and registration match between the token and statement.
func (v *Validator) ValidateWrite(claims *Claims, stmt *Statement) error {
    // Actor must match to prevent impersonation
    if !claims.Actor.Equals(stmt.Actor) {
        return ErrActorMismatch
    }
    // ... rest of validation
}
```

### API Documentation

When adding endpoints, document in OpenAPI/Swagger format:

```yaml
/auth/token:
  post:
    summary: Issue JWT token for xAPI access
    description: |
      Called by LMS to obtain time-limited JWT token for learner session.
      Token is scoped to specific actor, activity, and registration.
    security:
      - LMSApiKey: []
    requestBody:
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/TokenRequest'
    responses:
      200:
        description: Token issued successfully
```

## Standards Compliance

### cmi5 Compliance

When implementing cmi5 features:
1. Reference specific sections of cmi5 spec
2. Add test cases from spec examples
3. Document any deviations (must have justification)

### xAPI Compliance

When handling xAPI:
1. Support xAPI 1.0.3 (current version)
2. Follow ADL's conformance requirements
3. Test against ADL's conformance test suite (when available)

## Communication

### Channels

- **GitHub Issues:** Bug reports, feature requests
- **GitHub Discussions:** Questions, ideas, general discussion
- **Email:** security@inxsol.com (security issues only)

### IEEE LTSC Coordination

This project is aligned with IEEE LTSC cmi5 working group. Major changes should be discussed with the working group to ensure standards alignment.

## License

By contributing, you agree that your contributions will be licensed under the MIT License.

## Recognition

Contributors will be recognized in:
- CONTRIBUTORS.md file
- Release notes
- Project documentation

Significant contributors may be invited to join as maintainers.

## Questions?

Don't hesitate to ask! Create a GitHub Discussion or reach out to maintainers.

Thank you for helping make xAPI/cmi5 implementations more secure!
