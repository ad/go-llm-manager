package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ad/go-llm-manager/internal/auth"
	"github.com/ad/go-llm-manager/internal/config"
	"github.com/ad/go-llm-manager/internal/database"
)

// TestFullTaskLifecycleWithVoting тестирует полный цикл:
// создание задачи → завершение → голосование
func TestFullTaskLifecycleWithVoting(t *testing.T) {
	// Настройка тестовой базы данных
	db := database.NewTestDB(t)

	// Добавляем поле rating если его нет (для совместимости)
	_, err := db.Exec("ALTER TABLE tasks ADD COLUMN rating TEXT CHECK (rating IN ('upvote', 'downvote', NULL))")
	if err != nil && !strings.Contains(err.Error(), "duplicate column") {
		t.Fatalf("Failed to add rating column: %v", err)
	}

	// Настройка конфигурации и аутентификации
	cfg := &config.Config{
		Auth: config.AuthConfig{
			JWTSecret:      "test-secret-key-for-integration-tests",
			InternalAPIKey: "test-internal-key",
		},
	}
	jwtAuth := auth.NewJWTAuth(cfg.Auth.JWTSecret)

	// Создаем handlers
	publicHandlers := NewPublicHandlers(db, jwtAuth, cfg)
	internalHandlers := NewInternalHandlers(db, jwtAuth)

	// Test данные
	userID := "test-user-integration"
	productData := "iPhone 15 Pro 256GB для интеграционного теста"

	t.Run("full_lifecycle_with_voting", func(t *testing.T) {
		// 1. Генерируем JWT токен для создания задачи
		t.Log("Step 1: Generating JWT token...")

		tokenReq := map[string]interface{}{
			"user_id":      userID,
			"product_data": productData,
		}
		tokenReqBody, _ := json.Marshal(tokenReq)

		req := httptest.NewRequest(http.MethodPost, "/api/internal/generate-token", bytes.NewReader(tokenReqBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer test-internal-key")
		rr := httptest.NewRecorder()

		internalHandlers.GenerateToken(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d. Body: %s", rr.Code, rr.Body.String())
		}

		var tokenResp struct {
			Success bool   `json:"success"`
			Token   string `json:"token"`
		}
		if err := json.NewDecoder(rr.Body).Decode(&tokenResp); err != nil {
			t.Fatalf("Failed to decode token response: %v", err)
		}

		if !tokenResp.Success || tokenResp.Token == "" {
			t.Fatalf("Failed to get valid token: %+v", tokenResp)
		}
		t.Logf("✅ JWT token generated successfully")

		// 2. Создаем задачу
		t.Log("Step 2: Creating task...")

		req = httptest.NewRequest(http.MethodPost, "/api/create", nil)
		req.Header.Set("Authorization", "Bearer "+tokenResp.Token)
		rr = httptest.NewRecorder()

		publicHandlers.CreateTask(rr, req)

		if rr.Code != http.StatusCreated {
			t.Fatalf("Expected status 201, got %d. Body: %s", rr.Code, rr.Body.String())
		}

		var createResp struct {
			Success bool   `json:"success"`
			TaskID  string `json:"taskId"`
			Token   string `json:"token"`
		}
		if err := json.NewDecoder(rr.Body).Decode(&createResp); err != nil {
			t.Fatalf("Failed to decode create response: %v", err)
		}

		if !createResp.Success || createResp.TaskID == "" {
			t.Fatalf("Failed to create task: %+v", createResp)
		}
		t.Logf("✅ Task created successfully with ID: %s", createResp.TaskID)

		// 3. Симулируем завершение задачи (обычно это делает процессор)
		t.Log("Step 3: Completing task...")

		// Устанавливаем задачу в статус "completed" с результатом
		result := "Отличное описание товара для интеграционного теста!"
		err = db.UpdateTaskStatus(createResp.TaskID, "completed", &result, nil)
		if err != nil {
			t.Fatalf("Failed to complete task: %v", err)
		}
		t.Logf("✅ Task completed successfully")

		// 4. Проверяем результат задачи
		t.Log("Step 4: Checking task result...")

		req = httptest.NewRequest(http.MethodPost, "/api/result", nil)
		req.Header.Set("Authorization", "Bearer "+createResp.Token)
		rr = httptest.NewRecorder()

		publicHandlers.GetResult(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d. Body: %s", rr.Code, rr.Body.String())
		}

		var resultResp struct {
			Success    bool    `json:"success"`
			Status     string  `json:"status"`
			Result     string  `json:"result"`
			UserRating *string `json:"rating"`
		}
		if err := json.NewDecoder(rr.Body).Decode(&resultResp); err != nil {
			t.Fatalf("Failed to decode result response: %v", err)
		}

		if !resultResp.Success || resultResp.Status != "completed" {
			t.Fatalf("Expected completed task, got: %+v", resultResp)
		}

		if resultResp.UserRating != nil {
			t.Fatalf("Expected no user rating initially, got: %v", *resultResp.UserRating)
		}
		t.Logf("✅ Task result verified, no rating yet")

		// 5. Голосуем "upvote" за задачу
		t.Log("Step 5: Voting upvote...")

		voteReq := map[string]string{
			"vote_type": "upvote",
		}
		voteReqBody, _ := json.Marshal(voteReq)

		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/tasks/%s/vote", createResp.TaskID), bytes.NewReader(voteReqBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tokenResp.Token) // Используем исходный JWT токен
		rr = httptest.NewRecorder()

		publicHandlers.VoteTask(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d. Body: %s", rr.Code, rr.Body.String())
		}

		var voteResp database.VoteResponse
		if err := json.NewDecoder(rr.Body).Decode(&voteResp); err != nil {
			t.Fatalf("Failed to decode vote response: %v", err)
		}

		if !voteResp.Success {
			t.Fatalf("Vote was not successful: %+v", voteResp)
		}

		if voteResp.UserRating == nil || *voteResp.UserRating != "upvote" {
			t.Fatalf("Expected upvote rating, got: %v", voteResp.UserRating)
		}
		t.Logf("✅ Upvote successful")

		// 6. Проверяем что рейтинг сохранился
		t.Log("Step 6: Verifying rating persistence...")

		task, err := db.GetTask(createResp.TaskID)
		if err != nil {
			t.Fatalf("Failed to get task after voting: %v", err)
		}

		if task.UserRating == nil || *task.UserRating != "upvote" {
			t.Fatalf("Expected task to have upvote rating, got: %v", task.UserRating)
		}
		t.Logf("✅ Rating persisted in database")

		// 7. Проверяем результат с рейтингом
		t.Log("Step 7: Checking result with rating...")

		req = httptest.NewRequest(http.MethodPost, "/api/result", nil)
		req.Header.Set("Authorization", "Bearer "+createResp.Token)
		rr = httptest.NewRecorder()

		publicHandlers.GetResult(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d. Body: %s", rr.Code, rr.Body.String())
		}

		if err := json.NewDecoder(rr.Body).Decode(&resultResp); err != nil {
			t.Fatalf("Failed to decode result response: %v", err)
		}

		if resultResp.UserRating == nil || *resultResp.UserRating != "upvote" {
			t.Fatalf("Expected result to include upvote rating, got: %v", resultResp.UserRating)
		}
		t.Logf("✅ Result includes rating")

		// 8. Изменяем рейтинг на "downvote"
		t.Log("Step 8: Changing rating to downvote...")

		voteReq["vote_type"] = "downvote"
		voteReqBody, _ = json.Marshal(voteReq)

		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/tasks/%s/vote", createResp.TaskID), bytes.NewReader(voteReqBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tokenResp.Token)
		rr = httptest.NewRecorder()

		publicHandlers.VoteTask(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d. Body: %s", rr.Code, rr.Body.String())
		}

		if err := json.NewDecoder(rr.Body).Decode(&voteResp); err != nil {
			t.Fatalf("Failed to decode vote response: %v", err)
		}

		if voteResp.UserRating == nil || *voteResp.UserRating != "downvote" {
			t.Fatalf("Expected downvote rating, got: %v", voteResp.UserRating)
		}
		t.Logf("✅ Rating changed to downvote")

		// 9. Убираем рейтинг (toggle behavior)
		t.Log("Step 9: Removing rating (toggle)...")

		voteReq["vote_type"] = "downvote" // Повторное нажатие должно убрать рейтинг
		voteReqBody, _ = json.Marshal(voteReq)

		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/tasks/%s/vote", createResp.TaskID), bytes.NewReader(voteReqBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tokenResp.Token)
		rr = httptest.NewRecorder()

		publicHandlers.VoteTask(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d. Body: %s", rr.Code, rr.Body.String())
		}

		var toggleResp database.VoteResponse
		if err := json.NewDecoder(rr.Body).Decode(&toggleResp); err != nil {
			t.Fatalf("Failed to decode vote response: %v", err)
		}

		if toggleResp.UserRating != nil {
			t.Fatalf("Expected no rating after toggle, got: %v", *toggleResp.UserRating)
		}
		t.Logf("✅ Rating removed successfully")

		// 10. Проверяем финальное состояние
		t.Log("Step 10: Final verification...")

		task, err = db.GetTask(createResp.TaskID)
		if err != nil {
			t.Fatalf("Failed to get task for final check: %v", err)
		}

		if task.UserRating != nil {
			t.Fatalf("Expected no rating in final state, got: %v", *task.UserRating)
		}
		t.Logf("✅ Final state verified - no rating")

		t.Log("🎉 Full integration test completed successfully!")
	})
}

// TestRatingStatsIntegration тестирует интеграцию статистики рейтингов
func TestRatingStatsIntegration(t *testing.T) {
	// Настройка тестовой базы данных
	db := database.NewTestDB(t)

	// Добавляем поле rating если его нет
	_, err := db.Exec("ALTER TABLE tasks ADD COLUMN rating TEXT CHECK (rating IN ('upvote', 'downvote', NULL))")
	if err != nil && !strings.Contains(err.Error(), "duplicate column") {
		t.Fatalf("Failed to add rating column: %v", err)
	}

	cfg := &config.Config{
		Auth: config.AuthConfig{
			JWTSecret:      "test-secret-key-for-stats",
			InternalAPIKey: "test-internal-key",
		},
	}
	jwtAuth := auth.NewJWTAuth(cfg.Auth.JWTSecret)

	internalHandlers := NewInternalHandlers(db, jwtAuth)

	t.Run("rating_stats_integration", func(t *testing.T) {
		// Создаем несколько задач с разными рейтингами
		userIDs := []string{"user1", "user2", "user3"}
		taskIDs := make([]string, 0, len(userIDs))

		for i, userID := range userIDs {
			// Создаем задачу
			result := fmt.Sprintf("Result %d", i+1)
			task := &database.Task{
				ID:          fmt.Sprintf("task-%d", i+1),
				UserID:      userID,
				ProductData: fmt.Sprintf("Product %d", i+1),
				Status:      "completed",
				Result:      &result,
				CreatedAt:   time.Now().UnixMilli(),
			}
			completedAt := time.Now().UnixMilli()
			task.CompletedAt = &completedAt

			err := db.CreateTask(task)
			if err != nil {
				t.Fatalf("Failed to create task %d: %v", i+1, err)
			}
			taskIDs = append(taskIDs, task.ID)
		}

		// Устанавливаем рейтинги: upvote, downvote, no rating
		ratings := []*string{
			func() *string { s := "upvote"; return &s }(),
			func() *string { s := "downvote"; return &s }(),
			nil, // no rating
		}

		for i, rating := range ratings {
			if rating != nil {
				err := db.UpdateTaskRating(taskIDs[i], userIDs[i], rating)
				if err != nil {
					t.Fatalf("Failed to set rating for task %d: %v", i+1, err)
				}
			}
		}

		// Проверяем общую статистику
		t.Log("Checking rating stats...")

		req := httptest.NewRequest(http.MethodGet, "/api/internal/rating-stats", nil)
		req.Header.Set("Authorization", "Bearer test-internal-key")
		rr := httptest.NewRecorder()

		internalHandlers.GetRatingStats(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d. Body: %s", rr.Code, rr.Body.String())
		}

		var statsResp struct {
			Success    bool `json:"success"`
			Upvotes    int  `json:"upvotes"`
			Downvotes  int  `json:"downvotes"`
			TotalRated int  `json:"total_rated"`
		}

		if err := json.NewDecoder(rr.Body).Decode(&statsResp); err != nil {
			t.Fatalf("Failed to decode stats response: %v", err)
		}

		if !statsResp.Success {
			t.Fatalf("Stats request was not successful")
		}

		if statsResp.Upvotes != 1 {
			t.Errorf("Expected 1 upvote, got %d", statsResp.Upvotes)
		}

		if statsResp.Downvotes != 1 {
			t.Errorf("Expected 1 downvote, got %d", statsResp.Downvotes)
		}

		if statsResp.TotalRated != 2 {
			t.Errorf("Expected 2 total rated tasks, got %d", statsResp.TotalRated)
		}

		t.Log("✅ Rating stats integration test completed successfully!")
	})
}

// TestVotingEdgeCases тестирует граничные случаи голосования
func TestVotingEdgeCases(t *testing.T) {
	db := database.NewTestDB(t)

	_, err := db.Exec("ALTER TABLE tasks ADD COLUMN rating TEXT CHECK (rating IN ('upvote', 'downvote', NULL))")
	if err != nil && !strings.Contains(err.Error(), "duplicate column") {
		t.Fatalf("Failed to add rating column: %v", err)
	}

	cfg := &config.Config{
		Auth: config.AuthConfig{
			JWTSecret:      "test-secret-key-for-edge-cases",
			InternalAPIKey: "test-internal-key",
		},
	}
	jwtAuth := auth.NewJWTAuth(cfg.Auth.JWTSecret)
	publicHandlers := NewPublicHandlers(db, jwtAuth, cfg)

	t.Run("cannot_vote_on_pending_task", func(t *testing.T) {
		userID := "edge-case-user"
		taskID := "pending-task"

		// Создаем задачу в статусе pending
		task := &database.Task{
			ID:          taskID,
			UserID:      userID,
			ProductData: "Pending task product",
			Status:      "pending",
			CreatedAt:   time.Now().UnixMilli(),
		}

		err := db.CreateTask(task)
		if err != nil {
			t.Fatalf("Failed to create pending task: %v", err)
		}

		// Создаем JWT токен с правильной структурой
		payload := &database.JWTPayload{
			UserID:      userID,
			TaskID:      taskID,
			ProductData: "test-product",
		}

		token, err := jwtAuth.GenerateToken(payload, 3600)
		if err != nil {
			t.Fatalf("Failed to generate token: %v", err)
		}

		// Пытаемся голосовать за pending задачу
		voteReq := map[string]string{"vote_type": "upvote"}
		voteReqBody, _ := json.Marshal(voteReq)

		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/tasks/%s/vote", taskID), bytes.NewReader(voteReqBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()

		publicHandlers.VoteTask(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d. Body: %s", rr.Code, rr.Body.String())
		}

		t.Log("✅ Cannot vote on pending task - correct")
	})

	t.Run("cannot_vote_on_other_user_task", func(t *testing.T) {
		ownerID := "task-owner"
		voterID := "task-voter"
		taskID := "other-user-task"

		// Создаем задачу для одного пользователя
		result := "Completed result"
		task := &database.Task{
			ID:          taskID,
			UserID:      ownerID,
			ProductData: "Other user task",
			Status:      "completed",
			Result:      &result,
			CreatedAt:   time.Now().UnixMilli(),
		}
		completedAt := time.Now().UnixMilli()
		task.CompletedAt = &completedAt

		err := db.CreateTask(task)
		if err != nil {
			t.Fatalf("Failed to create task: %v", err)
		}

		// Создаем JWT токен для другого пользователя
		payload := &database.JWTPayload{
			UserID:      voterID,
			TaskID:      taskID,
			ProductData: "test-product",
		}

		token, err := jwtAuth.GenerateToken(payload, 3600)
		if err != nil {
			t.Fatalf("Failed to generate token: %v", err)
		}

		// Пытаемся голосовать за чужую задачу
		voteReq := map[string]string{"vote_type": "upvote"}
		voteReqBody, _ := json.Marshal(voteReq)

		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/tasks/%s/vote", taskID), bytes.NewReader(voteReqBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()

		publicHandlers.VoteTask(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("Expected status 403, got %d. Body: %s", rr.Code, rr.Body.String())
		}

		t.Log("✅ Cannot vote on other user's task - correct")
	})

	t.Log("🎉 Edge cases integration test completed successfully!")
}
