package handlers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ad/go-llm-manager/internal/api/handlers"
	"github.com/ad/go-llm-manager/internal/auth"
	"github.com/ad/go-llm-manager/internal/database"
)

func TestRequeueTask_Integration(t *testing.T) {
	db := database.NewTestDB(t)
	jwtAuth := auth.NewJWTAuth("test-secret")
	h := handlers.NewInternalHandlers(db, jwtAuth)

	// Настроить реальный http-сервер с нужным роутом
	mux := http.NewServeMux()
	mux.HandleFunc("/api/internal/requeue", h.RequeueTask)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Создать задачу и назначить процессору
	task := &database.Task{
		ID:          "task-1",
		UserID:      "user-1",
		Status:      "processing",
		ProcessorID: func() *string { s := "proc-1"; return &s }(),
		ProductData: "test-data",
		Priority:    0,
		MaxRetries:  3,
	}
	err := db.CreateTask(task)
	if err != nil {
		t.Fatalf("failed to create task: %v", err)
	}
	// Принудительно выставить processor_id и статус processing (интеграционный тест)
	_, err = db.Exec(`UPDATE tasks SET status = 'processing', processor_id = ? WHERE id = ?`, "proc-1", "task-1")
	if err != nil {
		t.Fatalf("failed to update task for integration: %v", err)
	}

	// Проверить, что задача создана корректно
	task0, err := db.GetTask("task-1")
	if err != nil {
		t.Fatalf("failed to get task after create: %v", err)
	}
	if task0.Status != "processing" || task0.ProcessorID == nil || *task0.ProcessorID != "proc-1" {
		t.Fatalf("task not in processing state before requeue")
	}

	// Отправить реальный HTTP-запрос
	body := map[string]interface{}{
		"taskId":       "task-1",
		"processor_id": "proc-1",
		"reason":       "integration test",
	}
	b, _ := json.Marshal(body)
	resp, err := http.Post(ts.URL+"/api/internal/requeue", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("http post failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d, body: %s", resp.StatusCode, string(data))
	}

	// Проверить, что задача теперь pending
	task2, err := db.GetTask("task-1")
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if task2.Status != "pending" {
		t.Errorf("expected status 'pending', got '%s'", task2.Status)
	}
	if task2.ProcessorID != nil {
		t.Errorf("expected ProcessorID nil, got %v", *task2.ProcessorID)
	}
	if task2.ErrorMessage == nil || *task2.ErrorMessage != "integration test" {
		t.Errorf("expected error_message 'integration test', got %v", task2.ErrorMessage)
	}
}
