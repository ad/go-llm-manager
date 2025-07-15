package database

import (
	"encoding/json"
	"time"
)

type Task struct {
	ID                  string  `json:"id" db:"id"`
	UserID              string  `json:"user_id" db:"user_id"`
	ProductData         string  `json:"product_data" db:"product_data"`
	Status              string  `json:"status" db:"status"`
	Result              *string `json:"result,omitempty" db:"result"`
	ErrorMessage        *string `json:"error_message,omitempty" db:"error_message"`
	CreatedAt           int64   `json:"created_at" db:"created_at"`
	UpdatedAt           int64   `json:"updated_at" db:"updated_at"`
	CompletedAt         *int64  `json:"completed_at,omitempty" db:"completed_at"`
	Priority            int     `json:"priority" db:"priority"`
	RetryCount          int     `json:"retry_count" db:"retry_count"`
	MaxRetries          int     `json:"max_retries" db:"max_retries"`
	ProcessorID         *string `json:"processor_id,omitempty" db:"processor_id"`
	ProcessingStartedAt *int64  `json:"processing_started_at,omitempty" db:"processing_started_at"`
	HeartbeatAt         *int64  `json:"heartbeat_at,omitempty" db:"heartbeat_at"`
	TimeoutAt           *int64  `json:"timeout_at,omitempty" db:"timeout_at"`
	OllamaParams        *string `json:"ollama_params,omitempty" db:"ollama_params"`
	EstimatedDuration   *int64  `json:"estimated_duration,omitempty" db:"estimated_duration"`
	ActualDuration      *int64  `json:"actual_duration,omitempty" db:"actual_duration"`
	UserRating          *string `json:"rating,omitempty" db:"rating"` // "upvote", "downvote" или NULL
}

type OllamaParams struct {
	Model         *string  `json:"model,omitempty"`
	Prompt        *string  `json:"prompt,omitempty"`
	Temperature   *float64 `json:"temperature,omitempty"`
	MaxTokens     *int     `json:"max_tokens,omitempty"`
	TopP          *float64 `json:"top_p,omitempty"`
	TopK          *int     `json:"top_k,omitempty"`
	RepeatPenalty *float64 `json:"repeat_penalty,omitempty"`
	Seed          *int     `json:"seed,omitempty"`
	Stop          []string `json:"stop,omitempty"`
}

func (t *Task) SetOllamaParams(params *OllamaParams) error {
	if params == nil {
		t.OllamaParams = nil
		return nil
	}

	data, err := json.Marshal(params)
	if err != nil {
		return err
	}

	str := string(data)
	t.OllamaParams = &str
	return nil
}

func (t *Task) GetOllamaParams() (*OllamaParams, error) {
	if t.OllamaParams == nil {
		return nil, nil
	}

	var params OllamaParams
	err := json.Unmarshal([]byte(*t.OllamaParams), &params)
	if err != nil {
		return nil, err
	}

	return &params, nil
}

type RateLimit struct {
	UserID       string `json:"user_id" db:"user_id"`
	RequestCount int    `json:"request_count" db:"request_count"`
	WindowStart  int64  `json:"window_start" db:"window_start"`
	LastRequest  int64  `json:"last_request" db:"last_request"`
}

type ProcessorMetrics struct {
	ProcessorID string  `json:"processor_id" db:"processor_id"`
	CPUUsage    float64 `json:"cpu_usage" db:"cpu_usage"`
	MemoryUsage float64 `json:"memory_usage" db:"memory_usage"`
	QueueSize   int     `json:"queue_size" db:"queue_size"`
	ActiveTasks int     `json:"active_tasks" db:"active_tasks"`
	LastUpdated int64   `json:"last_updated" db:"last_updated"`
	CreatedAt   int64   `json:"created_at" db:"created_at"`
}

// Request/Response models
type CreateTaskRequest struct {
	ProductData  string        `json:"product_data" binding:"required"`
	Priority     *int          `json:"priority"`
	OllamaParams *OllamaParams `json:"ollama_params"`
}

type CreateTaskResponse struct {
	Success       bool   `json:"success"`
	TaskID        string `json:"taskId"`
	EstimatedTime string `json:"estimatedTime"`
	Token         string `json:"token"`
}

type TaskResponse struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	CreatedAt    int64  `json:"created_at"`
	Result       string `json:"result,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

type VoteRequest struct {
	VoteType string `json:"vote_type" binding:"required"` // "upvote" или "downvote" (повторное нажатие убирает рейтинг)
}

type VoteResponse struct {
	Success    bool    `json:"success"`
	UserRating *string `json:"rating,omitempty"` // текущий рейтинг пользователя
}

type JWTPayload struct {
	UserID       string           `json:"user_id"`
	TaskID       string           `json:"taskId,omitempty"`
	ProductData  string           `json:"product_data,omitempty"`
	Priority     *int             `json:"priority,omitempty"`
	OllamaParams *OllamaParams    `json:"ollama_params,omitempty"`
	ProcessorID  string           `json:"processor_id,omitempty"`
	RateLimit    *RateLimitConfig `json:"rate_limit,omitempty"`
	Issuer       string           `json:"iss"`
	Audience     string           `json:"aud,omitempty"` // Optional, used in some tokens
	Subject      string           `json:"sub"`
	ExpiresAt    int64            `json:"exp"`
}

type RateLimitConfig struct {
	MaxRequests int   `json:"max_requests"`
	WindowMs    int64 `json:"window_ms"`
}

// SSE Events
type SSETaskEvent struct {
	Type      string                 `json:"type"`
	Data      map[string]interface{} `json:"data"`
	Timestamp int64                  `json:"timestamp"`
}

type PollingOptions struct {
	PollInterval      *int `json:"pollInterval"`
	HeartbeatInterval *int `json:"heartbeatInterval"`
	MaxDuration       *int `json:"maxDuration"`
}

// API responses
type APIResponse struct {
	Success bool        `json:"success,omitempty"`
	Error   string      `json:"error,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

type GenerateTokenRequest struct {
	UserID        string           `json:"user_id,omitempty"`
	ProcessorID   string           `json:"processor_id,omitempty"`
	DurationHours *int             `json:"duration_hours,omitempty"`
	TaskID        string           `json:"taskId,omitempty"`
	ExpiresIn     *int             `json:"expires_in,omitempty"`
	ProductData   string           `json:"product_data,omitempty"`
	Priority      *int             `json:"priority,omitempty"`
	OllamaParams  *OllamaParams    `json:"ollama_params,omitempty"`
	RateLimit     *RateLimitConfig `json:"rate_limit,omitempty"`
}

type ClaimTasksRequest struct {
	ProcessorID         string   `json:"processor_id" binding:"required"`
	BatchSize           *int     `json:"batch_size"`
	ProcessorLoad       *float64 `json:"processor_load"`
	TimeoutMs           *int64   `json:"timeout_ms"`
	UseFairDistribution *bool    `json:"use_fair_distribution"`
}

type HeartbeatRequest struct {
	TaskID      string   `json:"taskId" binding:"required"`
	ProcessorID string   `json:"processor_id" binding:"required"`
	CPUUsage    *float64 `json:"cpu_usage"`
	MemoryUsage *float64 `json:"memory_usage"`
	QueueSize   *int     `json:"queue_size"`
}

type CompleteTaskRequest struct {
	TaskID       string `json:"taskId" binding:"required"`
	ProcessorID  string `json:"processor_id" binding:"required"`
	Status       string `json:"status" binding:"required"`
	Result       string `json:"result,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

type WorkStealRequest struct {
	ProcessorID   string `json:"processor_id" binding:"required"`
	MaxStealCount *int   `json:"max_steal_count"`
	TimeoutMs     *int64 `json:"timeout_ms"`
}

// Task status constants
const (
	TaskStatusPending    = "pending"
	TaskStatusProcessing = "processing"
	TaskStatusCompleted  = "completed"
	TaskStatusFailed     = "failed"
)

// SSE event types
const (
	SSEEventTaskStatus    = "task_status"
	SSEEventTaskCompleted = "task_completed"
	SSEEventTaskFailed    = "task_failed"
	SSEEventHeartbeat     = "heartbeat"
	SSEEventError         = "error"
	SSEEventTaskAvailable = "task_available"
)

// Helper functions
func Now() int64 {
	return time.Now().UnixMilli()
}

func FormatTime(timestamp int64) string {
	return time.UnixMilli(timestamp).Format(time.RFC3339)
}
