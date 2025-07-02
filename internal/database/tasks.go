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
	return retryOnBusy(5, func() error { // Добавляем retry для создания задач
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

		_, err = db.Exec(query,
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
	var result, errorMessage, processorID sql.NullString
	var actualDuration sql.NullInt64

	err := retryOnBusy(3, func() error {
		query := `
			SELECT id, user_id, product_data, status, result, error_message,
				   created_at, updated_at, completed_at, priority, retry_count,
				   max_retries, processor_id, processing_started_at, heartbeat_at,
				   timeout_at, ollama_params, estimated_duration, actual_duration
			FROM tasks WHERE id = ?
		`

		return db.QueryRow(query, id).Scan(
			&task.ID, &task.UserID, &task.ProductData, &task.Status,
			&result, &errorMessage, &task.CreatedAt, &task.UpdatedAt,
			&completedAt, &task.Priority, &task.RetryCount, &task.MaxRetries,
			&processorID, &processingStartedAt, &heartbeatAt, &timeoutAt,
			&ollamaParamsJSON, &task.EstimatedDuration, &actualDuration,
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

	// Parse ollama params
	if ollamaParamsJSON.Valid && ollamaParamsJSON.String != "" {
		task.OllamaParams = &ollamaParamsJSON.String
	}

	return &task, nil
}

func (db *DB) UpdateTaskStatus(id, status string, result, errorMessage *string) error {
	return retryOnBusy(5, func() error {
		query := `
			UPDATE tasks 
			SET status = ?, updated_at = ?, result = ?, error_message = ?,
				completed_at = CASE WHEN ? IN ('completed', 'failed') THEN ? ELSE completed_at END
			WHERE id = ?
		`

		now := time.Now().UnixMilli()
		_, err := db.Exec(query, status, now, result, errorMessage, status, now, id)
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

	rows, err := db.Query(query, limit)
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
				   timeout_at, ollama_params, estimated_duration, actual_duration
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
				   timeout_at, ollama_params, estimated_duration, actual_duration
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

	err := retryOnBusy(10, func() error { // Увеличиваем до 10 попыток для rate limit
		now := time.Now().UnixMilli()
		windowStart := now - windowMs

		// Try to get existing rate limit
		query := `
			SELECT user_id, request_count, window_start, last_request
			FROM rate_limits 
			WHERE user_id = ?
		`

		err := db.QueryRow(query, userID).Scan(
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

		// Use UPSERT to handle concurrent requests safely
		upsertQuery := `
			INSERT INTO rate_limits (user_id, request_count, window_start, last_request)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(user_id) DO UPDATE SET
				request_count = excluded.request_count,
				window_start = excluded.window_start,
				last_request = excluded.last_request
		`
		_, err = db.Exec(upsertQuery, rl.UserID, rl.RequestCount, rl.WindowStart, rl.LastRequest)
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

		return db.QueryRow(query, userID).Scan(&count)
	})

	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// Helper function to scan task from rows
func scanTask(rows *sql.Rows) (*Task, error) {
	var task Task
	var ollamaParamsJSON sql.NullString
	var completedAt, processingStartedAt, heartbeatAt, timeoutAt sql.NullInt64
	var result, errorMessage, processorID sql.NullString
	var actualDuration sql.NullInt64

	err := rows.Scan(
		&task.ID, &task.UserID, &task.ProductData, &task.Status,
		&result, &errorMessage, &task.CreatedAt, &task.UpdatedAt,
		&completedAt, &task.Priority, &task.RetryCount, &task.MaxRetries,
		&processorID, &processingStartedAt, &heartbeatAt, &timeoutAt,
		&ollamaParamsJSON, &task.EstimatedDuration, &actualDuration,
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
		_, err := db.Exec(query, args...)
		return err
	})
}
