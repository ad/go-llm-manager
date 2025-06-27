package handlers

import (
	"net/http"
	"time"

	"github.com/ad/go-llm-manager/internal/auth"
	"github.com/ad/go-llm-manager/internal/database"
	"github.com/ad/go-llm-manager/internal/sse"
	"github.com/ad/go-llm-manager/internal/utils"

	"github.com/google/uuid"
)

type PublicHandlers struct {
	db      *database.DB
	jwtAuth *auth.JWTAuth
}

func NewPublicHandlers(db *database.DB, jwtAuth *auth.JWTAuth) *PublicHandlers {
	return &PublicHandlers{
		db:      db,
		jwtAuth: jwtAuth,
	}
}

// SSE manager для оповещения процессоров о новых задачах
var sseManagerInstance *sse.Manager

func SetSSEManager(m *sse.Manager) {
	sseManagerInstance = m
}

// GET / - Health check + endpoints info (matching TypeScript exactly)
func (h *PublicHandlers) HealthCheck(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{
		"message": "LLM Proxy API v1.0",
		"endpoints": map[string]interface{}{
			"api": map[string]string{
				"create":   "/api/create - Create task (JWT)",
				"result":   "/api/result - Get result (JWT)",
				"internal": "/api/internal/* - Internal endpoints",
			},
		},
	}
	utils.SendJSON(w, http.StatusOK, data)
}

// GET /health - Same as / (matching TypeScript)
func (h *PublicHandlers) Health(w http.ResponseWriter, r *http.Request) {
	h.HealthCheck(w, r)
}

// POST /api/create - Create new task (JWT auth required)
func (h *PublicHandlers) CreateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		utils.SendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Extract and validate JWT token (matching TypeScript logic: user_id || sub)
	payload, err := h.jwtAuth.ExtractPayload(r)
	if err != nil {
		utils.SendError(w, http.StatusUnauthorized, "Invalid token")
		return
	}

	userID := payload.UserID
	if userID == "" && payload.Subject != "" {
		userID = payload.Subject
	}

	if userID == "" {
		utils.SendError(w, http.StatusBadRequest, "Invalid token: missing user_id")
		return
	}

	if payload.ProductData == "" {
		utils.SendError(w, http.StatusBadRequest, "Missing product_data in request body or JWT token")
		return
	}

	// Check rate limit - use custom limits from JWT payload if provided
	windowMs := int64(86400000) // 24h default
	maxRequests := 100          // 100 requests default

	if payload.RateLimit != nil {
		windowMs = payload.RateLimit.WindowMs
		maxRequests = payload.RateLimit.MaxRequests
	}

	rateLimit, err := h.db.CheckRateLimit(userID, windowMs, maxRequests)
	if err != nil {
		utils.SendError(w, http.StatusInternalServerError, "Failed to check rate limit")
		return
	}

	if rateLimit.RequestCount > maxRequests {
		utils.SendError(w, http.StatusTooManyRequests, "Rate limit exceeded")
		return
	}

	taskID := uuid.New().String()
	priority := 0
	if payload.Priority != nil {
		priority = *payload.Priority
	}

	// Create task (matching TypeScript structure)
	task := &database.Task{
		ID:          taskID,
		UserID:      userID,
		ProductData: payload.ProductData,
		Status:      "pending",
		Priority:    priority,
		MaxRetries:  3,
	}

	// Set ollama_params if provided
	if payload.OllamaParams != nil {
		if err := task.SetOllamaParams(payload.OllamaParams); err != nil {
			utils.SendError(w, http.StatusInternalServerError, "Failed to set ollama params")
			return
		}
	}

	if err := h.db.CreateTask(task); err != nil {
		if err.Error() == "user already has an active task" {
			utils.SendError(w, http.StatusConflict, "User already has an active task. Please wait for the current task to complete.")
			return
		}
		utils.SendError(w, http.StatusInternalServerError, "Failed to create task")
		return
	}

	// Оповещение процессоров через SSE о новой задаче
	if sseManagerInstance != nil {
		sseManagerInstance.BroadcastPendingTaskToProcessors(task)
	}

	// Calculate estimated wait time (human-readable format like TypeScript)
	estimatedTime, err := calculateEstimatedWaitTime(h.db)
	if err != nil {
		utils.SendError(w, http.StatusInternalServerError, "Failed to calculate estimated time")
		return
	}
	// Generate result token for this specific task (matching TypeScript structure)
	resultPayload := &database.JWTPayload{
		Issuer:   "llm-proxy",
		Audience: "llm-proxy-api",
		Subject:  userID,
		UserID:   userID,
		TaskID:   taskID,
	}

	resultToken, err := h.jwtAuth.GenerateToken(resultPayload, 3600) // 1 hour
	if err != nil {
		utils.SendError(w, http.StatusInternalServerError, "Failed to generate result token")
		return
	}

	data := map[string]interface{}{
		"success":       true,
		"taskId":        taskID,
		"estimatedTime": estimatedTime,
		"token":         resultToken,
	}

	utils.SendJSON(w, http.StatusCreated, data)
}

// POST /api/result - Get task result (JWT auth required)
func (h *PublicHandlers) GetResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		utils.SendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Extract and validate JWT token (matching TypeScript logic: user_id || sub)
	payload, err := h.jwtAuth.ExtractPayload(r)
	if err != nil {
		utils.SendError(w, http.StatusUnauthorized, "Invalid token")
		return
	}

	userID := payload.UserID
	if userID == "" && payload.Subject != "" {
		userID = payload.Subject
	}

	if userID == "" {
		utils.SendError(w, http.StatusBadRequest, "Invalid token: missing user_id")
		return
	}

	if payload.TaskID == "" {
		utils.SendError(w, http.StatusBadRequest, "Invalid token: missing taskId")
		return
	}

	// Get task
	task, err := h.db.GetTask(payload.TaskID)
	if err != nil {
		utils.SendError(w, http.StatusNotFound, "Task not found")
		return
	}

	// Check if user owns the task (additional security check)
	if task.UserID != userID {
		utils.SendError(w, http.StatusForbidden, "Access denied")
		return
	}

	// Return task status and result (matching TypeScript response format)
	data := map[string]interface{}{
		"success":   true,
		"status":    task.Status,
		"result":    task.Result,
		"createdAt": time.Unix(0, task.CreatedAt*int64(time.Millisecond)).Format(time.RFC3339),
	}

	if task.CompletedAt != nil {
		data["processedAt"] = time.Unix(0, *task.CompletedAt*int64(time.Millisecond)).Format(time.RFC3339)
	}

	utils.SendJSON(w, http.StatusOK, data)
}

// GET /admin - HTML admin page
func (h *PublicHandlers) Admin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(AdminHTML))
}

// GET /admin.js - JavaScript for admin page
func (h *PublicHandlers) AdminJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(AdminJS))
}

// GET /admin.css - CSS for admin page
func (h *PublicHandlers) AdminCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(AdminCSS))
}

// GET /query - чистый SSE Polling Demo
func (h *PublicHandlers) Query(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(QueryHTML))
}
