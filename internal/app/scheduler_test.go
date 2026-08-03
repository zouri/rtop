package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"rtop/internal/model"
)

func TestSchedulerPublishesLatestAndStops(t *testing.T) {
	source := &countingSource{}
	scheduler := NewScheduler(source, 2*time.Millisecond, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	output := scheduler.Run(ctx)

	deadline := time.After(time.Second)
	for source.Count() < 3 {
		select {
		case <-deadline:
			t.Fatal("scheduler did not collect repeatedly")
		case <-time.After(time.Millisecond):
		}
	}
	snapshot := <-output
	if snapshot.Timestamp.IsZero() {
		t.Fatal("received empty snapshot")
	}
	if len(output) != 0 {
		t.Fatalf("output queue length = %d, want 0 after one read", len(output))
	}

	scheduler.Refresh()
	select {
	case <-output:
	case <-time.After(time.Second):
		t.Fatal("refresh did not publish a snapshot")
	}

	cancel()
	select {
	case _, ok := <-output:
		if ok {
			select {
			case _, ok = <-output:
				if ok {
					t.Fatal("output remained open")
				}
			case <-time.After(time.Second):
				t.Fatal("scheduler did not close output")
			}
		}
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
}

type countingSource struct {
	mu    sync.Mutex
	count int
}

func (s *countingSource) Build(context.Context) model.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count++
	return model.Snapshot{Timestamp: time.Now()}
}

func (s *countingSource) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}
