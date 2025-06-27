package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ad/go-llm-manager/internal/database"
)

type EventType string

const (
	EventTaskStatus       EventType = "task_status"
	EventTaskCompleted    EventType = "task_completed"
	EventTaskFailed       EventType = "task_failed"
	EventHeartbeat        EventType = "heartbeat"
	EventError            EventType = "error"
	EventTaskAvailable    EventType = "task_available"
	EventProcessorMetrics EventType = "processor_metrics"
)

type SSEEvent struct {
	Type      EventType              `json:"type"`
	Data      map[string]interface{} `json:"data"`
	Timestamp int64                  `json:"timestamp"`
	ID        string                 `json:"id,omitempty"`
}

type Client struct {
	ID      string
	UserID  string
	TaskID  string
	Writer  http.ResponseWriter
	Flusher http.Flusher
	Events  chan SSEEvent
	Done    chan bool
	mu      sync.Mutex
	closed  bool
}

type Manager struct {
	clients map[string]*Client
	mu      sync.RWMutex
}

func NewManager() *Manager {
	return &Manager{
		clients: make(map[string]*Client),
	}
}

func (m *Manager) AddClient(client *Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients[client.ID] = client
}

func (m *Manager) RemoveClient(clientID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if client, exists := m.clients[clientID]; exists {
		client.Close()
		delete(m.clients, clientID)
	}
}

func (m *Manager) BroadcastToTask(taskID string, event SSEEvent) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, client := range m.clients {
		if client.TaskID == taskID {
			select {
			case client.Events <- event:
			default:
				// Client channel is full, skip
			}
		}
	}
}

func (m *Manager) BroadcastToUser(userID string, event SSEEvent) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, client := range m.clients {
		if client.UserID == userID {
			select {
			case client.Events <- event:
			default:
				// Client channel is full, skip
			}
		}
	}
}

// Broadcasts a new pending task to all connected processor clients
func (m *Manager) BroadcastPendingTaskToProcessors(task *database.Task) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, client := range m.clients {
		// Processor clients have UserID set (processorID), TaskID is empty
		if client.UserID != "" && client.TaskID == "" {
			select {
			case client.Events <- SSEEvent{
				Type: EventTaskAvailable,
				Data: map[string]interface{}{
					"taskId":       task.ID,
					"priority":     task.Priority,
					"productData":  task.ProductData,
					"ollamaParams": task.OllamaParams,
				},
				Timestamp: time.Now().UnixMilli(),
			}:
			default:
				// Channel full, skip
			}
		}
	}
}

func NewClient(id, userID, taskID string, w http.ResponseWriter) *Client {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil
	}

	return &Client{
		ID:      id,
		UserID:  userID,
		TaskID:  taskID,
		Writer:  w,
		Flusher: flusher,
		Events:  make(chan SSEEvent, 10),
		Done:    make(chan bool),
	}
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.closed {
		c.closed = true
		close(c.Done)
		close(c.Events)
	}
}

func (c *Client) SendEvent(event SSEEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return fmt.Errorf("client connection closed")
	}

	if event.Type == "" {
		return fmt.Errorf("SSE event with empty type is not allowed")
	}

	// Format SSE event
	eventData, err := json.Marshal(event)
	if err != nil {
		return err
	}

	// Write SSE format
	// if event.ID != "" {
	// 	fmt.Fprintf(c.Writer, "id: %s\n", event.ID)
	// }
	fmt.Fprintf(c.Writer, "data: %s\n\n", eventData)

	c.Flusher.Flush()
	return nil
}

func (c *Client) Run() {
	defer c.Close()

	for {
		select {
		case event, ok := <-c.Events:
			if !ok {
				return
			}
			if err := c.SendEvent(event); err != nil {
				return
			}
		case <-c.Done:
			return
		}
	}
}

func WriteSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("X-Accel-Buffering", "no")
}
