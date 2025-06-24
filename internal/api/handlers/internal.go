package handlers

import (
	"database/sql"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ad/go-llm-manager/internal/auth"
	"github.com/ad/go-llm-manager/internal/database"
	"github.com/ad/go-llm-manager/internal/utils"
)

type InternalHandlers struct {
	db      *database.DB
	jwtAuth *auth.JWTAuth
}

func NewInternalHandlers(db *database.DB, jwtAuth *auth.JWTAuth) *InternalHandlers {
	return &InternalHandlers{
		db:      db,
		jwtAuth: jwtAuth,
	}
}

// POST /api/internal/generate-token - Generate JWT token
func (h *InternalHandlers) GenerateToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID        string                    `json:"user_id,omitempty"`
		ProcessorID   string                    `json:"processor_id,omitempty"`
		DurationHours *int                      `json:"duration_hours,omitempty"`
		TaskID        string                    `json:"taskId,omitempty"`
		ExpiresIn     *int                      `json:"expires_in,omitempty"`
		ProductData   string                    `json:"product_data,omitempty"`
		Priority      *int                      `json:"priority,omitempty"`
		OllamaParams  *database.OllamaParams    `json:"ollama_params,omitempty"`
		RateLimit     *database.RateLimitConfig `json:"rate_limit,omitempty"`
	}

	if err := utils.ParseJSON(r, &req); err != nil {
		utils.SendError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Support for processor tokens (matching TypeScript logic exactly)
	if req.ProcessorID != "" {
		expiresIn := 3600 // 1 hour default
		if req.DurationHours != nil {
			expiresIn = *req.DurationHours * 3600
		}

		payload := &database.JWTPayload{
			Issuer:      "llm-proxy",
			Audience:    "llm-proxy-api",
			Subject:     req.ProcessorID,
			ProcessorID: req.ProcessorID,
		}

		token, err := h.jwtAuth.GenerateToken(payload, expiresIn)
		if err != nil {
			utils.SendError(w, http.StatusInternalServerError, "Failed to generate token")
			return
		}

		data := map[string]interface{}{
			"success":    true,
			"token":      token,
			"expires_in": expiresIn,
		}

		utils.SendJSON(w, http.StatusOK, data)
		return
	}

	// Regular user tokens (matching TypeScript logic exactly)
	if req.UserID == "" {
		utils.SendError(w, http.StatusBadRequest, "user_id or processor_id is required")
		return
	}

	priority := 0
	if req.Priority != nil {
		priority = *req.Priority
	}

	payload := &database.JWTPayload{
		Issuer:       "llm-proxy",
		Audience:     "llm-proxy-api",
		Subject:      req.UserID,
		UserID:       req.UserID,
		TaskID:       req.TaskID,
		ProductData:  req.ProductData,
		Priority:     &priority,
		OllamaParams: req.OllamaParams,
		RateLimit:    req.RateLimit,
	}

	expiresIn := 3600 // 1 hour default
	if req.ExpiresIn != nil {
		expiresIn = *req.ExpiresIn
	}

	token, err := h.jwtAuth.GenerateToken(payload, expiresIn)
	if err != nil {
		utils.SendError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	data := map[string]interface{}{
		"success":    true,
		"token":      token,
		"expires_in": expiresIn,
	}

	utils.SendJSON(w, http.StatusOK, data)
}

// GET /api/internal/tasks - Get pending tasks
func (h *InternalHandlers) GetTasks(w http.ResponseWriter, r *http.Request) {
	limitStr := r.URL.Query().Get("limit")
	limit := 20 // default
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	tasks, err := h.db.GetPendingTasks(limit)
	if err != nil {
		utils.SendError(w, http.StatusInternalServerError, "Failed to get tasks")
		return
	}

	utils.SendJSON(w, http.StatusOK, map[string]interface{}{
		"tasks": tasks,
	})
}

// GET /api/internal/all-tasks - Get all tasks
func (h *InternalHandlers) GetAllTasks(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	limitStr := query.Get("limit")
	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 1000 {
			limit = l
		}
	}

	offsetStr := query.Get("offset")
	offset := 0
	if offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	var userID *string
	if uid := query.Get("user_id"); uid != "" {
		userID = &uid
	}

	tasks, err := h.db.GetAllTasks(userID, limit, offset)
	if err != nil {
		utils.SendError(w, http.StatusInternalServerError, "Failed to get tasks")
		return
	}

	utils.SendJSON(w, http.StatusOK, map[string]interface{}{
		"tasks": tasks,
	})
}

// POST /api/internal/claim - Batch claim tasks
func (h *InternalHandlers) ClaimTasks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProcessorID         string   `json:"processor_id"`
		BatchSize           *int     `json:"batch_size,omitempty"`
		ProcessorLoad       *float64 `json:"processor_load,omitempty"`
		TimeoutMs           *int64   `json:"timeout_ms,omitempty"`
		UseFairDistribution *bool    `json:"use_fair_distribution,omitempty"`
	}

	if err := utils.ParseJSON(r, &req); err != nil {
		utils.SendError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if req.ProcessorID == "" {
		utils.SendError(w, http.StatusBadRequest, "processor_id is required")
		return
	}

	batchSize := 5
	if req.BatchSize != nil && *req.BatchSize > 0 && *req.BatchSize <= 20 {
		batchSize = *req.BatchSize
	}

	processorLoad := 0.0
	if req.ProcessorLoad != nil {
		processorLoad = *req.ProcessorLoad
	}

	timeoutMs := int64(300000) // 5 minutes default
	if req.TimeoutMs != nil && *req.TimeoutMs > 0 {
		timeoutMs = *req.TimeoutMs
	}

	useFairDistribution := false
	if req.UseFairDistribution != nil {
		useFairDistribution = *req.UseFairDistribution
	}

	var claimedTasks []*database.Task
	var fairDistributionInfo string
	var err error

	if useFairDistribution {
		claimedTasks, fairDistributionInfo, err = h.claimTasksWithFairDistribution(req.ProcessorID, batchSize, processorLoad, timeoutMs)
	} else {
		claimedTasks, err = h.claimTasksBatch(req.ProcessorID, batchSize, timeoutMs)
		fairDistributionInfo = "Not used"
	}

	if err != nil {
		utils.SendError(w, http.StatusInternalServerError, "Failed to claim tasks")
		return
	}

	response := map[string]interface{}{
		"success":       true,
		"tasks":         claimedTasks,
		"claimed_count": len(claimedTasks),
	}

	if useFairDistribution {
		response["fair_distribution_info"] = fairDistributionInfo
	}

	utils.SendJSON(w, http.StatusOK, response)
}

func (h *InternalHandlers) claimTasksBatch(processorID string, batchSize int, timeoutMs int64) ([]*database.Task, error) {
	// Get pending tasks
	tasks, err := h.db.GetPendingTasks(batchSize)
	if err != nil {
		return nil, err
	}

	if len(tasks) == 0 {
		return []*database.Task{}, nil
	}

	// Claim tasks by updating status
	claimedTasks := make([]*database.Task, 0)
	now := time.Now().UnixMilli()
	timeoutAt := now + timeoutMs

	for _, task := range tasks {
		// Update task to processing
		query := `
			UPDATE tasks 
			SET status = 'processing', 
			    processor_id = ?, 
			    processing_started_at = ?, 
			    heartbeat_at = ?,
			    timeout_at = ?,
			    updated_at = ?
			WHERE id = ? AND status = 'pending'
		`

		result, err := h.db.Exec(query, processorID, now, now, timeoutAt, now, task.ID)
		if err != nil {
			continue
		}

		if rowsAffected, _ := result.RowsAffected(); rowsAffected > 0 {
			// Update task object
			task.Status = "processing"
			task.ProcessorID = &processorID
			task.ProcessingStartedAt = &now
			task.HeartbeatAt = &now
			task.TimeoutAt = &timeoutAt
			task.UpdatedAt = now

			claimedTasks = append(claimedTasks, task)
		}
	}

	return claimedTasks, nil
}

// claimTasksWithFairDistribution implements advanced fair distribution logic
func (h *InternalHandlers) claimTasksWithFairDistribution(processorID string, batchSize int, processorLoad float64, timeoutMs int64) ([]*database.Task, string, error) {
	// Adjust batch size based on processor load (higher load = fewer tasks)
	adjustedBatchSize := int(math.Max(1, math.Ceil(float64(batchSize)*(1.0-processorLoad*0.5))))

	now := time.Now().UnixMilli()
	timeoutAt := now + timeoutMs

	// First, select pending tasks with priority
	selectQuery := `
		SELECT id, user_id, product_data, priority, retry_count, 
		       estimated_duration, ollama_params, created_at
		FROM tasks 
		WHERE status = 'pending'
		ORDER BY priority DESC, created_at ASC
		LIMIT ?
	`

	rows, err := h.db.Query(selectQuery, adjustedBatchSize)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var selectedTasks []*database.Task
	for rows.Next() {
		task := &database.Task{}
		var ollamaParamsJSON sql.NullString

		err := rows.Scan(
			&task.ID, &task.UserID, &task.ProductData, &task.Priority,
			&task.RetryCount, &task.EstimatedDuration, &ollamaParamsJSON, &task.CreatedAt,
		)
		if err != nil {
			continue
		}

		if ollamaParamsJSON.Valid && ollamaParamsJSON.String != "" {
			task.OllamaParams = &ollamaParamsJSON.String
		}

		selectedTasks = append(selectedTasks, task)
	}

	if len(selectedTasks) == 0 {
		fairInfo := fmt.Sprintf("Load: %.1f, Adjusted batch size: %d, No tasks available", processorLoad, adjustedBatchSize)
		return []*database.Task{}, fairInfo, nil
	}

	// Atomic update of selected tasks using transaction
	tx, err := h.db.Begin()
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()

	claimedTasks := make([]*database.Task, 0)
	placeholders := make([]string, len(selectedTasks))
	taskIDs := make([]interface{}, len(selectedTasks))

	for i, task := range selectedTasks {
		placeholders[i] = "?"
		taskIDs[i] = task.ID
	}

	updateQuery := fmt.Sprintf(`
		UPDATE tasks 
		SET status = 'processing',
		    processor_id = ?,
		    processing_started_at = ?,
		    heartbeat_at = ?,
		    timeout_at = ?,
		    updated_at = ?
		WHERE id IN (%s) AND status = 'pending'
	`, strings.Join(placeholders, ","))

	args := append([]interface{}{processorID, now, now, timeoutAt, now}, taskIDs...)
	result, err := tx.Exec(updateQuery, args...)
	if err != nil {
		return nil, "", err
	}

	if err = tx.Commit(); err != nil {
		return nil, "", err
	}

	rowsAffected, _ := result.RowsAffected()

	// Update task objects for claimed tasks
	for i, task := range selectedTasks {
		if int64(i) < rowsAffected {
			task.Status = "processing"
			task.ProcessorID = &processorID
			task.ProcessingStartedAt = &now
			task.HeartbeatAt = &now
			task.TimeoutAt = &timeoutAt
			task.UpdatedAt = now
			claimedTasks = append(claimedTasks, task)
		}
	}

	fairInfo := fmt.Sprintf("Load: %.1f, Adjusted batch size: %d, Claimed: %d", processorLoad, adjustedBatchSize, len(claimedTasks))
	return claimedTasks, fairInfo, nil
}

// POST /api/internal/heartbeat - Enhanced heartbeat with metrics
func (h *InternalHandlers) Heartbeat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskID      string   `json:"taskId"` // Single task ID (matching TypeScript)
		ProcessorID string   `json:"processor_id"`
		CPUUsage    *float64 `json:"cpu_usage,omitempty"`
		MemoryUsage *float64 `json:"memory_usage,omitempty"`
		QueueSize   *int     `json:"queue_size,omitempty"`
	}

	if err := utils.ParseJSON(r, &req); err != nil {
		utils.SendError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if req.ProcessorID == "" || req.TaskID == "" {
		utils.SendError(w, http.StatusBadRequest, "taskId and processor_id are required")
		return
	}

	now := time.Now().UnixMilli()

	// Update processor metrics if provided
	if req.CPUUsage != nil || req.MemoryUsage != nil || req.QueueSize != nil {
		activeTasksCount := 1 // Since we have one task in heartbeat
		query := `
			INSERT OR REPLACE INTO processor_metrics 
			(processor_id, cpu_usage, memory_usage, queue_size, active_tasks, last_updated, created_at)
			VALUES (?, 
			        COALESCE(?, (SELECT cpu_usage FROM processor_metrics WHERE processor_id = ?)), 
			        COALESCE(?, (SELECT memory_usage FROM processor_metrics WHERE processor_id = ?)),
			        COALESCE(?, (SELECT queue_size FROM processor_metrics WHERE processor_id = ?)),
			        ?,
			        ?, 
			        COALESCE((SELECT created_at FROM processor_metrics WHERE processor_id = ?), ?))
		`

		_, err := h.db.Exec(query,
			req.ProcessorID, req.CPUUsage, req.ProcessorID,
			req.MemoryUsage, req.ProcessorID,
			req.QueueSize, req.ProcessorID,
			activeTasksCount,
			now, req.ProcessorID, now)

		if err != nil {
			utils.SendError(w, http.StatusInternalServerError, "Failed to update metrics")
			return
		}
	}

	// Update heartbeat for the task
	query := `
		UPDATE tasks 
		SET heartbeat_at = ?, updated_at = ?
		WHERE processor_id = ? AND id = ? AND status = 'processing'
	`

	result, err := h.db.Exec(query, now, now, req.ProcessorID, req.TaskID)
	if err != nil {
		utils.SendError(w, http.StatusInternalServerError, "Failed to update heartbeat")
		return
	}

	if rowsAffected, _ := result.RowsAffected(); rowsAffected == 0 {
		utils.SendError(w, http.StatusNotFound, "Task not found or not owned by processor")
		return
	}

	utils.SendJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

// POST /api/internal/processor-heartbeat - Processor general heartbeat with metrics
func (h *InternalHandlers) ProcessorHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProcessorID string   `json:"processor_id"`
		CPUUsage    *float64 `json:"cpu_usage,omitempty"`
		MemoryUsage *float64 `json:"memory_usage,omitempty"`
		QueueSize   *int     `json:"queue_size,omitempty"`
	}

	if err := utils.ParseJSON(r, &req); err != nil {
		utils.SendError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if req.ProcessorID == "" {
		utils.SendError(w, http.StatusBadRequest, "processor_id is required")
		return
	}

	now := time.Now().UnixMilli()

	// Count active tasks for this processor
	var activeTasksCount int
	countQuery := `SELECT COUNT(*) FROM tasks WHERE processor_id = ? AND status = 'processing'`
	err := h.db.QueryRow(countQuery, req.ProcessorID).Scan(&activeTasksCount)
	if err != nil {
		log.Printf("Error counting active tasks: %v", err)
		activeTasksCount = 0
	}

	// Update processor metrics
	query := `
		INSERT OR REPLACE INTO processor_metrics 
		(processor_id, cpu_usage, memory_usage, queue_size, active_tasks, last_updated, created_at)
		VALUES (?, 
		        COALESCE(?, (SELECT cpu_usage FROM processor_metrics WHERE processor_id = ?)), 
		        COALESCE(?, (SELECT memory_usage FROM processor_metrics WHERE processor_id = ?)),
		        COALESCE(?, (SELECT queue_size FROM processor_metrics WHERE processor_id = ?)),
		        ?,
		        ?, 
		        COALESCE((SELECT created_at FROM processor_metrics WHERE processor_id = ?), ?))
	`

	_, err = h.db.Exec(query,
		req.ProcessorID, req.CPUUsage, req.ProcessorID,
		req.MemoryUsage, req.ProcessorID,
		req.QueueSize, req.ProcessorID,
		activeTasksCount,
		now, req.ProcessorID, now)

	if err != nil {
		utils.SendError(w, http.StatusInternalServerError, "Failed to update processor metrics")
		return
	}

	utils.SendJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

// POST /api/internal/complete - Complete tasks
func (h *InternalHandlers) CompleteTasks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskID       string  `json:"taskId"`                 // Single task ID (matching TypeScript)
		ProcessorID  *string `json:"processor_id,omitempty"` // Optional for compatibility
		Status       string  `json:"status"`
		Result       *string `json:"result,omitempty"`
		ErrorMessage *string `json:"error_message,omitempty"`
	}

	if err := utils.ParseJSON(r, &req); err != nil {
		utils.SendError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if req.TaskID == "" {
		utils.SendError(w, http.StatusBadRequest, "taskId is required")
		return
	}

	if req.Status != "completed" && req.Status != "failed" {
		utils.SendError(w, http.StatusBadRequest, "status must be 'completed' or 'failed'")
		return
	}

	now := time.Now().UnixMilli()

	// Calculate actual duration - use processor_id if provided for security
	var actualDuration *int64
	var query string
	var args []interface{}

	if req.ProcessorID != nil && *req.ProcessorID != "" {
		query = `
			SELECT processing_started_at FROM tasks 
			WHERE id = ? AND processor_id = ? AND status = 'processing'
		`
		args = []interface{}{req.TaskID, *req.ProcessorID}
	} else {
		query = `
			SELECT processing_started_at FROM tasks 
			WHERE id = ? AND status = 'processing'
		`
		args = []interface{}{req.TaskID}
	}

	var startedAt sql.NullInt64
	if err := h.db.QueryRow(query, args...).Scan(&startedAt); err == nil && startedAt.Valid {
		duration := now - startedAt.Int64
		actualDuration = &duration
	}

	// Update task - use processor_id constraint if provided
	var updateQuery string
	var updateArgs []interface{}

	if req.ProcessorID != nil && *req.ProcessorID != "" {
		updateQuery = `
			UPDATE tasks 
			SET status = ?, result = ?, error_message = ?, completed_at = ?, 
			    actual_duration = ?, updated_at = ?
			WHERE id = ? AND processor_id = ? AND status = 'processing'
		`
		updateArgs = []interface{}{
			req.Status, req.Result, req.ErrorMessage,
			now, actualDuration, now,
			req.TaskID, *req.ProcessorID,
		}
	} else {
		updateQuery = `
			UPDATE tasks 
			SET status = ?, result = ?, error_message = ?, completed_at = ?, 
			    actual_duration = ?, updated_at = ?
			WHERE id = ? AND status = 'processing'
		`
		updateArgs = []interface{}{
			req.Status, req.Result, req.ErrorMessage,
			now, actualDuration, now,
			req.TaskID,
		}
	}

	result, err := h.db.Exec(updateQuery, updateArgs...)

	if err != nil {
		utils.SendError(w, http.StatusInternalServerError, "Failed to complete task")
		return
	}

	if rowsAffected, _ := result.RowsAffected(); rowsAffected == 0 {
		utils.SendError(w, http.StatusNotFound, "Task not found or not owned by processor")
		return
	}

	utils.SendJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

// POST /api/internal/cleanup - Manual cleanup trigger
func (h *InternalHandlers) Cleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		utils.SendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	stats, cleaned, err := h.performCleanup()
	if err != nil {
		utils.SendError(w, http.StatusInternalServerError, "Cleanup failed")
		return
	}

	data := map[string]interface{}{
		"message": "Cleanup completed",
		"stats":   stats,
		"cleaned": cleaned,
	}

	utils.SendJSON(w, http.StatusOK, data)
}

// GET /api/internal/cleanup/stats - Get cleanup statistics
func (h *InternalHandlers) CleanupStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		utils.SendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	stats, err := h.getCleanupStats()
	if err != nil {
		utils.SendError(w, http.StatusInternalServerError, "Failed to get cleanup stats")
		return
	}

	utils.SendJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"stats":   stats,
	})
}

func (h *InternalHandlers) performCleanup() (map[string]interface{}, map[string]interface{}, error) {
	now := time.Now().UnixMilli()
	sevenDaysAgo := now - (7 * 24 * 60 * 60 * 1000) // 7 days ago
	fiveMinutesAgo := now - (5 * 60 * 1000)         // 5 minutes ago

	// Get current stats before cleanup
	stats, err := h.getCleanupStats()
	if err != nil {
		return nil, nil, err
	}

	// 1. Clean old completed/failed tasks (older than 7 days)
	cleanTasksQuery := `
		DELETE FROM tasks 
		WHERE (status = 'completed' OR status = 'failed') 
		AND completed_at < ?
	`

	taskResult, err := h.db.Exec(cleanTasksQuery, sevenDaysAgo)
	var cleanedTasks int64 = 0
	if err == nil {
		cleanedTasks, _ = taskResult.RowsAffected()
	}

	// 2. Clean timed out tasks (processing but no heartbeat for 5+ minutes)
	timeoutQuery := `
		UPDATE tasks 
		SET status = 'failed', 
		    error_message = 'Task timed out - no heartbeat', 
		    completed_at = ?,
		    updated_at = ?
		WHERE status = 'processing' 
		AND (heartbeat_at < ? OR heartbeat_at IS NULL)
	`

	timeoutResult, err := h.db.Exec(timeoutQuery, now, now, fiveMinutesAgo)
	var timedoutTasks int64 = 0
	if err == nil {
		timedoutTasks, _ = timeoutResult.RowsAffected()
	}

	// 3. Clean old rate limit records (older than 7 days)
	rateLimitQuery := `
		DELETE FROM rate_limits 
		WHERE last_request < ?
	`

	rateLimitResult, err := h.db.Exec(rateLimitQuery, sevenDaysAgo)
	var cleanedRateLimits int64 = 0
	if err == nil {
		cleanedRateLimits, _ = rateLimitResult.RowsAffected()
	}

	// 4. Clean old processor metrics (older than 7 days)
	metricsQuery := `
		DELETE FROM processor_metrics 
		WHERE last_updated < ?
	`

	_, _ = h.db.Exec(metricsQuery, sevenDaysAgo)

	cleaned := map[string]interface{}{
		"tasks":      cleanedTasks,
		"timedout":   timedoutTasks,
		"rateLimits": cleanedRateLimits,
	}

	return stats, cleaned, nil
}

func (h *InternalHandlers) getCleanupStats() (map[string]interface{}, error) {
	now := time.Now().UnixMilli()
	sevenDaysAgo := now - (7 * 24 * 60 * 60 * 1000)
	fiveMinutesAgo := now - (5 * 60 * 1000)

	// Get task statistics
	taskStatsQuery := `
		SELECT 
			COUNT(*) as total_tasks,
			SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END) as pending_tasks,
			SUM(CASE WHEN status = 'processing' THEN 1 ELSE 0 END) as processing_tasks,
			SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END) as completed_tasks,
			SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) as failed_tasks,
			SUM(CASE WHEN status IN ('completed', 'failed') AND completed_at < ? THEN 1 ELSE 0 END) as tasks_older_than_7_days,
			SUM(CASE WHEN status = 'processing' AND heartbeat_at < ? THEN 1 ELSE 0 END) as timedout_tasks
		FROM tasks
	`

	var totalTasks, pendingTasks, processingTasks, completedTasks, failedTasks, oldTasks, timedoutTasks int64
	err := h.db.QueryRow(taskStatsQuery, sevenDaysAgo, fiveMinutesAgo).Scan(
		&totalTasks, &pendingTasks, &processingTasks, &completedTasks, &failedTasks, &oldTasks, &timedoutTasks,
	)
	if err != nil {
		return nil, err
	}

	// Get rate limit records count
	rateLimitQuery := `SELECT COUNT(*) FROM rate_limits`
	var rateLimitRecords int64
	h.db.QueryRow(rateLimitQuery).Scan(&rateLimitRecords)

	stats := map[string]interface{}{
		"totalTasks":          totalTasks,
		"pendingTasks":        pendingTasks,
		"processingTasks":     processingTasks,
		"completedTasks":      completedTasks,
		"failedTasks":         failedTasks,
		"tasksOlderThan7Days": oldTasks,
		"timedoutTasks":       timedoutTasks,
		"rateLimitRecords":    rateLimitRecords,
	}

	return stats, nil
}

// POST /api/internal/work-steal - Work stealing for load balancing
func (h *InternalHandlers) WorkSteal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		utils.SendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		ProcessorID   string `json:"processor_id"`
		MaxStealCount *int   `json:"max_steal_count,omitempty"`
		TimeoutMs     *int64 `json:"timeout_ms,omitempty"`
	}

	if err := utils.ParseJSON(r, &req); err != nil {
		utils.SendError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if req.ProcessorID == "" {
		utils.SendError(w, http.StatusBadRequest, "processor_id is required")
		return
	}

	maxStealCount := 2
	if req.MaxStealCount != nil && *req.MaxStealCount > 0 && *req.MaxStealCount <= 5 {
		maxStealCount = *req.MaxStealCount
	}

	timeoutMs := int64(300000)
	if req.TimeoutMs != nil && *req.TimeoutMs > 0 {
		timeoutMs = *req.TimeoutMs
	}

	stolenTasks, err := h.stealTasksFromOverloadedProcessors(req.ProcessorID, maxStealCount, timeoutMs)
	if err != nil {
		utils.SendError(w, http.StatusInternalServerError, "Failed to steal tasks")
		return
	}

	utils.SendJSON(w, http.StatusOK, map[string]interface{}{
		"success":      true,
		"stolen_tasks": stolenTasks,
		"stolen_count": len(stolenTasks),
	})
}

// stealTasksFromOverloadedProcessors implements work-stealing mechanism
func (h *InternalHandlers) stealTasksFromOverloadedProcessors(stealerProcessorID string, maxStealCount int, timeoutMs int64) ([]*database.Task, error) {
	now := time.Now().UnixMilli()
	timeoutAt := now + timeoutMs

	// Find stealable tasks from overloaded processors
	selectQuery := `
		WITH processor_loads AS (
			SELECT 
				processor_id,
				COUNT(*) as active_tasks
			FROM tasks 
			WHERE status = 'processing' 
			AND processor_id IS NOT NULL
			GROUP BY processor_id
			HAVING active_tasks > 5
		)
		SELECT 
			t.id,
			t.user_id,
			t.product_data,
			t.priority,
			t.retry_count,
			t.estimated_duration,
			t.ollama_params
		FROM tasks t
		JOIN processor_loads pl ON t.processor_id = pl.processor_id
		WHERE 
			t.status = 'processing'
			AND t.heartbeat_at < ? 
			AND t.processor_id != ?
		ORDER BY pl.active_tasks DESC, t.priority DESC
		LIMIT ?
	`

	rows, err := h.db.Query(selectQuery, now-60000, stealerProcessorID, maxStealCount)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stealableTasks []*database.Task
	for rows.Next() {
		task := &database.Task{}
		var ollamaParamsJSON sql.NullString

		err := rows.Scan(
			&task.ID, &task.UserID, &task.ProductData, &task.Priority,
			&task.RetryCount, &task.EstimatedDuration, &ollamaParamsJSON,
		)
		if err != nil {
			continue
		}

		if ollamaParamsJSON.Valid && ollamaParamsJSON.String != "" {
			task.OllamaParams = &ollamaParamsJSON.String
		}

		stealableTasks = append(stealableTasks, task)
	}

	if len(stealableTasks) == 0 {
		return []*database.Task{}, nil
	}

	// Update the selected tasks to new processor
	placeholders := make([]string, len(stealableTasks))
	taskIDs := make([]interface{}, len(stealableTasks))

	for i, task := range stealableTasks {
		placeholders[i] = "?"
		taskIDs[i] = task.ID
	}

	updateQuery := fmt.Sprintf(`
		UPDATE tasks 
		SET processor_id = ?,
		    heartbeat_at = ?,
		    timeout_at = ?,
		    updated_at = ?
		WHERE id IN (%s)
	`, strings.Join(placeholders, ","))

	args := append([]interface{}{stealerProcessorID, now, timeoutAt, now}, taskIDs...)
	_, err = h.db.Exec(updateQuery, args...)
	if err != nil {
		return nil, err
	}

	// Update task objects
	for _, task := range stealableTasks {
		task.ProcessorID = &stealerProcessorID
		task.HeartbeatAt = &now
		task.TimeoutAt = &timeoutAt
		task.UpdatedAt = now
	}

	return stealableTasks, nil
}

// GET /api/internal/metrics - Get processor metrics
func (h *InternalHandlers) ProcessorMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		utils.SendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	metrics, err := h.getProcessorLoadMetrics()
	if err != nil {
		utils.SendError(w, http.StatusInternalServerError, "Failed to get processor metrics")
		return
	}

	utils.SendJSON(w, http.StatusOK, map[string]interface{}{
		"success":    true,
		"processors": metrics,
	})
}

// GET /api/internal/estimated-time - Get estimated wait time for new tasks
func (h *InternalHandlers) EstimatedTime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		utils.SendError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	estimatedTime, err := h.calculateEstimatedWaitTime()
	if err != nil {
		utils.SendError(w, http.StatusInternalServerError, "Failed to calculate estimated time")
		return
	}

	utils.SendJSON(w, http.StatusOK, map[string]interface{}{
		"success":        true,
		"estimated_time": estimatedTime,
	})
}

// getProcessorLoadMetrics returns metrics for intelligent task distribution
func (h *InternalHandlers) getProcessorLoadMetrics() ([]map[string]interface{}, error) {
	query := `
		SELECT 
			pm.processor_id,
			pm.cpu_usage,
			pm.memory_usage,
			pm.queue_size,
			pm.last_updated,
			COUNT(t.id) as active_tasks,
			COALESCE(AVG((julianday('now') - julianday(t.processing_started_at / 86400000, 'unixepoch')) * 86400), 0) as avg_processing_time
		FROM processor_metrics pm
		LEFT JOIN tasks t ON pm.processor_id = t.processor_id AND t.status = 'processing'
		WHERE pm.last_updated > ? - 300000
		GROUP BY pm.processor_id
		ORDER BY 
			(pm.cpu_usage * 0.3 + pm.memory_usage * 0.3 + COUNT(t.id) * 0.4) ASC
	`

	now := time.Now().UnixMilli()
	rows, err := h.db.Query(query, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	metrics := make([]map[string]interface{}, 0)
	for rows.Next() {
		var processorID string
		var cpuUsage, memoryUsage sql.NullFloat64
		var queueSize sql.NullInt64
		var lastUpdated int64
		var activeTasks int
		var avgProcessingTime float64

		err := rows.Scan(
			&processorID, &cpuUsage, &memoryUsage, &queueSize,
			&lastUpdated, &activeTasks, &avgProcessingTime,
		)
		if err != nil {
			continue
		}

		metric := map[string]interface{}{
			"processor_id":        processorID,
			"cpu_usage":           cpuUsage.Float64,
			"memory_usage":        memoryUsage.Float64,
			"queue_size":          queueSize.Int64,
			"last_updated":        lastUpdated,
			"active_tasks":        activeTasks,
			"avg_processing_time": avgProcessingTime,
		}
		metrics = append(metrics, metric)
	}

	return metrics, nil
}

// calculateEstimatedWaitTime calculates wait time for new tasks
func (h *InternalHandlers) calculateEstimatedWaitTime() (string, error) {
	now := time.Now().UnixMilli()

	// Get active processors and their metrics
	processorsQuery := `
		SELECT 
			pm.processor_id,
			COALESCE(pm.cpu_usage, 0) as cpu_usage,
			COALESCE(pm.memory_usage, 0) as memory_usage,
			COALESCE(pm.queue_size, 0) as queue_size,
			pm.last_updated,
			COUNT(t.id) as active_tasks
		FROM processor_metrics pm
		LEFT JOIN tasks t ON pm.processor_id = t.processor_id AND t.status = 'processing'
		WHERE pm.last_updated > ? - 300000
		GROUP BY pm.processor_id
	`

	rows, err := h.db.Query(processorsQuery, now)
	if err != nil {
		return "Unable to estimate", err
	}
	defer rows.Close()

	var activeProcessors []map[string]interface{}
	for rows.Next() {
		var processorID string
		var cpuUsage, memoryUsage, queueSize float64
		var lastUpdated int64
		var activeTasks int

		err := rows.Scan(&processorID, &cpuUsage, &memoryUsage, &queueSize, &lastUpdated, &activeTasks)
		if err != nil {
			continue
		}

		activeProcessors = append(activeProcessors, map[string]interface{}{
			"processor_id": processorID,
			"cpu_usage":    cpuUsage,
			"memory_usage": memoryUsage,
			"queue_size":   queueSize,
			"active_tasks": activeTasks,
		})
	}

	// Get pending tasks count
	var pendingTasksCount int
	err = h.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE status = 'pending'").Scan(&pendingTasksCount)
	if err != nil {
		pendingTasksCount = 0
	}

	// Get average processing time from completed tasks (last 24 hours)
	avgTimeQuery := `
		SELECT 
			COALESCE(AVG(completed_at - processing_started_at), 45000) as avg_processing_time,
			COUNT(*) as completed_count
		FROM tasks 
		WHERE status = 'completed' 
			AND completed_at > ? - 86400000
			AND processing_started_at IS NOT NULL
			AND completed_at IS NOT NULL
	`

	var avgProcessingTime float64
	var completedCount int
	err = h.db.QueryRow(avgTimeQuery, now).Scan(&avgProcessingTime, &completedCount)
	if err != nil {
		avgProcessingTime = 45000 // Default 45 seconds
	}

	// If no active processors, return high estimate
	if len(activeProcessors) == 0 {
		return "10-15 minutes (no active processors)", nil
	}

	// Calculate total processing capacity
	totalCapacity := 0.0
	for _, processor := range activeProcessors {
		cpuUsage := processor["cpu_usage"].(float64)
		memoryUsage := processor["memory_usage"].(float64)
		activeTasks := float64(processor["active_tasks"].(int))

		loadFactor := (cpuUsage*0.3 + memoryUsage*0.3 + activeTasks*0.4) / 100
		capacityFactor := math.Max(0.1, 1-loadFactor) // Minimum 10% capacity
		totalCapacity += capacityFactor
	}

	// Calculate queue position (assuming fair distribution)
	queuePosition := math.Ceil(float64(pendingTasksCount) / math.Max(1, totalCapacity))

	// Calculate estimated wait time
	estimatedWaitMs := (queuePosition * avgProcessingTime) + (avgProcessingTime * 0.5) // Add buffer

	// Convert to human-readable format
	waitTimeMinutes := math.Ceil(estimatedWaitMs / 60000)

	switch {
	case waitTimeMinutes < 1:
		return "< 1 minute", nil
	case waitTimeMinutes <= 2:
		return "1-2 minutes", nil
	case waitTimeMinutes <= 5:
		return "2-5 minutes", nil
	case waitTimeMinutes <= 10:
		return "5-10 minutes", nil
	case waitTimeMinutes <= 15:
		return "10-15 minutes", nil
	default:
		return fmt.Sprintf("%.0f minutes", waitTimeMinutes), nil
	}
}
