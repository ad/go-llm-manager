package database

import (
	"os"
	"testing"
)

// NewTestDB создает временную SQLite базу для тестов (через temp файл)
func NewTestDB(t *testing.T) *DB {
	tmpfile, err := os.CreateTemp("", "testdb-*.sqlite")
	if err != nil {
		t.Fatalf("failed to create temp db file: %v", err)
	}
	db, err := NewSQLiteDB(tmpfile.Name())
	if err != nil {
		tmpfile.Close()
		os.Remove(tmpfile.Name())
		t.Fatalf("failed to create test db: %v", err)
	}
	if err := db.RunMigrations(); err != nil {
		db.Close()
		os.Remove(tmpfile.Name())
		t.Fatalf("failed to run migrations: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		os.Remove(tmpfile.Name())
	})
	return db
}
