package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ad/go-llm-manager/internal/auth"
	"github.com/ad/go-llm-manager/internal/database"
)

func TestPerformCleanup_RequeueAndFail(t *testing.T) {
	db := database.NewTestDB(t)
	jwtAuth := auth.NewJWTAuth("test")
	h := NewInternalHandlers(db, jwtAuth)

	now := time.Now().UnixMilli()
	fiveMinutesAgo := now - 5*60*1000
	old := fiveMinutesAgo - 1000 // гарантированно < fiveMinutesAgo

	// Задача 1: должна быть requeue (retry_count < max_retries)
	task1 := &database.Task{
		ID:          "task1",
		UserID:      "u1",
		ProductData: "p1",
		Status:      "processing",
		ProcessorID: func() *string { s := "proc1"; return &s }(),
		RetryCount:  1,
		MaxRetries:  3,
		HeartbeatAt: &old,
		Priority:    0,
	}
	// Задача 2: должна быть failed (retry_count+1 >= max_retries)
	task2 := &database.Task{
		ID:          "task2",
		UserID:      "u2",
		ProductData: "p2",
		Status:      "processing",
		ProcessorID: func() *string { s := "proc2"; return &s }(),
		RetryCount:  2,
		MaxRetries:  3,
		HeartbeatAt: &old,
		Priority:    0,
	}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("failed to insert task1: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("failed to insert task2: %v", err)
	}
	// Прямой апдейт для выставления processor_id, heartbeat_at, retry_count, max_retries, processing_started_at
	_, err := db.Exec(`UPDATE tasks SET status=?, processor_id=?, heartbeat_at=?, retry_count=?, max_retries=?, processing_started_at=? WHERE id=?`,
		"processing", "proc1", old, 1, 3, old, "task1")
	if err != nil {
		t.Fatalf("failed to update task1 fields: %v", err)
	}
	_, err = db.Exec(`UPDATE tasks SET status=?, processor_id=?, heartbeat_at=?, retry_count=?, max_retries=?, processing_started_at=? WHERE id=?`,
		"processing", "proc2", old, 2, 3, old, "task2")
	if err != nil {
		t.Fatalf("failed to update task2 fields: %v", err)
	}

	// Лог: выводим задачи до performCleanup
	rows, err := db.Query("SELECT id, status, processor_id, heartbeat_at, retry_count, max_retries FROM tasks")
	if err == nil {
		t.Log("Before performCleanup:")
		for rows.Next() {
			var id, status, processorID string
			var heartbeatAt int64
			var retryCount, maxRetries int
			_ = rows.Scan(&id, &status, &processorID, &heartbeatAt, &retryCount, &maxRetries)
			t.Logf("id=%s status=%s processor_id=%s heartbeat_at=%d retry_count=%d max_retries=%d", id, status, processorID, heartbeatAt, retryCount, maxRetries)
		}
		rows.Close()
	}

	// Вызов performCleanup через HTTP
	req := httptest.NewRequest("POST", "/api/internal/cleanup", nil)
	w := httptest.NewRecorder()
	h.Cleanup(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}

	// Лог: выводим задачи после performCleanup
	rows, err = db.Query("SELECT id, status, processor_id, heartbeat_at, retry_count, max_retries FROM tasks")
	if err == nil {
		t.Log("After performCleanup:")
		for rows.Next() {
			var id, status, processorID string
			var heartbeatAt int64
			var retryCount, maxRetries int
			_ = rows.Scan(&id, &status, &processorID, &heartbeatAt, &retryCount, &maxRetries)
			t.Logf("id=%s status=%s processor_id=%s heartbeat_at=%d retry_count=%d max_retries=%d", id, status, processorID, heartbeatAt, retryCount, maxRetries)
		}
		rows.Close()
	}

	// Проверяем статусы задач
	t1, err := db.GetTask("task1")
	if err != nil {
		t.Fatalf("failed to get task1: %v", err)
	}
	if t1.Status != "pending" {
		t.Errorf("task1: expected status 'pending', got '%s'", t1.Status)
	}
	if t1.RetryCount != 2 {
		t.Errorf("task1: expected retry_count 2, got %d", t1.RetryCount)
	}
	if t1.ErrorMessage == nil || *t1.ErrorMessage == "" {
		t.Errorf("task1: expected error_message to be set")
	}

	t2, err := db.GetTask("task2")
	if err != nil {
		t.Fatalf("failed to get task2: %v", err)
	}
	if t2.Status != "failed" {
		t.Errorf("task2: expected status 'failed', got '%s'", t2.Status)
	}
	if t2.RetryCount != 2 {
		t.Errorf("task2: expected retry_count 2, got %d", t2.RetryCount)
	}
	if t2.ErrorMessage == nil || *t2.ErrorMessage == "" {
		t.Errorf("task2: expected error_message to be set")
	}
}
