#!/bin/bash

# 🚀 Production Load Test - LLM Proxy
# Нагрузочный тест для production среды: одновременное создание задач от разных пользователей
#
# ИСПОЛЬЗОВАНИЕ:
# Настройте переменные окружения или отредактируйте файл:
#   export PRODUCTION_HOST="https://your-host.com"
#   export INTERNAL_AUTH_KEY="your-key"
#   export TEST_MODEL="gemma3:1b"
#   export TOTAL_TASKS=50
#   export CONCURRENT_USERS=10
#   ./test-production-load.sh
#
set -e

source .env

# Проверка зависимостей
if ! command -v jq &> /dev/null; then
    echo "❌ Утилита 'jq' не найдена. Установите её для продолжения:"
    echo "   macOS: brew install jq"
    echo "   Ubuntu/Debian: sudo apt-get install jq"
    echo "   CentOS/RHEL: sudo yum install jq"
    exit 1
fi

if ! command -v curl &> /dev/null; then
    echo "❌ Утилита 'curl' не найдена. Установите её для продолжения."
    exit 1
fi

# ===== КОНФИГУРАЦИЯ =====
# Основные параметры теста
TOTAL_TASKS=${TOTAL_TASKS:-100}                    # Общее количество задач для создания
CONCURRENT_USERS=${CONCURRENT_USERS:-100}          # Количество одновременных пользователей
BATCH_SIZE=1                                       # Количество задач на пользователя (TOTAL_TASKS / CONCURRENT_USERS)

# Настройки сервера
PRODUCTION_HOST=${PRODUCTION_HOST:-"https://your-production-host.com"}  # URL production сервера
INTERNAL_AUTH_KEY=${INTERNAL_AUTH_KEY:-"your-production-internal-key"}  # Ключ для internal API
TIMEOUT_SECONDS=${TIMEOUT_SECONDS:-600}            # Таймаут ожидания завершения всех задач (10 минут)

# Настройки тестовых задач
TEST_MODEL=${TEST_MODEL:-"gemma3:1b"}              # Модель для тестирования
PRIORITY_RANGE=${PRIORITY_RANGE:-3}               # Диапазон приоритетов (0-2)
MAX_TOKENS=${MAX_TOKENS:-1000}                    # Максимальное количество токенов
TEMPERATURE=${TEMPERATURE:-0.7}                   # Температура для генерации
RATE_LIMIT_REQUESTS=${RATE_LIMIT_REQUESTS:-100}   # Лимит запросов на пользователя
RATE_LIMIT_WINDOW_HOURS=${RATE_LIMIT_WINDOW_HOURS:-24}  # Окно лимита в часах

# ===== ЦВЕТА ДЛЯ ВЫВОДА =====
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
NC='\033[0m'

# ===== ВСПОМОГАТЕЛЬНЫЕ ФУНКЦИИ =====

# Логирование с временными метками
log() {
    echo -e "${CYAN}[$(date '+%H:%M:%S')]${NC} $1"
}

log_error() {
    echo -e "${RED}[$(date '+%H:%M:%S')] ERROR:${NC} $1"
}

log_success() {
    echo -e "${GREEN}[$(date '+%H:%M:%S')] SUCCESS:${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[$(date '+%H:%M:%S')] WARNING:${NC} $1"
}

# Проверка доступности сервиса
check_service_health() {
    log "Проверка доступности сервиса: $PRODUCTION_HOST"
    
    # Сначала пробуем /health, потом корневой endpoint
    local health_response=$(curl -s --max-time 10 "$PRODUCTION_HOST/health" 2>/dev/null)
    if echo "$health_response" | jq -e '.status | test("ok")' > /dev/null 2>&1; then
        log_success "Сервис доступен и работает"
        return 0
    fi
    
    # Если /health не работает, проверяем корневой endpoint
    health_response=$(curl -s --max-time 10 "$PRODUCTION_HOST/" 2>/dev/null)
    if echo "$health_response" | jq -e '.status | test("ok")' > /dev/null 2>&1; then
        log_success "Сервис доступен и работает"
        return 0
    elif echo "$health_response" | jq -e '.message' > /dev/null 2>&1; then
        # Если есть message, значит API отвечает
        log_success "Сервис доступен (обнаружен по корневому endpoint)"
        return 0
    else
        log_error "Сервис недоступен или не отвечает"
        echo "Ответ: $health_response"
        return 1
    fi
}

# Создание уникального пользователя и получение JWT токена
create_user_token() {
    local user_id="load_test_user_${1}_$(date +%s)_$$"
    local priority=$((RANDOM % PRIORITY_RANGE))
    
    local jwt_response=$(curl -s --max-time 10 -X POST "$PRODUCTION_HOST/api/internal/generate-token" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $INTERNAL_AUTH_KEY" \
        -d "{
            \"user_id\": \"$user_id\",
            \"product_data\": \"Production load test - user $1 - model $TEST_MODEL\",
            \"priority\": $priority,
            \"ollama_params\": {
                \"model\": \"$TEST_MODEL\",
                \"temperature\": $TEMPERATURE,
                \"max_tokens\": $MAX_TOKENS
            },
            \"rate_limit\": {
                \"max_requests\": $RATE_LIMIT_REQUESTS,
                \"window_ms\": $((RATE_LIMIT_WINDOW_HOURS * 3600 * 1000))
            }
        }" 2>/dev/null)
    
    if [ -z "$jwt_response" ]; then
        echo "ERROR:Empty response from server"
        return 1
    fi
    
    local token=$(echo "$jwt_response" | jq -r '.token // empty' 2>/dev/null)
    if [ -z "$token" ] || [ "$token" = "null" ]; then
        local error=$(echo "$jwt_response" | jq -r '.error // "Unknown error"' 2>/dev/null)
        echo "ERROR:$error"
        return 1
    fi
    
    echo "$token"
}

# Создание задачи для пользователя
create_task() {
    local token="$1"
    local user_num="$2"
    
    # /api/create не принимает данные в теле - все берется из JWT токена
    local task_response=$(curl -s --max-time 15 -X POST "$PRODUCTION_HOST/api/create" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $token" \
        -d '{}' 2>/dev/null)
    
    if [ -z "$task_response" ]; then
        echo "ERROR:Empty response from server"
        return 1
    fi
    
    local success=$(echo "$task_response" | jq -r '.success // false' 2>/dev/null)
    local task_id=$(echo "$task_response" | jq -r '.taskId // empty' 2>/dev/null)
    local result_token=$(echo "$task_response" | jq -r '.token // empty' 2>/dev/null)
    
    if [ "$success" = "true" ] && [ ! -z "$task_id" ] && [ "$task_id" != "null" ] && [ ! -z "$result_token" ] && [ "$result_token" != "null" ]; then
        echo "$task_id:$result_token"
        return 0
    else
        local error=$(echo "$task_response" | jq -r '.error // "Unknown error"' 2>/dev/null)
        echo "ERROR:$error"
        return 1
    fi
}

# Получение всех задач из internal API с поддержкой пагинации
get_all_tasks() {
    local requested_limit="${1:-10000}"  # Лимит, который хочет получить пользователь
    local max_api_limit=1000             # Максимальный лимит API (из кода сервера)
    
    # Если запрошенный лимит меньше максимального, используем его
    local limit_per_request=$max_api_limit
    if [ $requested_limit -lt $max_api_limit ]; then
        limit_per_request=$requested_limit
    fi
    
    local all_tasks_json='{"tasks": []}'
    local offset=0
    local total_fetched=0
    
    while [ $total_fetched -lt $requested_limit ]; do
        # Вычисляем лимит для текущего запроса
        local remaining=$((requested_limit - total_fetched))
        local current_limit=$limit_per_request
        if [ $remaining -lt $current_limit ]; then
            current_limit=$remaining
        fi
        
        local tasks_response=$(curl -s --max-time 30 -X GET "$PRODUCTION_HOST/api/internal/all-tasks?limit=$current_limit&offset=$offset" \
            -H "Content-Type: application/json" \
            -H "Authorization: Bearer $INTERNAL_AUTH_KEY" 2>/dev/null)
        
        if [ -z "$tasks_response" ]; then
            echo "ERROR:Empty response from server (offset=$offset, limit=$current_limit)"
            return 1
        fi
        
        # Проверяем, что ответ валидный JSON и содержит массив задач
        local batch_tasks=$(echo "$tasks_response" | jq -r '.tasks // empty' 2>/dev/null)
        if [ -z "$batch_tasks" ] || [ "$batch_tasks" = "null" ]; then
            local error=$(echo "$tasks_response" | jq -r '.error // "Failed to get tasks"' 2>/dev/null)
            echo "ERROR:$error (offset=$offset, limit=$current_limit)"
            return 1
        fi
        
        # Считаем количество полученных задач в этом запросе
        local batch_count=$(echo "$tasks_response" | jq -r '.tasks | length' 2>/dev/null)
        if [ -z "$batch_count" ] || [ "$batch_count" = "null" ]; then
            batch_count=0
        fi
        
        # Если получили 0 задач, значит достигли конца
        if [ $batch_count -eq 0 ]; then
            break
        fi
        
        # Объединяем задачи
        all_tasks_json=$(echo "$all_tasks_json $tasks_response" | jq -s '{"tasks": (.[0].tasks + .[1].tasks)}' 2>/dev/null)
        if [ $? -ne 0 ]; then
            echo "ERROR:Failed to merge tasks JSON"
            return 1
        fi
        
        total_fetched=$((total_fetched + batch_count))
        offset=$((offset + batch_count))
        
        # Если получили меньше задач, чем запрашивали, значит это последняя страница
        if [ $batch_count -lt $current_limit ]; then
            break
        fi
        
        # Небольшая пауза между запросами для снижения нагрузки
        sleep 0.1
    done
    
    echo "$all_tasks_json"
}

# Получение статуса конкретной задачи из массива всех задач
get_task_status_from_all_tasks() {
    local task_id="$1"
    local all_tasks_json="$2"
    
    # Ищем задачу по ID в массиве задач
    local task_status=$(echo "$all_tasks_json" | jq -r ".tasks[] | select(.id == \"$task_id\") | .status // \"not_found\"" 2>/dev/null)
    
    if [ -z "$task_status" ] || [ "$task_status" = "null" ] || [ "$task_status" = "not_found" ]; then
        echo "unknown"
        return 1
    fi
    
    echo "$task_status"
}

# Фильтрация задач теста из всего массива задач
filter_test_tasks() {
    local all_tasks_json="$1"
    local test_task_ids="$2"  # Массив ID задач теста (через пробел)
    
    # Создаем JSON массив из ID задач теста
    local task_ids_json=$(echo "$test_task_ids" | tr ' ' '\n' | jq -R . | jq -s .)
    
    # Фильтруем только наши задачи из общего списка
    local filtered_tasks=$(echo "$all_tasks_json" | jq --argjson ids "$task_ids_json" '.tasks | map(select(.id as $id | $ids | index($id)))' 2>/dev/null)
    
    echo "$filtered_tasks"
}

# Проверка статуса задачи (устаревшая функция для совместимости)
check_task_status() {
    local result_token="$1"
    local task_id="$2"
    
    local status_response=$(curl -s --max-time 10 -X POST "$PRODUCTION_HOST/api/result" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $result_token" 2>/dev/null)
    
    if [ -z "$status_response" ]; then
        echo "unknown"
        return 1
    fi
    
    # Проверяем, что ответ валидный JSON
    local status=$(echo "$status_response" | jq -r '.status // "unknown"' 2>/dev/null)
    
    # Если статус unknown, попробуем найти ошибку в ответе
    if [ "$status" = "unknown" ] || [ "$status" = "null" ]; then
        local error=$(echo "$status_response" | jq -r '.error // empty' 2>/dev/null)
        if [ ! -z "$error" ] && [ "$error" != "null" ]; then
            # Если есть ошибка, выводим её для отладки (только первые несколько раз)
            if [ "${DEBUG_ERRORS_SHOWN:-0}" -lt 3 ]; then
                log_warning "Task $task_id error: $error"
                export DEBUG_ERRORS_SHOWN=$((${DEBUG_ERRORS_SHOWN:-0} + 1))
            fi
            echo "failed"
            return 0
        fi
        echo "unknown"
        return 1
    fi
    
    echo "$status"
}

# Получение полной информации о задаче (для отладки)
get_task_info() {
    local result_token="$1"
    local task_id="$2"
    
    local status_response=$(curl -s --max-time 10 -X POST "$PRODUCTION_HOST/api/result" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $result_token" 2>/dev/null)
    
    if [ -z "$status_response" ]; then
        echo "{\"status\": \"unknown\", \"error\": \"Empty response\"}"
        return 1
    fi
    
    echo "$status_response"
}

# Функция для одного пользователя (создает несколько задач)
user_worker() {
    local user_num="$1"
    local tasks_per_user="$2"
    local start_time="$3"
    
    # Создаем токен для пользователя
    local token=$(create_user_token "$user_num")
    if [[ "$token" == ERROR:* ]]; then
        log_error "User $user_num: Не удалось создать токен"
        return 1
    fi
    
    local created_tasks=()
    local user_start=$(date +%s.%3N)
    
    # Создаем задачи для этого пользователя
    for task_num in $(seq 1 $tasks_per_user); do
        local task_result=$(create_task "$token" "$user_num")
        if [[ "$task_result" == ERROR:* ]]; then
            log_error "User $user_num: Ошибка создания задачи $task_num: ${task_result#ERROR:}"
            continue
        fi
        
        # Разбираем результат: task_id:result_token
        local task_id=$(echo "$task_result" | cut -d: -f1)
        local result_token=$(echo "$task_result" | cut -d: -f2)
        
        # Проверяем, что результат содержит корректные данные
        if [ -z "$task_id" ] || [ "$task_id" = "null" ] || [ -z "$result_token" ] || [ "$result_token" = "null" ]; then
            log_error "User $user_num: Некорректный результат для задачи $task_num: '$task_result'"
            continue
        fi
        
        created_tasks+=("$task_id:$result_token")
        echo -n -e "${GREEN}✓${NC}"
    done
    
    local user_create_end=$(date +%s.%3N)
    local user_create_time=$(echo "$user_create_end $user_start" | awk '{printf "%.2f", $1 - $2}')
    
    # Сохраняем информацию о пользователе и его задачах
    # Используем разделитель |, поскольку JWT токены содержат :
    echo "$user_num|$token|${created_tasks[*]}|$user_create_time" >> "/tmp/load_test_users_$$.txt"
    
    log "User $user_num: создал ${#created_tasks[@]} задач за ${user_create_time}s"
}

# ===== ОСНОВНОЙ ТЕСТ =====

echo -e "${BLUE}🚀 Production Load Test - LLM Proxy${NC}"
echo "============================================"

# Проверка конфигурации
log "Проверка конфигурации..."
if [ "$PRODUCTION_HOST" = "https://your-production-host.com" ]; then
    log_error "PRODUCTION_HOST не настроен! Измените значение в скрипте."
    exit 1
fi

if [ "$INTERNAL_AUTH_KEY" = "your-production-internal-key" ]; then
    log_error "INTERNAL_AUTH_KEY не настроен! Измените значение в скрипте."
    exit 1
fi

if [ -z "$PRODUCTION_HOST" ] || [ -z "$INTERNAL_AUTH_KEY" ]; then
    log_error "Необходимо настроить PRODUCTION_HOST и INTERNAL_AUTH_KEY"
    exit 1
fi

echo -e "${CYAN}📋 Конфигурация теста:${NC}"
echo "  • Всего задач: $TOTAL_TASKS"
echo "  • Одновременных пользователей: $CONCURRENT_USERS"
echo "  • Задач на пользователя: $((TOTAL_TASKS / CONCURRENT_USERS))"
echo "  • Production host: $PRODUCTION_HOST"
echo "  • Модель: $TEST_MODEL"
echo "  • Температура: $TEMPERATURE"
echo "  • Макс. токенов: $MAX_TOKENS"
echo "  • Rate limit: $RATE_LIMIT_REQUESTS req/${RATE_LIMIT_WINDOW_HOURS}h"
echo "  • Таймаут: ${TIMEOUT_SECONDS}s"
echo

# Проверяем доступность сервиса
if ! check_service_health; then
    exit 1
fi

# Проверяем доступность internal API для получения задач
log "Проверка доступности internal API /all-tasks..."
test_all_tasks_response=$(get_all_tasks 1)  # Проверяем с лимитом 1 задача
if [[ "$test_all_tasks_response" == ERROR:* ]]; then
    log_warning "Internal API /all-tasks недоступен: ${test_all_tasks_response#ERROR:}"
    log_warning "Будет использован fallback к индивидуальным запросам статусов"
else
    log_success "Internal API /all-tasks доступен"
    # Проверим, сколько задач в системе
    total_tasks_in_system=$(echo "$test_all_tasks_response" | jq -r '.tasks | length' 2>/dev/null)
    if [ ! -z "$total_tasks_in_system" ] && [ "$total_tasks_in_system" != "null" ]; then
        log "В системе найдено задач: $total_tasks_in_system (показано максимум 1 для теста)"
    fi
fi

# Вычисляем задачи на пользователя
TASKS_PER_USER=$((TOTAL_TASKS / CONCURRENT_USERS))
if [ $((TASKS_PER_USER * CONCURRENT_USERS)) -lt $TOTAL_TASKS ]; then
    TASKS_PER_USER=$((TASKS_PER_USER + 1))
fi

log "Будет создано $CONCURRENT_USERS пользователей по $TASKS_PER_USER задач"

# Очищаем временные файлы
rm -f "/tmp/load_test_users_$$.txt"

# ЭТАП 1: Одновременное создание задач
echo -e "${BLUE}=== ЭТАП 1: Одновременное создание задач ===${NC}"
test_start=$(date +%s.%3N)

log "Запуск $CONCURRENT_USERS пользователей..."
pids=()

for user_num in $(seq 1 $CONCURRENT_USERS); do
    user_worker "$user_num" "$TASKS_PER_USER" "$test_start" &
    pids+=($!)
    
    # Небольшая задержка между запуском пользователей
    sleep 0.1
done

# Ждем завершения всех пользователей
log "Ожидание завершения создания задач..."
for pid in "${pids[@]}"; do
    wait $pid
done

creation_end=$(date +%s.%3N)
creation_time=$(echo "$creation_end $test_start" | awk '{printf "%.2f", $1 - $2}')

echo
log_success "Все пользователи завершили создание задач за ${creation_time}s"

# Подсчитываем созданные задачи
if [ ! -f "/tmp/load_test_users_$$.txt" ]; then
    log_error "Файл с данными пользователей не найден"
    exit 1
fi

total_created=0
all_tasks=()
user_tokens=()

while IFS='|' read -r user_num token tasks_str create_time; do
    IFS=' ' read -ra tasks <<< "$tasks_str"
    total_created=$((total_created + ${#tasks[@]}))
    all_tasks+=("${tasks[@]}")
    user_tokens["$user_num"]="$token"
    
    log "User $user_num: ${#tasks[@]} задач, время создания: ${create_time}s"
done < "/tmp/load_test_users_$$.txt"

echo
echo -e "${CYAN}📊 Результаты создания:${NC}"
echo "  • Планировалось: $TOTAL_TASKS задач"
echo "  • Создано: $total_created задач"
echo "  • Время создания: ${creation_time}s"
if [ $(echo "$creation_time" | awk '{print ($1 > 0)}') -eq 1 ]; then
    echo "  • Скорость: $(echo "$total_created $creation_time" | awk '{printf "%.1f", $1 / $2}') задач/сек"
else
    echo "  • Скорость: N/A (время создания 0s)"
fi

# Диагностическая проверка: проверим статус первой созданной задачи
if [ ${#all_tasks[@]} -gt 0 ]; then
    first_task="${all_tasks[0]}"
    first_task_id=$(echo "$first_task" | cut -d: -f1)
    first_result_token=$(echo "$first_task" | cut -d: -f2)
    
    log "🔍 Диагностика: проверка статуса первой задачи ($first_task_id)..."
    
    # Сначала пробуем получить статус через internal API
    diagnostic_all_tasks=$(get_all_tasks 1000)  # Разумный лимит для диагностики
    if [[ "$diagnostic_all_tasks" != ERROR:* ]]; then
        diagnostic_tasks_count=$(echo "$diagnostic_all_tasks" | jq -r '.tasks | length' 2>/dev/null)
        log "Получено $diagnostic_tasks_count задач для диагностики"
        
        first_status=$(get_task_status_from_all_tasks "$first_task_id" "$diagnostic_all_tasks")
        log "Статус первой задачи (через internal API): $first_status"
        
        # Получим дополнительную информацию о задаче
        first_task_info=$(echo "$diagnostic_all_tasks" | jq ".tasks[] | select(.id == \"$first_task_id\")" 2>/dev/null)
        if [ ! -z "$first_task_info" ] && [ "$first_task_info" != "null" ]; then
            created_at=$(echo "$first_task_info" | jq -r '.created_at // "N/A"')
            priority=$(echo "$first_task_info" | jq -r '.priority // "N/A"')
            user_id=$(echo "$first_task_info" | jq -r '.user_id // "N/A"')
            log "Детали задачи: создана=$created_at, приоритет=$priority, пользователь=$user_id"
        fi
    else
        log_warning "Internal API недоступен, используем result API"
        if [ ! -z "$first_result_token" ] && [ "$first_result_token" != "null" ]; then
            first_status=$(check_task_status "$first_result_token" "$first_task_id")
            log "Статус первой задачи (через result API): $first_status"
            
            # Получим полную информацию для отладки
            first_info=$(get_task_info "$first_result_token" "$first_task_id")
            log "Полная информация: $first_info"
        else
            log_warning "У первой задачи отсутствует result_token!"
        fi
    fi
fi

if [ $total_created -eq 0 ]; then
    log_error "Не создано ни одной задачи. Тест остановлен."
    exit 1
fi

# ЭТАП 2: Мониторинг выполнения
echo -e "${BLUE}=== ЭТАП 2: Мониторинг выполнения задач ===${NC}"

log "Начало мониторинга $total_created задач (таймаут: ${TIMEOUT_SECONDS}s)"

# Собираем все ID задач теста для фильтрации
test_task_ids=()
for task_entry in "${all_tasks[@]}"; do
    task_id=$(echo "$task_entry" | cut -d: -f1)
    test_task_ids+=("$task_id")
done

# Преобразуем в строку для передачи в функции
test_task_ids_str="${test_task_ids[*]}"

completed_tasks=()
failed_tasks=()
monitoring_start=$(date +%s)
last_status_time=$monitoring_start

while [ ${#completed_tasks[@]} -lt $total_created ]; do
    current_time=$(date +%s)
    elapsed=$((current_time - monitoring_start))
    
    # Проверка таймаута
    if [ $elapsed -ge $TIMEOUT_SECONDS ]; then
        log_warning "Достигнут таймаут ${TIMEOUT_SECONDS}s"
        break
    fi
    
    # Проверяем статус каждые 10 секунд
    if [ $((current_time - last_status_time)) -ge 10 ]; then
        log "Получение актуальных статусов задач через internal API..."
        
        # Определяем разумный лимит: минимум из 5000 или в 3 раза больше наших задач
        api_limit=$((total_created * 3))
        if [ $api_limit -lt 5000 ]; then
            api_limit=5000
        fi
        
        # Получаем все задачи через internal API
        all_tasks_response=$(get_all_tasks $api_limit)
        
        if [[ "$all_tasks_response" == ERROR:* ]]; then
            log_warning "Ошибка получения задач: ${all_tasks_response#ERROR:}"
            log "Используем fallback к индивидуальным запросам..."
            
            # Fallback: проверяем выборочно через старый метод
            pending_count=0
            processing_count=0
            checked_count=0
            sample_size=10
            check_interval=1
            
            if [ ${#all_tasks[@]} -gt 50 ]; then
                check_interval=$((${#all_tasks[@]} / sample_size))
                if [ $check_interval -lt 1 ]; then
                    check_interval=1
                fi
            fi
            
            for ((i=0; i<${#all_tasks[@]}; i+=check_interval)); do
                task_entry="${all_tasks[$i]}"
                task_id=$(echo "$task_entry" | cut -d: -f1)
                result_token=$(echo "$task_entry" | cut -d: -f2)
                
                # Пропускаем уже завершенные/проваленные
                if [[ " ${completed_tasks[@]} " =~ " $task_id " ]] || [[ " ${failed_tasks[@]} " =~ " $task_id " ]]; then
                    continue
                fi
                
                if [ ! -z "$result_token" ] && [ "$result_token" != "null" ]; then
                    status=$(check_task_status "$result_token" "$task_id")
                    checked_count=$((checked_count + 1))
                    case "$status" in
                        "completed")
                            completed_tasks+=("$task_id")
                            ;;
                        "failed")
                            failed_tasks+=("$task_id")
                            ;;
                        "processing")
                            processing_count=$((processing_count + 1))
                            ;;
                        "pending")
                            pending_count=$((pending_count + 1))
                            ;;
                        *)
                            pending_count=$((pending_count + 1))
                            ;;
                    esac
                else
                    failed_tasks+=("$task_id")
                fi
            done
            
            # Обновляем счетчики
            completed_count=${#completed_tasks[@]}
            failed_count=${#failed_tasks[@]}
            
            # Для оставшихся непроверенных задач считаем их как pending
            unchecked_tasks=$((total_created - completed_count - failed_count - processing_count - pending_count))
            if [ $unchecked_tasks -gt 0 ]; then
                pending_count=$((pending_count + unchecked_tasks))
            fi
        else
            # Успешно получили данные через internal API
            api_tasks_count=$(echo "$all_tasks_response" | jq -r '.tasks | length' 2>/dev/null)
            log "Получено $api_tasks_count задач через internal API, фильтрация задач теста..."
            
            # Фильтруем только наши задачи из общего списка
            filtered_tasks=$(filter_test_tasks "$all_tasks_response" "$test_task_ids_str")
            
            # Сбрасываем счетчики
            pending_count=0
            processing_count=0
            
            # Очищаем списки завершенных и проваленных задач для пересчета
            completed_tasks=()
            failed_tasks=()
            
            # Подсчитываем статусы из отфильтрованных задач
            while IFS= read -r task_json; do
                if [ ! -z "$task_json" ] && [ "$task_json" != "null" ]; then
                    task_id=$(echo "$task_json" | jq -r '.id // empty' 2>/dev/null)
                    status=$(echo "$task_json" | jq -r '.status // "unknown"' 2>/dev/null)
                    
                    if [ ! -z "$task_id" ] && [ "$task_id" != "null" ]; then
                        case "$status" in
                            "completed")
                                completed_tasks+=("$task_id")
                                ;;
                            "failed")
                                failed_tasks+=("$task_id")
                                ;;
                            "processing")
                                processing_count=$((processing_count + 1))
                                ;;
                            "pending")
                                pending_count=$((pending_count + 1))
                                ;;
                            *)
                                pending_count=$((pending_count + 1))
                                ;;
                        esac
                    fi
                fi
            done <<< "$(echo "$filtered_tasks" | jq -c '.[]?' 2>/dev/null)"
            
            completed_count=${#completed_tasks[@]}
            failed_count=${#failed_tasks[@]}
            
            log "Обработано данных о ${#test_task_ids[@]} задачах теста"
        fi
        
        progress=$((completed_count * 100 / total_created))
        
        log "Прогресс: ${progress}% (✅$completed_count ⚠️$failed_count 🔄$processing_count ⏳$pending_count) [${elapsed}s]"
        
        last_status_time=$current_time
    fi
    
    sleep 2
done

monitoring_end=$(date +%s.%3N)
total_time=$(echo "$monitoring_end $test_start" | awk '{printf "%.2f", $1 - $2}')
processing_time=$(echo "$monitoring_end $creation_end" | awk '{printf "%.2f", $1 - $2}')

# ЭТАП 3: Финальная статистика
echo -e "${BLUE}=== ЭТАП 3: Финальная статистика ===${NC}"

# Финальная проверка всех задач через internal API
log "Выполнение финальной проверки статусов через internal API..."
final_completed=0
final_failed=0
final_pending=0
final_processing=0

# Получаем все задачи через internal API для финальной статистики
# Используем тот же лимит, что и в мониторинге
final_api_limit=$((total_created * 3))
if [ $final_api_limit -lt 5000 ]; then
    final_api_limit=5000
fi

final_all_tasks_response=$(get_all_tasks $final_api_limit)

if [[ "$final_all_tasks_response" == ERROR:* ]]; then
    log_warning "Ошибка получения задач для финальной статистики: ${final_all_tasks_response#ERROR:}"
    log "Используем fallback к индивидуальным запросам..."
    
    # Fallback: проверяем все задачи через старый метод
    task_count=0
    for task_entry in "${all_tasks[@]}"; do
        task_id=$(echo "$task_entry" | cut -d: -f1)
        result_token=$(echo "$task_entry" | cut -d: -f2)
        
        if [ ! -z "$result_token" ] && [ "$result_token" != "null" ]; then
            status=$(check_task_status "$result_token" "$task_id")
            case "$status" in
                "completed") final_completed=$((final_completed + 1)) ;;
                "failed") final_failed=$((final_failed + 1)) ;;
                "processing") final_processing=$((final_processing + 1)) ;;
                "pending") final_pending=$((final_pending + 1)) ;;
                *) final_pending=$((final_pending + 1)) ;;
            esac
        else
            final_failed=$((final_failed + 1))
        fi
        
        # Небольшая задержка каждые 5 запросов для снижения нагрузки на БД
        task_count=$((task_count + 1))
        if [ $((task_count % 5)) -eq 0 ]; then
            sleep 0.1
        fi
    done
else
    final_api_tasks_count=$(echo "$final_all_tasks_response" | jq -r '.tasks | length' 2>/dev/null)
    log "Получено $final_api_tasks_count задач через internal API для финальной статистики"
    
    # Фильтруем только наши задачи из общего списка
    final_filtered_tasks=$(filter_test_tasks "$final_all_tasks_response" "$test_task_ids_str")
    
    # Подсчитываем финальные статусы из отфильтрованных задач
    while IFS= read -r task_json; do
        if [ ! -z "$task_json" ] && [ "$task_json" != "null" ]; then
            status=$(echo "$task_json" | jq -r '.status // "unknown"' 2>/dev/null)
            case "$status" in
                "completed") final_completed=$((final_completed + 1)) ;;
                "failed") final_failed=$((final_failed + 1)) ;;
                "processing") final_processing=$((final_processing + 1)) ;;
                "pending") final_pending=$((final_pending + 1)) ;;
                *) final_pending=$((final_pending + 1)) ;;
            esac
        fi
    done <<< "$(echo "$final_filtered_tasks" | jq -c '.[]?' 2>/dev/null)"
    
    log "Финальная проверка: проанализировано ${#test_task_ids[@]} задач теста"
fi

echo
echo -e "${MAGENTA}🎯 ИТОГОВЫЕ РЕЗУЛЬТАТЫ НАГРУЗОЧНОГО ТЕСТА${NC}"
echo "=================================================="
echo -e "${CYAN}📊 Статистика выполнения:${NC}"
echo "  • Всего задач создано: $total_created"
if [ $total_created -gt 0 ]; then
    echo "  • Завершено успешно: $final_completed ($(( final_completed * 100 / total_created ))%)"
    echo "  • Завершено с ошибкой: $final_failed ($(( final_failed * 100 / total_created ))%)"
else
    echo "  • Завершено успешно: $final_completed (N/A)"
    echo "  • Завершено с ошибкой: $final_failed (N/A)"
fi
echo "  • В процессе: $final_processing"
echo "  • Ожидают: $final_pending"
echo
echo -e "${CYAN}⏱️ Временные метрики:${NC}"
echo "  • Время создания задач: ${creation_time}s"
echo "  • Время обработки: ${processing_time}s"
echo "  • Общее время теста: ${total_time}s"
echo
echo -e "${CYAN}🚀 Производительность:${NC}"
if [ $(echo "$creation_time" | awk '{print ($1 > 0)}') -eq 1 ]; then
    echo "  • Скорость создания: $(echo "$total_created $creation_time" | awk '{printf "%.1f", $1 / $2}') задач/сек"
else
    echo "  • Скорость создания: N/A (время создания 0s)"
fi
if [ $final_completed -gt 0 ] && [ $(echo "$processing_time" | awk '{print ($1 > 0)}') -eq 1 ]; then
    echo "  • Скорость обработки: $(echo "$final_completed $processing_time" | awk '{printf "%.1f", $1 / $2}') задач/сек"
else
    echo "  • Скорость обработки: N/A"
fi
echo "  • Пользователей одновременно: $CONCURRENT_USERS"
if [ $CONCURRENT_USERS -gt 0 ] && [ $(echo "$creation_time" | awk '{print ($1 > 0)}') -eq 1 ]; then
    echo "  • Среднее время на пользователя: $(echo "$creation_time $CONCURRENT_USERS" | awk '{printf "%.2f", $1 / $2}')s"
else
    echo "  • Среднее время на пользователя: N/A"
fi

# Оценка результата
echo
echo -e "${CYAN}📈 Оценка нагрузочного теста:${NC}"

if [ $total_created -gt 0 ]; then
    success_rate=$((final_completed * 100 / total_created))
else
    success_rate=0
fi
if [ $success_rate -ge 95 ]; then
    echo -e "${GREEN}🏆 ОТЛИЧНО: ${success_rate}% задач выполнено успешно${NC}"
    test_result="PASSED"
elif [ $success_rate -ge 80 ]; then
    echo -e "${YELLOW}👍 ХОРОШО: ${success_rate}% задач выполнено успешно${NC}"
    test_result="PASSED"
elif [ $success_rate -ge 60 ]; then
    echo -e "${YELLOW}⚠️ ПРИЕМЛЕМО: ${success_rate}% задач выполнено успешно${NC}"
    test_result="WARNING"
else
    echo -e "${RED}❌ ПЛОХО: только ${success_rate}% задач выполнено успешно${NC}"
    test_result="FAILED"
fi

# Проверка времени отклика
if [ $(echo "$creation_time" | awk '{print ($1 < 30)}') -eq 1 ]; then
    echo -e "${GREEN}⚡ Быстрое создание задач: ${creation_time}s < 30s${NC}"
elif [ $(echo "$creation_time" | awk '{print ($1 < 60)}') -eq 1 ]; then
    echo -e "${YELLOW}⚡ Приемлемое создание задач: ${creation_time}s < 60s${NC}"
else
    echo -e "${RED}🐌 Медленное создание задач: ${creation_time}s >= 60s${NC}"
    test_result="FAILED"
fi

# Очистка временных файлов
rm -f "/tmp/load_test_users_$$.txt"

echo
echo -e "${MAGENTA}🏁 Нагрузочный тест завершен: $test_result${NC}"

case "$test_result" in
    "PASSED") exit 0 ;;
    "WARNING") exit 1 ;;
    "FAILED") exit 2 ;;
esac
