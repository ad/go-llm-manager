package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
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

	// Set connection pool settings
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)

	// Test connection
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	db := &DB{DB: sqlDB}

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
