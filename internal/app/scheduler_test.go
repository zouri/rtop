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
	scheduler := NewScheduler(source.Build, 2*time.Millisecond, time.Second)
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

func TestPeriodicSnapshotTimeoutCoversSamplingWindow(t *testing.T) {
	tests := []struct {
		name       string
		interval   time.Duration
		cpuSample  time.Duration
		wantBudget time.Duration
	}{
		{name: "default", interval: time.Second, cpuSample: 200 * time.Millisecond, wantBudget: 1200 * time.Millisecond},
		{name: "long interval", interval: 5 * time.Second, cpuSample: 200 * time.Millisecond, wantBudget: 5 * time.Second},
		{name: "long sample", interval: 100 * time.Millisecond, cpuSample: 2 * time.Second, wantBudget: 3 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := runtimeConfig{interval: test.interval, cpuSampleDuration: test.cpuSample}
			if got := periodicSnapshotTimeout(config); got != test.wantBudget {
				t.Fatalf("periodicSnapshotTimeout() = %s, want %s", got, test.wantBudget)
			}
		})
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
