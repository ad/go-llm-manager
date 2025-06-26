# go-llm manager

## Описание
`manager` — сервис управления задачами, API, аутентификацией и административным интерфейсом для платформы go-llm. Предоставляет HTTP API (JWT и API-ключи), SSE для стриминга статусов задач, административный web-интерфейс и внутренние API для процессоров.

## Структура проекта
```
manager/
├── cmd/server/main.go         # Точка входа HTTP-сервера
├── internal/
│   ├── api/handlers/          # HTTP-обработчики (REST, SSE, admin)
│   ├── auth/                  # Аутентификация (API-ключи, JWT)
│   ├── config/                # Загрузка и валидация конфигов
│   ├── database/              # Модели, работа с SQLite, бизнес-логика задач
│   ├── middleware/            # HTTP middleware
│   ├── sse/                   # SSE-менеджер
│   └── utils/                 # Ответы, ошибки, утилиты
├── data/                      # Файлы БД
├── migrations/                # SQL-миграции
├── Dockerfile, Makefile       # Сборка и запуск
├── README.md                  # Документация
└── API.md                     # Документация API
```

## Основные возможности
- Создание задач через JWT (POST /api/create, параметры только из JWT)
- Получение результата задачи (POST /api/result, параметры только из JWT)
- SSE polling статуса задачи (`/api/result-polling?token=...`)
- Встроенный web-интерфейс администратора (`/admin`, `/admin.js`, `/admin.css`)
- Внутренние API для процессоров: claim, heartbeat, complete, work-stealing, очистка, метрики
- SQLite для хранения задач и метаданных
- Чистая архитектура, явная обработка ошибок, без глобальных переменных состояния

## Быстрый старт

```bash
make dev
```

## Переменные окружения

| Переменная                | Назначение                                 | Значение по умолчанию         |
|---------------------------|--------------------------------------------|-------------------------------|
| HOST                      | Адрес для HTTP-сервера                     | 0.0.0.0                       |
| PORT                      | Порт для HTTP-сервера                      | 8080                          |
| DB_PATH                   | Путь к SQLite-БД                           | ./data/llm-proxy.db           |
| MIGRATIONS_PATH           | Путь к SQL-миграциям                       | ./migrations                  |
| JWT_SECRET                | Секрет для подписи JWT                     | dev-secret-key                |
| INTERNAL_API_KEY          | Ключ для внутренних API                    | dev-internal-key              |
| RATE_LIMIT_WINDOW         | Окно лимита запросов (мс)                  | 86400000                      |
| RATE_LIMIT_MAX_REQUESTS   | Максимум запросов в окне                   | 100                           |
| CLEANUP_ENABLED           | Включить автоматическую очистку            | true                          |
| CLEANUP_DAYS              | Сколько дней хранить завершённые задачи    | 7                             |
| TASK_TIMEOUT_MINUTES      | Таймаут задачи (минуты)                    | 30                            |
| SSE_HEARTBEAT_INTERVAL    | Интервал heartbeat для SSE (Go duration)   | 30s                           |
| SSE_CLIENT_TIMEOUT        | Таймаут SSE-клиента (Go duration)          | 5m                            |

Все переменные можно переопределять через окружение или конфиг-файл (см. internal/config/config.go).

## Тесты и линтинг
```bash
make test
make lint
```

## Безопасность
- Все публичные и внутренние эндпоинты требуют аутентификации (JWT или API-ключ)
- Не храните секреты в публичных репозиториях
- Не используйте глобальные переменные для состояния задач или пользователей

## Документация и ссылки
- [API.md](./API.md) — подробное описание всех эндпоинтов, форматов JWT, SSE, ошибок
- [internal/database/models.go](./internal/database/models.go) — структуры задач, параметры, модели
- [internal/utils/response.go](./internal/utils/response.go) — формат ошибок и ответов
- [internal/api/handlers/](./internal/api/handlers/) — реализация всех HTTP-эндпоинтов

## Лицензия
MIT