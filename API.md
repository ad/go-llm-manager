# API документация для manager

## Общая информация

`manager` — сервис управления задачами, пользователями, API, аутентификацией и административным интерфейсом для платформы go-llm. Все публичные и внутренние API реализованы через HTTP/JSON. Аутентификация — через JWT или API-ключ.

## Аутентификация
- **JWT**: для публичных эндпоинтов (`/api/create`, `/api/result`, `/api/get`, SSE polling). Передаётся в заголовке `Authorization: Bearer <token>` или в query-параметре `token`.
- **API-ключ**: для внутренних эндпоинтов (`/api/internal/*`). Передаётся в заголовке `Authorization: Bearer <key>`.

## Коды ошибок
Все ошибки возвращаются в формате JSON с кодом, сообщением и деталями (см. internal/utils/response.go):
```json
{
  "error": "Описание ошибки"
}
```

---

## Публичные эндпоинты

### 1. Health/Информация
- `GET /` — Проверка работоспособности и список основных эндпоинтов.
- `GET /health` — То же, что и `/`.

### 2. Создание задачи (POST /api/create)
- **Параметры задачи берутся только из JWT** (см. internal/database/models.go, JWTPayload):
  - `user_id` (обязателен)
  - `product_data` (обязателен)
  - `priority` (опционально)
  - `ollama_params` (опционально)
  - `rate_limit` (опционально, структура: `{ "max_requests": int, "window_ms": int64 }`)
- Тело запроса — пустое, все параметры должны быть в JWT.
- Пример payload для JWT:
```json
{
  "user_id": "user-123",
  "product_data": "...",
  "priority": 0,
  "ollama_params": { "model": "llama3", "prompt": "..." },
  "rate_limit": { "max_requests": 10, "window_ms": 86400000 }
}
```
- Ответ:
```json
{
  "success": true,
  "taskId": "...",
  "estimatedTime": "...",
  "token": "<result_token>"
}
```

### 3. Получение результата задачи (POST /api/result)
- JWT должен содержать `user_id` и `taskId`.
- Тело запроса — пустое, всё берётся из JWT.
- Ответ:
```json
{
  "success": true,
  "status": "pending|processing|completed|failed",
  "result": "...",
  "createdAt": "...",
  "processedAt": "..."
}
```

### 4. Получение данных пользователя и последней задачи (GET /api/get)
- JWT передаётся в query-параметре `token` (например: `/api/get?token=...`)
- Возвращает данные о пользователе, лимитах запросов и последней задаче.
- Ответ:
```json
{
  "success": true,
  "user_id": "user-123",
  "rate_limit": {
    "request_count": 5,
    "request_limit": 100,
    "window_start": 1719360000000,
    "last_request": 1719400000000,
    "period_start": "2024-06-26T00:00:00Z",
    "period_end": "2024-06-27T00:00:00Z"
  },
  "last_task": {
    "id": "task-456",
    "status": "completed",
    "product_data": "...",
    "priority": 0,
    "result": "...",
    "created_at": "2024-06-26T12:00:00Z",
    "updated_at": "2024-06-26T12:05:00Z",
    "completed_at": "2024-06-26T12:05:00Z",
    "processing_started_at": "2024-06-26T12:02:00Z",
    "ollama_params": { "model": "llama3", "prompt": "..." }
  }
}
```

### 5. Получение статуса задачи (SSE polling)
- `GET /api/result-polling?token=...`
  - Требуется JWT-токен задачи (тот, что возвращается при создании задачи).
  - SSE-соединение, события приходят по мере изменения статуса задачи.
  - Поддерживаются query-параметры:
    - `pollInterval` (мс, по умолчанию 2000, диапазон 1000–10000)
    - `heartbeatInterval` (мс, по умолчанию 30000, диапазон 15000–60000)
    - `maxDuration` (мс, по умолчанию 300000, диапазон 60000–600000)
  - Пример:
    ```bash
    curl -N "http://localhost:8080/api/result-polling?token=<result_token>"
    ```
  - Примеры событий:
    ```json
    { "type": "task_status", "data": { ... }, "timestamp": 1719400000000 }
    { "type": "task_completed", "data": { ... }, "timestamp": 1719400000000 }
    { "type": "task_failed", "data": { ... }, "timestamp": 1719400000000 }
    { "type": "heartbeat", "data": { ... }, "timestamp": 1719400000000 }
    { "type": "error", "data": { ... }, "timestamp": 1719400000000 }
    ```
  - Форматы событий см. internal/database/models.go (SSEEventTaskStatus, SSEEventTaskCompleted и др.).

### 6. Оценка выполнения задачи (POST /api/tasks/{id}/vote)
- **Аутентификация**: JWT токен должен содержать `user_id`.
- **Доступ**: Только владелец задачи может голосовать.
- **Ограничения**: Голосование доступно только для завершенных задач (`status = 'completed'`).
- Тело запроса:
```json
{
  "vote_type": "upvote" | "downvote" | ""
}
```
- **Логика голосования**:
  - `"upvote"` — положительная оценка качества выполнения
  - `"downvote"` — отрицательная оценка качества выполнения
  - `""` (пустая строка) — удаление текущего голоса
  - Повторное голосование тем же типом удаляет голос (toggle behavior)
- Ответ:
```json
{
  "success": true,
  "user_rating": "upvote" | "downvote" | null
}
```
- Примеры использования:
```bash
# Поставить положительную оценку
curl -X POST "http://localhost:8080/api/tasks/task-123/vote" \
  -H "Authorization: Bearer <jwt_token>" \
  -H "Content-Type: application/json" \
  -d '{"vote_type": "upvote"}'

# Убрать оценку (повторный upvote)
curl -X POST "http://localhost:8080/api/tasks/task-123/vote" \
  -H "Authorization: Bearer <jwt_token>" \
  -H "Content-Type: application/json" \
  -d '{"vote_type": "upvote"}'
```

#### 7. HTML-страницы
- `GET /admin` — HTML-страница для администрирования.
- `GET /query` — HTML-страница для тестирования SSE polling.


## Внутренние эндпоинты (требуют API-ключ)

Все эндпоинты требуют заголовок `Authorization: Bearer <api_key>` (см. internal/auth/apikey.go).

### 1. Генерация JWT
- `POST /api/internal/generate-token`
  - Сгенерировать JWT для пользователя или процессора.
  - Тело запроса (пример):
    ```json
    {
      "user_id": "user-123",
      "product_data": "...",
      "priority": 1,
      "ollama_params": { "model": "llama3", "prompt": "..." },
      "rate_limit": { "max_requests": 10, "window_ms": 86400000 },
      "expires_in": 3600
    }
    ```
  - Ответ: `{ "success": true, "token": "...", "expires_in": 3600 }`

### 2. Получение задач
- `GET /api/internal/tasks?limit=20` — Получить pending задачи (по умолчанию 20, максимум 100).
- `GET /api/internal/all-tasks?limit=50&offset=0&user_id=...` — Получить все задачи (фильтрация по user_id, пагинация).
  - Ответ: `{ "tasks": [ ... ] }`

### 3. Claim задач для процессора
- `POST /api/internal/claim`
  - Тело запроса:
    ```json
    {
      "processor_id": "proc-1",           // (string, обязателен) — идентификатор процессора
      "batch_size": 5,                      // (int, опционально, по умолчанию 5) — сколько задач запросить за раз (максимум 20)
      "processor_load": 0.2,                // (float, опционально) — текущая загрузка процессора (0.0–1.0), влияет на fair distribution
      "timeout_ms": 300000,                 // (int64, опционально, по умолчанию 300000) — таймаут обработки задачи в миллисекундах
      "use_fair_distribution": true         // (bool, опционально, по умолчанию false) — использовать ли справедливое распределение задач
    }
    ```
  - Пояснения к параметрам:
    - `processor_id`: уникальный идентификатор процессора/воркера, который запрашивает задачи.
    - `batch_size`: сколько задач выдать процессору за один запрос (чем больше — тем выше нагрузка, максимум 20).
    - `processor_load`: текущая загрузка процессора (например, 0.5 = 50% CPU), влияет на количество выдаваемых задач при fair distribution.
    - `timeout_ms`: сколько миллисекунд задача считается активной до таймаута (обычно 5 минут).
    - `use_fair_distribution`: если true — задачи распределяются с учётом загрузки процессоров и приоритетов.
  - Ответ:
    ```json
    {
      "success": true,
      "tasks": [ ... ],
      "claimed_count": 2,
      "fair_distribution_info": "..." // если использовался fair distribution
    }
    ```
  - Каждый элемент в `tasks` — структура задачи (см. ниже).

### 4. Heartbeat
- `POST /api/internal/heartbeat`
  - Обновляет heartbeat (признак активности) задачи и метрики процессора.
  - Тело запроса:
    ```json
    {
      "taskId": "...",           // (string, обязателен) — идентификатор задачи
      "processor_id": "proc-1", // (string, обязателен) — идентификатор процессора
      "cpu_usage": 0.1,           // (float, опционально) — загрузка CPU процессора (0.0–1.0)
      "memory_usage": 0.2,        // (float, опционально) — загрузка памяти процессора (0.0–1.0)
      "queue_size": 2             // (int, опционально) — размер очереди задач у процессора
    }
    ```
  - Пояснения к параметрам:
    - `taskId`: ID задачи, для которой отправляется heartbeat (обязателен).
    - `processor_id`: ID процессора, который обрабатывает задачу (обязателен).
    - `cpu_usage`, `memory_usage`, `queue_size`: метрики процессора, обновляются если указаны.
  - Если задача не найдена или не принадлежит процессору — возвращается ошибка.

- `POST /api/internal/processor-heartbeat`
  - Обновляет только метрики процессора (без привязки к задаче).
  - Тело запроса:
    ```json
    {
      "processor_id": "proc-1", // (string, обязателен)
      "cpu_usage": 0.1,           // (float, опционально)
      "memory_usage": 0.2,        // (float, опционально)
      "queue_size": 2             // (int, опционально)
    }
    ```
  - Пояснения к параметрам:
    - `processor_id`: ID процессора (обязателен).
    - `cpu_usage`, `memory_usage`, `queue_size`: метрики процессора, обновляются если указаны.

- Оба эндпоинта возвращают `{ "success": true }` при успешном обновлении.

### 5. Завершение задачи
- `POST /api/internal/complete`
  - Тело запроса:
    ```json
    {
      "taskId": "...",
      "processor_id": "proc-1",
      "status": "completed",
      "result": "..."
    }
    ```
  - Или для ошибки:
    ```json
    {
      "taskId": "...",
      "processor_id": "proc-1",
      "status": "failed",
      "error_message": "ошибка"
    }
    ```

### 6. Очистка и статистика
- `POST /api/internal/cleanup`
  - Запускает ручную очистку:
    - Удаляет завершённые (completed/failed) задачи старше 7 дней.
    - Переводит зависшие задачи (processing без heartbeat > 5 минут) обратно в очередь или помечает как failed, если превышен лимит попыток.
    - Очищает устаревшие записи rate-limit и метрик процессоров.
  - Ответ:
    ```json
    {
      "message": "Cleanup completed",
      "stats": { ... },   // статистика до очистки
      "cleaned": {        // что было удалено/переведено
        "tasks": 12,
        "timedout": 2,
        "failed": 1,
        "rateLimits": 5
      }
    }
    ```
  - В случае ошибки возвращает подробное описание ошибки.

- `GET /api/internal/cleanup/stats`
  - Возвращает статистику по задачам и лимитам:
    - Общее количество задач, по статусам (pending, processing, completed, failed)
    - Количество задач старше 7 дней
    - Количество зависших задач (processing без heartbeat > 5 минут)
    - Количество записей rate-limit
  - Ответ:
    ```json
    {
      "success": true,
      "stats": {
        "totalTasks": 100,
        "pendingTasks": 5,
        "processingTasks": 2,
        "completedTasks": 80,
        "failedTasks": 13,
        "tasksOlderThan7Days": 10,
        "timedoutTasks": 1,
        "rateLimitRecords": 7
      }
    }
    ```

- Оба эндпоинта полезны для мониторинга и обслуживания очереди задач, позволяют поддерживать БД в актуальном состоянии и предотвращать накопление мусора.

### 7. Work-stealing
- `POST /api/internal/work-steal`
  - Позволяет процессору "украсть" задачи у перегруженных процессоров для балансировки нагрузки.
  - Тело запроса:
    ```json
    {
      "processor_id": "proc-2",      // (string, обязателен) — идентификатор процессора, который инициирует кражу
      "max_steal_count": 2,            // (int, опционально, по умолчанию 2, максимум 5) — сколько задач можно украсть за раз
      "timeout_ms": 300000             // (int64, опционально, по умолчанию 300000) — таймаут для украденных задач
    }
    ```
  - Пояснения к параметрам:
    - `processor_id`: ID процессора, который хочет получить дополнительные задачи.
    - `max_steal_count`: максимальное количество задач для кражи (чтобы не перегружать процессор).
    - `timeout_ms`: сколько миллисекунд украденная задача будет считаться активной.
  - Логика:
    - Задачи выбираются только у процессоров, у которых активных задач > 5 и которые давно не обновляли heartbeat по этим задачам.
    - После кражи задачи перепривязываются к новому процессору и получают новый таймаут.
  - Ответ:
    ```json
    {
      "success": true,
      "stolen_tasks": [ ... ], // массив задач, которые были украдены
      "stolen_count": 1
    }
    ```
  - Каждый элемент в `stolen_tasks` — структура задачи (см. ниже).
  - Если задач для кражи нет — возвращается пустой массив.

### 8. Метрики и оценка времени
- `GET /api/internal/metrics` — Метрики процессоров.
- `GET /api/internal/estimated-time` — Оценка времени ожидания новой задачи.

### 9. SSE для процессоров
- `GET /api/internal/task-stream?processor_id=...&token=...`
  - SSE-соединение для процессоров, события о новых задачах и heartbeat.
  - Поддерживаются query-параметры:
    - `heartbeat` (мс, по умолчанию 30000)
    - `maxDuration` (мс, по умолчанию 3600000)
  - Примеры событий: `task_available`, `heartbeat`, `error`.

### 10. Requeue задачи
- `POST /api/internal/requeue`
  - Тело запроса:
    ```json
    {
      "taskId": "...",
      "processor_id": "proc-1",
      "reason": "manual requeue"
    }
    ```
  - Возвращает задачу в пул (например, при сбое воркера).

### 11. Статистика рейтингов
- `GET /api/internal/rating-stats` — Получить статистику голосований по задачам.
- `GET /api/internal/rating-stats?user_id=<user_id>` — Получить статистику голосований для конкретного пользователя.
- Ответ (глобальная статистика):
```json
{
  "success": true,
  "total_rated": 15,
  "upvotes": 10,
  "downvotes": 5
}
```
- Ответ (статистика пользователя):
```json
{
  "success": true,
  "user_id": "user-123",
  "total_rated": 3,
  "upvotes": 2,
  "downvotes": 1,
  "tasks": [
    {
      "id": "task-456",
      "status": "completed",
      "user_rating": "upvote",
      "created_at": "2024-06-26T12:00:00Z",
      ...
    }
  ]
}
```

---

## Пример структуры задачи

```json
{
  "id": "string",
  "user_id": "string",
  "product_data": "string",
  "status": "pending|processing|completed|failed",
  "result": "string|null",
  "error_message": "string|null",
  "created_at": 1719400000000,
  "priority": 0,
  "ollama_params": "{...}",
  "user_rating": "upvote|downvote|null"
}
```

---

## SSE события

- `task_status`, `task_completed`, `task_failed`, `heartbeat`, `error`, `task_available` (см. internal/database/models.go).

---

## Примеры JWT для задач

### JWT для создания задачи
```json
{
  "user_id": "user-123",
  "product_data": "Hello world!",
  "priority": 1,
  "ollama_params": { "model": "llama3", "prompt": "..." },
  "rate_limit": { "max_requests": 10, "window_ms": 86400000 },
  "iss": "llm-proxy",
  "aud": "llm-proxy-api",
  "sub": "user-123",
  "exp": 1719500000
}
```

### JWT для получения результата
```json
{
  "user_id": "user-123",
  "taskId": "...",
  "iss": "llm-proxy",
  "aud": "llm-proxy-api",
  "sub": "user-123",
  "exp": 1719500000
}
```

---

## Подробнее
- Структуры: internal/database/models.go
- Ошибки: internal/utils/response.go
- SSE: internal/worker/sse_client.go, internal/database/models.go
