package handlers_test

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ad/go-llm-manager/internal/api/handlers"
	"github.com/ad/go-llm-manager/internal/auth"
	"github.com/ad/go-llm-manager/internal/database"
)

func setupTestDB(t *testing.T) *database.DB {
	db, err := database.NewSQLiteDB("file:memdb1?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}

	// Полная схема для всех полей, которые читает GetTask
	_, err = db.Exec(`CREATE TABLE tasks (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		product_data TEXT NOT NULL,
		status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
		result TEXT,
		error_message TEXT,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		completed_at INTEGER,
		priority INTEGER DEFAULT 0,
		retry_count INTEGER DEFAULT 0,
		max_retries INTEGER DEFAULT 3,
		processor_id TEXT,
		processing_started_at INTEGER,
		heartbeat_at INTEGER,
		timeout_at INTEGER,
		ollama_params TEXT,
		estimated_duration INTEGER DEFAULT 300000, -- 5 minutes default
		actual_duration INTEGER
	);
	
	CREATE TABLE rate_limits (
		user_id TEXT PRIMARY KEY,
		request_count INTEGER NOT NULL DEFAULT 0,
		window_start INTEGER NOT NULL,
		last_request INTEGER NOT NULL
	);

	CREATE TABLE processor_metrics (
		processor_id TEXT PRIMARY KEY,
		cpu_usage REAL NOT NULL DEFAULT 0.0,
		memory_usage REAL NOT NULL DEFAULT 0.0,
		queue_size INTEGER NOT NULL DEFAULT 0,
		active_tasks INTEGER NOT NULL DEFAULT 0,
		last_updated INTEGER NOT NULL,
		created_at INTEGER NOT NULL DEFAULT (unixepoch() * 1000)
	);`)
	if err != nil {
		t.Fatalf("failed to create initial db tables: %v", err)
	}

	return db
}

func TestResultPolling_CompletedTask_Integration(t *testing.T) {
	db := setupTestDB(t)
	secret := "testsecret"
	jwtAuth := auth.NewJWTAuth(secret)

	userID := uuid.New().String()
	payload := &database.JWTPayload{UserID: userID, Issuer: "test", Subject: userID, ExpiresAt: time.Now().Add(time.Hour).Unix(), ProductData: "integration-data"}
	token, err := jwtAuth.GenerateToken(payload, 3600)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	hCreate := handlers.NewPublicHandlers(db, jwtAuth)
	internalHandlers := handlers.NewInternalHandlers(db, jwtAuth)
	hSSE := handlers.NewSSEHandlers(db, jwtAuth)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/create", func(w http.ResponseWriter, r *http.Request) {
		hCreate.CreateTask(w, r)
	})
	mux.HandleFunc("/api/internal/claim", func(w http.ResponseWriter, r *http.Request) {
		internalHandlers.ClaimTasks(w, r)
	})
	mux.HandleFunc("/api/internal/complete", func(w http.ResponseWriter, r *http.Request) {
		internalHandlers.CompleteTasks(w, r)
	})
	mux.HandleFunc("/api/result-polling", func(w http.ResponseWriter, r *http.Request) {
		hSSE.ResultPolling(w, r)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Создаём задачу через реальный сервер
	req, _ := http.NewRequest("POST", ts.URL+"/api/create", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create task request failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create task failed: %s", string(body))
	}
	var createResp struct {
		Success bool   `json:"success"`
		TaskID  string `json:"taskId"`
		Token   string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&createResp)
	resp.Body.Close()
	if !createResp.Success || createResp.TaskID == "" {
		t.Fatalf("create task response invalid: %+v", createResp)
	}

	// Запускаем SSE polling в отдельной горутине с реальным http клиентом
	done := make(chan string, 1)
	go func() {
		reqPoll, _ := http.NewRequest("GET", ts.URL+"/api/result-polling?token="+createResp.Token, nil)
		respPoll, err := http.DefaultClient.Do(reqPoll)
		if err != nil {
			done <- "error: " + err.Error()
			return
		}
		defer respPoll.Body.Close()
		scanner := bufio.NewScanner(respPoll.Body)
		for scanner.Scan() {
			line := scanner.Text()
			// t.Logf("SSE line: %s", line)
			if strings.HasPrefix(line, "data: ") && strings.Contains(line, "integration-ok") {
				done <- line
				return
			}
		}
		done <- "not found"
	}()

	// time.Sleep(5 * time.Second)

	// Имитация claim и complete
	claimReqBody := map[string]interface{}{
		"processor_id": "test-processor",
		"batch_size":   1,
	}
	claimBody, _ := json.Marshal(claimReqBody)
	claimReq, _ := http.NewRequest("POST", ts.URL+"/api/internal/claim", bytes.NewReader(claimBody))
	claimReq.Header.Set("Authorization", "Bearer "+token)
	claimResp, err := http.DefaultClient.Do(claimReq)
	if err != nil {
		t.Fatalf("claim tasks failed: %v", err)
	}
	claimResp.Body.Close()

	// time.Sleep(5 * time.Second)

	completeReqBody := map[string]interface{}{
		"taskId": createResp.TaskID,
		"result": "integration-ok",
		"status": "completed",
	}
	completeBody, _ := json.Marshal(completeReqBody)
	completeReq, _ := http.NewRequest("POST", ts.URL+"/api/internal/complete", bytes.NewReader(completeBody))
	completeReq.Header.Set("Authorization", "Bearer "+token)
	completeResp, err := http.DefaultClient.Do(completeReq)
	if err != nil {
		t.Fatalf("complete task failed: %v", err)
	}
	completeResp.Body.Close()

	// Ждём SSE polling
	select {
	case sseLine := <-done:
		if !strings.Contains(sseLine, "integration-ok") {
			t.Errorf("ожидался результат задачи в SSE, got: %s", sseLine)
		}
	case <-time.After(5 * time.Second):
		t.Error("SSE polling timeout")
	}
}

func GetTask(db *database.DB, id string) (*database.Task, error) {
	query := `
		SELECT id, user_id, product_data, status, result, error_message,
			   created_at, updated_at, completed_at, priority, retry_count,
			   max_retries, processor_id, processing_started_at, heartbeat_at,
			   timeout_at, ollama_params, estimated_duration, actual_duration
		FROM tasks WHERE id = ?
	`

	var task database.Task
	var ollamaParamsJSON sql.NullString
	var completedAt, processingStartedAt, heartbeatAt, timeoutAt sql.NullInt64
	var result, errorMessage, processorID sql.NullString
	var actualDuration sql.NullInt64

	err := db.QueryRow(query, id).Scan(
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
