package pgvector

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const embeddingDim = 512

type Store struct {
	db *sql.DB
}

// Init 初始化数据库连接池，并保证 extension、表、索引存在。
// 只在 main 启动时调用一次。
func Init(ctx context.Context, databaseURL string) (*Store, error) {
	databaseURL = strings.TrimSpace(databaseURL)
	if databaseURL == "" {
		return nil, errors.New("database url cannot be empty")
	}

	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	store := &Store{
		db: db,
	}

	if err := store.initSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

// Close 关闭数据库连接池。
// 只在 main 退出时调用。
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) initSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE EXTENSION IF NOT EXISTS vector;

		CREATE TABLE IF NOT EXISTS faces (
			id BIGSERIAL PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			embedding vector(512) NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS faces_embedding_hnsw_idx
		ON faces
		USING hnsw (embedding vector_cosine_ops);
	`)

	if err != nil {
		return fmt.Errorf("init face schema: %w", err)
	}

	return nil
}
