package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"

	"github.com/inxsol/xapi-lrs-auth-proxy/internal/config"
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
)

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

	// Initialize handlers
	h := handlers.New(tenantStore)

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

	// xAPI Proxy (content-facing) - requires JWT
	xapiRouter := r.PathPrefix("/xapi").Subrouter()
	xapiRouter.Use(middleware.TenantMiddleware(tenantStore))
	xapiRouter.Use(middleware.JWTAuthMiddleware)
	xapiRouter.HandleFunc("/statements", h.ProxyStatements).Methods("POST", "PUT", "GET")
	xapiRouter.HandleFunc("/activities/state", h.ProxyState).Methods("POST", "PUT", "GET", "DELETE")
	xapiRouter.HandleFunc("/activities/profile", h.ProxyActivityProfile).Methods("POST", "PUT", "GET", "DELETE")
	xapiRouter.HandleFunc("/agents/profile", h.ProxyAgentProfile).Methods("POST", "PUT", "GET", "DELETE")
	xapiRouter.HandleFunc("/about", h.ProxyAbout).Methods("GET")

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
		Handler:      r,
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
