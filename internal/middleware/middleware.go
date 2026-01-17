package middleware

import (
	"context"
	"net/http"
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
		tenant := r.Context().Value(TenantKey).(*store.TenantConfig)

		// Extract JWT from Authorization header
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
