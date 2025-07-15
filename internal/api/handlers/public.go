package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/ad/go-llm-manager/internal/auth"
	"github.com/ad/go-llm-manager/internal/config"
	"github.com/ad/go-llm-manager/internal/database"
	"github.com/ad/go-llm-manager/internal/sse"
	"github.com/ad/go-llm-manager/internal/utils"

	"github.com/google/uuid"
)

type PublicHandlers struct {
	db      *database.DB
	jwtAuth *auth.JWTAuth
	config  *config.Config
}

func NewPublicHandlers(db *database.DB, jwtAuth *auth.JWTAuth, cfg *config.Config) *PublicHandlers {
	return &PublicHandlers{
		db:      db,
		jwtAuth: jwtAuth,
		config:  cfg,
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
		"status":  "ok",
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
		log.Printf("Failed to check rate limit for user %s (window: %d ms, max: %d): %v", userID, windowMs, maxRequests, err)
		utils.SendError(w, http.StatusInternalServerError, "Failed to check rate limit")
		return
	}

	if rateLimit.RequestCount > maxRequests {
		log.Printf("Rate limit exceeded for user %s: %d requests in %d ms", userID, rateLimit.RequestCount, windowMs)
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
		"rating":    task.UserRating,
	}

	if task.CompletedAt != nil {
		data["processedAt"] = time.Unix(0, *task.CompletedAt*int64(time.Millisecond)).Format(time.RFC3339)
	}

	utils.SendJSON(w, http.StatusOK, data)
}

// GET /api/get - Get user's latest task and rate limits (JWT auth required)
func (h *PublicHandlers) GetUserData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		utils.SendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Extract JWT from query parameter
	token := r.URL.Query().Get("token")
	if token == "" {
		utils.SendError(w, http.StatusBadRequest, "Missing token parameter")
		return
	}

	// Validate JWT token
	payload, err := h.jwtAuth.ExtractPayloadFromToken(token)
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

	// Get user's latest task
	latestTask, err := h.db.GetUserLatestTask(userID)
	if err != nil {
		utils.SendError(w, http.StatusInternalServerError, "Failed to get latest task")
		return
	}

	// Get user's rate limits
	rateLimit, err := h.db.GetUserRateLimit(userID)
	if err != nil {
		utils.SendError(w, http.StatusInternalServerError, "Failed to get rate limits")
		return
	}

	// Prepare response
	data := map[string]interface{}{
		"success": true,
		"user_id": userID,
		"rate_limit": map[string]interface{}{
			"request_count": rateLimit.RequestCount,
			"request_limit": h.config.RateLimit.MaxRequests,
			"window_start":  rateLimit.WindowStart,
			"last_request":  rateLimit.LastRequest,
			"period_start":  time.Unix(0, rateLimit.WindowStart*int64(time.Millisecond)).Format(time.RFC3339),
			"period_end":    time.Unix(0, (rateLimit.WindowStart+h.config.RateLimit.WindowMs)*int64(time.Millisecond)).Format(time.RFC3339),
			// "token_count":   0,     // TODO: implement token counting
			// "token_limit":   10000, // TODO: get from config
		},
	}

	// Add latest task if exists
	if latestTask != nil {
		taskData := map[string]interface{}{
			"id":           latestTask.ID,
			"status":       latestTask.Status,
			"product_data": latestTask.ProductData,
			"priority":     latestTask.Priority,
			"created_at":   time.Unix(0, latestTask.CreatedAt*int64(time.Millisecond)).Format(time.RFC3339),
			"updated_at":   time.Unix(0, latestTask.UpdatedAt*int64(time.Millisecond)).Format(time.RFC3339),
			"rating":       latestTask.UserRating,
		}

		if latestTask.Result != nil {
			taskData["result"] = *latestTask.Result
		}
		if latestTask.ErrorMessage != nil {
			taskData["error_message"] = *latestTask.ErrorMessage
		}
		if latestTask.CompletedAt != nil {
			taskData["completed_at"] = time.Unix(0, *latestTask.CompletedAt*int64(time.Millisecond)).Format(time.RFC3339)
		}
		if latestTask.ProcessingStartedAt != nil {
			taskData["processing_started_at"] = time.Unix(0, *latestTask.ProcessingStartedAt*int64(time.Millisecond)).Format(time.RFC3339)
		}
		if latestTask.OllamaParams != nil {
			// Parse ollama_params from JSON string to object
			var ollamaParams map[string]interface{}
			if err := json.Unmarshal([]byte(*latestTask.OllamaParams), &ollamaParams); err == nil {
				taskData["ollama_params"] = ollamaParams
			} else {
				// Fallback to string if parsing fails
				taskData["ollama_params"] = *latestTask.OllamaParams
			}
		}

		// If task is active (processing or pending), generate result token
		if latestTask.Status == "processing" || latestTask.Status == "pending" || latestTask.Status == "completed" {
			resultPayload := &database.JWTPayload{
				Issuer:   "llm-proxy",
				Audience: "llm-proxy-api",
				Subject:  userID,
				UserID:   userID,
				TaskID:   latestTask.ID,
			}

			resultToken, err := h.jwtAuth.GenerateToken(resultPayload, 3600) // 1 hour
			if err != nil {
				log.Printf("Failed to generate result token for task %s: %v", latestTask.ID, err)
			} else {
				taskData["token"] = resultToken
			}
		}

		data["last_task"] = taskData
	} else {
		data["last_task"] = nil
	}

	utils.SendJSON(w, http.StatusOK, data)
}

// POST /api/tasks/vote - Vote on a task (JWT auth required)
func (h *PublicHandlers) VoteTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		utils.SendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Extract and validate JWT token
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

	taskID := payload.TaskID
	if taskID == "" {
		utils.SendError(w, http.StatusBadRequest, "Invalid token: missing taskId")
		return
	}

	// Parse request body
	var req database.VoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.SendError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	log.Printf("VoteTask: userID=%s, taskID=%s, voteType=%s", userID, taskID, req.VoteType)

	// Validate vote value
	if req.VoteType != "upvote" && req.VoteType != "downvote" && req.VoteType != "" {
		utils.SendError(w, http.StatusBadRequest, "Invalid vote value. Must be 'upvote', 'downvote', or empty string")
		return
	}

	// Get task to verify ownership and status
	task, err := h.db.GetTask(taskID)
	if err != nil {
		utils.SendError(w, http.StatusNotFound, "Task not found")
		return
	}

	// Check if user owns the task
	if task.UserID != userID {
		utils.SendError(w, http.StatusForbidden, "You can only vote on your own tasks")
		return
	}

	// Check if task is completed
	if task.Status != "completed" {
		utils.SendError(w, http.StatusBadRequest, "You can only vote on completed tasks")
		return
	}

	// Update task rating with toggle logic
	var newRating *string

	// Check if user is trying to vote the same way (toggle behavior)
	if req.VoteType != "" {
		if task.UserRating != nil && *task.UserRating == req.VoteType {
			// Same vote - remove it (toggle)
			newRating = nil
		} else {
			// Different vote or no vote - set new vote
			newRating = &req.VoteType
		}
	} else {
		// Empty vote type - remove current vote
		newRating = nil
	}

	err = h.db.UpdateTaskRating(taskID, userID, newRating)
	if err != nil {
		log.Printf("Failed to update task rating: %v", err)
		utils.SendError(w, http.StatusInternalServerError, "Failed to update rating")
		return
	}

	log.Printf("Successfully updated task rating: taskID=%s, newRating=%v", taskID, newRating)

	// Return response
	response := database.VoteResponse{
		Success:    true,
		UserRating: newRating,
	}

	utils.SendJSON(w, http.StatusOK, response)
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
