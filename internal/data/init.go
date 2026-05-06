package facedb

import (
	"context"

	_ "github.com/lib/pq"
)

// InitFacesTable 初始化 pgvector 插件和 faces 表。
// 如果你已经在数据库里手动建过表，可以不调用这个函数。
func InitFacesTable(ctx context.Context) error {
	db, err := GetDB(ctx)
	if err != nil {
		return err
	}

	_, err = db.ExecContext(ctx, `
		CREATE EXTENSION IF NOT EXISTS vector;

		CREATE TABLE IF NOT EXISTS faces (
			id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			embedding vector(512) NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS faces_embedding_cosine_idx
		ON faces
		USING hnsw (embedding vector_cosine_ops);
	`)

	return err
}