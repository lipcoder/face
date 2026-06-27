package pgvector

import (
	"database/sql"
	"errors"
	"fmt"
	"lipcoder/face/internal/record"
	"math"
	"strconv"
	"strings"
)

// AddFace 添加人脸。
// 同一个 name 可以录入多条人脸数据，唯一身份以返回的 id 为准。
func (s *Store) AddFace(name string, embedding []float64) (int64, error) {
	if err := s.check(); err != nil {
		return 0, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("%w: name cannot be empty", record.ErrInvalidInput)
	}

	embeddingText, err := embeddingToPGVector(embedding)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", record.ErrInvalidEmbedding, err)
	}

	var id int64

	err = s.db.QueryRowContext(s.ctx, `
		INSERT INTO faces (name, embedding)
		VALUES ($1, $2::vector)
		RETURNING id
	`, name, embeddingText).Scan(&id)

	if err != nil {
		return 0, fmt.Errorf("%w: add face: %w", record.ErrRequestFailed, err)
	}

	return id, nil
}

// DeleteFaceByName 删除指定 name 的人脸。
// 不存在返回 ErrNotFound。
func (s *Store) DeleteFaceByName(name string) error {
	if err := s.check(); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%w: name cannot be empty", record.ErrInvalidInput)
	}

	result, err := s.db.ExecContext(s.ctx, `
		DELETE FROM faces
		WHERE name = $1
	`, name)

	if err != nil {
		return fmt.Errorf("%w: delete face by name: %w", record.ErrRequestFailed, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%w: get affected rows: %w", record.ErrRequestFailed, err)
	}

	if affected == 0 {
		return fmt.Errorf("%w: %s", record.ErrNotFound, name)
	}

	return nil
}

// FaceExistsByName 查询指定 name 是否存在。
func (s *Store) FaceExistsByName(name string) (bool, error) {
	if err := s.check(); err != nil {
		return false, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return false, fmt.Errorf("%w: name cannot be empty", record.ErrInvalidInput)
	}

	var exists bool

	err := s.db.QueryRowContext(s.ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM faces
			WHERE name = $1
		)
	`, name).Scan(&exists)

	if err != nil {
		return false, fmt.Errorf("%w: check face exists by name: %w", record.ErrRequestFailed, err)
	}

	return exists, nil
}

// ListFaceNames 查询当前已添加的所有姓名。同名多条人脸只返回一次。
func (s *Store) ListFaceNames() ([]string, error) {
	if err := s.check(); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(s.ctx, `
		SELECT DISTINCT name
		FROM faces
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("%w: list face names: %w", record.ErrRequestFailed, err)
	}
	defer rows.Close()

	names := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("%w: scan face name: %w", record.ErrRequestFailed, err)
		}

		names = append(names, name)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: iterate face names: %w", record.ErrRequestFailed, err)
	}

	return names, nil
}

// SearchFaceByEmbedding 根据 embedding 查询最相似的人脸。
// 没有人脸、相似度低于 threshold，都返回 ErrNotFound。
func (s *Store) SearchFaceByEmbedding(
	embedding []float64,
	threshold float64,
) (record.FaceMatch, error) {
	if err := s.check(); err != nil {
		return record.FaceMatch{}, err
	}
	if math.IsNaN(threshold) || math.IsInf(threshold, 0) {
		return record.FaceMatch{}, fmt.Errorf("%w: threshold must be a finite number", record.ErrInvalidInput)
	}

	if threshold < 0 || threshold > 1 {
		return record.FaceMatch{}, fmt.Errorf("%w: threshold must be between 0 and 1", record.ErrInvalidInput)
	}

	embeddingText, err := embeddingToPGVector(embedding)
	if err != nil {
		return record.FaceMatch{}, fmt.Errorf("%w: %w", record.ErrInvalidEmbedding, err)
	}

	var match record.FaceMatch

	err = s.db.QueryRowContext(s.ctx, `
		WITH nearest AS (
			SELECT
				id,
				name,
				1 - (embedding <=> $1::vector) AS similarity
			FROM faces
			ORDER BY embedding <=> $1::vector
			LIMIT 1
		)
		SELECT
			id,
			name,
			similarity
		FROM nearest
		WHERE similarity >= $2
	`, embeddingText, threshold).Scan(
		&match.ID,
		&match.Name,
		&match.Similarity,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return record.FaceMatch{}, record.ErrNotFound
	}

	if err != nil {
		return record.FaceMatch{}, fmt.Errorf("%w: search face by embedding: %w", record.ErrRequestFailed, err)
	}

	return match, nil
}

func (s *Store) RecordSignLog(faceID int64, faceSimilarity float64) error {
	if err := s.check(); err != nil {
		return err
	}
	if faceID <= 0 {
		return fmt.Errorf("%w: face id must be positive", record.ErrInvalidInput)
	}
	if math.IsNaN(faceSimilarity) || math.IsInf(faceSimilarity, 0) {
		return fmt.Errorf("%w: face similarity must be a finite number", record.ErrInvalidInput)
	}

	_, err := s.db.ExecContext(s.ctx, `
		INSERT INTO signin_logs (
			face_id,
			face_similarity
		) VALUES ($1, $2)
	`, faceID, faceSimilarity)
	if err != nil {
		return fmt.Errorf("%w: record sign log: %w", record.ErrRequestFailed, err)
	}

	return nil
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

func (s *Store) check() error {
	if s == nil {
		return record.ErrInvalidState
	}
	if s.ctx == nil || s.db == nil {
		return record.ErrInvalidState
	}
	return nil
}
