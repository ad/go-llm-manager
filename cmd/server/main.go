package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ad/go-llm-manager/internal/api/handlers"
	"github.com/ad/go-llm-manager/internal/auth"
	"github.com/ad/go-llm-manager/internal/config"
	"github.com/ad/go-llm-manager/internal/database"
	"github.com/ad/go-llm-manager/internal/middleware"
)

var version = "dev" // Set by build system

func main() {
	// Load configuration
	cfg := config.Load(os.Args)

	// Initialize database
	db, err := database.NewSQLiteDB(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Run migrations
	if err := db.RunMigrations(); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Initialize auth
	jwtAuth := auth.NewJWTAuth(cfg.Auth.JWTSecret)
	apiKeyAuth := auth.NewAPIKeyManager(cfg.Auth.InternalAPIKey)

	// Initialize handlers
	publicHandlers := handlers.NewPublicHandlers(db, jwtAuth)
	internalHandlers := handlers.NewInternalHandlers(db, jwtAuth)
	sseHandlers := handlers.NewSSEHandlers(db, jwtAuth)

	// Setup router
	mux := http.NewServeMux()

	// Public endpoints (with CORS)
	mux.Handle("/", middleware.Chain(
		http.HandlerFunc(publicHandlers.HealthCheck),
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/health", middleware.Chain(
		http.HandlerFunc(publicHandlers.Health),
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/example", middleware.Chain(
		http.HandlerFunc(publicHandlers.Example),
		middleware.CORS,
	))

	mux.Handle("/query", middleware.Chain(
		http.HandlerFunc(publicHandlers.Query),
		middleware.CORS,
	))

	// JWT-protected endpoints
	mux.Handle("/api/create", middleware.Chain(
		http.HandlerFunc(publicHandlers.CreateTask),
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/result", middleware.Chain(
		http.HandlerFunc(publicHandlers.GetResult),
		middleware.CORS,
		middleware.ContentType,
	))

	// SSE endpoints
	mux.Handle("/api/result-polling", middleware.Chain(
		http.HandlerFunc(sseHandlers.ResultPolling),
		middleware.CORS,
	))

	// Internal API endpoints (API key protected)
	mux.Handle("/api/internal/generate-token", middleware.Chain(
		http.HandlerFunc(internalHandlers.GenerateToken),
		requireAPIKey(apiKeyAuth),
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/tasks", middleware.Chain(
		http.HandlerFunc(internalHandlers.GetTasks),
		requireAPIKey(apiKeyAuth),
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/all-tasks", middleware.Chain(
		http.HandlerFunc(internalHandlers.GetAllTasks),
		requireAPIKey(apiKeyAuth),
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/claim", middleware.Chain(
		http.HandlerFunc(internalHandlers.ClaimTasks),
		requireAPIKey(apiKeyAuth),
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/heartbeat", middleware.Chain(
		http.HandlerFunc(internalHandlers.Heartbeat),
		requireAPIKey(apiKeyAuth),
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/processor-heartbeat", middleware.Chain(
		http.HandlerFunc(internalHandlers.ProcessorHeartbeat),
		requireAPIKey(apiKeyAuth),
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/complete", middleware.Chain(
		http.HandlerFunc(internalHandlers.CompleteTasks),
		requireAPIKey(apiKeyAuth),
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/cleanup", middleware.Chain(
		http.HandlerFunc(internalHandlers.Cleanup),
		requireAPIKey(apiKeyAuth),
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/cleanup/stats", middleware.Chain(
		http.HandlerFunc(internalHandlers.CleanupStats),
		requireAPIKey(apiKeyAuth),
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/work-steal", middleware.Chain(
		http.HandlerFunc(internalHandlers.WorkSteal),
		requireAPIKey(apiKeyAuth),
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/metrics", middleware.Chain(
		http.HandlerFunc(internalHandlers.ProcessorMetrics),
		requireAPIKey(apiKeyAuth),
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/estimated-time", middleware.Chain(
		http.HandlerFunc(internalHandlers.EstimatedTime),
		requireAPIKey(apiKeyAuth),
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/task-stream", middleware.Chain(
		http.HandlerFunc(sseHandlers.TaskStream),
		requireAPIKey(apiKeyAuth),
		middleware.CORS,
	))

	// Create server
	addr := fmt.Sprintf("%s:%s", cfg.Server.Host, cfg.Server.Port)
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
		// Security timeouts
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Printf("Starting server v.%s on %s", version, addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}

// API key middleware
func requireAPIKey(apiKeyAuth *auth.APIKeyManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check Authorization header first
			auth := r.Header.Get("Authorization")
			if auth != "" && len(auth) > 7 && auth[:7] == "Bearer " {
				token := auth[7:]
				if apiKeyAuth.ValidateKey(token) {
					next.ServeHTTP(w, r)
					return
				}
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "Invalid or missing API key"}`))
		})
	}
}
