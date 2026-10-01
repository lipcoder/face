package source

import (
	"context"

	"github.com/lipcoder/face/internal/media"
)

type Source interface {
	Read(ctx context.Context) (*media.Frame, error)
	Close() error
}
