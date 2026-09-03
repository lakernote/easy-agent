package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("SQLite 路径不能为空")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := protectDatabaseFiles(path); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func protectDatabaseFiles(path string) error {
	for _, file := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(file, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("保护 SQLite 文件 %s: %w", file, err)
		}
	}
	return nil
}

func (store *Store) Close() error { return store.db.Close() }

func (store *Store) Ping(ctx context.Context) error { return store.db.PingContext(ctx) }

func formatTime(value time.Time) string { return value.Format(time.RFC3339Nano) }
