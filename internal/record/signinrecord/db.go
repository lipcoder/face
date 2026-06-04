package signinrecord

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"lipcoder/face/internal/record"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const createSigninTableSQL = `
CREATE TABLE IF NOT EXISTS signin_logs (
	id BIGSERIAL PRIMARY KEY,
	name TEXT NOT NULL,
	face_similarity DOUBLE PRECISION NOT NULL,
	recognized_at TIMESTAMPTZ NOT NULL
);
`

const insertSigninLogSQL = `
INSERT INTO signin_logs (
	name,
	face_similarity,
	recognized_at
) VALUES ($1, $2, $3);
`

type Config struct {
	CSVDir      string
	DatabaseURL string
	Timeout     time.Duration
}

type Recorder struct {
	csvDir  string
	db      *sql.DB
	timeout time.Duration
	mu      sync.Mutex
}

func New(config Config) (*Recorder, error) {

	if strings.TrimSpace(config.DatabaseURL) == "" {
		return nil, fmt.Errorf("signin database url cannot be empty")
	}

	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	db, err := sql.Open("pgx", config.DatabaseURL)
	if err != nil {
		return nil, err
	}

	recorder := &Recorder{
		csvDir:  config.CSVDir,
		db:      db,
		timeout: timeout,
	}

	if err := recorder.initDB(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return recorder, nil
}

func (r *Recorder) Close() error {
	if r == nil || r.db == nil {
		return nil
	}

	return r.db.Close()
}

func (r *Recorder) RecordSignLog(name string, faceSimilarity float64) error {
	if r == nil {
		return fmt.Errorf("recorder cannot be nil")
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}

	signLog := record.SignLog{
		Name:           name,
		FaceSimilarity: faceSimilarity,
		RecognizedAt:   time.Now(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	if err := r.writeDB(ctx, signLog); err != nil {
		return err
	}

	return nil
}

func (r *Recorder) initDB() error {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	if err := r.db.PingContext(ctx); err != nil {
		return err
	}

	_, err := r.db.ExecContext(ctx, createSigninTableSQL)
	return err
}

func (r *Recorder) writeDB(ctx context.Context, signLog record.SignLog) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("signin database cannot be nil")
	}

	_, err := r.db.ExecContext(
		ctx,
		insertSigninLogSQL,
		signLog.Name,
		signLog.FaceSimilarity,
		signLog.RecognizedAt,
	)

	return err
}
