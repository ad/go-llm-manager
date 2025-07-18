package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ad/go-llm-manager/internal/auth"
	"github.com/ad/go-llm-manager/internal/database"
	"github.com/ad/go-llm-manager/internal/sse"
	"github.com/ad/go-llm-manager/internal/utils"
	"github.com/google/uuid"
)

type SSEHandlers struct {
	db      *database.DB
	jwtAuth *auth.JWTAuth
	manager *sse.Manager
}

func NewSSEHandlers(db *database.DB, jwtAuth *auth.JWTAuth) *SSEHandlers {
	return &SSEHandlers{
		db:      db,
		jwtAuth: jwtAuth,
		manager: sse.NewManager(),
	}
}

// GET /api/result-polling - SSE для результатов задач
func (h *SSEHandlers) ResultPolling(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		utils.SendError(w, http.StatusMethodNotAllowed, "Method not allowed. Use GET for SSE.")
		return
	}

	// Извлечение токена из query параметра
	token := r.URL.Query().Get("token")
	if token == "" {
		utils.SendError(w, http.StatusBadRequest, "Missing token parameter")
		return
	}

	// Проверка JWT токена
	payload, err := h.jwtAuth.ExtractPayloadFromToken(token)
	if err != nil {
		utils.SendError(w, http.StatusUnauthorized, "Invalid or expired token")
		return
	}

	userID := payload.UserID
	if userID == "" && payload.Subject != "" {
		userID = payload.Subject
	}

	taskID := payload.TaskID

	if userID == "" || taskID == "" {
		utils.SendError(w, http.StatusBadRequest, "Invalid token: missing user_id or taskId")
		return
	}

	// Проверка существования задачи
	task, err := h.db.GetTask(taskID)
	if err != nil {
		utils.SendError(w, http.StatusNotFound, "Task not found")
		return
	}

	if task.UserID != userID {
		utils.SendError(w, http.StatusForbidden, "Access denied")
		return
	}

	// Если задача уже завершена, отправить результат сразу
	if task.Status == "completed" || task.Status == "failed" {
		h.sendImmediateResult(w, task)
		return
	}

	// Парсинг опций polling
	pollInterval := h.parseIntParam(r.URL.Query().Get("pollInterval"), 2000, 1000, 10000)
	heartbeatInterval := h.parseIntParam(r.URL.Query().Get("heartbeatInterval"), 30000, 15000, 60000)
	maxDuration := h.parseIntParam(r.URL.Query().Get("maxDuration"), 300000, 60000, 600000)

	// Настройка SSE headers
	sse.WriteSSEHeaders(w)
	w.WriteHeader(http.StatusOK)

	// Создание SSE клиента
	clientID := uuid.New().String()
	client := sse.NewClient(clientID, userID, taskID, w, func(id string) { h.manager.RemoveClient(id) })
	if client == nil {
		utils.SendError(w, http.StatusInternalServerError, "Failed to create SSE client")
		return
	}

	h.manager.AddClient(client)

	// Следим за разрывом соединения
	go func() {
		<-r.Context().Done()
		client.Close()
	}()

	// Отправка начального heartbeat
	client.Events <- sse.SSEEvent{
		Type: sse.EventHeartbeat,
		Data: map[string]interface{}{
			"message": "Connected to task polling",
			"taskId":  taskID,
		},
		Timestamp: time.Now().UnixMilli(),
	}

	// Канал для уведомления о завершении задачи
	taskDone := make(chan bool, 1)

	// Запуск polling в отдельной goroutine
	go h.pollTask(client, taskID, userID, pollInterval, maxDuration, taskDone)

	// Запуск heartbeat в отдельной goroutine
	go h.sendHeartbeats(client, heartbeatInterval, maxDuration, taskDone)

	// Запуск keepalive в отдельной goroutine
	go h.keepAlive(client, r, taskDone)

	// Запуск клиента (блокирующий)
	client.Run()
}

func (h *SSEHandlers) pollTask(client *sse.Client, taskID, userID string, pollInterval, maxDuration int, taskDone chan bool) {
	startTime := time.Now()
	ticker := time.NewTicker(time.Duration(pollInterval) * time.Millisecond)
	defer ticker.Stop()

	var lastStatus string
	taskCompleted := false

	for {
		select {
		case <-ticker.C:
			// Если задача уже завершена, прекратить polling
			if taskCompleted {
				return
			}

			// Проверка таймаута
			if time.Since(startTime) > time.Duration(maxDuration)*time.Millisecond {
				select {
				case client.Events <- sse.SSEEvent{
					Type: sse.EventError,
					Data: map[string]interface{}{
						"error":           "Polling timeout exceeded",
						"maxDuration":     maxDuration,
						"taskId":          taskID,
						"shouldReconnect": true,
						"reconnectDelay":  1000,
					},
					Timestamp: time.Now().UnixMilli(),
				}:
					// успешно отправили
				default:
					// канал закрыт, не отправляем
				}
				return
			}

			// Получение задачи
			task, err := h.db.GetTask(taskID)
			if err != nil {
				select {
				case client.Events <- sse.SSEEvent{
					Type: sse.EventError,
					Data: map[string]interface{}{
						"error":           "Database error during polling",
						"taskId":          taskID,
						"shouldReconnect": true,
						"reconnectDelay":  1000,
					},
					Timestamp: time.Now().UnixMilli(),
				}:
					// успешно отправили
				default:
					// канал закрыт, не отправляем
				}
				return
			}

			// Проверка изменения статуса
			if task.Status != lastStatus {
				lastStatus = task.Status

				// Если задача завершена - отправляем только финальное событие с результатом
				if task.Status == "completed" {
					client.Events <- sse.SSEEvent{
						Type: sse.EventTaskCompleted,
						Data: map[string]interface{}{
							"taskId":      task.ID,
							"status":      task.Status,
							"result":      task.Result,
							"rating":      task.UserRating,
							"createdAt":   time.Unix(0, task.CreatedAt*int64(time.Millisecond)).Format(time.RFC3339),
							"completedAt": formatTimePtr(task.CompletedAt),
						},
						Timestamp: time.Now().UnixMilli(),
					}
					taskCompleted = true
					// Уведомляем heartbeat о завершении задачи
					select {
					case taskDone <- true:
					default:
					}
					// Закрыть клиент после отправки финального события
					go func() {
						// Явно отправить финальный heartbeat перед закрытием
						select {
						case client.Events <- sse.SSEEvent{
							Type: sse.EventHeartbeat,
							Data: map[string]interface{}{
								"message": "Final heartbeat before close",
								"taskId":  taskID,
							},
							Timestamp: time.Now().UnixMilli(),
						}:
							// успешно отправили
						default:
							// канал закрыт, не отправляем
						}
						time.Sleep(100 * time.Millisecond)
						client.Close()
					}()
					return
				}

				if task.Status == "failed" {
					client.Events <- sse.SSEEvent{
						Type: sse.EventTaskFailed,
						Data: map[string]interface{}{
							"taskId":      task.ID,
							"status":      task.Status,
							"error":       task.ErrorMessage,
							"createdAt":   time.Unix(0, task.CreatedAt*int64(time.Millisecond)).Format(time.RFC3339),
							"completedAt": formatTimePtr(task.CompletedAt),
						},
						Timestamp: time.Now().UnixMilli(),
					}
					taskCompleted = true
					// Уведомляем heartbeat о завершении задачи
					select {
					case taskDone <- true:
					default:
					}
					// Закрыть клиент после отправки финального события
					go func() {
						// time.Sleep(1 * time.Second) // Дать больше времени на отправку
						// Явно отправить финальный heartbeat перед закрытием
						select {
						case client.Events <- sse.SSEEvent{
							Type: sse.EventHeartbeat,
							Data: map[string]interface{}{
								"message": "Final heartbeat before close",
								"taskId":  taskID,
							},
							Timestamp: time.Now().UnixMilli(),
						}:
							// успешно отправили
						default:
							// канал закрыт, не отправляем
						}
						time.Sleep(100 * time.Millisecond)
						client.Close()
					}()
					return
				}

				// Для промежуточных статусов отправляем task_status
				select {
				case client.Events <- sse.SSEEvent{
					Type: sse.EventTaskStatus,
					Data: map[string]interface{}{
						"taskId":              task.ID,
						"status":              task.Status,
						"createdAt":           time.Unix(0, task.CreatedAt*int64(time.Millisecond)).Format(time.RFC3339),
						"updatedAt":           time.Unix(0, task.UpdatedAt*int64(time.Millisecond)).Format(time.RFC3339),
						"processingStartedAt": formatTimePtr(task.ProcessingStartedAt),
					},
					Timestamp: time.Now().UnixMilli(),
				}:
					// успешно отправили
				default:
					// канал закрыт, не отправляем
				}
			}

		case <-client.Done:
			return
		}
	}
}

func (h *SSEHandlers) sendHeartbeats(client *sse.Client, interval, maxDuration int, taskDone chan bool) {
	startTime := time.Now()
	ticker := time.NewTicker(time.Duration(interval) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if time.Since(startTime) > time.Duration(maxDuration)*time.Millisecond {
				return
			}

			select {
			case client.Events <- sse.SSEEvent{
				Type: sse.EventHeartbeat,
				Data: map[string]interface{}{
					"timestamp": time.Now().UnixMilli(),
					"taskId":    client.TaskID,
				},
				Timestamp: time.Now().UnixMilli(),
			}:
				// успешно отправили
			default:
				// канал закрыт, не отправляем
			}

		case <-taskDone:
			// Задача завершена, прекращаем heartbeat
			return

		case <-client.Done:
			return
		}
	}
}

// keepAlive отправляет SSE комментарии для поддержания соединения
func (h *SSEHandlers) keepAlive(client *sse.Client, r *http.Request, taskDone chan bool) {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	// Получаем flusher из клиента
	flusher := client.Flusher

	for {
		select {
		case <-ticker.C:
			// Отправляем SSE комментарий для keepalive
			fmt.Fprintf(client.Writer, ": ping\n\n")
			flusher.Flush()

		case <-taskDone:
			// Задача завершена, прекращаем keepalive
			return

		case <-r.Context().Done():
			// Контекст запроса отменен
			return

		case <-client.Done:
			// Клиент закрыт
			return
		}
	}
}

func (h *SSEHandlers) sendImmediateResult(w http.ResponseWriter, task *database.Task) {
	sse.WriteSSEHeaders(w)
	w.WriteHeader(http.StatusOK)

	var eventData map[string]interface{}
	eventType := sse.EventTaskCompleted

	if task.Status == "completed" {
		eventData = map[string]interface{}{
			"taskId":      task.ID,
			"status":      task.Status,
			"result":      task.Result,
			"rating":      task.UserRating,
			"createdAt":   time.Unix(0, task.CreatedAt*int64(time.Millisecond)).Format(time.RFC3339),
			"completedAt": formatTimePtr(task.CompletedAt),
		}
	} else {
		eventType = sse.EventTaskFailed
		eventData = map[string]interface{}{
			"taskId":      task.ID,
			"status":      task.Status,
			"error":       task.ErrorMessage,
			"createdAt":   time.Unix(0, task.CreatedAt*int64(time.Millisecond)).Format(time.RFC3339),
			"completedAt": formatTimePtr(task.CompletedAt),
		}
	}

	if eventType == "" {
		utils.SendError(w, http.StatusInternalServerError, "Internal error: empty SSE event type")
		return
	}
	event := sse.SSEEvent{
		Type:      eventType,
		Data:      eventData,
		Timestamp: time.Now().UnixMilli(),
	}

	eventJSON, _ := json.Marshal(event)
	fmt.Fprintf(w, "data: %s\n\n", eventJSON)

	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (h *SSEHandlers) parseIntParam(param string, defaultVal, min, max int) int {
	if param == "" {
		return defaultVal
	}

	val, err := strconv.Atoi(param)
	if err != nil {
		return defaultVal
	}

	if val < min {
		return min
	}
	if val > max {
		return max
	}

	return val
}

func formatTimePtr(t *int64) interface{} {
	if t == nil {
		return nil
	}
	return time.Unix(0, *t*int64(time.Millisecond)).Format(time.RFC3339)
}

// GET /api/internal/task-stream - SSE для процессоров
func (h *SSEHandlers) TaskStream(w http.ResponseWriter, r *http.Request) {
	// log.Printf("Received SSE request for task stream: %s", r.URL.String())

	if r.Method != http.MethodGet {
		utils.SendError(w, http.StatusMethodNotAllowed, "Method not allowed. Use GET for SSE.")
		return
	}

	// Поддержка авторизации через Authorization header ИЛИ token в query
	var token string
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		token = strings.TrimPrefix(authHeader, "Bearer ")
	} else {
		token = r.URL.Query().Get("token")
	}

	if token == "" {
		utils.SendError(w, http.StatusUnauthorized, "Missing or invalid Authorization header")
		return
	}

	processorID := r.URL.Query().Get("processor_id")
	if processorID == "" {
		utils.SendError(w, http.StatusBadRequest, "Missing processor_id parameter")
		return
	}

	// Парсинг опций
	heartbeat := h.parseIntParam(r.URL.Query().Get("heartbeat"), 30000, 15000, 60000)
	maxDuration := h.parseIntParam(r.URL.Query().Get("maxDuration"), 3600000, 60000, 7200000)

	// Настройка SSE headers
	sse.WriteSSEHeaders(w)
	w.WriteHeader(http.StatusOK)

	// Создание SSE клиента
	clientID := uuid.New().String()
	client := sse.NewClient(clientID, processorID, "", w, func(id string) { h.manager.RemoveClient(id) })
	if client == nil {
		utils.SendError(w, http.StatusInternalServerError, "Failed to create SSE client")
		return
	}

	h.manager.AddClient(client)

	// Логируем начало соединения
	fmt.Printf("SSE connection started for processor %s at %s\n", processorID, time.Now().Format(time.RFC3339))

	// Следим за разрывом соединения
	go func() {
		<-r.Context().Done()
		fmt.Printf("SSE connection context cancelled for processor %s at %s\n", processorID, time.Now().Format(time.RFC3339))
		client.Close()
	}()

	// Отправка начального соединения
	client.Events <- sse.SSEEvent{
		Type: sse.EventHeartbeat,
		Data: map[string]interface{}{
			"message":        "Connected to task stream",
			"processorId":    processorID,
			"reconnectDelay": 5000,
		},
		Timestamp: time.Now().UnixMilli(),
	}

	// Проверка существующих pending задач
	go h.checkPendingTasks(client)

	// Запуск heartbeat для процессора
	go h.sendProcessorHeartbeats(client, processorID, heartbeat, maxDuration)

	// Запуск keepalive для процессора
	go h.keepAliveProcessor(client, r)

	// Запуск клиента
	client.Run()
}

func (h *SSEHandlers) checkPendingTasks(client *sse.Client) {
	tasks, err := h.db.GetPendingTasks(10)
	if err != nil {
		// log.Printf("checkPendingTasks: error fetching tasks: %v", err)
		return
	}

	// log.Printf("checkPendingTasks: found %d pending tasks for processor %s", len(tasks), client.UserID)

	for _, task := range tasks {
		// log.Printf("checkPendingTasks: sending task %s (priority=%d) to processor %s", task.ID, task.Priority, client.UserID)
		select {
		case client.Events <- sse.SSEEvent{
			Type: sse.EventTaskAvailable,
			Data: map[string]interface{}{
				"taskId":              task.ID,
				"priority":            task.Priority,
				"estimatedComplexity": 3,
				"productData":         task.ProductData,
				"ollamaParams":        task.OllamaParams,
			},
			Timestamp: time.Now().UnixMilli(),
		}:
			// успешно отправили
		default:
			// канал закрыт, не отправляем
		}
	}
}

func (h *SSEHandlers) sendProcessorHeartbeats(client *sse.Client, processorID string, interval, maxDuration int) {
	startTime := time.Now()
	ticker := time.NewTicker(time.Duration(interval) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if time.Since(startTime) > time.Duration(maxDuration)*time.Millisecond {
				select {
				case client.Events <- sse.SSEEvent{
					Type: sse.EventError,
					Data: map[string]interface{}{
						"error":       "Connection timeout exceeded",
						"maxDuration": maxDuration,
						"processorId": processorID,
					},
					Timestamp: time.Now().UnixMilli(),
				}:
					// успешно отправили
				default:
					// канал закрыт, не отправляем
				}
				return
			}

			select {
			case client.Events <- sse.SSEEvent{
				Type: sse.EventHeartbeat,
				Data: map[string]interface{}{
					"processorId": processorID,
					"uptime":      time.Since(startTime).Milliseconds(),
				},
				Timestamp: time.Now().UnixMilli(),
			}:
				// успешно отправили
				// Принудительно сбрасываем буфер после каждого heartbeat
				if client.Flusher != nil {
					client.Flusher.Flush()
				}
			default:
				// канал закрыт, не отправляем
			}

		case <-client.Done:
			return
		}
	}
}

// keepAliveProcessor отправляет SSE комментарии для поддержания соединения процессора
func (h *SSEHandlers) keepAliveProcessor(client *sse.Client, r *http.Request) {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	// Получаем flusher из клиента
	flusher := client.Flusher

	for {
		select {
		case <-ticker.C:
			// Отправляем SSE комментарий для keepalive
			fmt.Fprintf(client.Writer, ": ping\n\n")
			if flusher != nil {
				flusher.Flush()
			}

		case <-r.Context().Done():
			// Контекст запроса отменен
			return

		case <-client.Done:
			// Клиент закрыт
			return
		}
	}
}

// Экспортируем менеджер для интеграции с public.go
func (h *SSEHandlers) Manager() *sse.Manager {
	return h.manager
}
