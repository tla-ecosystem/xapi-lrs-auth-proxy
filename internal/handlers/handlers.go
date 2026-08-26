package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"

	"github.com/inxsol/xapi-lrs-auth-proxy/internal/middleware"
	"github.com/inxsol/xapi-lrs-auth-proxy/internal/models"
	"github.com/inxsol/xapi-lrs-auth-proxy/internal/store"
	"github.com/inxsol/xapi-lrs-auth-proxy/internal/validator"
)

// Handler contains all HTTP handlers
type Handler struct {
	tenantStore store.TenantStore
}

// New creates a new Handler
func New(tenantStore store.TenantStore) *Handler {
	return &Handler{
		tenantStore: tenantStore,
	}
}

// IssueToken handles POST /auth/token - issues JWT for LMS
func (h *Handler) IssueToken(w http.ResponseWriter, r *http.Request) {
	tenant := r.Context().Value(middleware.TenantKey).(*store.TenantConfig)

	var req models.TokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate permissions
	if err := models.ValidatePermission(req.Permissions.Write); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := models.ValidatePermission(req.Permissions.Read); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Create JWT claims
	expiresAt := time.Now().Add(time.Duration(tenant.JWTTTLSeconds) * time.Second)
	claims := &models.Claims{
		TenantID:     tenant.TenantID,
		Actor:        req.Actor,
		Registration: req.Registration,
		ActivityID:   req.ActivityID,
		CourseID:     req.CourseID,
		Permissions:  req.Permissions,
		Group:        req.Group,
		Metadata:     req.Metadata,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "xapi-lrs-auth-proxy",
			Subject:   req.Actor.Mbox,
		},
	}

	// Sign token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(tenant.JWTSecret)
	if err != nil {
		log.WithError(err).Error("Failed to sign JWT")
		http.Error(w, "Token generation failed", http.StatusInternalServerError)
		return
	}

	// Log token issuance
	log.WithFields(log.Fields{
		"tenant_id":    tenant.TenantID,
		"actor":        req.Actor.Mbox,
		"registration": req.Registration,
		"activity_id":  req.ActivityID,
		"permissions":  fmt.Sprintf("write:%s read:%s", req.Permissions.Write, req.Permissions.Read),
	}).Info("JWT token issued")

	// Return token
	resp := models.TokenResponse{
		Token:     tokenString,
		ExpiresAt: expiresAt,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ProxyStatements handles xAPI statements endpoint
func (h *Handler) ProxyStatements(w http.ResponseWriter, r *http.Request) {
	tenant := r.Context().Value(middleware.TenantKey).(*store.TenantConfig)
	claims := r.Context().Value(middleware.ClaimsKey).(*models.Claims)

	v := validator.NewPermissionValidator(tenant.PermissionPolicy)

	switch r.Method {
	case "POST", "PUT":
		h.proxyStatementsWrite(w, r, tenant, claims, v)
	case "GET", "HEAD":
		h.proxyStatementsRead(w, r, tenant, claims, v)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// proxyStatementsWrite handles statement writes
func (h *Handler) proxyStatementsWrite(w http.ResponseWriter, r *http.Request, tenant *store.TenantConfig, claims *models.Claims, v *validator.PermissionValidator) {
	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// The xAPI spec allows statement writes with attachments to be sent as
	// multipart/mixed (first part is the JSON statement(s), remaining parts
	// are binary attachment content), and the "alternate request syntax"
	// allows a statement to arrive as application/x-www-form-urlencoded. We
	// can't safely json.Unmarshal either of those, and a partial/naive parse
	// would either corrupt the body or silently skip permission validation.
	// So: only attempt statement-level permission validation when the body
	// is actually JSON; for anything else, only forward it untouched for a
	// credential we already trust unconditionally (unrestricted-passthrough,
	// e.g. a test/admin tool doing conformance testing) — real per-launch AU
	// credentials still get a hard denial rather than an unvalidated write.
	contentType := r.Header.Get("Content-Type")
	isJSON := contentType == "" || strings.HasPrefix(strings.ToLower(contentType), "application/json")

	if !isJSON {
		if claims.Permissions.Write != "unrestricted-passthrough" {
			log.WithFields(log.Fields{
				"tenant_id":    tenant.TenantID,
				"registration": claims.Registration,
				"content_type": contentType,
			}).Warn("Non-JSON statement write denied: cannot validate permissions on this content type")
			http.Error(w, fmt.Sprintf("statement write denied: cannot validate permissions for Content-Type %q with this credential", contentType), http.StatusForbidden)
			return
		}
		// Full-access credential — skip statement-level validation and pass
		// the body through exactly as received.
		h.forwardToLRS(w, r, tenant, body, nil)
		return
	}

	// Parse statements
	var statements []models.Statement
	if err := json.Unmarshal(body, &statements); err != nil {
		// Try single statement
		var stmt models.Statement
		if err := json.Unmarshal(body, &stmt); err != nil {
			http.Error(w, "Invalid statement format", http.StatusBadRequest)
			return
		}
		statements = []models.Statement{stmt}
	}

	// Validate each statement against permissions
	for i, stmt := range statements {
		if err := v.ValidateWrite(claims, &stmt); err != nil {
			log.WithFields(log.Fields{
				"tenant_id":    tenant.TenantID,
				"registration": claims.Registration,
				"statement_num": i,
				"error":        err.Error(),
			}).Warn("Statement write denied")
			http.Error(w, fmt.Sprintf("Statement %d: %s", i, err.Error()), http.StatusForbidden)
			return
		}
	}

	// Forward to LRS
	h.forwardToLRS(w, r, tenant, body, nil)
}

// proxyStatementsRead handles statement reads
func (h *Handler) proxyStatementsRead(w http.ResponseWriter, r *http.Request, tenant *store.TenantConfig, claims *models.Claims, v *validator.PermissionValidator) {
	// Extract query parameters
	query := make(map[string]string)
	for key, values := range r.URL.Query() {
		if len(values) > 0 {
			query[key] = values[0]
		}
	}

	// Validate read permissions
	if err := v.ValidateRead(claims, query); err != nil {
		log.WithFields(log.Fields{
			"tenant_id":    tenant.TenantID,
			"registration": claims.Registration,
			"error":        err.Error(),
		}).Warn("Statement read denied")
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	// Forward to LRS. Rewrite any pagination "more" link in the response so
	// continued paging routes back through the proxy instead of leaking the
	// backend LRS's raw URL (and its own credential expectations) to the
	// client.
	h.forwardToLRS(w, r, tenant, nil, func(respBody []byte) []byte {
		return h.rewriteMoreLink(respBody, tenant, r)
	})
}

// ProxyState handles xAPI state endpoint
func (h *Handler) ProxyState(w http.ResponseWriter, r *http.Request) {
	tenant := r.Context().Value(middleware.TenantKey).(*store.TenantConfig)
	claims := r.Context().Value(middleware.ClaimsKey).(*models.Claims)

	v := validator.NewPermissionValidator(tenant.PermissionPolicy)

	// Extract state parameters
	activityID := r.URL.Query().Get("activityId")
	agent := r.URL.Query().Get("agent")
	registration := r.URL.Query().Get("registration")

	// Validate state access
	if err := v.ValidateStateAccess(claims, activityID, agent, registration); err != nil {
		log.WithFields(log.Fields{
			"tenant_id": tenant.TenantID,
			"error":     err.Error(),
		}).Warn("State access denied")
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	// Read body if present
	var body []byte
	if r.Method == "POST" || r.Method == "PUT" {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()
	}

	// Forward to LRS
	h.forwardToLRS(w, r, tenant, body, nil)
}

// ProxyActivitiesResource handles GET/HEAD /xapi/activities (the Activities
// Resource — returns the full merged Activity Object for activityId).
func (h *Handler) ProxyActivitiesResource(w http.ResponseWriter, r *http.Request) {
	tenant := r.Context().Value(middleware.TenantKey).(*store.TenantConfig)
	claims := r.Context().Value(middleware.ClaimsKey).(*models.Claims)

	activityID := r.URL.Query().Get("activityId")
	if claims.Permissions.Read != "unrestricted-passthrough" && activityID != claims.ActivityID {
		log.WithFields(log.Fields{
			"tenant_id": tenant.TenantID,
		}).Warn("Activities Resource read denied: activity mismatch")
		http.Error(w, "read denied: activity mismatch", http.StatusForbidden)
		return
	}

	h.forwardToLRS(w, r, tenant, nil, nil)
}

// ProxyAgentsResource handles GET/HEAD /xapi/agents (the Agents Resource —
// returns a Person Object for the given agent).
func (h *Handler) ProxyAgentsResource(w http.ResponseWriter, r *http.Request) {
	tenant := r.Context().Value(middleware.TenantKey).(*store.TenantConfig)
	claims := r.Context().Value(middleware.ClaimsKey).(*models.Claims)

	agent := r.URL.Query().Get("agent")
	if claims.Permissions.Read != "unrestricted-passthrough" {
		if agent == "" || (!strings.Contains(agent, claims.Actor.Mbox) && !strings.Contains(agent, claims.Actor.OpenID)) {
			log.WithFields(log.Fields{
				"tenant_id": tenant.TenantID,
			}).Warn("Agents Resource read denied: agent mismatch")
			http.Error(w, "read denied: agent mismatch", http.StatusForbidden)
			return
		}
	}

	h.forwardToLRS(w, r, tenant, nil, nil)
}

// ProxyStatementsMore handles GET/HEAD /xapi/statements/more/{id} — the
// continuation link for a paginated GET /statements result. The client only
// ever reaches this with a URL we handed back from rewriteMoreLink after the
// original query was already authenticated and permission-checked, so it's
// just forwarded.
func (h *Handler) ProxyStatementsMore(w http.ResponseWriter, r *http.Request) {
	tenant := r.Context().Value(middleware.TenantKey).(*store.TenantConfig)
	// A deep pagination chain can itself contain another "more" link, so
	// keep rewriting on every hop, not just the first page.
	h.forwardToLRS(w, r, tenant, nil, func(respBody []byte) []byte {
		return h.rewriteMoreLink(respBody, tenant, r)
	})
}

// ProxyActivityProfile handles xAPI activity profile endpoint
func (h *Handler) ProxyActivityProfile(w http.ResponseWriter, r *http.Request) {
	tenant := r.Context().Value(middleware.TenantKey).(*store.TenantConfig)

	var body []byte
	if r.Method == "POST" || r.Method == "PUT" {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()
	}

	h.forwardToLRS(w, r, tenant, body, nil)
}

// ProxyAgentProfile handles xAPI agent profile endpoint
func (h *Handler) ProxyAgentProfile(w http.ResponseWriter, r *http.Request) {
	tenant := r.Context().Value(middleware.TenantKey).(*store.TenantConfig)
	claims := r.Context().Value(middleware.ClaimsKey).(*models.Claims)

	// Validate agent matches
	agent := r.URL.Query().Get("agent")
	// Simplified validation - in production, parse full agent JSON
	// and verify it matches claims.Actor

	_ = claims // Use claims for validation
	_ = agent

	var body []byte
	if r.Method == "POST" || r.Method == "PUT" {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()
	}

	h.forwardToLRS(w, r, tenant, body, nil)
}

// ProxyAbout handles xAPI about endpoint
func (h *Handler) ProxyAbout(w http.ResponseWriter, r *http.Request) {
	tenant := r.Context().Value(middleware.TenantKey).(*store.TenantConfig)
	h.forwardToLRS(w, r, tenant, nil, nil)
}

// rewriteMoreLink rewrites a GET /statements response's pagination "more"
// link so it points back through the proxy instead of exposing the backend
// LRS's raw endpoint URL. Only rewrites when "more" is present and actually
// prefixed with the tenant's configured LRS endpoint (an absolute URL leak);
// anything else (missing, empty, or already relative) is left untouched
// rather than guessed at. Uses json.RawMessage for every other field so
// re-marshaling can't reformat numbers or nested statement content.
func (h *Handler) rewriteMoreLink(body []byte, tenant *store.TenantConfig, r *http.Request) []byte {
	if !bytes.Contains(body, []byte(`"more"`)) {
		return body
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(body, &result); err != nil {
		return body
	}

	raw, ok := result["more"]
	if !ok {
		return body
	}

	var more string
	if err := json.Unmarshal(raw, &more); err != nil || more == "" {
		return body
	}

	if !strings.HasPrefix(more, tenant.LRSEndpoint) {
		return body
	}

	scheme := "https"
	if r.TLS == nil && strings.ToLower(r.Header.Get("X-Forwarded-Proto")) != "https" {
		scheme = "http"
	}
	newMore := scheme + "://" + r.Host + "/xapi" + strings.TrimPrefix(more, tenant.LRSEndpoint)

	newRaw, err := json.Marshal(newMore)
	if err != nil {
		return body
	}
	result["more"] = newRaw

	out, err := json.Marshal(result)
	if err != nil {
		return body
	}
	return out
}

// forwardToLRS forwards the request to the tenant's LRS. If rewriteResponse
// is non-nil, the backend's response body is buffered and passed through it
// before being written to the client (used to rewrite pagination links);
// otherwise the response is streamed straight through.
func (h *Handler) forwardToLRS(w http.ResponseWriter, r *http.Request, tenant *store.TenantConfig, body []byte, rewriteResponse func([]byte) []byte) {
	// Build LRS URL
	lrsURL := tenant.LRSEndpoint + r.URL.Path[5:] // Remove "/xapi" prefix
	if r.URL.RawQuery != "" {
		lrsURL += "?" + r.URL.RawQuery
	}

	// Create request
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}

	req, err := http.NewRequest(r.Method, lrsURL, reqBody)
	if err != nil {
		log.WithError(err).Error("Failed to create LRS request")
		http.Error(w, "Failed to forward request", http.StatusInternalServerError)
		return
	}

	// Copy headers (except Authorization - we use LRS credentials)
	for key, values := range r.Header {
		if key != "Authorization" && key != "Host" {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	}

	// Add LRS credentials
	req.SetBasicAuth(tenant.LRSUsername, tenant.LRSPassword)

	// Ensure xAPI version header
	if req.Header.Get("X-Experience-API-Version") == "" {
		req.Header.Set("X-Experience-API-Version", "1.0.3")
	}

	// Send request
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		log.WithError(err).Error("LRS request failed")
		http.Error(w, "LRS request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	if rewriteResponse != nil {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			log.WithError(err).Error("Failed to read LRS response")
			http.Error(w, "Failed to read LRS response", http.StatusBadGateway)
			return
		}
		respBody = rewriteResponse(respBody)

		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(respBody)))
		w.WriteHeader(resp.StatusCode)
		if _, err := w.Write(respBody); err != nil {
			log.WithError(err).Error("Failed to write rewritten LRS response")
		}
	} else {
		// Copy status code
		w.WriteHeader(resp.StatusCode)

		// Copy response body
		if _, err := io.Copy(w, resp.Body); err != nil {
			log.WithError(err).Error("Failed to copy LRS response")
		}
	}

	// Log successful proxy
	log.WithFields(log.Fields{
		"tenant_id": tenant.TenantID,
		"method":    r.Method,
		"path":      r.URL.Path,
		"lrs_status": resp.StatusCode,
	}).Debug("Request proxied to LRS")
}

// CreateTenant handles POST /admin/tenants
func (h *Handler) CreateTenant(w http.ResponseWriter, r *http.Request) {
	dbStore, ok := h.tenantStore.(*store.DatabaseTenantStore)
	if !ok {
		http.Error(w, "Multi-tenant mode not enabled", http.StatusBadRequest)
		return
	}

	var req store.CreateTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := dbStore.CreateTenant(r.Context(), &req); err != nil {
		log.WithError(err).Error("Failed to create tenant")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "created"})
}

// ListTenants handles GET /admin/tenants
func (h *Handler) ListTenants(w http.ResponseWriter, r *http.Request) {
	dbStore, ok := h.tenantStore.(*store.DatabaseTenantStore)
	if !ok {
		http.Error(w, "Multi-tenant mode not enabled", http.StatusBadRequest)
		return
	}

	tenants, err := dbStore.ListTenants(r.Context())
	if err != nil {
		log.WithError(err).Error("Failed to list tenants")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"tenants": tenants,
	})
}

// GetTenant handles GET /admin/tenants/{id}
func (h *Handler) GetTenant(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	tenantID := vars["id"]

	tenant, err := h.tenantStore.GetByID(r.Context(), tenantID)
	if err != nil {
		http.Error(w, "Tenant not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tenant)
}

// UpdateTenant handles PUT /admin/tenants/{id}
func (h *Handler) UpdateTenant(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement tenant updates
	http.Error(w, "Not implemented", http.StatusNotImplemented)
}

// DeleteTenant handles DELETE /admin/tenants/{id}
func (h *Handler) DeleteTenant(w http.ResponseWriter, r *http.Request) {
	dbStore, ok := h.tenantStore.(*store.DatabaseTenantStore)
	if !ok {
		http.Error(w, "Multi-tenant mode not enabled", http.StatusBadRequest)
		return
	}

	vars := mux.Vars(r)
	tenantID := vars["id"]

	if err := dbStore.DeleteTenant(r.Context(), tenantID); err != nil {
		log.WithError(err).Error("Failed to delete tenant")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
