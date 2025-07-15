package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ad/go-llm-manager/internal/auth"
	"github.com/ad/go-llm-manager/internal/config"
	"github.com/ad/go-llm-manager/internal/database"
)

// Helper function to create string pointer
func stringPtr(s string) *string {
	return &s
}

func TestVoteTask(t *testing.T) {
	// Setup test database
	db, err := database.NewSQLiteDB(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	// Run migrations
	if err := db.RunMigrations(); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	// Add rating column for test (since our test uses in-memory DB)
	_, err = db.Exec("ALTER TABLE tasks ADD COLUMN rating TEXT CHECK (rating IN ('upvote', 'downvote', NULL))")
	if err != nil {
		// Column might already exist, ignore error
		t.Logf("Note: rating column might already exist: %v", err)
	}

	// Setup auth
	cfg := &config.Config{
		Auth: config.AuthConfig{
			JWTSecret: "test-secret",
		},
	}
	jwtAuth := auth.NewJWTAuth(cfg.Auth.JWTSecret)

	// Create test user and task
	userID := "test-user"
	taskID := "test-task-id"

	// Create a completed task
	task := &database.Task{
		ID:          taskID,
		UserID:      userID,
		ProductData: `{"prompt": "Test prompt"}`,
		Status:      "completed",
		Result:      stringPtr("Test response"),
		CreatedAt:   1234567890,
		UpdatedAt:   1234567890,
		UserRating:  nil, // No rating initially
	}

	// Insert task into database
	err = db.CreateTask(task)
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Create handlers
	handlers := NewPublicHandlers(db, jwtAuth, cfg)

	t.Run("upvote task", func(t *testing.T) {
		// Generate JWT token
		payload := &database.JWTPayload{
			UserID: userID,
			TaskID: taskID,
		}
		token, err := jwtAuth.GenerateToken(payload, 3600)
		if err != nil {
			t.Fatalf("Failed to generate JWT token: %v", err)
		}

		// Prepare request
		voteReq := database.VoteRequest{
			VoteType: "upvote",
		}
		reqBody, _ := json.Marshal(voteReq)

		req := httptest.NewRequest(http.MethodPost, "/api/tasks/vote", bytes.NewReader(reqBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		handlers.VoteTask(rr, req)

		// Check response
		if rr.Code != http.StatusOK {
			t.Errorf("Expected status OK, got %d. Response: %s", rr.Code, rr.Body.String())
		}

		var response database.VoteResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
			t.Fatalf("Failed to parse response: %v", err)
		}

		if !response.Success {
			t.Error("Expected success to be true")
		}

		if response.UserRating == nil || *response.UserRating != "upvote" {
			t.Errorf("Expected rating to be 'upvote', got %v", response.UserRating)
		}

		// Verify in database
		updatedTask, err := db.GetTask(taskID)
		if err != nil {
			t.Fatalf("Failed to get updated task: %v", err)
		}

		if updatedTask.UserRating == nil || *updatedTask.UserRating != "upvote" {
			t.Errorf("Expected task rating to be 'upvote', got %v", updatedTask.UserRating)
		}
	})

	t.Run("remove vote by repeat click", func(t *testing.T) {
		// Generate JWT token
		payload := &database.JWTPayload{
			UserID: userID,
			TaskID: taskID,
		}
		token, err := jwtAuth.GenerateToken(payload, 3600)
		if err != nil {
			t.Fatalf("Failed to generate JWT token: %v", err)
		}

		// Prepare request to upvote again (should remove vote)
		voteReq := database.VoteRequest{
			VoteType: "upvote",
		}
		reqBody, _ := json.Marshal(voteReq)

		req := httptest.NewRequest(http.MethodPost, "/api/tasks/vote", bytes.NewReader(reqBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		handlers.VoteTask(rr, req)

		// Check response
		if rr.Code != http.StatusOK {
			t.Errorf("Expected status OK, got %d. Response: %s", rr.Code, rr.Body.String())
		}

		var response database.VoteResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
			t.Fatalf("Failed to parse response: %v", err)
		}

		if !response.Success {
			t.Error("Expected success to be true")
		}

		if response.UserRating != nil {
			t.Errorf("Expected rating to be nil (vote removed), got %v", response.UserRating)
		}

		// Verify in database
		updatedTask, err := db.GetTask(taskID)
		if err != nil {
			t.Fatalf("Failed to get updated task: %v", err)
		}

		if updatedTask.UserRating != nil {
			t.Errorf("Expected task rating to be nil (vote removed), got %v", updatedTask.UserRating)
		}
	})

	t.Run("unauthorized access", func(t *testing.T) {
		voteReq := database.VoteRequest{
			VoteType: "upvote",
		}
		reqBody, _ := json.Marshal(voteReq)

		req := httptest.NewRequest(http.MethodPost, "/api/tasks/vote", bytes.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		// No Authorization header

		rr := httptest.NewRecorder()
		handlers.VoteTask(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("Expected status Unauthorized, got %d", rr.Code)
		}
	})

	t.Run("vote on other user task", func(t *testing.T) {
		// Generate JWT token for different user
		payload := &database.JWTPayload{
			UserID: "other-user",
			TaskID: taskID,
		}
		token, err := jwtAuth.GenerateToken(payload, 3600)
		if err != nil {
			t.Fatalf("Failed to generate JWT token: %v", err)
		}

		voteReq := database.VoteRequest{
			VoteType: "upvote",
		}
		reqBody, _ := json.Marshal(voteReq)

		req := httptest.NewRequest(http.MethodPost, "/api/tasks/vote", bytes.NewReader(reqBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		handlers.VoteTask(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("Expected status Forbidden, got %d", rr.Code)
		}
	})

	t.Run("vote on pending task", func(t *testing.T) {
		// Create pending task
		pendingTaskID := "pending-task-id"
		pendingTask := &database.Task{
			ID:          pendingTaskID,
			UserID:      userID,
			ProductData: `{"prompt": "Test prompt"}`,
			Status:      "pending",
			CreatedAt:   1234567890,
			UpdatedAt:   1234567890,
		}

		err = db.CreateTask(pendingTask)
		if err != nil {
			t.Fatalf("Failed to create pending task: %v", err)
		}

		// Generate JWT token
		payload := &database.JWTPayload{
			UserID: userID,
			TaskID: pendingTaskID,
		}
		token, err := jwtAuth.GenerateToken(payload, 3600)
		if err != nil {
			t.Fatalf("Failed to generate JWT token: %v", err)
		}

		voteReq := database.VoteRequest{
			VoteType: "upvote",
		}
		reqBody, _ := json.Marshal(voteReq)

		req := httptest.NewRequest(http.MethodPost, "/api/tasks/vote", bytes.NewReader(reqBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		handlers.VoteTask(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("Expected status BadRequest, got %d", rr.Code)
		}
	})
}
