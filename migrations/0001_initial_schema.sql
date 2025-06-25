-- Migration: Initial schema
-- Version: 0001
-- Created: 2025-06-24

CREATE TABLE tasks (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    product_data TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    result TEXT,
    error_message TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    completed_at INTEGER,
    priority INTEGER DEFAULT 0,
    retry_count INTEGER DEFAULT 0,
    max_retries INTEGER DEFAULT 3,
    processor_id TEXT,
    processing_started_at INTEGER,
    heartbeat_at INTEGER,
    timeout_at INTEGER,
    ollama_params TEXT,
    estimated_duration INTEGER DEFAULT 300000, -- 5 minutes default
    actual_duration INTEGER
);

-- Rate limiting table
CREATE TABLE rate_limits (
    user_id TEXT PRIMARY KEY,
    request_count INTEGER NOT NULL DEFAULT 0,
    window_start INTEGER NOT NULL,
    last_request INTEGER NOT NULL
);

-- Table for tracking processor performance metrics
CREATE TABLE processor_metrics (
    processor_id TEXT PRIMARY KEY,
    cpu_usage REAL NOT NULL DEFAULT 0.0,
    memory_usage REAL NOT NULL DEFAULT 0.0,
    queue_size INTEGER NOT NULL DEFAULT 0,
    active_tasks INTEGER NOT NULL DEFAULT 0,
    last_updated INTEGER NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch() * 1000)
);

-- Add processor affinity for certain task types (optional)
CREATE TABLE task_processor_affinity (
    task_type TEXT NOT NULL,
    processor_id TEXT NOT NULL,
    affinity_score REAL NOT NULL DEFAULT 1.0,
    PRIMARY KEY (task_type, processor_id)
);

-- Create view for processor load overview
CREATE VIEW processor_load_view AS
SELECT 
    pm.processor_id,
    pm.cpu_usage,
    pm.memory_usage,
    pm.queue_size,
    pm.last_updated,
    COUNT(t.id) as current_active_tasks,
    COALESCE(AVG(unixepoch() * 1000 - t.processing_started_at), 0) as avg_processing_time,
    CASE 
        WHEN pm.last_updated < unixepoch() * 1000 - 300000 THEN 'offline'
        WHEN pm.cpu_usage > 80 OR pm.memory_usage > 80 THEN 'overloaded'
        WHEN COUNT(t.id) > 10 THEN 'busy'
        ELSE 'available'
    END as status
FROM processor_metrics pm
LEFT JOIN tasks t ON pm.processor_id = t.processor_id AND t.status = 'processing'
GROUP BY pm.processor_id, pm.cpu_usage, pm.memory_usage, pm.queue_size, pm.last_updated;


CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_tasks_user_id ON tasks(user_id);
CREATE INDEX idx_tasks_created_at ON tasks(created_at);
CREATE INDEX idx_tasks_priority ON tasks(priority DESC, created_at ASC);

-- Create index for timeout checks
CREATE INDEX idx_tasks_processor_id ON tasks(processor_id);
CREATE INDEX idx_tasks_timeout ON tasks(timeout_at) WHERE timeout_at IS NOT NULL;
CREATE INDEX idx_tasks_timeout_processing ON tasks(timeout_at) WHERE status = 'processing';
CREATE INDEX idx_tasks_heartbeat ON tasks(heartbeat_at) WHERE heartbeat_at IS NOT NULL;
CREATE INDEX idx_tasks_processor_status ON tasks(processor_id, status);

-- Add additional index for better performance on ollama_params queries
CREATE INDEX idx_tasks_ollama_params_not_null ON tasks(ollama_params) WHERE ollama_params IS NOT NULL;
-- Index for tasks with custom models in ollama_params
CREATE INDEX idx_tasks_ollama_model ON tasks(json_extract(ollama_params, '$.model')) WHERE json_extract(ollama_params, '$.model') IS NOT NULL;

-- Enhanced indexes for work stealing and load balancing
CREATE INDEX idx_tasks_processor_heartbeat ON tasks(processor_id, heartbeat_at) WHERE status = 'processing';
CREATE INDEX idx_tasks_steal_candidates ON tasks(processor_id, heartbeat_at, priority) WHERE status = 'processing';
CREATE INDEX idx_tasks_priority_created ON tasks(priority DESC, created_at ASC) WHERE status = 'pending';


CREATE INDEX idx_rate_limits_window ON rate_limits(window_start);

CREATE INDEX idx_processor_metrics_updated ON processor_metrics(last_updated);
CREATE INDEX idx_processor_metrics_load ON processor_metrics(cpu_usage, memory_usage, queue_size);

-- Index for pending task claiming (most critical)
CREATE INDEX IF NOT EXISTS idx_tasks_claim_pending ON tasks(status, priority DESC, created_at ASC) 
WHERE status = 'pending';

-- Index for processor task management
CREATE INDEX IF NOT EXISTS idx_tasks_processor_management ON tasks(processor_id, status, heartbeat_at);

-- Index for work stealing queries  
CREATE INDEX IF NOT EXISTS idx_tasks_work_steal ON tasks(processor_id, heartbeat_at, priority DESC)
WHERE status = 'processing';

-- Composite index for timeout detection
CREATE INDEX IF NOT EXISTS idx_tasks_timeout_detection ON tasks(status, timeout_at)
WHERE status = 'processing' AND timeout_at IS NOT NULL;

-- Index for processor metrics queries
CREATE INDEX IF NOT EXISTS idx_processor_metrics_active ON processor_metrics(last_updated, cpu_usage, memory_usage)
WHERE last_updated > 0;

-- Index for completed tasks analysis (for estimated time calculation)
CREATE INDEX IF NOT EXISTS idx_tasks_completed_analysis ON tasks(status, completed_at, processing_started_at)
WHERE status = 'completed' AND completed_at IS NOT NULL AND processing_started_at IS NOT NULL;

-- Index for user task lookup
CREATE INDEX IF NOT EXISTS idx_tasks_user_status ON tasks(user_id, status, created_at DESC);

-- Index for task priority querying with processor assignment
CREATE INDEX IF NOT EXISTS idx_tasks_priority_processor ON tasks(priority DESC, processor_id, created_at ASC);

-- Performance optimization: partial index for active processing tasks only
CREATE INDEX IF NOT EXISTS idx_tasks_active_processing ON tasks(processor_id, processing_started_at, heartbeat_at)
WHERE status = 'processing';

-- Index for cleanup operations
CREATE INDEX IF NOT EXISTS idx_tasks_cleanup ON tasks(status, completed_at, updated_at)
WHERE status IN ('completed', 'failed');
