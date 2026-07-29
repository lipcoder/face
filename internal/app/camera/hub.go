package camera

import (
	"context"
	"fmt"
	"sync"
)

const defaultSubscriberBuffer = 2

type Subscription struct {
	Frames <-chan Frame
	cancel func()
	once   sync.Once
}

func (s *Subscription) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
	})
}

// Hub broadcasts one camera stream to independent consumers. Every subscriber
// has a small bounded queue. Slow consumers lose old frames instead of adding
// latency or blocking camera capture.
type Hub struct {
	source Source

	mu          sync.Mutex
	started     bool
	closed      bool
	nextID      uint64
	subscribers map[uint64]chan Frame
	errors      chan error
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

func NewHub(source Source) (*Hub, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: source cannot be nil", ErrInvalidConfig)
	}
	return &Hub{
		source:      source,
		subscribers: make(map[uint64]chan Frame),
		errors:      make(chan error, 4),
	}, nil
}

func (h *Hub) ID() string {
	if h == nil || h.source == nil {
		return ""
	}
	return h.source.ID()
}

func (h *Hub) Start(ctx context.Context) error {
	if h == nil || h.source == nil || ctx == nil {
		return ErrInvalidConfig
	}

	h.mu.Lock()
	if h.started || h.closed {
		h.mu.Unlock()
		return ErrInvalidState
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := h.source.Start(streamCtx)
	if err != nil {
		cancel()
		h.mu.Unlock()
		return err
	}
	h.started = true
	h.cancel = cancel
	h.wg.Add(1)
	h.mu.Unlock()

	go h.broadcast(streamCtx, stream)
	return nil
}

func (h *Hub) Errors() <-chan error {
	if h == nil {
		return nil
	}
	return h.errors
}

func (h *Hub) Subscribe(buffer int) (*Subscription, error) {
	if h == nil {
		return nil, ErrInvalidState
	}
	if buffer <= 0 {
		buffer = defaultSubscriberBuffer
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.started || h.closed {
		return nil, ErrInvalidState
	}

	h.nextID++
	id := h.nextID
	frames := make(chan Frame, buffer)
	h.subscribers[id] = frames
	return &Subscription{
		Frames: frames,
		cancel: func() { h.unsubscribe(id) },
	}, nil
}

func (h *Hub) Close() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	cancel := h.cancel
	h.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	h.wg.Wait()
	return h.source.Close()
}

func (h *Hub) broadcast(ctx context.Context, stream Stream) {
	defer h.wg.Done()
	defer h.closeSubscribers()

	frames := stream.Frames
	errs := stream.Errors
	for frames != nil || errs != nil {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-frames:
			if !ok {
				frames = nil
				continue
			}
			h.publish(frame)
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				select {
				case h.errors <- err:
				default:
				}
			}
		}
	}
}

func (h *Hub) publish(frame Frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, subscriber := range h.subscribers {
		select {
		case subscriber <- frame:
			continue
		default:
		}

		// Keep the newest frame. This is the key backpressure policy for a
		// real-time stream: bounded memory and bounded latency.
		select {
		case <-subscriber:
		default:
		}
		select {
		case subscriber <- frame:
		default:
		}
	}
}

func (h *Hub) unsubscribe(id uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if subscriber, ok := h.subscribers[id]; ok {
		delete(h.subscribers, id)
		close(subscriber)
	}
}

func (h *Hub) closeSubscribers() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, subscriber := range h.subscribers {
		delete(h.subscribers, id)
		close(subscriber)
	}
	select {
	case <-h.errors:
	default:
	}
	close(h.errors)
}
