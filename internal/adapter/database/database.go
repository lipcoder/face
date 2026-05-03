package facedb

import (
	"context"
	"database/sql"
	"errors"

	_ "github.com/lib/pq"

	"lipcoder/face/internal/config"
)

var (
	ErrNotFound = errors.New("face not found")
)

// openDB 统一从 config.Load().DatabaseURL 获取数据库连接地址
func openDB(ctx context.Context) (*sql.DB, error) {
	con := config.Load()

	if con.DatabaseURL == "" {
		return nil, errors.New("database url cannot be empty")
	}

	db, err := sql.Open("postgres", con.DatabaseURL)
	if err != nil {
		return nil, err
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

// InitFacesTable 初始化 pgvector 插件和 faces 表
// 如果你已经在数据库里手动建过表，可以不调用这个函数
func InitFacesTable(ctx context.Context) error {
	db, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

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

// AddFace新增一张人脸 id不需要手动传，数据库会自动生成
func AddFace(ctx context.Context, name string, embedding string) (int64, error) {
	if name == "" {
		return 0, errors.New("name cannot be empty")
	}

	if embedding == "" {
		return 0, errors.New("embedding cannot be empty")
	}

	db, err := openDB(ctx)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	var id int64

	err = db.QueryRowContext(ctx, `
		INSERT INTO faces (name, embedding)
		VALUES ($1, $2::vector)
		RETURNING id
	`, name, embedding).Scan(&id)

	if err != nil {
		return 0, err
	}

	return id, nil
}

// DeleteFaceByName 按 name 删除人脸
// 当前 InitFacesTable 里已经给 name 加了 UNIQUE，否则同名会被全部删除
func DeleteFaceByName(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("name cannot be empty")
	}

	db, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	result, err := db.ExecContext(ctx, `
		DELETE FROM faces
		WHERE name = $1
	`, name)

	if err != nil {
		return err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if affected == 0 {
		return ErrNotFound
	}

	return nil
}

// FaceExistsByName 按 name 检查数据库里有没有这个人
func FaceExistsByName(ctx context.Context, name string) (bool, error) {
	if name == "" {
		return false, errors.New("name cannot be empty")
	}

	db, err := openDB(ctx)
	if err != nil {
		return false, err
	}
	defer db.Close()

	var exists bool

	err = db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM faces
			WHERE name = $1
		)
	`, name).Scan(&exists)

	if err != nil {
		return false, err
	}

	return exists, nil
}

// SearchFaceByEmbedding 按 embedding 查询最相似的人脸
// - pgvector 的 <=> 是 cosine distance
// - 1 - cosine distance 是 cosine similarity
// - cosine_similarity >= threshold 才认为匹配成功
func SearchFaceByEmbedding(
	ctx context.Context,
	embedding string,
	threshold float64,
) (string, float64, error) {
	if embedding == "" {
		return "", 0, errors.New("embedding cannot be empty")
	}

	db, err := openDB(ctx)
	if err != nil {
		return "", 0, err
	}
	defer db.Close()

	var name string
	var similarity float64

	err = db.QueryRowContext(ctx, `
		SELECT
			name,
			1 - (embedding <=> $1::vector) AS cosine_similarity
		FROM faces
		WHERE 1 - (embedding <=> $1::vector) >= $2
		ORDER BY embedding <=> $1::vector
		LIMIT 1
	`, embedding, threshold).Scan(
		&name,
		&similarity,
	)

	if err == sql.ErrNoRows {
		return "", 0, ErrNotFound
	}

	if err != nil {
		return "", 0, err
	}

	return name, similarity, nil
}
