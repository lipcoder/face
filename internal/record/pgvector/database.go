package pgvector

import (
	"context"
	"database/sql"
	"fmt"
	"lipcoder/face/internal/record"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

const embeddingDim = 512

type Store struct {
	ctx context.Context
	db  *sql.DB
}

// Init 初始化数据库连接池，并保证 extension、表、索引存在。
// 只在 main 启动时调用一次。
func Init(ctx context.Context, databaseURL string) (*Store, error) {
	databaseURL = strings.TrimSpace(databaseURL)
	if ctx == nil {
		return nil, fmt.Errorf("%w: context cannot be nil", record.ErrInvalidConfig)
	}
	if databaseURL == "" {
		return nil, fmt.Errorf("%w: database url cannot be empty", record.ErrInvalidConfig)
	}

	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("%w: open database: %w", record.ErrInvalidConfig, err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("%w: ping database: %w", record.ErrUnavailable, err)
	}

	store := &Store{
		ctx: ctx,
		db:  db,
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
			name TEXT NOT NULL,
			embedding vector(512) NOT NULL,
			created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')
		);

		DO $$
		BEGIN
			IF EXISTS (
				SELECT 1
				FROM information_schema.columns
				WHERE table_schema = current_schema()
					AND table_name = 'faces'
					AND column_name = 'created_at'
					AND data_type = 'timestamp with time zone'
			) THEN
				ALTER TABLE faces
				ALTER COLUMN created_at TYPE TIMESTAMP WITHOUT TIME ZONE
				USING created_at AT TIME ZONE 'Asia/Shanghai';
			END IF;
		END $$;

		ALTER TABLE faces
		ALTER COLUMN created_at SET DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai');

		ALTER TABLE faces
		DROP CONSTRAINT IF EXISTS faces_name_key;

		CREATE INDEX IF NOT EXISTS faces_embedding_hnsw_idx
		ON faces
		USING hnsw (embedding vector_cosine_ops);

		CREATE INDEX IF NOT EXISTS faces_name_idx
		ON faces (name);

		CREATE TABLE IF NOT EXISTS signin_logs (
			id BIGSERIAL PRIMARY KEY,
			face_id BIGINT NOT NULL REFERENCES faces(id) ON DELETE CASCADE,
			face_similarity DOUBLE PRECISION NOT NULL,
			recognized_at TIMESTAMP WITHOUT TIME ZONE NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')
		);

		DO $$
		BEGIN
			IF EXISTS (
				SELECT 1
				FROM information_schema.columns
				WHERE table_schema = current_schema()
					AND table_name = 'signin_logs'
					AND column_name = 'recognized_at'
					AND data_type = 'timestamp with time zone'
			) THEN
				ALTER TABLE signin_logs
				ALTER COLUMN recognized_at TYPE TIMESTAMP WITHOUT TIME ZONE
				USING recognized_at AT TIME ZONE 'Asia/Shanghai';
			END IF;
		END $$;

		ALTER TABLE signin_logs
		ALTER COLUMN recognized_at SET DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai');

		CREATE INDEX IF NOT EXISTS signin_logs_face_id_idx
		ON signin_logs (face_id);

		CREATE INDEX IF NOT EXISTS signin_logs_recognized_at_idx
		ON signin_logs (recognized_at DESC);
	`)

	if err != nil {
		return fmt.Errorf("%w: init schema: %w", record.ErrRequestFailed, err)
	}

	return nil
}
