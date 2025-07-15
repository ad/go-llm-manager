let currentJWT = null;
let resultJWT = null;
let currentTaskId = null;
let pollingInterval = null;
let sseConnection = null;
let magicSSEConnection = null;
let metricsInterval = null;
let sseReconnectCount = 0;
let magicSSEReconnectCount = 0;
let sseReconnectTimeout = null;
let magicSSEReconnectTimeout = null;
let processorSSEConnection = null;
let ssePollingConnection = null;
let ssePollingTaskId = null;
let ssePollingTaskCompleted = false;
let tasksAutoRefreshInterval = null;
let ratingPollingInterval = null;

// Автоматически устанавливаем базовый URL
window.addEventListener('load', function() {
    const baseUrl = window.location.origin;
    document.getElementById('baseUrl').value = baseUrl;
    log('🚀 LLM Proxy Admin Dashboard загружен');
    log('💡 Начните с проверки подключения');
});

// Закрываем соединения при выходе со страницы
window.addEventListener('beforeunload', function() {
    if (pollingInterval) {
        clearInterval(pollingInterval);
    }
    if (sseConnection) {
        sseConnection.close();
    }
    if (magicSSEConnection) {
        magicSSEConnection.close();
    }
    if (ssePollingConnection) {
        ssePollingConnection.close();
    }
    if (metricsInterval) {
        clearInterval(metricsInterval);
    }
});

function log(message, type = 'info') {
    const logs = document.getElementById('logs');
    const timestamp = new Date().toLocaleTimeString();
    const logEntry = document.createElement('div');
    
    let color = '#e2e8f0';
    if (type === 'error') color = '#fed7d7';
    if (type === 'success') color = '#9ae6b4';
    if (type === 'warning') color = '#faf089';
    
    logEntry.innerHTML = `<span style="color: #a0aec0;">[${timestamp}]</span> <span style="color: ${color};">${message}</span>`;
    logs.appendChild(logEntry);
    logs.scrollTop = logs.scrollHeight;
}

function clearLogs() {
    document.getElementById('logs').innerHTML = '';
    log('📝 Логи очищены');
}

function switchTab(tabName) {
    // Скрыть все содержимое табов
    document.querySelectorAll('.tab-content').forEach(content => {
        content.classList.remove('active');
    });
    // Убрать активность с всех табов
    document.querySelectorAll('.tab').forEach(tab => {
        tab.classList.remove('active');
    });
    // Показать выбранный таб
    document.getElementById(tabName + '-content').classList.add('active');
    event.target.classList.add('active');
    // Автоматически загружать данные для админ панели
    if (tabName === 'admin') {
        setTimeout(() => {
            loadAndDisplayAllTasks();
            startTasksAutoRefresh();
        }, 100);
    } else {
        stopTasksAutoRefresh();
    }
    log(`📂 Переключение на вкладку: ${tabName}`);
}

async function testConnection() {
    try {
        const baseUrl = document.getElementById('baseUrl').value;
        log('🔍 Проверка подключения к API...');
        
        const response = await fetch(`${baseUrl}/`);
        const data = await response.json();
        
        const statusEl = document.getElementById('connectionStatus');
        if (response.ok) {
            statusEl.innerHTML = `
                <div class="result success" style="display:block; margin-top: 10px;">
                    <strong>✅ Подключение установлено</strong><br>
                    Версия API: ${data.message}<br>
                    Доступные эндпойнты: ${Object.keys(data.endpoints || {}).length}
                </div>
            `;
            log('✅ Подключение к API успешно', 'success');
        } else {
            throw new Error(`HTTP ${response.status}`);
        }
    } catch (error) {
        document.getElementById('connectionStatus').innerHTML = `
            <div class="result error" style="display:block; margin-top: 10px;">
                <strong>❌ Ошибка подключения</strong><br>
                ${error.message}
            </div>
        `;
        log(`❌ Ошибка подключения: ${error.message}`, 'error');
    }
}

async function loadUserData() {
    try {
        const baseUrl = document.getElementById('baseUrl').value;
        const apiKey = document.getElementById('apiKey').value;
        const userId = document.getElementById('userId').value;

        if (!userId) {
            log('⚠️ Введите User ID для загрузки данных пользователя', 'warning');
            return;
        }

        log('🔑 Генерация JWT токена для загрузки данных...');

        // Генерируем JWT токен специально для получения данных пользователя
        const tokenResponse = await fetch(`${baseUrl}/api/internal/generate-token`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Authorization': `Bearer ${apiKey}`
            },
            body: JSON.stringify({
                user_id: userId,
                product_data: "temp" // Минимальные данные для генерации токена
            })
        });

        if (!tokenResponse.ok) {
            throw new Error(`Ошибка генерации токена: HTTP ${tokenResponse.status}`);
        }

        const tokenData = await tokenResponse.json();
        
        if (!tokenData.success) {
            throw new Error(tokenData.error || 'Ошибка генерации токена');
        }

        const tempToken = tokenData.token;
        log('✅ JWT токен для загрузки данных получен');
        log('📥 Загрузка данных пользователя...');

        const response = await fetch(`${baseUrl}/api/get?token=${encodeURIComponent(tempToken)}`, {
            method: 'GET',
            headers: {
                'Content-Type': 'application/json'
            }
        });

        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }

        const data = await response.json();
        
        if (!data.success) {
            throw new Error(data.error || 'Ошибка загрузки данных пользователя');
        }

        log('✅ Данные пользователя загружены');

        // Заполняем поля формы данными последней задачи, если она есть
        if (data.last_task) {
            const task = data.last_task;
            log(`📋 Последняя задача: ID ${task.id}, статус: ${task.status}`);
            
            // Заполняем поля, если в задаче есть данные
            if (task.product_data) {
                document.getElementById('productData').value = task.product_data;
                log('📝 Заполнено описание товара из последней задачи');
            }
            
            // Заполняем параметры Ollama, если они есть
            if (task.ollama_params) {
                const params = task.ollama_params;
                if (params.model) {
                    const modelSelect = document.getElementById('ollamaModel');
                    if ([...modelSelect.options].some(option => option.value === params.model)) {
                        modelSelect.value = params.model;
                        log('🤖 Заполнена модель из последней задачи');
                    }
                }
                if (params.temperature !== undefined) {
                    document.getElementById('temperature').value = params.temperature;
                }
                if (params.max_tokens !== undefined) {
                    document.getElementById('maxTokens').value = params.max_tokens;
                }
                if (params.top_p !== undefined) {
                    document.getElementById('topP').value = params.top_p;
                }
                if (params.top_k !== undefined) {
                    document.getElementById('topK').value = params.top_k;
                }
                if (params.repeat_penalty !== undefined) {
                    document.getElementById('repeatPenalty').value = params.repeat_penalty;
                }
                if (params.seed !== undefined) {
                    document.getElementById('seed').value = params.seed;
                }
                if (params.stop && params.stop.length > 0) {
                    document.getElementById('stopSequences').value = params.stop.join(', ');
                }
                if (params.prompt) {
                    document.getElementById('promptOverride').value = params.prompt;
                }
            }
        }

        // Отображаем информацию о rate limits
        if (data.rate_limit) {
            const rl = data.rate_limit;
            log(`📊 Rate limits: Запросы: ${rl.request_count}/${rl.request_limit}`);
            
            // Показываем информацию о rate limits в интерфейсе
            let rateLimitInfo = document.getElementById('rateLimitInfo');
            if (!rateLimitInfo) {
                // Создаем блок для отображения rate limits, если его нет
                rateLimitInfo = document.createElement('div');
                rateLimitInfo.id = 'rateLimitInfo';
                rateLimitInfo.className = 'result info';
                rateLimitInfo.style.marginTop = '10px';
                
                // Находим место для вставки (после формы создания задачи)
                const container = document.querySelector('#user-content .container');
                if (container) {
                    container.appendChild(rateLimitInfo);
                }
            }
            
            rateLimitInfo.innerHTML = `
                <strong>📊 Текущие лимиты пользователя:</strong><br>
                • Запросы: ${rl.request_count} / ${rl.request_limit}<br>
                • Период: ${rl.period_start} - ${rl.period_end}
            `;
        }

    } catch (error) {
        log(`❌ Ошибка загрузки данных пользователя: ${error.message}`, 'error');
    }
}

async function runMagic() {
    const magicInput = document.getElementById('magicInput');
    const magicBtn = document.getElementById('magicBtn');
    const wrapper = document.querySelector('.magic-input-wrapper');
    
    // Немедленно отключаем интерфейс
    magicInput.disabled = true;
    magicBtn.disabled = true;
    wrapper.classList.add('loading');
    
    function restoreMagicInterface(errorMessage = null) {
        magicInput.disabled = false;
        magicBtn.disabled = false;
        wrapper.classList.remove('loading');
        
        if (errorMessage) {
            magicInput.placeholder = errorMessage;
            // Возвращаем обычный placeholder через 3 секунды
            setTimeout(() => {
                magicInput.placeholder = 'Введите товар для создания описания...';
            }, 3000);
        } else {
            magicInput.placeholder = 'Введите товар для создания описания...';
        }
    }
    
    try {
        const productText = magicInput.value.trim();
        if (!productText) {
            restoreMagicInterface('⚠️ Введите описание товара');
            log('⚠️ Введите описание товара для магического преобразования', 'warning');
            return;
        }
        
        log('✨ Начинаем магическое преобразование...');
        
        const baseUrl = document.getElementById('baseUrl').value;
        const apiKey = document.getElementById('apiKey').value;
        const userId = 'magic_user_' + Date.now();
        
        // Создаем токен для магической задачи
        const tokenResponse = await fetch(`${baseUrl}/api/internal/generate-token`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Authorization': `Bearer ${apiKey}`
            },
            body: JSON.stringify({
                user_id: userId,
                product_data: productText,
            })
        });

        if (!tokenResponse.ok) {
            throw new Error(`Ошибка создания токена: HTTP ${tokenResponse.status}`);
        }

        const tokenData = await tokenResponse.json();
        if (!tokenData.success) {
            throw new Error(tokenData.error || 'Ошибка создания токена');
        }

        // Создаем задачу
        const taskResponse = await fetch(`${baseUrl}/api/create`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Authorization': 'Bearer ' + tokenData.token
            }
        });

        if (!taskResponse.ok) {
            throw new Error(`HTTP ${taskResponse.status}`);
        }

        const taskData = await taskResponse.json();
        if (!taskData.success) {
            throw new Error(taskData.error || 'Ошибка создания задачи');
        }

        const magicTaskId = taskData.taskId;
        const resultToken = taskData.token; // Получаем токен из ответа
        log(`🎯 Магическая задача создана: ${magicTaskId}`);

        // Начинаем SSE поллинг для результата
        startMagicSSEPolling(resultToken, magicInput, wrapper, restoreMagicInterface);
        
    } catch (error) {
        // Восстанавливаем интерфейс при ошибке
        restoreMagicInterface(`❌ ${error.message}`);
        log(`❌ Ошибка магического преобразования: ${error.message}`, 'error');
    }
}

async function createTask() {
    try {
        const baseUrl = document.getElementById('baseUrl').value;
        const apiKey = document.getElementById('apiKey').value;
        const userId = document.getElementById('userId').value;
        const productData = document.getElementById('productData').value;
        const priority = parseInt(document.getElementById('priority').value);
        const model = document.getElementById('ollamaModel').value;
        const temperature = parseFloat(document.getElementById('temperature').value);
        const maxTokens = parseInt(document.getElementById('maxTokens').value);
        const topP = parseFloat(document.getElementById('topP').value);
        const topK = parseInt(document.getElementById('topK').value);
        const repeatPenalty = parseFloat(document.getElementById('repeatPenalty').value);
        const seedInput = document.getElementById('seed').value;
        const stopSequencesInput = document.getElementById('stopSequences').value;
        const promptOverride = document.getElementById('promptOverride').value;

        log('� Генерация JWT токена для задачи...');

        // Build ollama_params object
        const ollamaParams = {
            model: model,
            temperature: temperature,
            max_tokens: maxTokens,
            top_p: topP,
            top_k: topK,
            repeat_penalty: repeatPenalty
        };

        // Add optional parameters if provided
        if (seedInput) {
            ollamaParams.seed = parseInt(seedInput);
        }
        if (stopSequencesInput) {
            ollamaParams.stop = stopSequencesInput.split(',').map(s => s.trim()).filter(s => s);
        }
        if (promptOverride) {
            ollamaParams.prompt = promptOverride;
        }

        // Генерируем JWT токен
        const tokenResponse = await fetch(`${baseUrl}/api/internal/generate-token`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Authorization': `Bearer ${apiKey}`
            },
            body: JSON.stringify({
                user_id: userId,
                product_data: productData,
                priority: priority,
                ollama_params: ollamaParams
            })
        });

        if (!tokenResponse.ok) {
            throw new Error(`Ошибка генерации токена: HTTP ${tokenResponse.status}`);
        }

        const tokenData = await tokenResponse.json();
        
        if (!tokenData.success) {
            throw new Error(tokenData.error || 'Ошибка генерации токена');
        }

        currentJWT = tokenData.token;
        document.getElementById('jwtToken').value = currentJWT;
        document.getElementById('tokenResult').style.display = 'block';
        
        // Показать время истечения
        const expiryTime = new Date(Date.now() + tokenData.expires_in * 1000);
        document.getElementById('tokenExpiry').textContent = expiryTime.toLocaleString();
        
        log('✅ JWT токен создан, создание задачи...');

        // Создаем задачу с полученным токеном
        const response = await fetch(`${baseUrl}/api/create`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Authorization': 'Bearer ' + currentJWT
            }
        });

        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }

        const data = await response.json();
        
        if (data.success) {
            currentTaskId = data.taskId;
            resultJWT = data.token; // Получаем токен из ответа
            document.getElementById('taskId').textContent = currentTaskId;
            document.getElementById('estimatedTime').textContent = data.estimatedTime;
            document.getElementById('taskResult').style.display = 'block';
            
            log('✅ Задача успешно создана', 'success');
            log(`📊 Предварительное время: ${data.estimatedTime}`);
            log('✅ Токен для результата получен');
            
            // Теперь разблокируем кнопки управления
            document.getElementById('resultBtn').disabled = false;
            document.getElementById('pollBtn').disabled = false;
            document.getElementById('realtimePollBtn').disabled = false;
            document.getElementById('createBtn').disabled = false;
            
            // Автоматически запускаем real-time опрос
            log('⚡ Автоматический запуск real-time опроса результата...');
            startRealtimePolling();
            
        } else {
            throw new Error(data.error || 'Неизвестная ошибка');
        }
    } catch (error) {
        log(`❌ Ошибка создания задачи: ${error.message}`, 'error');
    }
}

async function getResult() {
    try {
        const baseUrl = document.getElementById('baseUrl').value;
        
        if (!resultJWT) {
            throw new Error('Токен для получения результата не найден. Создайте задачу сначала.');
        }
        
        log('🔍 Проверка результата...');
        
        const response = await fetch(`${baseUrl}/api/result`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Authorization': `Bearer ${resultJWT}`
            }
        });

        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }

        const data = await response.json();
        
        if (data.success) {
            document.getElementById('status').textContent = data.status;
            document.getElementById('status').className = `task-status status-${data.status}`;
            document.getElementById('finalResult').style.display = 'block';
            
            if (data.createdAt) {
                document.getElementById('createdAt').textContent = new Date(data.createdAt).toLocaleString();
            }
            
            if (data.status === 'completed' && data.result) {
                document.getElementById('resultText').textContent = JSON.stringify({
                    status: data.status,
                    result: data.result,
                    createdAt: data.createdAt,
                    completedAt: new Date().toISOString()
                }, null, 2);
                log('🎉 Задача выполнена!', 'success');
                stopPolling();
                
                // Добавляем кнопки оценки для завершенной задачи
                let votingContainer = document.getElementById('finalResultVotingContainer');
                if (!votingContainer) {
                    votingContainer = document.createElement('div');
                    votingContainer.id = 'finalResultVotingContainer';
                    const finalResultDiv = document.getElementById('finalResult');
                    if (finalResultDiv) {
                        finalResultDiv.appendChild(votingContainer);
                    }
                }
                
                if (currentTaskId) {
                    const taskForVoting = {
                        id: currentTaskId,
                        status: data.status,
                        rating: data.rating || null
                    };
                    votingContainer.innerHTML = createVotingButtons(taskForVoting);
                } else {
                    votingContainer.innerHTML = '';
                }
            } else if (data.status === 'failed') {
                document.getElementById('resultText').textContent = JSON.stringify({
                    status: data.status,
                    error: 'Ошибка выполнения задачи',
                    createdAt: data.createdAt
                }, null, 2);
                log('❌ Задача завершилась с ошибкой', 'error');
                stopPolling();
                
                // Очищаем кнопки оценки для неудачных задач
                const votingContainer = document.getElementById('finalResultVotingContainer');
                if (votingContainer) {
                    votingContainer.innerHTML = '';
                }
            } else {
                document.getElementById('resultText').textContent = JSON.stringify({
                    status: data.status,
                    createdAt: data.createdAt,
                    message: 'Задача в процессе обработки...'
                }, null, 2);
                log(`⏳ Задача в процессе: ${data.status}`);
                
                // Очищаем кнопки оценки для задач в процессе
                const votingContainer = document.getElementById('finalResultVotingContainer');
                if (votingContainer) {
                    votingContainer.innerHTML = '';
                }
            }
        } else {
            throw new Error(data.error || 'Неизвестная ошибка');
        }
    } catch (error) {
        log(`❌ Ошибка получения результата: ${error.message}`, 'error');
    }
}

function startPolling() {
    document.getElementById('pollBtn').disabled = true;
    document.getElementById('stopBtn').disabled = false;
    log('🔄 Начат автоматический опрос каждые 5 секунд');
    
    pollingInterval = setInterval(getResult, 5000);
}

function stopPolling() {
    if (pollingInterval) {
        clearInterval(pollingInterval);
        pollingInterval = null;
        document.getElementById('pollBtn').disabled = false;
        document.getElementById('stopBtn').disabled = true;
        log('⏹️ Автоматический опрос остановлен');
    }
    
    if (sseConnection) {
        sseConnection.close();
        sseConnection = null;
        document.getElementById('realtimePollBtn').disabled = false;
        document.getElementById('stopBtn').disabled = true;
        log('⏹️ Real-time поллинг остановлен');
    }
    
    if (sseReconnectTimeout) {
        clearTimeout(sseReconnectTimeout);
        sseReconnectTimeout = null;
    }
    
    if (magicSSEConnection) {
        magicSSEConnection.close();
        magicSSEConnection = null;
        log('⏹️ Магический real-time поллинг остановлен');
    }
    
    if (magicSSEReconnectTimeout) {
        clearTimeout(magicSSEReconnectTimeout);
        magicSSEReconnectTimeout = null;
    }
    
    // Сброс счетчиков
    sseReconnectCount = 0;
    magicSSEReconnectCount = 0;
}

function startRealtimePolling() {
    try {
        if (!resultJWT) {
            log('❌ Нет токена для результата. Создайте задачу сначала.', 'error');
            return;
        }

        const realtimeBtn = document.getElementById('realtimePollBtn');
        const stopBtn = document.getElementById('stopBtn');
        
        if (!realtimeBtn) {
            log('❌ Кнопка Real-time SSE не найдена в DOM', 'error');
            return;
        }
        
        if (!stopBtn) {
            log('❌ Кнопка остановки не найдена в DOM', 'error');
            return;
        }

        realtimeBtn.disabled = true;
        stopBtn.disabled = false;
        log('⚡ Подключение к real-time поллингу...');

        const baseUrl = document.getElementById('baseUrl')?.value;
        if (!baseUrl) {
            log('❌ Base URL не найден', 'error');
            realtimeBtn.disabled = false;
            stopBtn.disabled = true;
            return;
        }
        
        const sseUrl = baseUrl + '/api/result-polling?token=' + encodeURIComponent(resultJWT);
        let taskFinalized = false; // Флаг для отслеживания финального статуса

        function connectSSE() {
            // Не переподключаемся, если задача уже завершена
            if (taskFinalized) {
                log('ℹ️ Задача уже завершена, переподключение не требуется');
                return;
            }
            
            if (sseReconnectCount >= 5) {
                log('❌ Превышено максимальное количество попыток переподключения (5)', 'error');
                realtimeBtn.disabled = false;
                stopBtn.disabled = true;
                return;
            }
            
            if (sseReconnectCount > 0) {
                log('🔄 Попытка переподключения #' + (sseReconnectCount + 1));
            }
            
            sseConnection = new EventSource(sseUrl);

            sseConnection.onopen = function(event) {
                log('✅ Real-time соединение установлено');
                sseReconnectCount = 0; // Сброс счетчика при успешном подключении
            };

            sseConnection.onmessage = function(event) {
                try {
                    log('🔵 Получено SSE событие: ' + event.data);
                    const data = JSON.parse(event.data);
                    log('🔵 Парсинг успешен, тип события: ' + data.type);
                    log('🔵 Данные события: ' + JSON.stringify(data.data, null, 2));
                    
                    switch(data.type) {
                        case 'heartbeat':
                            if (data.data.message) {
                                log('💓 ' + data.data.message);
                            } else {
                                log('💓 Heartbeat');
                            }
                            break;
                            
                        case 'task_status':
                            log('📊 Статус изменился: ' + data.data.status);
                            if (data.data.processingStartedAt) {
                                log('⏰ Обработка началась: ' + new Date(data.data.processingStartedAt).toLocaleString());
                            }
                            // Отображаем промежуточный статус
                            displayTaskStatus(data.data);
                            break;
                            
                        case 'task_completed':
                            log('🎉 Задача выполнена!');
                            log('🔵 Данные завершенной задачи: ' + JSON.stringify(data.data, null, 2));
                            displayTaskResult(data.data);
                            taskFinalized = true;
                            stopSSEPolling();
                            break;
                            
                        case 'task_failed':
                            log('❌ Задача провалена: ' + (data.data.error || 'Неизвестная ошибка'), 'error');
                            log('🔵 Данные провалившейся задачи: ' + JSON.stringify(data.data, null, 2));
                            displayTaskResult(data.data);
                            taskFinalized = true;
                            stopSSEPolling();
                            break;
                            
                        case 'error':
                            log('❌ Ошибка SSE: ' + data.data.error, 'error');
                            if (data.data.shouldReconnect) {
                                const delay = data.data.reconnectDelay || 5000;
                                log('🔄 Переподключение через ' + (delay/1000) + ' секунд...');
                                sseConnection.close();
                                sseReconnectCount++;
                                sseReconnectTimeout = setTimeout(connectSSE, delay);
                            } else {
                                sseConnection.close();
                                sseConnection = null;
                                realtimeBtn.disabled = false;
                                stopBtn.disabled = true;
                            }
                            break;
                            
                        default:
                            log('📝 Неизвестное SSE событие: ' + data.type);
                            log('🔵 Данные неизвестного события: ' + JSON.stringify(data, null, 2));
                    }
                } catch (error) {
                    log('❌ Ошибка парсинга SSE данных: ' + error.message, 'error');
                    log('🔵 Сырые данные события: ' + event.data);
                }
            };

            sseConnection.onerror = function(event) {
                // Не пытаемся переподключиться, если задача уже завершена
                if (taskFinalized) {
                    log('ℹ️ Соединение закрыто после завершения задачи');
                    return;
                }
                
                log('❌ Ошибка SSE соединения, попытка переподключения через 5 секунд...', 'error');
                if (sseConnection) {
                    sseConnection.close();
                    sseReconnectCount++;
                    sseReconnectTimeout = setTimeout(connectSSE, 5000);
                }
            };
        }

        // Сброс счетчика при старте
        sseReconnectCount = 0;
        connectSSE();
    } catch (error) {
        log('❌ Ошибка в Real-time поллинге: ' + error.message, 'error');
        const realtimeBtn = document.getElementById('realtimePollBtn');
        const stopBtn = document.getElementById('stopBtn');
        if (realtimeBtn) realtimeBtn.disabled = false;
        if (stopBtn) stopBtn.disabled = true;
    }
}

function displayTaskStatus(taskData) {
    log('🔵 displayTaskStatus вызвана с данными: ' + JSON.stringify(taskData, null, 2));
    const resultDiv = document.getElementById('taskResult');
    resultDiv.style.display = 'block';
    
    // Обновляем основную информацию о задаче
    const taskIdEl = document.getElementById('taskId');
    const taskStatusEl = document.getElementById('taskStatus');
    const taskCreatedAtEl = document.getElementById('taskCreatedAt');
    const taskCompletedAtEl = document.getElementById('taskCompletedAt');
    const taskResultTextEl = document.getElementById('taskResultText');
    
    if (taskIdEl) taskIdEl.textContent = taskData.taskId || '-';
    if (taskStatusEl) {
        taskStatusEl.textContent = taskData.status || '-';
        // Добавляем цветовую индикацию статуса
        taskStatusEl.className = 'task-status status-' + (taskData.status || 'unknown');
    }
    if (taskCreatedAtEl) {
        taskCreatedAtEl.textContent = taskData.createdAt ? 
            new Date(taskData.createdAt).toLocaleString() : 'N/A';
    }
    if (taskCompletedAtEl) {
        taskCompletedAtEl.textContent = taskData.completedAt ? 
            new Date(taskData.completedAt).toLocaleString() : 'N/A';
    }
    
    // Обновляем текст результата в зависимости от статуса
    if (taskResultTextEl) {
        switch(taskData.status) {
            case 'pending':
                taskResultTextEl.textContent = '⏳ Задача ожидает обработки...';
                taskResultTextEl.style.color = '#f39c12';
                break;
            case 'processing':
                const startedText = taskData.processingStartedAt ? 
                    ' (началась: ' + new Date(taskData.processingStartedAt).toLocaleTimeString() + ')' : '';
                taskResultTextEl.textContent = '⚙️ Задача обрабатывается...' + startedText;
                taskResultTextEl.style.color = '#3498db';
                break;
            default:
                taskResultTextEl.textContent = '📊 Статус: ' + (taskData.status || 'неизвестно');
                taskResultTextEl.style.color = '#7f8c8d';
        }
    }
    
    log('✅ displayTaskStatus: отображение обновлено для статуса ' + taskData.status);
}

function displayTaskResult(taskData) {
    log('🔵 displayTaskResult вызвана с данными: ' + JSON.stringify(taskData, null, 2));
    const resultDiv = document.getElementById('taskResult');
    resultDiv.style.display = 'block';
    
    const taskIdEl = document.getElementById('taskId');
    const taskStatusEl = document.getElementById('taskStatus');
    const taskCreatedAtEl = document.getElementById('taskCreatedAt');
    const taskCompletedAtEl = document.getElementById('taskCompletedAt');
    const taskResultTextEl = document.getElementById('taskResultText');
    
    if (taskIdEl) taskIdEl.textContent = taskData.taskId || '-';
    if (taskStatusEl) {
        taskStatusEl.textContent = taskData.status || '-';
        taskStatusEl.className = 'task-status status-' + (taskData.status || 'unknown');
    }
    if (taskCreatedAtEl) {
        taskCreatedAtEl.textContent = taskData.createdAt ? 
            new Date(taskData.createdAt).toLocaleString() : 'N/A';
    }
    if (taskCompletedAtEl) {
        taskCompletedAtEl.textContent = taskData.completedAt ? 
            new Date(taskData.completedAt).toLocaleString() : 'N/A';
    }
    
    if (taskResultTextEl) {
        if (taskData.status === 'completed') {
            const result = (taskData.result !== undefined && taskData.result !== null) ? 
                taskData.result : '[Нет результата]';
            taskResultTextEl.textContent = result;
            taskResultTextEl.style.color = '#2ecc71';
            log('✅ Отображен результат completed задачи: ' + result.substring(0, 100) + '...');
        } else if (taskData.status === 'failed') {
            const error = (taskData.error !== undefined && taskData.error !== null) ? 
                taskData.error : '[Ошибка без сообщения]';
            taskResultTextEl.textContent = error;
            taskResultTextEl.style.color = '#e74c3c';
            log('❌ Отображена ошибка failed задачи: ' + error);
        } else {
            taskResultTextEl.textContent = '[Статус: ' + (taskData.status || 'неизвестно') + ']';
            taskResultTextEl.style.color = '#888';
            log('ℹ️ Отображен неопределенный статус: ' + taskData.status);
        }
    } else {
        log('❌ Элемент taskResultText не найден в DOM!');
    }
    
    // Добавляем кнопки оценки для завершенных задач
    let votingContainer = document.getElementById('userTaskVotingContainer');
    if (!votingContainer) {
        votingContainer = document.createElement('div');
        votingContainer.id = 'userTaskVotingContainer';
        if (resultDiv) {
            resultDiv.appendChild(votingContainer);
        }
    }
    
    if (taskData.status === 'completed' && taskData.taskId) {
        const taskForVoting = {
            id: taskData.taskId,
            status: taskData.status,
            rating: taskData.rating || null
        };
        votingContainer.innerHTML = createVotingButtons(taskForVoting);
    } else {
        votingContainer.innerHTML = '';
    }
    
    log('✅ displayTaskResult: финальное отображение обновлено');
}

// Административные функции
// 
// Функции отображения задач поддерживают:
// ⏱️ Время выполнения/ожидания:
//    - pending: время в ожидании с момента создания
//    - processing: время выполнения с момента начала
//    - completed/failed: общее время выполнения
// ❌ Отображение ошибок:
//    - для failed задач показывается error_message
//    - красная цветовая индикация для ошибок
// 🎨 Цветовая индикация времени:
//    - зеленый: completed задачи
//    - красный: failed задачи  
//    - синий: processing задачи
//    - желтый: pending задачи

async function loadAndDisplayAllTasks() {
    try {
        const baseUrl = document.getElementById('baseUrl').value;
        const apiKey = document.getElementById('apiKey').value;
        log('📄 Загрузка всех задач...');
        const response = await fetch(`${baseUrl}/api/internal/all-tasks`, {
            method: 'GET',
            headers: {
                'Authorization': `Bearer ${apiKey}`
            }
        });
        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }
        const data = await response.json();
        const allTasks = data.tasks || [];
        const pendingTasks = allTasks.filter(t => t.status === 'pending');
        displayPendingTasks(pendingTasks);
        displayAllTasks(allTasks);
        log(`✅ Загружено задач: всего ${allTasks.length}, ожидающих ${pendingTasks.length}`, 'success');
    } catch (error) {
        displayPendingTasks([]);
        displayAllTasks([]);
        log(`❌ Ошибка загрузки задач: ${error.message}`, 'error');
    }
}

function startTasksAutoRefresh() {
    if (tasksAutoRefreshInterval) return;
    tasksAutoRefreshInterval = setInterval(loadAndDisplayAllTasks, 5000);
}

function stopTasksAutoRefresh() {
    if (tasksAutoRefreshInterval) {
        clearInterval(tasksAutoRefreshInterval);
        tasksAutoRefreshInterval = null;
    }
}

// Заменяем refreshTaskList на единый вызов
async function refreshTaskList() {
    log('🔄 Обновление списков задач...');
    await loadAndDisplayAllTasks();
}

async function runCleanup() {
    try {
        const baseUrl = document.getElementById('baseUrl').value;
        const apiKey = document.getElementById('apiKey').value;
        
        log('🧹 Запуск процедуры очистки...');

        const response = await fetch(`${baseUrl}/api/internal/cleanup`, {
            method: 'POST',
            headers: {
                'Authorization': `Bearer ${apiKey}`
            }
        });

        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }

        const data = await response.json();
        
        document.getElementById('cleanupResults').innerHTML = `
            <h4>✅ Результаты очистки</h4>
            <div class="json-viewer">${JSON.stringify(data, null, 2)}</div>
        `;
        document.getElementById('cleanupResults').style.display = 'block';
        
        log('✅ Очистка выполнена успешно', 'success');
        
    } catch (error) {
        log(`❌ Ошибка очистки: ${error.message}`, 'error');
    }
}

async function getCleanupStats() {
    try {
        const baseUrl = document.getElementById('baseUrl').value;
        const apiKey = document.getElementById('apiKey').value;
        
        log('📊 Получение статистики очистки...');

        const response = await fetch(`${baseUrl}/api/internal/cleanup/stats`, {
            method: 'GET',
            headers: {
                'Authorization': `Bearer ${apiKey}`
            }
        });

        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }

        const data = await response.json();
        
        document.getElementById('cleanupResults').innerHTML = `
            <h4>📊 Статистика очистки</h4>
            <div class="json-viewer">${JSON.stringify(data, null, 2)}</div>
        `;
        document.getElementById('cleanupResults').style.display = 'block';
        
        log('✅ Статистика очистки получена', 'success');
        
    } catch (error) {
        log(`❌ Ошибка получения статистики: ${error.message}`, 'error');
    }
}

async function workSteal() {
    try {
        const baseUrl = document.getElementById('baseUrl').value;
        const apiKey = document.getElementById('apiKey').value;
        
        log('⚖️ Запуск перераспределения задач...');

        const response = await fetch(`${baseUrl}/api/internal/work-steal`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Authorization': `Bearer ${apiKey}`
            },
            body: JSON.stringify({
                processor_id: 'admin-dashboard',
                max_steal_count: 3,
                timeout_ms: 300000
            })
        });

        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }

        const data = await response.json();
        
        document.getElementById('workStealResults').innerHTML = `
            <h4>⚖️ Результаты перераспределения</h4>
            <div class="json-viewer">${JSON.stringify(data, null, 2)}</div>
        `;
        document.getElementById('workStealResults').style.display = 'block';
        
        log('✅ Перераспределение выполнено', 'success');
        
    } catch (error) {
        log(`❌ Ошибка перераспределения: ${error.message}`, 'error');
    }
}

async function getProcessorMetrics() {
    try {
        const baseUrl = document.getElementById('baseUrl').value;
        const apiKey = document.getElementById('apiKey').value;
        
        log('📈 Получение метрик процессоров...');

        const response = await fetch(`${baseUrl}/api/internal/metrics`, {
            method: 'GET',
            headers: {
                'Authorization': `Bearer ${apiKey}`
            }
        });

        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }

        const data = await response.json();
        
        // Отображение результатов в секции работы с балансировкой
        let metricsHtml = '<h4>📈 Метрики процессоров</h4>';
        
        if (data.success && data.processors) {
            const processors = data.processors;
            const processorCount = processors.length;
            
            metricsHtml += `
                <div class="metrics-grid">
                    <div class="metric-card">
                        <strong>Всего процессоров</strong><br>
                        <span class="metric-value">${processorCount}</span>
                    </div>
            `;
            
            // Данные по каждому процессору
            processors.forEach(processor => {
                const lastUpdated = new Date(processor.last_updated);
                const isActive = (Date.now() - processor.last_updated) < 60000; // активен если обновлялся менее минуты назад
                const statusColor = isActive ? '#00b894' : '#6c757d';
                const statusText = isActive ? 'active' : 'inactive';
                
                metricsHtml += `
                    <div class="metric-card">
                        <strong>${processor.processor_id}</strong><br>
                        <span style="color: ${statusColor};">●</span> ${statusText}<br>
                        <small>Активных задач: ${processor.active_tasks || 0}</small><br>
                        <small>Очередь: ${processor.queue_size}</small><br>
                        <small>CPU: ${processor.cpu_usage}%</small><br>
                        <small>Память: ${processor.memory_usage}%</small><br>
                        <small>Обновлено: ${lastUpdated.toLocaleTimeString()}</small>
                    </div>
                `;
            });
            
            metricsHtml += '</div>';
            
            // Общая статистика
            const totalTasks = processors.reduce((sum, p) => sum + (p.active_tasks || 0), 0);
            const totalQueue = processors.reduce((sum, p) => sum + (p.queue_size || 0), 0);
            const avgCpu = processorCount > 0 ? processors.reduce((sum, p) => sum + (p.cpu_usage || 0), 0) / processorCount : 0;
            const avgMemory = processorCount > 0 ? processors.reduce((sum, p) => sum + (p.memory_usage || 0), 0) / processorCount : 0;
            
            metricsHtml += `
                <h5>📊 Общая статистика</h5>
                <div class="metrics-grid">
                    <div class="metric-card">
                        <strong>Всего активных задач</strong><br>
                        <span class="metric-value">${totalTasks}</span>
                    </div>
                    <div class="metric-card">
                        <strong>Общий размер очереди</strong><br>
                        <span class="metric-value">${totalQueue}</span>
                    </div>
                    <div class="metric-card">
                        <strong>Средний CPU</strong><br>
                        <span class="metric-value">${avgCpu.toFixed(1)}%</span>
                    </div>
                    <div class="metric-card">
                        <strong>Средняя память</strong><br>
                        <span class="metric-value">${avgMemory.toFixed(1)}%</span>
                    </div>
                </div>
            `;
        } else {
            metricsHtml += `
                <div class="json-viewer">${JSON.stringify(data, null, 2)}</div>
            `;
        }
        
        document.getElementById('workStealResults').innerHTML = metricsHtml;
        document.getElementById('workStealResults').style.display = 'block';
        
        log('✅ Метрики процессоров получены', 'success');
        
    } catch (error) {
        log(`❌ Ошибка получения метрик: ${error.message}`, 'error');
    }
}

async function loadMetrics() {
    try {
        const baseUrl = document.getElementById('baseUrl').value;
        const apiKey = document.getElementById('apiKey').value;
        
        log('📊 Загрузка метрик системы...');

        const response = await fetch(`${baseUrl}/api/internal/metrics`, {
            method: 'GET',
            headers: {
                'Authorization': `Bearer ${apiKey}`
            }
        });

        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }

        const data = await response.json();
        displayMetrics(data);
        log('✅ Метрики загружены', 'success');
        
    } catch (error) {
        log(`❌ Ошибка загрузки метрик: ${error.message}`, 'error');
    }
}

function displayMetrics(data) {
    const container = document.getElementById('metricsContainer');
    const statsContainer = document.getElementById('realtimeStats');
    
    // Основные метрики
    container.innerHTML = `
        <div class="json-viewer">${JSON.stringify(data, null, 2)}</div>
    `;
    
    // Статистика в реальном времени
    if (data.processors) {
        const totalProcessors = Object.keys(data.processors).length;
        const activeProcessors = Object.values(data.processors).filter(p => p.status === 'active').length;
        const totalTasks = Object.values(data.processors).reduce((sum, p) => sum + (p.current_tasks || 0), 0);
        
        statsContainer.innerHTML = `
            <div class="metric-card">
                <div class="metric-value">${totalProcessors}</div>
                <div class="metric-label">Всего процессоров</div>
            </div>
            <div class="metric-card">
                <div class="metric-value">${activeProcessors}</div>
                <div class="metric-label">Активных</div>
            </div>
            <div class="metric-card">
                <div class="metric-value">${totalTasks}</div>
                <div class="metric-label">Задач в обработке</div>
            </div>
            <div class="metric-card">
                <div class="metric-value">${new Date().toLocaleTimeString()}</div>
                <div class="metric-label">Последнее обновление</div>
            </div>
        `;
    }
}

function startMetricsPolling() {
    document.getElementById('metricsPollingBtn').disabled = true;
    document.getElementById('stopMetricsBtn').disabled = false;
    log('📊 Начато автообновление метрик каждые 10 секунд');
    
    metricsInterval = setInterval(loadMetrics, 10000);
    loadMetrics(); // Загрузить сразу
}

function stopMetricsPolling() {
    if (metricsInterval) {
        clearInterval(metricsInterval);
        metricsInterval = null;
        document.getElementById('metricsPollingBtn').disabled = false;
        document.getElementById('stopMetricsBtn').disabled = true;
        log('⏹️ Автообновление метрик остановлено');
    }
}

function exportLogs() {
    const logs = document.getElementById('logs').innerText;
    const blob = new Blob([logs], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `llm-proxy-logs-${new Date().toISOString().slice(0,19).replace(/:/g,'-')}.txt`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
    log('💾 Логи экспортированы', 'success');
}

function startMagicSSEPolling(resultToken, magicInput, wrapper, restoreMagicInterface) {
    try {
        const baseUrl = document.getElementById('baseUrl').value;
        if (!baseUrl) {
            restoreMagicInterface('❌ Base URL не найден');
            return;
        }
        
        log('⚡ Подключение к магическому real-time поллингу...');
        const sseUrl = baseUrl + '/api/result-polling?token=' + encodeURIComponent(resultToken);
        let magicTaskFinalized = false; // Флаг для отслеживания финального статуса
        
        function connectMagicSSE() {
            // Не переподключаемся, если задача уже завершена
            if (magicTaskFinalized) {
                log('ℹ️ Магическая задача уже завершена, переподключение не требуется');
                return;
            }
            
            if (magicSSEReconnectCount >= 5) {
                log('❌ Превышено максимальное количество попыток переподключения магического SSE (5)', 'error');
                restoreMagicInterface('❌ Не удалось подключиться');
                return;
            }
            
            if (magicSSEReconnectCount > 0) {
                log('🔄 Магическая попытка переподключения #' + (magicSSEReconnectCount + 1));
            }
            
            magicSSEConnection = new EventSource(sseUrl);

            magicSSEConnection.onopen = function(event) {
                log('✨ Магическое real-time соединение установлено');
                magicSSEReconnectCount = 0; // Сброс счетчика при успешном подключении
            };

        magicSSEConnection.onmessage = function(event) {
            try {
                log('🔮 Получено магическое SSE событие: ' + event.data);
                const data = JSON.parse(event.data);
                log('🔮 Магический парсинг успешен, тип события: ' + data.type);
                log('🔮 Магические данные события: ' + JSON.stringify(data.data, null, 2));
                
                switch(data.type) {
                    case 'heartbeat':
                        if (data.data.message) {
                            log('💓 ' + data.data.message);
                        } else {
                            log('💓 Магическое сердцебиение');
                        }
                        break;
                        
                    case 'task_status':
                        log('📊 Магический статус: ' + data.data.status);
                        if (data.data.processingStartedAt) {
                            log('⏰ Магическая обработка началась: ' + new Date(data.data.processingStartedAt).toLocaleString());
                        }
                        break;
                        
                    case 'task_completed':
                        log('🎉 Магическое описание готово!', 'success');
                        log('🔮 Магические данные завершенной задачи: ' + JSON.stringify(data.data, null, 2));
                        
                        // Показываем результат в инпуте
                        if (data.data.result) {
                            log('✨ Устанавливаем магический результат: ' + data.data.result.substring(0, 100) + '...');
                            magicInput.value = data.data.result.trim();
                            // Также обновляем основной инпут в форме
                            const productDataInput = document.getElementById('productData');
                            if (productDataInput) {
                                productDataInput.value = data.data.result.trim();
                                log('✨ Основной инпут также обновлен');
                            }
                        } else {
                            log('⚠️ Магический результат пуст или отсутствует!', 'warning');
                        }
                        
                        // Добавляем визуальный эффект успеха
                        wrapper.style.background = 'rgba(40, 167, 69, 0.2)';
                        setTimeout(() => {
                            wrapper.style.background = 'rgba(255, 255, 255, 0.95)';
                        }, 2000);
                        
                        // Закрываем соединение и восстанавливаем интерфейс
                        magicTaskFinalized = true; // Устанавливаем флаг финализации
                        magicSSEConnection.close();
                        magicSSEConnection = null;
                        if (magicSSEReconnectTimeout) {
                            clearTimeout(magicSSEReconnectTimeout);
                            magicSSEReconnectTimeout = null;
                        }
                        magicSSEReconnectCount = 0;
                        restoreMagicInterface();
                        break;
                        
                    case 'task_failed':
                        log('❌ Магическая задача провалена: ' + (data.data.error || 'неизвестная ошибка'), 'error');
                        magicTaskFinalized = true; // Устанавливаем флаг финализации
                        magicSSEConnection.close();
                        magicSSEConnection = null;
                        if (magicSSEReconnectTimeout) {
                            clearTimeout(magicSSEReconnectTimeout);
                            magicSSEReconnectTimeout = null;
                        }
                        magicSSEReconnectCount = 0;
                        restoreMagicInterface('❌ Магия не сработала');
                        break;
                        
                    case 'error':
                        log('❌ Ошибка магического SSE: ' + data.data.error, 'error');
                        if (data.data.shouldReconnect) {
                            const delay = data.data.reconnectDelay || 5000;
                            log('🔄 Магическое переподключение через ' + (delay/1000) + ' секунд...');
                            magicSSEConnection.close();
                            magicSSEReconnectCount++;
                            magicSSEReconnectTimeout = setTimeout(connectMagicSSE, delay);
                        } else {
                            magicSSEConnection.close();
                            magicSSEConnection = null;
                            restoreMagicInterface('❌ Ошибка соединения');
                        }
                        break;
                        
                    default:
                        log('📝 Магическое SSE событие: ' + data.type);
                }
            } catch (error) {
                log('❌ Ошибка парсинга магических SSE данных: ' + error.message, 'error');
            }
        };

        magicSSEConnection.onerror = function(event) {
            // Не пытаемся переподключиться, если задача уже завершена
            if (magicTaskFinalized) {
                log('ℹ️ Магическое соединение закрыто после завершения задачи');
                return;
            }
            
            log('❌ Ошибка магического SSE соединения, попытка переподключения через 5 секунд...', 'error');
            if (magicSSEConnection) {
                magicSSEConnection.close();
                magicSSEReconnectCount++;
                magicSSEReconnectTimeout = setTimeout(connectMagicSSE, 5000);
            }
        };
        }

        // Сброс счетчика при старте
        magicSSEReconnectCount = 0;
        connectMagicSSE();

        // Таймаут на случай если SSE не отвечает
        setTimeout(() => {
            if (magicSSEConnection && magicSSEConnection.readyState === EventSource.CONNECTING) {
                log('❌ Таймаут магического соединения', 'error');
                magicSSEConnection.close();
                magicSSEConnection = null;
                restoreMagicInterface('❌ Превышено время ожидания магии');
            }
        }, 150000); // 2.5 минуты
        
    } catch (error) {
        log('❌ Ошибка в магическом Real-time поллинге: ' + error.message, 'error');
        restoreMagicInterface('❌ ' + error.message);
    }
}

// === SSE POLLING DEMO FUNCTIONS ===
function startSSEPollingDemo() {
    const prompt = document.getElementById('ssePollingPrompt').value.trim();
    if (!prompt) {
        showSSEPollingStatus('error', '❌ Введите запрос для создания задачи');
        return;
    }

    const btn = document.getElementById('ssePollingBtn');
    const stopBtn = document.getElementById('stopSSEPollingBtn');
    
    // Сброс состояния
    ssePollingTaskCompleted = false;
    
    btn.disabled = true;
    stopBtn.disabled = false;
    document.getElementById('ssePollingPrompt').disabled = true;

    showSSEPollingStatus('info', '� Генерация JWT токена...');
    
    // Сначала генерируем JWT токен
    fetch('/api/internal/generate-token', {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
            'Authorization': 'Bearer ' + document.getElementById('apiKey').value
        },
        body: JSON.stringify({
            user_id: 'sse-demo-user',
            product_data: prompt
        })
    })
    .then(response => response.json())
    .then(tokenData => {
        if (!tokenData.success || !tokenData.token) {
            throw new Error(tokenData.error || 'Не удалось получить JWT токен');
        }
        
        showSSEPollingStatus('success', '✅ JWT токен получен');
        showSSEPollingStatus('info', '�🚀 Создание задачи...');
        
        // Теперь создаём задачу
        return fetch('/api/create', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Authorization': 'Bearer ' + tokenData.token
            },
            body: JSON.stringify({})
        });
    })
    .then(response => response.json())
    .then(data => {
        if (data.taskId && data.token) {
            ssePollingTaskId = data.taskId;
            showSSEPollingStatus('success', `✅ Задача создана: ${data.taskId}`);
            startSSEResultPolling(data.taskId, data.token);
        } else {
            throw new Error(data.error || 'Неизвестная ошибка при создании задачи');
        }
    })
    .catch(error => {
        showSSEPollingStatus('error', `❌ Ошибка: ${error.message}`);
        resetSSEPollingUI();
    });
}

function startSSEResultPolling(taskId, token) {
    showSSEPollingStatus('info', `📡 Подключение SSE к /api/result-polling?taskId=${taskId}&token=***...`);

    const sseUrl = `/api/result-polling?taskId=${taskId}&token=${encodeURIComponent(token)}`;
    ssePollingConnection = new EventSource(sseUrl);
    
    ssePollingConnection.onopen = function() {
        showSSEPollingStatus('success', '📡 SSE соединение установлено');
    };
    
    ssePollingConnection.onmessage = function(event) {
        try {
            const data = JSON.parse(event.data);
            const timestamp = new Date().toLocaleTimeString();
            
            // Отображаем различные типы событий
            switch(data.type) {
                case 'heartbeat':
                    showSSEPollingStatus('info', `[${timestamp}] 💓 Heartbeat`);
                    break;
                    
                case 'task_status':
                    showSSEPollingStatus('info', `[${timestamp}] 📊 Статус: ${data.data.status}`);
                    
                    // Если статус финальный - готовимся к закрытию
                    if (data.data.status === 'completed' || data.data.status === 'failed' || data.data.status === 'error') {
                        showSSEPollingStatus('success', `🎯 Финальный статус: ${data.data.status}`);
                    }
                    break;
                    
                case 'task_completed':
                    ssePollingTaskCompleted = true;
                    showSSEPollingStatus('success', `✅ Задача завершена успешно`);
                    if (data.data.result) {
                        showSSEPollingResult(data.data.result);
                    }
                    // Соединение закроется автоматически, не нужно переподключаться
                    break;
                    
                case 'task_failed':
                    ssePollingTaskCompleted = true;
                    showSSEPollingStatus('error', `❌ Задача завершена с ошибкой: ${data.data.error || 'Неизвестная ошибка'}`);
                    // Соединение закроется автоматически, не нужно переподключаться
                    break;
                    
                case 'error':
                    ssePollingTaskCompleted = true;
                    showSSEPollingStatus('error', `❌ Ошибка SSE: ${data.data.error || 'Неизвестная ошибка'}`);
                    break;
                    
                default:
                    showSSEPollingStatus('info', `[${timestamp}] 📝 Событие ${data.type}`);
            }
            
        } catch (error) {
            showSSEPollingStatus('warning', `⚠️ Ошибка парсинга SSE данных: ${error.message}`);
        }
    };
    
    ssePollingConnection.onerror = function() {
        // Показываем ошибку только если задача не завершена
        if (!ssePollingTaskCompleted) {
            showSSEPollingStatus('error', '❌ Ошибка SSE соединения');
            resetSSEPollingUI();
        }
    };
    
    ssePollingConnection.onclose = function() {
        // Показываем сообщение о закрытии только если задача не завершена
        if (!ssePollingTaskCompleted) {
            showSSEPollingStatus('info', '🔒 SSE соединение закрыто сервером');
        } else {
            showSSEPollingStatus('success', '🔒 SSE соединение закрыто после завершения задачи');
        }
        resetSSEPollingUI();
    };
}

function stopSSEPollingDemo() {
    if (ssePollingConnection) {
        ssePollingTaskCompleted = true; // Принудительно завершаем
        ssePollingConnection.close();
        ssePollingConnection = null;
        showSSEPollingStatus('info', '⏹️ SSE соединение закрыто пользователем');
    }
    resetSSEPollingUI();
}

function resetSSEPollingUI() {
    document.getElementById('ssePollingBtn').disabled = false;
    document.getElementById('stopSSEPollingBtn').disabled = true;
    document.getElementById('ssePollingPrompt').disabled = false;
    ssePollingTaskId = null;
    ssePollingConnection = null;
    ssePollingTaskCompleted = true;
}

function showSSEPollingStatus(type, message) {
    const statusDiv = document.getElementById('ssePollingStatus');
    const timestamp = new Date().toLocaleTimeString();
    const newStatus = `<div class="${type}">[${timestamp}] ${message}</div>`;
    statusDiv.innerHTML = newStatus + statusDiv.innerHTML.split('</div>').slice(0, 10).join('</div>');
}

function showSSEPollingResult(result) {
    const resultDiv = document.getElementById('ssePollingResult');
    resultDiv.innerHTML = `<div class="success"><strong>🎯 Результат задачи:</strong><br><pre style="white-space: pre-wrap; word-wrap: break-word; margin-top: 10px; padding: 10px; background: #f8f9fa; border-radius: 4px;">${result}</pre></div>`;
}

function clearSSEPollingDemo() {
    // Сначала останавливаем любое активное соединение
    if (ssePollingConnection) {
        stopSSEPollingDemo();
    }
    
    document.getElementById('ssePollingStatus').innerHTML = '';
    document.getElementById('ssePollingResult').innerHTML = '';
    document.getElementById('ssePollingPrompt').value = '';
    ssePollingTaskCompleted = false;
}

function estimateTime() {
    const baseUrl = document.getElementById('baseUrl').value;
    const apiKey = document.getElementById('apiKey').value;
    const resultDiv = document.getElementById('estimateTimeResult');
    resultDiv.style.display = 'block';
    resultDiv.innerHTML = '⏳ Запрос...';
    fetch(`${baseUrl}/api/internal/estimated-time`, {
        method: 'GET',
        headers: {
            'Content-Type': 'application/json',
            'Authorization': `Bearer ${apiKey}`
        },
    })
    .then(r => r.json())
    .then(data => {
        if (data.success) {
            resultDiv.innerHTML = `<strong>Оценка времени:</strong> ${data.estimated_time || data.estimatedTime || '???'}`;
        } else {
            resultDiv.innerHTML = `<span style='color:#e74c3c;'>Ошибка: ${data.error || 'Не удалось получить оценку'}</span>`;
        }
    })
    .catch(e => {
        resultDiv.innerHTML = `<span style='color:#e74c3c;'>Ошибка: ${e.message}</span>`;
    });
}


// ===== Internal API Key Cookie Logic =====
function setCookie(name, value, days) {
    let expires = "";
    if (days) {
        const date = new Date();
        date.setTime(date.getTime() + (days*24*60*60*1000));
        expires = "; expires=" + date.toUTCString();
    }
    document.cookie = name + "=" + (value || "")  + expires + "; path=/";
}
function getCookie(name) {
    const nameEQ = name + "=";
    const ca = document.cookie.split(';');
    for(let i=0;i < ca.length;i++) {
        let c = ca[i];
        while (c.charAt(0)==' ') c = c.substring(1,c.length);
        if (c.indexOf(nameEQ) == 0) return c.substring(nameEQ.length,c.length);
    }
    return null;
}
function eraseCookie(name) {   
    document.cookie = name+'=; Max-Age=-99999999; path=/';  
}

function showLogin() {
    document.getElementById('login-modal').style.display = '';
    document.getElementById('main-content').style.display = 'none';
}
function showMain() {
    document.getElementById('login-modal').style.display = 'none';
    document.getElementById('main-content').style.display = '';
    document.getElementById('logoutBtn').style.display = '';
}
function loginWithApiKey() {
    const key = document.getElementById('loginApiKey').value.trim();
    if (!key) {
        document.getElementById('loginError').textContent = 'Введите Internal API Key';
        return;
    }
    setCookie('internal_api_key', key, 30);
    document.getElementById('apiKey').value = key;
    document.getElementById('loginError').textContent = '';
    showMain();
}
function logoutApiKey() {
    eraseCookie('internal_api_key');
    showLogin();
    log('🔑 Вы вышли из системы', 'info');
}

// При старте страницы — если есть ключ в cookie, подставить его в #apiKey
window.addEventListener('DOMContentLoaded', function() {
    const savedKey = getCookie('internal_api_key');
    if (savedKey) {
        const apiKeyInput = document.getElementById('apiKey');
        if (apiKeyInput) {
            apiKeyInput.value = savedKey;
            showMain();
        } else {
            showLogin();
        }
    } else {
        showLogin();
    }
});
// При выходе — очищать поле и cookie
window.logoutApiKey = function() {
    eraseCookie('internal_api_key');
    const apiKeyInput = document.getElementById('apiKey');
    if (apiKeyInput) apiKeyInput.value = '';
    // Скрыть основной интерфейс, показать окно входа
    if (document.getElementById('main-content')) document.getElementById('main-content').style.display = 'none';
    if (document.getElementById('login-modal')) document.getElementById('login-modal').style.display = '';
};

function displayPendingTasks(tasks) {
    const container = document.getElementById('pendingTasksList');
    const title = document.getElementById('pendingTasksTitle');
    title.textContent = `⏳ Ожидающие задачи (${tasks.length})`;
    if (tasks.length === 0) {
        container.innerHTML = '<div style="padding: 20px; text-align: center; color: #666;">Нет ожидающих задач</div>';
        return;
    }
    container.innerHTML = '';
    tasks.forEach(task => {
        const taskEl = document.createElement('div');
        taskEl.className = 'task-item';
        const createdAt = task.created_at ? new Date(task.created_at).toLocaleString() : 'Unknown';
        const waitingTime = task.created_at ? Math.floor((Date.now() - task.created_at) / 1000) : 0;
        const waitingTimeStr = waitingTime > 60 ? `${Math.floor(waitingTime / 60)}м ${waitingTime % 60}с` : `${waitingTime}с`;
        taskEl.innerHTML = `
            <div style="flex: 1;">
                <div style="font-weight: bold; margin-bottom: 5px;">
                    ID: ${task.id}
                    <span class="status pending">⏳ ${task.status || 'pending'}</span>
                </div>
                <div style="font-size: 0.9em; color: #666; margin-bottom: 5px;">
                    User: ${task.user_id || 'Unknown'} | Created: ${createdAt}
                </div>
                <div style="font-size: 0.85em; color: #856404; margin-bottom: 5px; font-weight: 500;">
                    ⏱️ В ожидании: ${waitingTimeStr}
                </div>
                <div style="max-height: 80px; overflow-y: auto; background: #f8f9fa; padding: 8px; border-radius: 4px; font-family: monospace; font-size: 0.85em;">
                    Prompt: ${(() => {
                        try {
                            const params = JSON.parse(task.ollama_params || '{}');
                            return (params.prompt || 'Default prompt').substring(0, 150) + ((params.prompt || '').length > 150 ? '...' : '');
                        } catch {
                            return 'No prompt data';
                        }
                    })()}
                </div>
                ${(task.error_message && task.error_message.length > 0) ? `
                    <div style="margin-top: 8px; max-height: 80px; overflow-y: auto; background: #f8d7da; padding: 8px; border-radius: 4px; font-family: monospace; font-size: 0.85em; color: #721c24;">
                        <strong>Ошибка:</strong> ${task.error_message.substring(0, 200)}${task.error_message.length > 200 ? '...' : ''}
                    </div>
                ` : ''}
            </div>
        `;
        container.appendChild(taskEl);
    });
}

function displayAllTasks(tasks) {
    const container = document.getElementById('allTasksList');
    const title = document.getElementById('allTasksTitle');
    title.textContent = `📄 Все задачи (${tasks.length})`;
    if (tasks.length === 0) {
        container.innerHTML = '<div style="padding: 20px; text-align: center; color: #666;">Нет задач</div>';
        return;
    }
    container.innerHTML = '';
    tasks.forEach(task => {
        const taskEl = document.createElement('div');
        taskEl.className = 'task-item';
        const createdAt = task.created_at ? new Date(task.created_at).toLocaleString() : 'Unknown';
        const statusIcon = task.status === 'completed' ? '✅' : task.status === 'failed' ? '❌' : task.status === 'pending' ? '⏳' : '⚠️';
        let executionTimeStr = '';
        if (task.status === 'completed' || task.status === 'failed') {
            if (task.completed_at && task.created_at) {
                const totalTime = Math.floor((task.completed_at - task.created_at) / 1000);
                const totalTimeStr = totalTime > 60 ? `${Math.floor(totalTime / 60)}м ${totalTime % 60}с` : `${totalTime}с`;
                if (task.processing_started_at) {
                    const processingTime = Math.floor((task.completed_at - task.processing_started_at) / 1000);
                    const processingTimeStr = processingTime > 60 ? `${Math.floor(processingTime / 60)}м ${processingTime % 60}с` : `${processingTime}с`;
                    executionTimeStr = `${totalTimeStr} (обработка: ${processingTimeStr})`;
                } else {
                    executionTimeStr = totalTimeStr;
                }
            }
        } else if (task.status === 'processing' && task.processing_started_at) {
            const currentTime = Math.floor((Date.now() - task.processing_started_at) / 1000);
            executionTimeStr = currentTime > 60 ? `${Math.floor(currentTime / 60)}м ${currentTime % 60}с` : `${currentTime}с`;
        } else if (task.status === 'pending') {
            const waitingTime = task.created_at ? Math.floor((Date.now() - task.created_at) / 1000) : 0;
            executionTimeStr = waitingTime > 60 ? `${Math.floor(waitingTime / 60)}м ${waitingTime % 60}с` : `${waitingTime}с`;
        }
        taskEl.innerHTML = `
            <div style="flex: 1;">
                <div style="font-weight: bold; margin-bottom: 5px;">
                    <span class="status ${task.status || 'unknown'}">${statusIcon}</span>
                    ID: ${task.id}
                </div>
                <div style="font-size: 0.9em; color: #666; margin-bottom: 5px;">
                    User: ${task.user_id || 'Unknown'} | Created: ${createdAt}
                </div>
                ${executionTimeStr ? `
                    <div style="font-size: 0.85em; color: ${task.status === 'completed' ? '#155724' : task.status === 'failed' ? '#721c24' : task.status === 'processing' ? '#004085' : '#856404'}; margin-bottom: 5px; font-weight: 500;">
                        ⏱️ ${task.status === 'completed' ? 'Выполнено за:' : task.status === 'failed' ? 'Не удалось за:' : task.status === 'processing' ? 'Выполняется:' : 'В ожидании:'} ${executionTimeStr}
                    </div>
                ` : ''}
                <div style="max-height: 80px; overflow-y: auto; background: #f8f9fa; padding: 8px; border-radius: 4px; font-family: monospace; font-size: 0.85em;">
                    Prompt: ${(() => {
                        try {
                            const params = JSON.parse(task.ollama_params || '{}');
                            return (params.prompt || 'Default prompt').substring(0, 150) + ((params.prompt || '').length > 150 ? '...' : '');
                        } catch {
                            return 'No prompt data';
                        }
                    })()}
                </div>
                <div style="max-height: 80px; overflow-y: auto; background: #f8f9fa; padding: 8px; border-radius: 4px; font-family: monospace; font-size: 0.85em;">
                    Data: ${(task.product_data || 'No data').substring(0, 150)}${(task.product_data || '').length > 150 ? '...' : ''}
                </div>
                ${(task.result && task.result.length > 0) ? `
                    <div style="margin-top: 8px; max-height: 80px; overflow-y: auto; background: #e7f3ff; padding: 8px; border-radius: 4px; font-family: monospace; font-size: 0.85em;">
                        <strong>Result:</strong> ${task.result.substring(0, 150)}${task.result.length > 150 ? '...' : ''}
                    </div>
                ` : ''}
                ${(task.error_message && task.error_message.length > 0) ? `
                    <div style="margin-top: 8px; max-height: 80px; overflow-y: auto; background: #f8d7da; padding: 8px; border-radius: 4px; font-family: monospace; font-size: 0.85em; color: #721c24;">
                        <strong>Ошибка:</strong> ${task.error_message.substring(0, 200)}${task.error_message.length > 200 ? '...' : ''}
                    </div>
                ` : ''}
                ${createVotingButtons(task)}
            </div>
        `;
        container.appendChild(taskEl);
    });
}

// Функции для голосования за задачи
async function voteTask(taskId, voteType) {
    try {
        const baseUrl = document.getElementById('baseUrl').value;
        const apiKey = document.getElementById('apiKey').value;
        
        // Определяем, какой токен использовать
        let authHeader;
        if (currentJWT) {
            authHeader = `Bearer ${currentJWT}`;
        } else if (apiKey) {
            authHeader = `Bearer ${apiKey}`;
        } else {
            throw new Error('Не настроен ни JWT токен, ни API ключ');
        }

        log(`🗳️ Голосование за задачу ${taskId}: ${voteType}`);

        const response = await fetch(`${baseUrl}/api/tasks/${taskId}/vote`, {
            method: 'POST',
            headers: {
                'Authorization': authHeader,
                'Content-Type': 'application/json'
            },
            body: JSON.stringify({
                vote_type: voteType
            })
        });

        if (!response.ok) {
            const errorData = await response.json().catch(() => ({}));
            throw new Error(errorData.error || `HTTP ${response.status}: ${response.statusText}`);
        }

        const data = await response.json();
        log(`✅ Голос принят: ${data.rating || 'убран'}`, 'success');
        
        // Обновляем список задач в админ панели
        await refreshTaskList();
        
        // Обновляем кнопки оценки в пользовательском интерфейсе
        const userVotingContainer = document.getElementById('userTaskVotingContainer');
        const finalResultVotingContainer = document.getElementById('finalResultVotingContainer');
        
        if (userVotingContainer) {
            const taskForVoting = {
                id: taskId,
                status: 'completed',
                rating: data.rating || null
            };
            userVotingContainer.innerHTML = createVotingButtons(taskForVoting);
        }
        
        if (finalResultVotingContainer) {
            const taskForVoting = {
                id: taskId,
                status: 'completed',
                rating: data.rating || null
            };
            finalResultVotingContainer.innerHTML = createVotingButtons(taskForVoting);
        }
        
        return data;
    } catch (error) {
        log(`❌ Ошибка голосования: ${error.message}`, 'error');
        throw error;
    }
}

function createVotingButtons(task) {
    if (!task.id || task.status !== 'completed') {
        return '';
    }

    const currentRating = task.rating;
    const upvoteClass = currentRating === 'upvote' ? 'vote-active' : '';
    const downvoteClass = currentRating === 'downvote' ? 'vote-active' : '';

    return `
        <div style="margin-top: 8px; padding: 8px; background: #f0f0f0; border-radius: 4px;">
            <div style="font-size: 0.9em; margin-bottom: 5px; color: #555;">
                Оцените качество выполнения:
            </div>
            <div style="display: flex; gap: 5px;">
                <button 
                    class="vote-button ${upvoteClass}" 
                    onclick="voteTask('${task.id}', '${currentRating}' === 'upvote' ? '' : 'upvote')"
                    title="Хорошо выполнено"
                >
                    👍 ${currentRating === 'upvote' ? 'Понравилось' : 'Нравится'}
                </button>
                <button 
                    class="vote-button ${downvoteClass}" 
                    onclick="voteTask('${task.id}', '${currentRating}' === 'downvote' ? '' : 'downvote')"
                    title="Плохо выполнено"
                >
                    👎 ${currentRating === 'downvote' ? 'Не понравилось' : 'Не нравится'}
                </button>
            </div>
        </div>
    `;
}

// Rating Analytics Functions
async function loadRatingAnalytics() {
    try {
        const baseUrl = document.getElementById('baseUrl').value;
        const apiKey = document.getElementById('apiKey').value;
        
        log('📊 Загрузка аналитики рейтингов...');
        
        const response = await fetch(`${baseUrl}/api/internal/rating-analytics`, {
            method: 'GET',
            headers: {
                'Authorization': `Bearer ${apiKey}`,
                'Content-Type': 'application/json'
            }
        });
        
        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }
        
        const data = await response.json();
        
        if (data.success) {
            displayRatingAnalytics(data);
            log('✅ Аналитика рейтингов загружена', 'success');
        } else {
            throw new Error('Failed to load rating analytics');
        }
    } catch (error) {
        log(`❌ Ошибка загрузки аналитики: ${error.message}`, 'error');
        displayRatingAnalyticsError(error.message);
    }
}

function displayRatingAnalytics(data) {
    const summary = data.summary;
    const charts = data.charts;
    const recentRatings = data.recent_ratings;
    
    // Update summary cards
    document.getElementById('totalUpvotes').textContent = summary.upvotes;
    document.getElementById('totalDownvotes').textContent = summary.downvotes;
    document.getElementById('totalRated').textContent = summary.total_rated;
    document.getElementById('qualityScore').textContent = summary.quality_score.toFixed(1);
    
    // Update percentages
    document.getElementById('upvotePercentage').textContent = `${summary.upvote_percentage.toFixed(1)}%`;
    document.getElementById('downvotePercentage').textContent = `${summary.downvote_percentage.toFixed(1)}%`;
    document.getElementById('ratingCoverage').textContent = `${summary.rating_coverage.toFixed(1)}% покрытие`;
    
    // Update quality trend
    const qualityTrendEl = document.getElementById('qualityTrend');
    if (summary.quality_score > 50) {
        qualityTrendEl.textContent = '📈 Отлично';
        qualityTrendEl.style.color = '#28a745';
    } else if (summary.quality_score > 0) {
        qualityTrendEl.textContent = '📊 Хорошо';
        qualityTrendEl.style.color = '#17a2b8';
    } else if (summary.quality_score > -50) {
        qualityTrendEl.textContent = '📉 Средне';
        qualityTrendEl.style.color = '#ffc107';
    } else {
        qualityTrendEl.textContent = '📉 Плохо';
        qualityTrendEl.style.color = '#dc3545';
    }
    
    // Display charts
    displayDailyChart(charts.daily);
    displayHourlyChart(charts.hourly);
    
    // Display recent ratings
    displayRecentRatings(recentRatings);
}

function displayDailyChart(dailyData) {
    const chartContainer = document.getElementById('dailyRatingChart');
    
    if (!dailyData || dailyData.length === 0) {
        chartContainer.innerHTML = '<div class="chart-placeholder">Нет данных за последние 7 дней</div>';
        return;
    }
    
    const maxValue = Math.max(...dailyData.map(d => Math.max(d.upvotes, d.downvotes))) || 1;
    const chartHeight = 200;
    
    let chartHTML = '<div style="position: relative; height: 250px; display: flex; align-items: end; justify-content: space-around; padding: 25px 10px;">';
    
    dailyData.forEach((day, index) => {
        const upvoteHeight = (day.upvotes / maxValue) * chartHeight;
        const downvoteHeight = (day.downvotes / maxValue) * chartHeight;
        const date = new Date(day.period).toLocaleDateString('ru-RU', { month: 'short', day: 'numeric' });
        
        chartHTML += `
            <div style="display: flex; flex-direction: column; align-items: center; position: relative;">
                <div style="display: flex; align-items: end; gap: 2px;">
                    <div class="chart-bar chart-upvote" 
                         style="height: ${upvoteHeight}px; width: 25px;"
                         title="👍 ${day.upvotes} положительных оценок">
                        <div class="chart-bar-value">${day.upvotes}</div>
                    </div>
                    <div class="chart-bar chart-downvote" 
                         style="height: ${downvoteHeight}px; width: 25px;"
                         title="👎 ${day.downvotes} отрицательных оценок">
                        <div class="chart-bar-value">${day.downvotes}</div>
                    </div>
                </div>
                <div class="chart-bar-label" style="margin-top: 10px;">${date}</div>
            </div>
        `;
    });
    
    chartHTML += '</div>';
    chartContainer.innerHTML = chartHTML;
}

function displayHourlyChart(hourlyData) {
    const chartContainer = document.getElementById('hourlyRatingChart');
    
    if (!hourlyData || hourlyData.length === 0) {
        chartContainer.innerHTML = '<div class="chart-placeholder">Нет данных за сегодня</div>';
        return;
    }
    
    const maxValue = Math.max(...hourlyData.map(d => Math.max(d.upvotes, d.downvotes))) || 1;
    const chartHeight = 180;
    
    let chartHTML = '<div style="position: relative; height: 220px; display: flex; align-items: end; justify-content: space-around; padding: 25px 5px; overflow-x: auto;">';
    
    hourlyData.forEach((hour, index) => {
        const upvoteHeight = (hour.upvotes / maxValue) * chartHeight;
        const downvoteHeight = (hour.downvotes / maxValue) * chartHeight;
        const hourLabel = hour.period.split(' ')[1] || hour.period;
        
        chartHTML += `
            <div style="display: flex; flex-direction: column; align-items: center; position: relative; min-width: 30px;">
                <div style="display: flex; align-items: end; gap: 1px;">
                    <div class="chart-bar chart-upvote" 
                         style="height: ${upvoteHeight}px; width: 12px;"
                         title="👍 ${hour.upvotes} оценок в ${hourLabel}:00">
                        ${hour.upvotes > 0 ? `<div class="chart-bar-value" style="font-size: 0.6em;">${hour.upvotes}</div>` : ''}
                    </div>
                    <div class="chart-bar chart-downvote" 
                         style="height: ${downvoteHeight}px; width: 12px;"
                         title="👎 ${hour.downvotes} оценок в ${hourLabel}:00">
                        ${hour.downvotes > 0 ? `<div class="chart-bar-value" style="font-size: 0.6em;">${hour.downvotes}</div>` : ''}
                    </div>
                </div>
                <div class="chart-bar-label" style="margin-top: 8px; font-size: 0.6em;">${hourLabel}</div>
            </div>
        `;
    });
    
    chartHTML += '</div>';
    chartContainer.innerHTML = chartHTML;
}

function displayRecentRatings(recentRatings) {
    const container = document.getElementById('recentRatings');
    
    if (!recentRatings || recentRatings.length === 0) {
        container.innerHTML = '<div class="rating-placeholder">Нет оцененных задач</div>';
        return;
    }
    
    let html = '';
    recentRatings.forEach(task => {
        const rating = task.rating;
        const voteIcon = rating === 'upvote' ? '👍' : '👎';
        const voteClass = rating === 'upvote' ? 'upvote' : 'downvote';
        const timeAgo = getTimeAgo(task.updated_at);
        const shortId = task.id.substring(0, 8) + '...';
        const productPreview = task.product_data.length > 50 
            ? task.product_data.substring(0, 50) + '...'
            : task.product_data;
        
        html += `
            <div class="rating-item">
                <div class="rating-item-info">
                    <div class="rating-item-task" title="${task.product_data}">
                        ${productPreview}
                    </div>
                    <div class="rating-item-user">
                        👤 ${task.user_id} | 🆔 ${shortId}
                    </div>
                </div>
                <div class="rating-item-time">${timeAgo}</div>
                <div class="rating-item-vote ${voteClass}" title="${rating === 'upvote' ? 'Положительная оценка' : 'Отрицательная оценка'}">
                    ${voteIcon}
                </div>
            </div>
        `;
    });
    
    container.innerHTML = html;
}

function getTimeAgo(timestamp) {
    const now = Date.now();
    const diff = now - timestamp;
    const minutes = Math.floor(diff / 60000);
    const hours = Math.floor(diff / 3600000);
    const days = Math.floor(diff / 86400000);
    
    if (days > 0) return `${days}д назад`;
    if (hours > 0) return `${hours}ч назад`;
    if (minutes > 0) return `${minutes}м назад`;
    return 'только что';
}

function displayRatingAnalyticsError(error) {
    const containers = [
        'ratingAnalyticsContainer',
        'dailyRatingChart', 
        'hourlyRatingChart',
        'recentRatings'
    ];
    
    containers.forEach(id => {
        const el = document.getElementById(id);
        if (el) {
            el.innerHTML = `<div class="rating-placeholder">❌ Ошибка загрузки: ${error}</div>`;
        }
    });
    
    // Reset summary values
    ['totalUpvotes', 'totalDownvotes', 'totalRated', 'qualityScore'].forEach(id => {
        const el = document.getElementById(id);
        if (el) el.textContent = '0';
    });
    
    ['upvotePercentage', 'downvotePercentage', 'ratingCoverage'].forEach(id => {
        const el = document.getElementById(id);
        if (el) el.textContent = '0%';
    });
    
    const qualityTrendEl = document.getElementById('qualityTrend');
    if (qualityTrendEl) {
        qualityTrendEl.textContent = '❌';
        qualityTrendEl.style.color = '#dc3545';
    }
}

// === БАЗОВАЯ СТАТИСТИКА РЕЙТИНГОВ ===

// Загрузка базовой статистики рейтингов
async function loadBasicRatingStats() {
    try {
        const apiKey = document.getElementById('apiKey').value;

        log('📊 Загружаю базовую статистику рейтингов...', 'info');
        
        const response = await fetch('/api/internal/rating-stats', {
            headers: {
                'Authorization': `Bearer ${apiKey}`
            }
        });
        
        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }
        
        const data = await response.json();
        
        if (data.success) {
            displayBasicRatingStats(data);
            log('✅ Базовая статистика рейтингов загружена', 'success');
        } else {
            throw new Error('Ответ сервера содержит ошибку');
        }
        
    } catch (error) {
        log(`❌ Ошибка загрузки базовой статистики: ${error.message}`, 'error');
        displayBasicRatingStatsError();
    }
}

// Загрузка статистики для конкретного пользователя
async function loadUserRatingStats() {
    const userIdInput = document.getElementById('userIdInput');
    const userId = userIdInput.value.trim();
    
    if (!userId) {
        log('⚠️ Введите ID пользователя', 'warning');
        userIdInput.focus();
        return;
    }
    
    try {
        const apiKey = document.getElementById('apiKey').value;
        
        log(`👤 Загружаю статистику для пользователя ${userId}...`, 'info');
        
        const response = await fetch(`/api/internal/rating-stats?user_id=${encodeURIComponent(userId)}`, {
            headers: {
                'Authorization': `Bearer ${apiKey}`
            }
        });
        
        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }
        
        const data = await response.json();
        
        if (data.success) {
            displayBasicRatingStats(data, userId);
            displayUserTasks(data.tasks || []);
            log(`✅ Статистика для пользователя ${userId} загружена`, 'success');
        } else {
            throw new Error('Ответ сервера содержит ошибку');
        }
        
    } catch (error) {
        log(`❌ Ошибка загрузки статистики пользователя: ${error.message}`, 'error');
        displayBasicRatingStatsError();
    }
}

// Очистка пользовательской статистики и возврат к общей
function clearUserStats() {
    document.getElementById('userIdInput').value = '';
    document.getElementById('userTasksList').style.display = 'none';
    loadBasicRatingStats();
    log('🔄 Переключение на общую статистику', 'info');
}

// Отображение базовой статистики рейтингов
function displayBasicRatingStats(data, userId = null) {
    const totalElement = document.getElementById('basicTotalRated');
    const upvotesElement = document.getElementById('basicUpvotes');
    const downvotesElement = document.getElementById('basicDownvotes');
    const upvotePercentageElement = document.getElementById('basicUpvotePercentage');
    const downvotePercentageElement = document.getElementById('basicDownvotePercentage');
    const labelElement = document.getElementById('basicStatsLabel');
    
    const total = data.total_rated || 0;
    const upvotes = data.upvotes || 0;
    const downvotes = data.downvotes || 0;
    
    // Подсчет процентов
    const upvotePercentage = total > 0 ? ((upvotes / total) * 100).toFixed(1) : 0;
    const downvotePercentage = total > 0 ? ((downvotes / total) * 100).toFixed(1) : 0;
    
    // Обновление значений
    totalElement.textContent = total;
    upvotesElement.textContent = upvotes;
    downvotesElement.textContent = downvotes;
    upvotePercentageElement.textContent = `${upvotePercentage}%`;
    downvotePercentageElement.textContent = `${downvotePercentage}%`;
    
    // Обновление метки
    if (userId) {
        labelElement.textContent = `Пользователь: ${userId}`;
    } else {
        labelElement.textContent = 'Общая статистика';
    }
    
    // Анимация обновления значений
    animateValue(totalElement, 0, total, 1000);
    animateValue(upvotesElement, 0, upvotes, 1000);
    animateValue(downvotesElement, 0, downvotes, 1000);
}

// Отображение задач пользователя
function displayUserTasks(tasks) {
    const container = document.getElementById('userTasksList');
    const tasksList = document.getElementById('userTasks');
    
    if (!tasks || tasks.length === 0) {
        tasksList.innerHTML = '<div class="tasks-placeholder">У пользователя нет оцененных задач</div>';
        container.style.display = 'block';
        return;
    }
    
    const tasksHtml = tasks.map(task => {
        const ratingIcon = task.rating === 'upvote' ? '👍' : '👎';
        const ratingClass = task.rating === 'upvote' ? 'upvote' : 'downvote';
        const createdAt = new Date(task.created_at).toLocaleString('ru-RU');
        const completedAt = task.completed_at ? new Date(task.completed_at).toLocaleString('ru-RU') : 'Не завершена';
        
        // Обрезаем длинные тексты
        const shortQuery = task.product_data && task.product_data.length > 100 ? 
            task.product_data.substring(0, 100) + '...' : (task.product_data || 'Нет запроса');
        const shortResponse = task.result && task.result.length > 512 ? 
            task.result.substring(0, 512) + '...' : (task.result || 'Нет ответа');
        
        return `
            <div class="user-task-item">
                <div class="user-task-header">
                    <span class="user-task-id">ID: ${task.id}</span>
                    <span class="user-task-rating ${ratingClass}">${ratingIcon}</span>
                </div>
                <div class="user-task-query">
                    <strong>Запрос:</strong> ${shortQuery}
                </div>
                <div class="user-task-response">
                    <strong>Ответ:</strong> ${shortResponse}
                </div>
                <div class="user-task-meta">
                    <span><strong>Создана:</strong> ${createdAt}</span>
                    <span><strong>Завершена:</strong> ${completedAt}</span>
                </div>
            </div>
        `;
    }).join('');
    
    tasksList.innerHTML = tasksHtml;
    container.style.display = 'block';
}

// Отображение ошибки базовой статистики
function displayBasicRatingStatsError() {
    document.getElementById('basicTotalRated').textContent = '?';
    document.getElementById('basicUpvotes').textContent = '?';
    document.getElementById('basicDownvotes').textContent = '?';
    document.getElementById('basicUpvotePercentage').textContent = '?%';
    document.getElementById('basicDownvotePercentage').textContent = '?%';
    document.getElementById('basicStatsLabel').textContent = 'Ошибка загрузки';
    document.getElementById('userTasksList').style.display = 'none';
}

// Анимация изменения числовых значений
function animateValue(element, start, end, duration) {
    if (start === end) return;
    
    const range = end - start;
    const startTime = performance.now();
    
    function updateValue(currentTime) {
        const elapsed = currentTime - startTime;
        const progress = Math.min(elapsed / duration, 1);
        
        // Easing функция для плавной анимации
        const easeProgress = 1 - Math.pow(1 - progress, 3);
        const current = Math.round(start + (range * easeProgress));
        
        element.textContent = current;
        
        if (progress < 1) {
            requestAnimationFrame(updateValue);
        }
    }
    
    requestAnimationFrame(updateValue);
}

// Обработчик для Enter в поле ввода пользователя
document.addEventListener('DOMContentLoaded', function() {
    const userIdInput = document.getElementById('userIdInput');
    if (userIdInput) {
        userIdInput.addEventListener('keypress', function(e) {
            if (e.key === 'Enter') {
                loadUserRatingStats();
            }
        });
    }
    
    // Загружаем базовую статистику при загрузке страницы
    loadBasicRatingStats();
});
