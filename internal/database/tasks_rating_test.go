package database

import (
	"testing"
)

func setupTestDB(t *testing.T) *DB {
	db, err := NewSQLiteDB("file:memdb_rating?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}

	// Run migrations to set up the database schema including user_rating column
	err = db.RunMigrations()
	if err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	return db
}

func TestUpdateTaskRating(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create a test user and task
	userID := "test_user_rating"
	task := &Task{
		ID:          "task_rating_test",
		UserID:      userID,
		ProductData: "Test data",
		Status:      "completed", // Task must be completed to allow rating
		Priority:    0,
		MaxRetries:  3,
	}

	err := db.CreateTask(task)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Test upvote
	upvote := "upvote"
	err = db.UpdateTaskRating(task.ID, userID, &upvote)
	if err != nil {
		t.Fatalf("Failed to update task rating: %v", err)
	}

	// Verify rating was set
	updatedTask, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("Failed to get updated task: %v", err)
	}
	if updatedTask.UserRating == nil || *updatedTask.UserRating != "upvote" {
		t.Errorf("Expected upvote, got %v", updatedTask.UserRating)
	}

	// Test downvote (should replace upvote)
	downvote := "downvote"
	err = db.UpdateTaskRating(task.ID, userID, &downvote)
	if err != nil {
		t.Fatalf("Failed to update task rating to downvote: %v", err)
	}

	updatedTask, err = db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("Failed to get updated task: %v", err)
	}
	if updatedTask.UserRating == nil || *updatedTask.UserRating != "downvote" {
		t.Errorf("Expected downvote, got %v", updatedTask.UserRating)
	}

	// Test removing rating
	err = db.UpdateTaskRating(task.ID, userID, nil)
	if err != nil {
		t.Fatalf("Failed to remove task rating: %v", err)
	}

	updatedTask, err = db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("Failed to get updated task: %v", err)
	}
	if updatedTask.UserRating != nil {
		t.Errorf("Expected nil rating, got %v", updatedTask.UserRating)
	}
}

func TestUpdateTaskRating_NotOwner(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create a test task
	task := &Task{
		ID:          "task_not_owner",
		UserID:      "owner_user",
		ProductData: "Test data",
		Status:      "completed",
		Priority:    0,
		MaxRetries:  3,
	}

	err := db.CreateTask(task)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Try to rate with different user
	upvote := "upvote"
	err = db.UpdateTaskRating(task.ID, "different_user", &upvote)
	if err == nil {
		t.Fatal("Expected error when non-owner tries to rate task")
	}
}

func TestUpdateTaskRating_NotCompleted(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create a test task with pending status
	task := &Task{
		ID:          "task_not_completed",
		UserID:      "test_user",
		ProductData: "Test data",
		Status:      "pending", // Not completed
		Priority:    0,
		MaxRetries:  3,
	}

	err := db.CreateTask(task)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Try to rate pending task
	upvote := "upvote"
	err = db.UpdateTaskRating(task.ID, "test_user", &upvote)
	if err == nil {
		t.Fatal("Expected error when trying to rate non-completed task")
	}
}

func TestGetTasksRatingStats(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create test tasks with ratings
	tasks := []*Task{
		{
			ID:          "task_stats_1",
			UserID:      "user1",
			ProductData: "Test data 1",
			Status:      "completed",
			Priority:    0,
			MaxRetries:  3,
		},
		{
			ID:          "task_stats_2",
			UserID:      "user1",
			ProductData: "Test data 2",
			Status:      "completed",
			Priority:    0,
			MaxRetries:  3,
		},
		{
			ID:          "task_stats_3",
			UserID:      "user2",
			ProductData: "Test data 3",
			Status:      "completed",
			Priority:    0,
			MaxRetries:  3,
		},
	}

	for _, task := range tasks {
		err := db.CreateTask(task)
		if err != nil {
			t.Fatalf("Failed to create task %s: %v", task.ID, err)
		}
	}

	// Add ratings
	upvote := "upvote"
	downvote := "downvote"

	err := db.UpdateTaskRating("task_stats_1", "user1", &upvote)
	if err != nil {
		t.Fatalf("Failed to add upvote: %v", err)
	}

	err = db.UpdateTaskRating("task_stats_2", "user1", &downvote)
	if err != nil {
		t.Fatalf("Failed to add downvote: %v", err)
	}

	err = db.UpdateTaskRating("task_stats_3", "user2", &upvote)
	if err != nil {
		t.Fatalf("Failed to add upvote: %v", err)
	}

	// Test global stats
	stats, err := db.GetTasksRatingStats(nil)
	if err != nil {
		t.Fatalf("Failed to get global rating stats: %v", err)
	}

	if stats["upvote"] != 2 {
		t.Errorf("Expected 2 upvotes, got %d", stats["upvote"])
	}
	if stats["downvote"] != 1 {
		t.Errorf("Expected 1 downvote, got %d", stats["downvote"])
	}

	// Test user-specific stats
	user1 := "user1"
	userStats, err := db.GetTasksRatingStats(&user1)
	if err != nil {
		t.Fatalf("Failed to get user rating stats: %v", err)
	}

	if userStats["upvote"] != 1 {
		t.Errorf("Expected 1 upvote for user1, got %d", userStats["upvote"])
	}
	if userStats["downvote"] != 1 {
		t.Errorf("Expected 1 downvote for user1, got %d", userStats["downvote"])
	}
}

func TestGetUserRatedTasks(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID := "test_user_rated"

	// Create test tasks
	tasks := []*Task{
		{
			ID:          "rated_task_1",
			UserID:      userID,
			ProductData: "Test data 1",
			Status:      "completed",
			Priority:    0,
			MaxRetries:  3,
		},
		{
			ID:          "rated_task_2",
			UserID:      userID,
			ProductData: "Test data 2",
			Status:      "completed",
			Priority:    0,
			MaxRetries:  3,
		},
		{
			ID:          "unrated_task",
			UserID:      userID,
			ProductData: "Test data 3",
			Status:      "completed",
			Priority:    0,
			MaxRetries:  3,
		},
	}

	for _, task := range tasks {
		err := db.CreateTask(task)
		if err != nil {
			t.Fatalf("Failed to create task %s: %v", task.ID, err)
		}
	}

	// Add ratings to first two tasks
	upvote := "upvote"
	downvote := "downvote"

	err := db.UpdateTaskRating("rated_task_1", userID, &upvote)
	if err != nil {
		t.Fatalf("Failed to add upvote: %v", err)
	}

	err = db.UpdateTaskRating("rated_task_2", userID, &downvote)
	if err != nil {
		t.Fatalf("Failed to add downvote: %v", err)
	}

	// Test getting all rated tasks
	ratedTasks, err := db.GetUserRatedTasks(userID, nil, 100, 0)
	if err != nil {
		t.Fatalf("Failed to get user rated tasks: %v", err)
	}

	if len(ratedTasks) != 2 {
		t.Errorf("Expected 2 rated tasks, got %d", len(ratedTasks))
	}

	// Test getting only upvoted tasks
	upvotedTasks, err := db.GetUserRatedTasks(userID, &upvote, 100, 0)
	if err != nil {
		t.Fatalf("Failed to get user upvoted tasks: %v", err)
	}

	if len(upvotedTasks) != 1 {
		t.Errorf("Expected 1 upvoted task, got %d", len(upvotedTasks))
	}

	if upvotedTasks[0].ID != "rated_task_1" {
		t.Errorf("Expected rated_task_1, got %s", upvotedTasks[0].ID)
	}

	// Test getting only downvoted tasks
	downvotedTasks, err := db.GetUserRatedTasks(userID, &downvote, 100, 0)
	if err != nil {
		t.Fatalf("Failed to get user downvoted tasks: %v", err)
	}

	if len(downvotedTasks) != 1 {
		t.Errorf("Expected 1 downvoted task, got %d", len(downvotedTasks))
	}

	if downvotedTasks[0].ID != "rated_task_2" {
		t.Errorf("Expected rated_task_2, got %s", downvotedTasks[0].ID)
	}
}
