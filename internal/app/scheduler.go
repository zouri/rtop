package app

import (
	"context"
	"time"

	"rtop/internal/model"
)

// Scheduler performs one collection at a time and publishes only the newest
// snapshot. Slow consumers never cause an unbounded queue.
type Scheduler struct {
	build    func(context.Context) model.Snapshot
	interval time.Duration
	timeout  time.Duration
	refresh  chan struct{}
}

func NewScheduler(build func(context.Context) model.Snapshot, interval, timeout time.Duration) *Scheduler {
	if interval <= 0 {
		interval = time.Second
	}
	if timeout <= 0 {
		timeout = interval
		if timeout < time.Second {
			timeout = time.Second
		}
	}
	return &Scheduler{
		build: build, interval: interval, timeout: timeout, refresh: make(chan struct{}, 1),
	}
}

func (s *Scheduler) Run(ctx context.Context) <-chan model.Snapshot {
	output := make(chan model.Snapshot, 1)
	go func() {
		defer close(output)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		s.publish(ctx, output)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.publish(ctx, output)
			case <-s.refresh:
				s.publish(ctx, output)
			}
		}
	}()
	return output
}

func (s *Scheduler) Refresh() {
	select {
	case s.refresh <- struct{}{}:
	default:
	}
}

func (s *Scheduler) publish(parent context.Context, output chan model.Snapshot) {
	ctx, cancel := context.WithTimeout(parent, s.timeout)
	snapshot := s.build(ctx)
	cancel()
	if parent.Err() != nil {
		return
	}
	select {
	case output <- snapshot:
		return
	default:
	}
	select {
	case <-output:
	default:
	}
	select {
	case output <- snapshot:
	default:
	}
}
