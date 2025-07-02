package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// RequestQueue manages database requests to prevent BUSY errors
type RequestQueue struct {
	mutex     sync.Mutex
	semaphore chan struct{}
}

// NewRequestQueue creates a new request queue with specified concurrency limit
func NewRequestQueue(maxConcurrency int) *RequestQueue {
	return &RequestQueue{
		semaphore: make(chan struct{}, maxConcurrency),
	}
}

// Execute runs a function with controlled concurrency
func (rq *RequestQueue) Execute(ctx context.Context, fn func() error) error {
	// Try to acquire semaphore with context timeout
	select {
	case rq.semaphore <- struct{}{}:
		defer func() { <-rq.semaphore }()
		return fn()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ExecuteWithWriteLock runs a function with exclusive write access
func (rq *RequestQueue) ExecuteWithWriteLock(ctx context.Context, fn func() error) error {
	// For critical write operations, use mutex for exclusive access
	rq.mutex.Lock()
	defer rq.mutex.Unlock()

	select {
	case rq.semaphore <- struct{}{}:
		defer func() { <-rq.semaphore }()
		return fn()
	case <-ctx.Done():
		return ctx.Err()
	}
}

type DB struct {
	*sql.DB
	requestQueue *RequestQueue
}

func NewSQLiteDB(dbPath string) (*DB, error) {
	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Open database
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Set connection pool settings - reduced for better control
	sqlDB.SetMaxOpenConns(5)
	sqlDB.SetMaxIdleConns(3)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	// Test connection
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	db := &DB{
		DB:           sqlDB,
		requestQueue: NewRequestQueue(3), // Allow max 3 concurrent DB operations
	}

	// Enable foreign keys and other SQLite optimizations
	if err := db.initSQLite(); err != nil {
		return nil, fmt.Errorf("failed to initialize SQLite: %w", err)
	}

	return db, nil
}

func (db *DB) initSQLite() error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA cache_size = 1000",
		"PRAGMA temp_store = MEMORY",
		"PRAGMA busy_timeout = 30000", // 30 seconds timeout for BUSY
		"PRAGMA wal_autocheckpoint = 1000",
		"PRAGMA wal_checkpoint(TRUNCATE)", // Clean up WAL file
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("failed to execute pragma %s: %w", pragma, err)
		}
	}

	return nil
}

func (db *DB) Close() error {
	return db.DB.Close()
}

// Transaction helper
func (db *DB) WithTransaction(fn func(*sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}

	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		} else if err != nil {
			tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	err = fn(tx)
	return err
}

// RunMigrations executes database migrations
func (db *DB) RunMigrations() error {
	migrationSQL := `
	-- Основная таблица задач
	CREATE TABLE IF NOT EXISTS tasks (
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
		estimated_duration INTEGER DEFAULT 300000,
		actual_duration INTEGER
	);

	-- Rate limiting
	CREATE TABLE IF NOT EXISTS rate_limits (
		user_id TEXT PRIMARY KEY,
		request_count INTEGER NOT NULL DEFAULT 0,
		window_start INTEGER NOT NULL,
		last_request INTEGER NOT NULL
	);

	-- Метрики процессоров
	CREATE TABLE IF NOT EXISTS processor_metrics (
		processor_id TEXT PRIMARY KEY,
		cpu_usage REAL NOT NULL DEFAULT 0.0,
		memory_usage REAL NOT NULL DEFAULT 0.0,
		queue_size INTEGER NOT NULL DEFAULT 0,
		active_tasks INTEGER NOT NULL DEFAULT 0,
		last_updated INTEGER NOT NULL,
		created_at INTEGER NOT NULL DEFAULT (unixepoch() * 1000)
	);

	-- Индексы для производительности
	CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
	CREATE INDEX IF NOT EXISTS idx_tasks_user_id ON tasks(user_id);
	CREATE INDEX IF NOT EXISTS idx_tasks_processor_id ON tasks(processor_id);
	CREATE INDEX IF NOT EXISTS idx_tasks_created_at ON tasks(created_at);
	CREATE INDEX IF NOT EXISTS idx_tasks_timeout_at ON tasks(timeout_at);
	CREATE INDEX IF NOT EXISTS idx_rate_limits_window_start ON rate_limits(window_start);
	CREATE INDEX IF NOT EXISTS idx_processor_metrics_last_updated ON processor_metrics(last_updated);
	`

	_, err := db.Exec(migrationSQL)
	return err
}

// QueuedQuery executes a SELECT query through the request queue
func (db *DB) QueuedQuery(query string, args ...interface{}) (*sql.Rows, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var rows *sql.Rows
	var err error

	err = db.requestQueue.Execute(ctx, func() error {
		rows, err = db.Query(query, args...)
		return err
	})

	return rows, err
}

// QueuedQueryRow executes a SELECT query for a single row through the request queue
func (db *DB) QueuedQueryRow(query string, args ...interface{}) *sql.Row {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var row *sql.Row

	// For QueryRow, we don't need to handle error here as it's returned by Scan()
	db.requestQueue.Execute(ctx, func() error {
		row = db.QueryRow(query, args...)
		return nil
	})

	return row
}

// QueuedExec executes an INSERT/UPDATE/DELETE query through the request queue
func (db *DB) QueuedExec(query string, args ...interface{}) (sql.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var result sql.Result
	var err error

	err = db.requestQueue.Execute(ctx, func() error {
		result, err = db.Exec(query, args...)
		return err
	})

	return result, err
}

// QueuedExecWithWriteLock executes a critical write operation with exclusive access
func (db *DB) QueuedExecWithWriteLock(query string, args ...interface{}) (sql.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var result sql.Result
	var err error

	err = db.requestQueue.ExecuteWithWriteLock(ctx, func() error {
		result, err = db.Exec(query, args...)
		return err
	})

	return result, err
}
