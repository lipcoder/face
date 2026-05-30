package pgvector

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"lipcoder/face/internal/data"
	"math"
	"strconv"
	"strings"

	"github.com/lib/pq"
)

// AddFace 添加人脸。
// name 唯一，重复添加返回 ErrAlreadyExists。
func (s *Store) AddFace(ctx context.Context, name string, embedding []float64) (int64, error) {
	if err := s.check(); err != nil {
		return 0, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New("name cannot be empty")
	}

	embeddingText, err := embeddingToPGVector(embedding)
	if err != nil {
		return 0, err
	}

	var id int64

	err = s.db.QueryRowContext(ctx, `
		INSERT INTO faces (name, embedding)
		VALUES ($1, $2::vector)
		RETURNING id
	`, name, embeddingText).Scan(&id)

	if err != nil {
		if isUniqueViolation(err) {
			return 0, fmt.Errorf("%w: %s", data.ErrAlreadyExists, name)
		}

		return 0, fmt.Errorf("add face: %w", err)
	}

	return id, nil
}

// DeleteFaceByName 删除指定 name 的人脸。
// 不存在返回 ErrNotFound。
func (s *Store) DeleteFaceByName(ctx context.Context, name string) error {
	if err := s.check(); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("name cannot be empty")
	}

	result, err := s.db.ExecContext(ctx, `
		DELETE FROM faces
		WHERE name = $1
	`, name)

	if err != nil {
		return fmt.Errorf("delete face by name: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get affected rows: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("%w: %s", data.ErrNotFound, name)
	}

	return nil
}

// FaceExistsByName 查询指定 name 是否存在。
func (s *Store) FaceExistsByName(ctx context.Context, name string) (bool, error) {
	if err := s.check(); err != nil {
		return false, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return false, errors.New("name cannot be empty")
	}

	var exists bool

	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM faces
			WHERE name = $1
		)
	`, name).Scan(&exists)

	if err != nil {
		return false, fmt.Errorf("check face exists by name: %w", err)
	}

	return exists, nil
}

// ListFaceNames 查询当前已添加的所有人脸姓名。
func (s *Store) ListFaceNames(ctx context.Context) ([]string, error) {
	if err := s.check(); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT name
		FROM faces
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list face names: %w", err)
	}
	defer rows.Close()

	names := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan face name: %w", err)
		}

		names = append(names, name)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate face names: %w", err)
	}

	return names, nil
}

// SearchFaceByEmbedding 根据 embedding 查询最相似的人脸。
// 没有人脸、相似度低于 threshold，都返回 ErrNotFound。
func (s *Store) SearchFaceByEmbedding(
	ctx context.Context,
	embedding []float64,
	threshold float64,
) (string, float64, error) {
	if err := s.check(); err != nil {
		return "", 0, err
	}
	if math.IsNaN(threshold) || math.IsInf(threshold, 0) {
		return "", 0, errors.New("threshold must be a finite number")
	}

	if threshold < 0 || threshold > 1 {
		return "", 0, errors.New("threshold must be between 0 and 1")
	}

	embeddingText, err := embeddingToPGVector(embedding)
	if err != nil {
		return "", 0, err
	}

	var name string
	var similarity float64

	err = s.db.QueryRowContext(ctx, `
		WITH nearest AS (
			SELECT
				name,
				1 - (embedding <=> $1::vector) AS similarity
			FROM faces
			ORDER BY embedding <=> $1::vector
			LIMIT 1
		)
		SELECT
			name,
			similarity
		FROM nearest
		WHERE similarity >= $2
	`, embeddingText, threshold).Scan(
		&name,
		&similarity,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, data.ErrNotFound
	}

	if err != nil {
		return "", 0, fmt.Errorf("search face by embedding: %w", err)
	}

	return name, similarity, nil
}

func embeddingToPGVector(embedding []float64) (string, error) {
	if len(embedding) == 0 {
		return "", errors.New("embedding cannot be empty")
	}

	if len(embedding) != embeddingDim {
		return "", fmt.Errorf(
			"embedding dimension mismatch: got %d, want %d",
			len(embedding),
			embeddingDim,
		)
	}

	var builder strings.Builder
	builder.WriteByte('[')

	for i, value := range embedding {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return "", fmt.Errorf("embedding value at index %d must be finite", i)
		}

		if i > 0 {
			builder.WriteByte(',')
		}

		builder.WriteString(strconv.FormatFloat(value, 'g', -1, 64))
	}

	builder.WriteByte(']')

	return builder.String(), nil
}

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error

	if errors.As(err, &pqErr) {
		return pqErr.Code == "23505"
	}

	return false
}

func (s *Store) check() error {
	if s == nil {
		return errors.New("pgvector store is nil")
	}
	if s.db == nil {
		return errors.New("pgvector database is nil")
	}
	return nil
}
