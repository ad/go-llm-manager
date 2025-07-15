package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
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
	publicHandlers := handlers.NewPublicHandlers(db, jwtAuth, cfg)
	internalHandlers := handlers.NewInternalHandlers(db, jwtAuth)
	sseHandlers := handlers.NewSSEHandlers(db, jwtAuth)

	// Связываем SSE manager с publicHandlers для push новых задач
	handlers.SetSSEManager(sseHandlers.Manager())

	// Setup router
	mux := http.NewServeMux()

	// Public endpoints (with CORS)
	mux.Handle("/", middleware.Chain(
		http.HandlerFunc(publicHandlers.HealthCheck),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/health", middleware.Chain(
		http.HandlerFunc(publicHandlers.Health),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/admin", middleware.Chain(
		http.HandlerFunc(publicHandlers.Admin),
		middleware.Logging,
		middleware.CORS,
	))
	mux.Handle("/admin.js", middleware.Chain(
		http.HandlerFunc(publicHandlers.AdminJS),
		middleware.Logging,
		middleware.CORS,
	))
	mux.Handle("/admin.css", middleware.Chain(
		http.HandlerFunc(publicHandlers.AdminCSS),
		middleware.Logging,
		middleware.CORS,
	))

	mux.Handle("/query", middleware.Chain(
		http.HandlerFunc(publicHandlers.Query),
		middleware.Logging,
		middleware.CORS,
	))

	// JWT-protected endpoints
	mux.Handle("/api/create", middleware.Chain(
		http.HandlerFunc(publicHandlers.CreateTask),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/result", middleware.Chain(
		http.HandlerFunc(publicHandlers.GetResult),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/get", middleware.Chain(
		http.HandlerFunc(publicHandlers.GetUserData),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	// Task voting endpoint (JWT-protected)
	mux.Handle("/api/tasks/", middleware.Chain(
		handleTaskVote(publicHandlers),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	// SSE endpoints
	mux.Handle("/api/result-polling", middleware.Chain(
		http.HandlerFunc(sseHandlers.ResultPolling),
		// middleware.Logging,
		middleware.CORS,
	))

	// Internal API endpoints (API key protected)
	mux.Handle("/api/internal/generate-token", middleware.Chain(
		http.HandlerFunc(internalHandlers.GenerateToken),
		requireAPIKey(apiKeyAuth),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/tasks", middleware.Chain(
		http.HandlerFunc(internalHandlers.GetTasks),
		requireAPIKey(apiKeyAuth),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/all-tasks", middleware.Chain(
		http.HandlerFunc(internalHandlers.GetAllTasks),
		requireAPIKey(apiKeyAuth),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/claim", middleware.Chain(
		http.HandlerFunc(internalHandlers.ClaimTasks),
		requireAPIKey(apiKeyAuth),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/heartbeat", middleware.Chain(
		http.HandlerFunc(internalHandlers.Heartbeat),
		requireAPIKey(apiKeyAuth),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/processor-heartbeat", middleware.Chain(
		http.HandlerFunc(internalHandlers.ProcessorHeartbeat),
		requireAPIKey(apiKeyAuth),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/complete", middleware.Chain(
		http.HandlerFunc(internalHandlers.CompleteTasks),
		requireAPIKey(apiKeyAuth),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/cleanup", middleware.Chain(
		http.HandlerFunc(internalHandlers.Cleanup),
		requireAPIKey(apiKeyAuth),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/cleanup/stats", middleware.Chain(
		http.HandlerFunc(internalHandlers.CleanupStats),
		requireAPIKey(apiKeyAuth),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/work-steal", middleware.Chain(
		http.HandlerFunc(internalHandlers.WorkSteal),
		requireAPIKey(apiKeyAuth),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/metrics", middleware.Chain(
		http.HandlerFunc(internalHandlers.ProcessorMetrics),
		requireAPIKey(apiKeyAuth),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/estimated-time", middleware.Chain(
		http.HandlerFunc(internalHandlers.EstimatedTime),
		requireAPIKey(apiKeyAuth),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/task-stream", middleware.Chain(
		http.HandlerFunc(sseHandlers.TaskStream),
		requireAPIKey(apiKeyAuth),
		// middleware.Logging,
		middleware.CORS,
	))

	mux.Handle("/api/internal/requeue", middleware.Chain(
		http.HandlerFunc(internalHandlers.RequeueTask),
		requireAPIKey(apiKeyAuth),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/rating-stats", middleware.Chain(
		http.HandlerFunc(internalHandlers.GetRatingStats),
		requireAPIKey(apiKeyAuth),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	mux.Handle("/api/internal/rating-analytics", middleware.Chain(
		http.HandlerFunc(internalHandlers.GetRatingAnalytics),
		requireAPIKey(apiKeyAuth),
		middleware.Logging,
		middleware.CORS,
		middleware.ContentType,
	))

	// Create server
	addr := fmt.Sprintf("%s:%s", cfg.Server.Host, cfg.Server.Port)
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
		// Security timeouts
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Start server in goroutine
	go func(version string) {
		log.Printf("Starting server v.%s on %s\n", version, addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}(version)

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown: %v\n", err)
	}

	log.Println("Server exited")
}

// API key middleware
func requireAPIKey(apiKeyAuth *auth.APIKeyManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check Authorization header first
			auth := r.Header.Get("Authorization")
			if auth != "" {
				// Parse "Bearer <token>" format
				parts := strings.SplitN(auth, " ", 2)
				if len(parts) == 2 && parts[0] == "Bearer" && parts[1] != "" {
					token := parts[1]
					if apiKeyAuth.ValidateKey(token) {
						next.ServeHTTP(w, r)
						return
					}
				}
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "Invalid or missing API key"}`))
		})
	}
}

// Helper function to handle path patterns with parameters
func handleTaskVote(publicHandlers *handlers.PublicHandlers) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Parse path: /api/tasks/{id}/vote
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")

		// Expected: ["api", "tasks", "{task_id}", "vote"]
		if len(parts) == 4 && parts[0] == "api" && parts[1] == "tasks" && parts[3] == "vote" {
			// parts[2] contains the task ID, we can validate it's not empty
			if parts[2] != "" {
				publicHandlers.VoteTask(w, r)
			} else {
				http.NotFound(w, r)
			}
		} else {
			http.NotFound(w, r)
		}
	}
}
