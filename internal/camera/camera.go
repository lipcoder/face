package camera

import "context"

type Camera interface {
	Capture(ctx context.Context) ([]byte, error)
}
