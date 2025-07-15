package database

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Task operations

// retryOnBusy executes a function with retry logic for SQLite BUSY errors
func retryOnBusy(maxRetries int, fn func() error) error {
	var err error
	for i := 0; i < maxRetries; i++ {
		err = fn()
		if err == nil {
			return nil
		}

		// Check if it's a SQLite BUSY error and retry
		if i < maxRetries-1 {
			errStr := strings.ToLower(err.Error())
			isBusyError := strings.Contains(errStr, "database is locked") ||
				strings.Contains(errStr, "database table is locked") ||
				strings.Contains(errStr, "busy") ||
				strings.Contains(errStr, "sqlite_busy") ||
				strings.Contains(errStr, "locked")

			if isBusyError {
				// Exponential backoff with jitter for BUSY errors
				baseDelay := time.Duration(i+1) * 100 * time.Millisecond
				jitter := time.Duration(i*50) * time.Millisecond
				time.Sleep(baseDelay + jitter)
				continue
			}
			// For other errors, also retry but with less delay
			time.Sleep(time.Duration(i+1) * 20 * time.Millisecond)
		}
	}
	return err
}

func (db *DB) CreateTask(task *Task) error {
	return retryOnBusy(3, func() error { // Reduced retries since we have queue now
		// Check if user already has an active task
		hasActiveTask, err := db.CheckUserActiveTask(task.UserID)
		if err != nil {
			return err
		}

		if hasActiveTask {
			return fmt.Errorf("user already has an active task")
		}

		query := `
			INSERT INTO tasks (
				id, user_id, product_data, status, created_at, updated_at, 
				priority, max_retries, estimated_duration, ollama_params
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`

		now := time.Now().UnixMilli()
		ollamaParamsJSON := ""
		if task.OllamaParams != nil {
			ollamaParamsJSON = *task.OllamaParams
		}

		_, err = db.QueuedExec(query,
			task.ID, task.UserID, task.ProductData, task.Status,
			now, now, task.Priority, task.MaxRetries,
			task.EstimatedDuration, ollamaParamsJSON,
		)
		return err
	})
}

func (db *DB) GetTask(id string) (*Task, error) {
	var task Task
	var ollamaParamsJSON sql.NullString
	var completedAt, processingStartedAt, heartbeatAt, timeoutAt sql.NullInt64
	var result, errorMessage, processorID, userRating sql.NullString
	var actualDuration sql.NullInt64

	err := retryOnBusy(3, func() error {
		query := `
			SELECT id, user_id, product_data, status, result, error_message,
				   created_at, updated_at, completed_at, priority, retry_count,
				   max_retries, processor_id, processing_started_at, heartbeat_at,
				   timeout_at, ollama_params, estimated_duration, actual_duration, user_rating
			FROM tasks WHERE id = ?
		`

		return db.QueuedQueryRow(query, id).Scan(
			&task.ID, &task.UserID, &task.ProductData, &task.Status,
			&result, &errorMessage, &task.CreatedAt, &task.UpdatedAt,
			&completedAt, &task.Priority, &task.RetryCount, &task.MaxRetries,
			&processorID, &processingStartedAt, &heartbeatAt, &timeoutAt,
			&ollamaParamsJSON, &task.EstimatedDuration, &actualDuration, &userRating,
		)
	})

	if err != nil {
		return nil, err
	}

	// Handle nullable fields
	if result.Valid {
		task.Result = &result.String
	}
	if errorMessage.Valid {
		task.ErrorMessage = &errorMessage.String
	}
	if processorID.Valid {
		task.ProcessorID = &processorID.String
	}
	if completedAt.Valid {
		task.CompletedAt = &completedAt.Int64
	}
	if processingStartedAt.Valid {
		task.ProcessingStartedAt = &processingStartedAt.Int64
	}
	if heartbeatAt.Valid {
		task.HeartbeatAt = &heartbeatAt.Int64
	}
	if timeoutAt.Valid {
		task.TimeoutAt = &timeoutAt.Int64
	}
	if actualDuration.Valid {
		task.ActualDuration = &actualDuration.Int64
	}
	if userRating.Valid {
		task.UserRating = &userRating.String
	}

	// Parse ollama params
	if ollamaParamsJSON.Valid && ollamaParamsJSON.String != "" {
		task.OllamaParams = &ollamaParamsJSON.String
	}

	return &task, nil
}

func (db *DB) UpdateTaskStatus(id, status string, result, errorMessage *string) error {
	return retryOnBusy(3, func() error {
		query := `
			UPDATE tasks 
			SET status = ?, updated_at = ?, result = ?, error_message = ?,
				completed_at = CASE WHEN ? IN ('completed', 'failed') THEN ? ELSE completed_at END
			WHERE id = ?
		`

		now := time.Now().UnixMilli()
		_, err := db.QueuedExecWithWriteLock(query, status, now, result, errorMessage, status, now, id)
		return err
	})
}

func (db *DB) GetPendingTasks(limit int) ([]*Task, error) {
	// log.Printf("Fetching up to %d pending tasks", limit)

	query := `
		SELECT id, user_id, product_data, status, created_at, updated_at,
			   priority, max_retries, estimated_duration, ollama_params, error_message
		FROM tasks 
		WHERE status = 'pending' 
		ORDER BY priority DESC, created_at ASC 
		LIMIT ?
	`

	rows, err := db.QueuedQuery(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		var task Task
		var ollamaParamsJSON sql.NullString

		err := rows.Scan(
			&task.ID, &task.UserID, &task.ProductData, &task.Status,
			&task.CreatedAt, &task.UpdatedAt, &task.Priority, &task.MaxRetries,
			&task.EstimatedDuration, &ollamaParamsJSON, &task.ErrorMessage,
		)
		if err != nil {
			return nil, err
		}

		// Parse ollama params
		if ollamaParamsJSON.Valid && ollamaParamsJSON.String != "" {
			task.OllamaParams = &ollamaParamsJSON.String
		}

		tasks = append(tasks, &task)
	}

	// log.Printf("Fetched %d pending tasks", len(tasks))

	return tasks, rows.Err()
}

func (db *DB) GetAllTasks(userID *string, limit, offset int) ([]*Task, error) {
	var query string
	var args []interface{}

	if userID != nil {
		query = `
			SELECT id, user_id, product_data, status, result, error_message,
				   created_at, updated_at, completed_at, priority, retry_count,
				   max_retries, processor_id, processing_started_at, heartbeat_at,
				   timeout_at, ollama_params, estimated_duration, actual_duration, user_rating
			FROM tasks 
			WHERE user_id = ?
			ORDER BY created_at DESC 
			LIMIT ? OFFSET ?
		`
		args = []interface{}{*userID, limit, offset}
	} else {
		query = `
			SELECT id, user_id, product_data, status, result, error_message,
				   created_at, updated_at, completed_at, priority, retry_count,
				   max_retries, processor_id, processing_started_at, heartbeat_at,
				   timeout_at, ollama_params, estimated_duration, actual_duration, user_rating
			FROM tasks 
			ORDER BY created_at DESC 
			LIMIT ? OFFSET ?
		`
		args = []interface{}{limit, offset}
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}

	return tasks, rows.Err()
}

// Rate limiting operations

func (db *DB) CheckRateLimit(userID string, windowMs int64, maxRequests int) (*RateLimit, error) {
	var rl RateLimit

	err := retryOnBusy(5, func() error { // Reduced retries since we have queue now
		now := time.Now().UnixMilli()
		windowStart := now - windowMs

		// Try to get existing rate limit
		query := `
			SELECT user_id, request_count, window_start, last_request
			FROM rate_limits 
			WHERE user_id = ?
		`

		err := db.QueuedQueryRow(query, userID).Scan(
			&rl.UserID, &rl.RequestCount, &rl.WindowStart, &rl.LastRequest,
		)

		if err == sql.ErrNoRows {
			// First request for this user - use UPSERT
			rl = RateLimit{
				UserID:       userID,
				RequestCount: 1,
				WindowStart:  now,
				LastRequest:  now,
			}
		} else if err != nil {
			return fmt.Errorf("failed to query rate limit for user %s: %w", userID, err)
		} else {
			// Existing user - check if window has expired
			if rl.WindowStart < windowStart {
				// Reset window
				rl.RequestCount = 1
				rl.WindowStart = now
				rl.LastRequest = now
			} else {
				// Increment count
				rl.RequestCount++
				rl.LastRequest = now
			}
		}

		// Use UPSERT to handle concurrent requests safely - with write lock for safety
		upsertQuery := `
			INSERT INTO rate_limits (user_id, request_count, window_start, last_request)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(user_id) DO UPDATE SET
				request_count = excluded.request_count,
				window_start = excluded.window_start,
				last_request = excluded.last_request
		`
		_, err = db.QueuedExecWithWriteLock(upsertQuery, rl.UserID, rl.RequestCount, rl.WindowStart, rl.LastRequest)
		if err != nil {
			return fmt.Errorf("failed to upsert rate limit for user %s: %w", userID, err)
		}

		return nil
	})

	return &rl, err
}

// CheckUserActiveTask checks if user has any active (pending or processing) tasks
func (db *DB) CheckUserActiveTask(userID string) (bool, error) {
	var count int
	err := retryOnBusy(3, func() error {
		query := `
			SELECT COUNT(*) 
			FROM tasks 
			WHERE user_id = ? AND status IN ('pending', 'processing')
		`

		return db.QueuedQueryRow(query, userID).Scan(&count)
	})

	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// GetUserLatestTask gets the latest task for a user (most recent by created_at)
func (db *DB) GetUserLatestTask(userID string) (*Task, error) {
	var task Task
	var ollamaParamsJSON sql.NullString
	var completedAt, processingStartedAt, heartbeatAt, timeoutAt sql.NullInt64
	var result, errorMessage, processorID, userRating sql.NullString
	var actualDuration sql.NullInt64

	err := retryOnBusy(3, func() error {
		query := `
			SELECT id, user_id, product_data, status, result, error_message,
				   created_at, updated_at, completed_at, priority, retry_count,
				   max_retries, processor_id, processing_started_at, heartbeat_at,
				   timeout_at, ollama_params, estimated_duration, actual_duration, user_rating
			FROM tasks 
			WHERE user_id = ? 
			ORDER BY created_at DESC 
			LIMIT 1
		`

		return db.QueuedQueryRow(query, userID).Scan(
			&task.ID, &task.UserID, &task.ProductData, &task.Status,
			&result, &errorMessage, &task.CreatedAt, &task.UpdatedAt,
			&completedAt, &task.Priority, &task.RetryCount, &task.MaxRetries,
			&processorID, &processingStartedAt, &heartbeatAt, &timeoutAt,
			&ollamaParamsJSON, &task.EstimatedDuration, &actualDuration, &userRating,
		)
	})

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // No tasks found for user
		}
		return nil, err
	}

	// Handle nullable fields
	if result.Valid {
		task.Result = &result.String
	}
	if errorMessage.Valid {
		task.ErrorMessage = &errorMessage.String
	}
	if processorID.Valid {
		task.ProcessorID = &processorID.String
	}
	if completedAt.Valid {
		task.CompletedAt = &completedAt.Int64
	}
	if processingStartedAt.Valid {
		task.ProcessingStartedAt = &processingStartedAt.Int64
	}
	if heartbeatAt.Valid {
		task.HeartbeatAt = &heartbeatAt.Int64
	}
	if timeoutAt.Valid {
		task.TimeoutAt = &timeoutAt.Int64
	}
	if actualDuration.Valid {
		task.ActualDuration = &actualDuration.Int64
	}
	if userRating.Valid {
		task.UserRating = &userRating.String
	}

	// Parse ollama params
	if ollamaParamsJSON.Valid && ollamaParamsJSON.String != "" {
		task.OllamaParams = &ollamaParamsJSON.String
	}

	return &task, nil
}

// GetUserRateLimit gets current rate limit data for a user
func (db *DB) GetUserRateLimit(userID string) (*RateLimit, error) {
	var rl RateLimit

	err := retryOnBusy(3, func() error {
		query := `
			SELECT user_id, request_count, window_start, last_request
			FROM rate_limits 
			WHERE user_id = ?
		`

		return db.QueuedQueryRow(query, userID).Scan(
			&rl.UserID, &rl.RequestCount, &rl.WindowStart, &rl.LastRequest,
		)
	})

	if err != nil {
		if err == sql.ErrNoRows {
			// Return empty rate limit if not found
			return &RateLimit{
				UserID:       userID,
				RequestCount: 0,
				WindowStart:  0,
				LastRequest:  0,
			}, nil
		}
		return nil, err
	}

	return &rl, nil
}

// Helper function to scan task from rows
func scanTask(rows *sql.Rows) (*Task, error) {
	var task Task
	var ollamaParamsJSON sql.NullString
	var completedAt, processingStartedAt, heartbeatAt, timeoutAt sql.NullInt64
	var result, errorMessage, processorID, userRating sql.NullString
	var actualDuration sql.NullInt64

	err := rows.Scan(
		&task.ID, &task.UserID, &task.ProductData, &task.Status,
		&result, &errorMessage, &task.CreatedAt, &task.UpdatedAt,
		&completedAt, &task.Priority, &task.RetryCount, &task.MaxRetries,
		&processorID, &processingStartedAt, &heartbeatAt, &timeoutAt,
		&ollamaParamsJSON, &task.EstimatedDuration, &actualDuration, &userRating,
	)

	if err != nil {
		return nil, err
	}

	// Handle nullable fields
	if result.Valid {
		task.Result = &result.String
	}
	if errorMessage.Valid {
		task.ErrorMessage = &errorMessage.String
	}
	if processorID.Valid {
		task.ProcessorID = &processorID.String
	}
	if completedAt.Valid {
		task.CompletedAt = &completedAt.Int64
	}
	if processingStartedAt.Valid {
		task.ProcessingStartedAt = &processingStartedAt.Int64
	}
	if heartbeatAt.Valid {
		task.HeartbeatAt = &heartbeatAt.Int64
	}
	if timeoutAt.Valid {
		task.TimeoutAt = &timeoutAt.Int64
	}
	if actualDuration.Valid {
		task.ActualDuration = &actualDuration.Int64
	}
	if userRating.Valid {
		task.UserRating = &userRating.String
	}

	// Parse ollama params
	if ollamaParamsJSON.Valid && ollamaParamsJSON.String != "" {
		task.OllamaParams = &ollamaParamsJSON.String
	}

	return &task, nil
}

func (db *DB) RequeueTask(taskID, processorID string, reason *string) error {
	return retryOnBusy(3, func() error {
		var query string
		var args []interface{}
		if reason != nil {
			query = `
				UPDATE tasks
				SET status = 'pending',
					processor_id = NULL,
					heartbeat_at = NULL,
					processing_started_at = NULL,
					timeout_at = NULL,
					retry_count = retry_count + 1,
					error_message = ?
				WHERE id = ? AND processor_id = ? AND status = 'processing'
			`
			args = []interface{}{*reason, taskID, processorID}
		} else {
			query = `
				UPDATE tasks
				SET status = 'pending',
					processor_id = NULL,
					heartbeat_at = NULL,
					processing_started_at = NULL,
					timeout_at = NULL,
					retry_count = retry_count + 1
				WHERE id = ? AND processor_id = ? AND status = 'processing'
			`
			args = []interface{}{taskID, processorID}
		}
		_, err := db.QueuedExecWithWriteLock(query, args...)
		return err
	})
}

// UpdateTaskRating updates the rating for a task
func (db *DB) UpdateTaskRating(taskID, userID string, rating *string) error {
	return retryOnBusy(3, func() error {
		// First, check if task exists, belongs to user, and is completed
		var task Task
		query := `
			SELECT id, user_id, status 
			FROM tasks 
			WHERE id = ? AND user_id = ?
		`

		err := db.QueuedQueryRow(query, taskID, userID).Scan(
			&task.ID, &task.UserID, &task.Status,
		)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("task not found or not owned by user")
			}
			return fmt.Errorf("failed to get task: %w", err)
		}

		// Check if task is completed
		if task.Status != "completed" {
			return fmt.Errorf("task must be completed to rate it")
		}

		// Update the rating
		updateQuery := `
			UPDATE tasks 
			SET user_rating = ?, updated_at = ?
			WHERE id = ? AND user_id = ?
		`

		now := time.Now().UnixMilli()
		_, err = db.QueuedExec(updateQuery, rating, now, taskID, userID)
		if err != nil {
			return fmt.Errorf("failed to update task rating: %w", err)
		}

		return nil
	})
}

// GetTasksRatingStats gets rating statistics for tasks
func (db *DB) GetTasksRatingStats(userID *string) (map[string]int, error) {
	var query string
	var args []interface{}

	if userID != nil {
		query = `
			SELECT user_rating, COUNT(*) as count 
			FROM tasks 
			WHERE user_id = ? AND user_rating IS NOT NULL
			GROUP BY user_rating
		`
		args = []interface{}{*userID}
	} else {
		query = `
			SELECT user_rating, COUNT(*) as count 
			FROM tasks 
			WHERE user_rating IS NOT NULL
			GROUP BY user_rating
		`
		args = []interface{}{}
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make(map[string]int)
	for rows.Next() {
		var rating string
		var count int
		if err := rows.Scan(&rating, &count); err != nil {
			return nil, err
		}
		stats[rating] = count
	}

	return stats, rows.Err()
}

// GetUserRatedTasks gets tasks with user ratings
func (db *DB) GetUserRatedTasks(userID string, rating *string, limit, offset int) ([]*Task, error) {
	var query string
	var args []interface{}

	if rating != nil {
		query = `
			SELECT id, user_id, product_data, status, result, error_message,
				   created_at, updated_at, completed_at, priority, retry_count,
				   max_retries, processor_id, processing_started_at, heartbeat_at,
				   timeout_at, ollama_params, estimated_duration, actual_duration, user_rating
			FROM tasks 
			WHERE user_id = ? AND user_rating = ?
			ORDER BY created_at DESC 
			LIMIT ? OFFSET ?
		`
		args = []interface{}{userID, *rating, limit, offset}
	} else {
		query = `
			SELECT id, user_id, product_data, status, result, error_message,
				   created_at, updated_at, completed_at, priority, retry_count,
				   max_retries, processor_id, processing_started_at, heartbeat_at,
				   timeout_at, ollama_params, estimated_duration, actual_duration, user_rating
			FROM tasks 
			WHERE user_id = ? AND user_rating IS NOT NULL
			ORDER BY created_at DESC 
			LIMIT ? OFFSET ?
		`
		args = []interface{}{userID, limit, offset}
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}

	return tasks, rows.Err()
}

// GetRatingStatsByPeriod gets rating statistics grouped by time period
func (db *DB) GetRatingStatsByPeriod(period string, count int) ([]map[string]interface{}, error) {
	var results []map[string]interface{}

	// Define time format and grouping based on period
	var timeFormat, groupBy string
	switch period {
	case "hour":
		timeFormat = "%Y-%m-%d %H"
		groupBy = "datetime(completed_at/1000, 'unixepoch', 'localtime', 'start of hour')"
	case "day":
		timeFormat = "%Y-%m-%d"
		groupBy = "date(completed_at/1000, 'unixepoch', 'localtime')"
	default:
		return results, fmt.Errorf("unsupported period: %s", period)
	}

	query := fmt.Sprintf(`
		WITH periods AS (
			SELECT %s as period_start,
				   strftime('%s', %s) as period_label,
				   SUM(CASE WHEN user_rating = 'upvote' THEN 1 ELSE 0 END) as upvotes,
				   SUM(CASE WHEN user_rating = 'downvote' THEN 1 ELSE 0 END) as downvotes,
				   COUNT(CASE WHEN user_rating IS NOT NULL THEN 1 END) as total_rated
			FROM tasks 
			WHERE status = 'completed' 
			  AND completed_at IS NOT NULL
			  AND %s >= datetime('now', '-%d %s', 'localtime')
			GROUP BY %s
			ORDER BY period_start DESC
			LIMIT ?
		)
		SELECT period_label, upvotes, downvotes, total_rated
		FROM periods 
		ORDER BY period_start ASC
	`, groupBy, timeFormat, groupBy, groupBy, count, period, groupBy)

	rows, err := db.Query(query, count)
	if err != nil {
		return nil, fmt.Errorf("failed to get rating stats by period: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var periodLabel string
		var upvotes, downvotes, totalRated int

		err := rows.Scan(&periodLabel, &upvotes, &downvotes, &totalRated)
		if err != nil {
			return nil, fmt.Errorf("failed to scan rating stats: %w", err)
		}

		var qualityScore float64
		if totalRated > 0 {
			qualityScore = float64(upvotes-downvotes) / float64(totalRated) * 100
		}

		results = append(results, map[string]interface{}{
			"period":        periodLabel,
			"upvotes":       upvotes,
			"downvotes":     downvotes,
			"total_rated":   totalRated,
			"quality_score": qualityScore,
		})
	}

	return results, rows.Err()
}

// GetRecentRatedTasks gets the most recently rated tasks
func (db *DB) GetRecentRatedTasks(limit int) ([]*Task, error) {
	query := `
		SELECT id, user_id, product_data, status, result, error_message,
			   created_at, updated_at, completed_at, priority, retry_count,
			   max_retries, processor_id, processing_started_at, heartbeat_at,
			   timeout_at, ollama_params, estimated_duration, actual_duration, user_rating
		FROM tasks 
		WHERE user_rating IS NOT NULL AND status = 'completed'
		ORDER BY updated_at DESC 
		LIMIT ?
	`

	rows, err := db.Query(query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent rated tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}
		tasks = append(tasks, task)
	}

	return tasks, rows.Err()
}
