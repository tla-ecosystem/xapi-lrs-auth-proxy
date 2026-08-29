package middleware

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	log "github.com/sirupsen/logrus"

	"github.com/inxsol/xapi-lrs-auth-proxy/internal/models"
	"github.com/inxsol/xapi-lrs-auth-proxy/internal/store"
)

// ContextKey type for context keys
type ContextKey string

const (
	TenantKey ContextKey = "tenant"
	ClaimsKey ContextKey = "claims"
)

// TenantMiddleware resolves tenant from Host header
func TenantMiddleware(tenantStore store.TenantStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// A CORS preflight (OPTIONS) never carries a tenant-identifying
			// Authorization header and shouldn't need to - it's just the
			// browser asking permission to send the real request. The
			// router-level CORSMiddleware is meant to answer these before
			// they get this far, but that depends on gorilla/mux's exact
			// middleware-composition order for a subrouter's Use() versus
			// the root router's Use(); this guard makes the outcome correct
			// either way instead of depending on it.
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			tenant, err := tenantStore.GetByHost(r.Context(), r.Host)
			if err != nil {
				log.WithFields(log.Fields{
					"host":  r.Host,
					"error": err.Error(),
				}).Warn("Tenant not found")
				http.Error(w, "Tenant not found", http.StatusNotFound)
				return
			}

			// Add tenant to context
			ctx := context.WithValue(r.Context(), TenantKey, tenant)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// LMSAuthMiddleware validates LMS API key
func LMSAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// See the matching guard in TenantMiddleware above - lets an OPTIONS
		// preflight through without requiring the tenant context or an
		// Authorization header it was never going to carry.
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		tenant := r.Context().Value(TenantKey).(*store.TenantConfig)

		// Extract API key from Authorization header
		auth := r.Header.Get("Authorization")
		if auth == "" {
			http.Error(w, "Authorization required", http.StatusUnauthorized)
			return
		}

		// Parse Bearer token
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			http.Error(w, "Invalid authorization format", http.StatusUnauthorized)
			return
		}

		apiKey := parts[1]

		// Validate API key against tenant's keys
		if !tenant.LMSAPIKeys[apiKey] {
			log.WithFields(log.Fields{
				"tenant_id": tenant.TenantID,
			}).Warn("Invalid LMS API key")
			http.Error(w, "Invalid API key", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// JWTAuthMiddleware validates JWT token
func JWTAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// See the matching guard in TenantMiddleware above - lets an OPTIONS
		// preflight through without requiring the tenant context or an
		// Authorization header it was never going to carry. This is the one
		// that matters most in practice: every cross-origin call an AU makes
		// (State, Statements, Activity Profile, ...) goes through this
		// middleware, and a browser will only send the real request if the
		// preflight for it succeeds.
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		tenant := r.Context().Value(TenantKey).(*store.TenantConfig)

		// Extract JWT from Authorization header
		auth := r.Header.Get("Authorization")

		// "Alternate request syntax" (xAPI spec, for clients like HTML forms
		// that can't set arbitrary headers): a POST with a "method" query
		// override and Content-Type application/x-www-form-urlencoded can
		// carry Authorization as a form field instead of a real header. Only
		// fall back to the body when there's no real header at all, so a
		// normal request with a proper Authorization header is never
		// affected by this.
		if auth == "" && r.URL.Query().Get("method") != "" &&
			strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/x-www-form-urlencoded") {
			if bodyBytes, err := io.ReadAll(r.Body); err == nil {
				r.Body = io.NopCloser(bytes.NewReader(bodyBytes)) // restore for the handler
				if values, err := url.ParseQuery(string(bodyBytes)); err == nil {
					if v := values.Get("Authorization"); v != "" {
						auth = v
					}
				}
			}
		}

		if auth == "" {
			http.Error(w, "Authorization required", http.StatusUnauthorized)
			return
		}

		// Parse Bearer or Basic token. Per the cmi5/xAPI Launch spec, the AU sends the
		// fetch()-issued auth-token verbatim as "Authorization: Basic <token>" — this is
		// NOT standard base64(username:password) Basic Auth, it's the spec's chosen wire
		// format for an opaque, LMS-issued credential. Accept both schemes so real AUs
		// (Storyline/Rise/ADL reference content) and Bearer-based callers (manual testing,
		// other tooling) both work against the same token.
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) != 2 || (parts[0] != "Bearer" && parts[0] != "Basic") {
			http.Error(w, "Invalid authorization format", http.StatusUnauthorized)
			return
		}

		// Try full-access passthrough Basic Auth first (see PassthroughKeys in
		// config) — a static username:password credential for test/admin tools
		// (e.g. xAPI conformance suites), distinct from the per-launch opaque
		// cmi5 auth-token that's also sent via "Basic". A real auth-token won't
		// happen to base64-decode into a "user:pass" string matching a
		// configured credential, so trying this first is safe.
		if parts[0] == "Basic" {
			if decoded, err := base64.StdEncoding.DecodeString(parts[1]); err == nil {
				if idx := strings.IndexByte(string(decoded), ':'); idx >= 0 {
					user, pass := string(decoded[:idx]), string(decoded[idx+1:])
					if configured, ok := tenant.PassthroughKeys[user]; ok && configured == pass {
						log.WithFields(log.Fields{
							"tenant_id": tenant.TenantID,
							"user":      user,
						}).Info("Passthrough credential authenticated")
						claims := &models.Claims{
							TenantID: tenant.TenantID,
							Permissions: models.Permissions{
								Write: "unrestricted-passthrough",
								Read:  "unrestricted-passthrough",
							},
						}
						ctx := context.WithValue(r.Context(), ClaimsKey, claims)
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
				}
			}
		}

		tokenString := parts[1]

		// Parse and validate JWT
		token, err := jwt.ParseWithClaims(tokenString, &models.Claims{}, func(token *jwt.Token) (interface{}, error) {
			// Verify signing method
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return tenant.JWTSecret, nil
		})

		if err != nil {
			log.WithFields(log.Fields{
				"tenant_id": tenant.TenantID,
				"error":     err.Error(),
			}).Warn("JWT validation failed")
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}

		if !token.Valid {
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}

		claims, ok := token.Claims.(*models.Claims)
		if !ok {
			http.Error(w, "Invalid token claims", http.StatusUnauthorized)
			return
		}

		// Verify tenant matches
		if claims.TenantID != tenant.TenantID {
			log.WithFields(log.Fields{
				"token_tenant": claims.TenantID,
				"host_tenant":  tenant.TenantID,
			}).Warn("Tenant mismatch in token")
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}

		// Add claims to context
		ctx := context.WithValue(r.Context(), ClaimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AdminAuthMiddleware validates admin API access
func AdminAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// In production, implement proper admin authentication
		// For now, just check for admin token
		auth := r.Header.Get("Authorization")
		if auth == "" {
			http.Error(w, "Authorization required", http.StatusUnauthorized)
			return
		}

		// TODO: Implement proper admin auth (OAuth, API keys, etc.)
		// For reference implementation, accept any Bearer token
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			http.Error(w, "Invalid authorization format", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// LoggingMiddleware logs all requests
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create response writer wrapper to capture status code
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		// Get tenant if available
		var tenantID string
		if tenant, ok := r.Context().Value(TenantKey).(*store.TenantConfig); ok {
			tenantID = tenant.TenantID
		}

		// Log request
		log.WithFields(log.Fields{
			"method":     r.Method,
			"path":       r.URL.Path,
			"status":     wrapped.statusCode,
			"duration":   time.Since(start).Milliseconds(),
			"tenant_id":  tenantID,
			"remote_addr": r.RemoteAddr,
			"user_agent": r.UserAgent(),
		}).Info("Request processed")
	})
}

// CORSMiddleware adds CORS headers
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// In production, configure CORS properly based on requirements
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Experience-API-Version")
		w.Header().Set("Access-Control-Expose-Headers", "X-Experience-API-Version")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
