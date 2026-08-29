package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"

	"github.com/inxsol/xapi-lrs-auth-proxy/internal/config"
	"github.com/inxsol/xapi-lrs-auth-proxy/internal/forwarder"
	"github.com/inxsol/xapi-lrs-auth-proxy/internal/handlers"
	"github.com/inxsol/xapi-lrs-auth-proxy/internal/middleware"
	"github.com/inxsol/xapi-lrs-auth-proxy/internal/store"
)

var (
	configFile    = flag.String("config", "config.yaml", "Path to configuration file")
	multiTenant   = flag.Bool("multi-tenant", false, "Enable multi-tenant mode")
	dbConnStr     = flag.String("db", "", "Database connection string (required for multi-tenant)")
	port          = flag.Int("port", 0, "Server port (overrides config)")
	version       = "1.0.0"
	buildTime     = "unknown"

	multiSlashRe = regexp.MustCompile(`/{2,}`)
)

// collapseSlashes normalizes repeated slashes in the request path (e.g.
// "/xapi//statements" -> "/xapi/statements") before the router ever sees it.
// Some clients (LRS conformance suites, tools that naively concatenate a
// trailing-slash base URL with a leading-slash path) produce double slashes;
// without this, gorilla/mux's default behavior is to 301-redirect to the
// clean path, which a test suite that doesn't follow redirects just sees as
// a failure. Fixing it here means the route is served directly instead.
func collapseSlashes(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "//") {
			r.URL.Path = multiSlashRe.ReplaceAllString(r.URL.Path, "/")
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	flag.Parse()

	// Setup logging
	log.SetFormatter(&log.JSONFormatter{})
	log.SetOutput(os.Stdout)
	log.SetLevel(log.InfoLevel)

	log.WithFields(log.Fields{
		"version":    version,
		"build_time": buildTime,
	}).Info("Starting xAPI LRS Auth Proxy")

	// Load configuration
	cfg, err := config.Load(*configFile)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Override port if specified
	if *port > 0 {
		cfg.Server.Port = *port
	}

	// Initialize tenant store
	var tenantStore store.TenantStore
	if *multiTenant {
		if *dbConnStr == "" {
			log.Fatal("Database connection string required for multi-tenant mode")
		}
		log.Info("Initializing multi-tenant mode with database")
		tenantStore, err = store.NewDatabaseTenantStore(*dbConnStr)
		if err != nil {
			log.Fatalf("Failed to initialize database tenant store: %v", err)
		}
	} else {
		log.Info("Initializing single-tenant mode")
		tenantStore, err = store.NewSingleTenantStore(cfg)
		if err != nil {
			log.Fatalf("Failed to initialize single tenant store: %v", err)
		}
	}

	// Initialize the statement forwarding sink (fans accepted cmi5/answered/
	// interacted statement writes out to a listener, e.g. HazReady's SQL
	// reporting endpoint - see internal/forwarder). fwd is always non-nil;
	// it's simply inert when cfg.StatementForwarding.Enabled is false.
	fwd := forwarder.New(cfg.StatementForwarding)

	// Initialize handlers
	h := handlers.New(tenantStore, fwd)

	// Setup router
	r := mux.NewRouter()

	// Health check endpoint
	r.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok","version":"%s"}`, version)
	}).Methods("GET")

	// Auth API (LMS-facing) - requires LMS API key
	authRouter := r.PathPrefix("/auth").Subrouter()
	authRouter.Use(middleware.TenantMiddleware(tenantStore))
	authRouter.Use(middleware.LMSAuthMiddleware)
	authRouter.HandleFunc("/token", h.IssueToken).Methods("POST")

	// About Resource - the xAPI spec treats About as capability discovery
	// and this conformance suite requires it to be reachable with no
	// Authorization header at all, so it gets its own subrouter without
	// JWTAuthMiddleware (still tenant-scoped, since forwarding still needs
	// to know which backend LRS to hit).
	aboutRouter := r.PathPrefix("/xapi").Subrouter()
	aboutRouter.Use(middleware.TenantMiddleware(tenantStore))
	aboutRouter.HandleFunc("/about", h.ProxyAbout).Methods("GET", "HEAD")

	// xAPI Proxy (content-facing) - requires JWT
	xapiRouter := r.PathPrefix("/xapi").Subrouter()
	xapiRouter.Use(middleware.TenantMiddleware(tenantStore))
	xapiRouter.Use(middleware.JWTAuthMiddleware)
	// OPTIONS is added to every one of these Methods() lists below, even
	// though no handler here does anything for it, purely so a CORS
	// preflight actually MATCHES the route. Without it, gorilla/mux treats
	// an OPTIONS request to e.g. "/activities/state" as a method mismatch
	// on that route, and whether the router-level CORSMiddleware (which
	// short-circuits OPTIONS with a 200 + the Access-Control-* headers, see
	// middleware.go) still gets a chance to run for that mismatch depends on
	// subrouter middleware-composition details that aren't worth relying on.
	// A browser AU calling these directly (State/Statements/etc. from
	// TinCan.js, cmi5.js) sends a real preflight because Authorization is a
	// non-simple header - if that preflight doesn't come back with CORS
	// headers, the browser blocks the real request before it's ever sent,
	// which surfaces client-side as exactly the symptom this was chasing:
	// the actual GET/PUT never appears to complete, xhr.status stays 0, and
	// nothing about it reaches the server's own logs (this proxy's or
	// HazReady's) because the browser never let the request through.
	xapiRouter.HandleFunc("/statements", h.ProxyStatements).Methods("POST", "PUT", "GET", "HEAD", "OPTIONS")
	// Pagination continuation link. Different LRS backends express this
	// differently - Veracity uses a query parameter ("/statements/more?id=..."),
	// others use a trailing path segment - so both shapes route to the same
	// handler, which just forwards whatever path/query it received.
	xapiRouter.HandleFunc("/statements/more", h.ProxyStatementsMore).Methods("GET", "HEAD", "OPTIONS")
	xapiRouter.HandleFunc("/statements/more/{id}", h.ProxyStatementsMore).Methods("GET", "HEAD", "OPTIONS")
	xapiRouter.HandleFunc("/activities/state", h.ProxyState).Methods("POST", "PUT", "GET", "DELETE", "HEAD", "OPTIONS")
	xapiRouter.HandleFunc("/activities/profile", h.ProxyActivityProfile).Methods("POST", "PUT", "GET", "DELETE", "HEAD", "OPTIONS")
	xapiRouter.HandleFunc("/agents/profile", h.ProxyAgentProfile).Methods("POST", "PUT", "GET", "DELETE", "HEAD", "OPTIONS")
	xapiRouter.HandleFunc("/activities", h.ProxyActivitiesResource).Methods("GET", "HEAD", "OPTIONS")
	xapiRouter.HandleFunc("/agents", h.ProxyAgentsResource).Methods("GET", "HEAD", "OPTIONS")

	// Admin API (if multi-tenant)
	if *multiTenant {
		adminRouter := r.PathPrefix("/admin").Subrouter()
		adminRouter.Use(middleware.AdminAuthMiddleware)
		adminRouter.HandleFunc("/tenants", h.CreateTenant).Methods("POST")
		adminRouter.HandleFunc("/tenants", h.ListTenants).Methods("GET")
		adminRouter.HandleFunc("/tenants/{id}", h.GetTenant).Methods("GET")
		adminRouter.HandleFunc("/tenants/{id}", h.UpdateTenant).Methods("PUT")
		adminRouter.HandleFunc("/tenants/{id}", h.DeleteTenant).Methods("DELETE")
	}

	// Apply logging middleware to all routes
	r.Use(middleware.LoggingMiddleware)
	r.Use(middleware.CORSMiddleware)

	// Create server
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      collapseSlashes(r),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.WithField("addr", addr).Info("Starting HTTP server")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down server...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Info("Server stopped")
}
