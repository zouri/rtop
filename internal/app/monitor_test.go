package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

func TestMonitorKeepsOtherCollectorsWhenCPUFails(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "generic-linux")
	base := sysfs.MustNew(root)
	systemFS := &readErrorFS{
		SystemFS: base,
		path:     "/proc/stat",
		err:      os.ErrPermission,
	}

	snapshot := NewMonitor(systemFS, time.Nanosecond).Snapshot(context.Background())
	if snapshot.CPU.Overall.Status != model.PermissionDenied {
		t.Fatalf("CPU status = %q", snapshot.CPU.Overall.Status)
	}
	if snapshot.Memory.Total.Status != model.Available {
		t.Fatalf("memory status = %q", snapshot.Memory.Total.Status)
	}
	if len(snapshot.Thermals) != 2 {
		t.Fatalf("thermal zones = %d", len(snapshot.Thermals))
	}
	if len(snapshot.Cooling) != 1 {
		t.Fatalf("cooling devices = %d", len(snapshot.Cooling))
	}
	if len(snapshot.Power) != 4 || len(snapshot.Fans) != 1 {
		t.Fatalf("power sensors=%d fans=%d", len(snapshot.Power), len(snapshot.Fans))
	}
	if len(snapshot.Warnings) == 0 {
		t.Fatal("expected a CPU warning")
	}
}

func TestMonitorCachesSystemInformation(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "generic-linux")
	systemFS := &countingFS{SystemFS: sysfs.MustNew(root), reads: make(map[string]int)}
	monitor := NewMonitor(systemFS, time.Nanosecond)
	systemFS.reset()

	monitor.SystemInfo()
	monitor.SystemInfo()
	monitor.Snapshot(context.Background())
	monitor.Snapshot(context.Background())

	for _, path := range []string{
		"/proc/sys/kernel/hostname",
		"/proc/sys/kernel/osrelease",
		"/etc/os-release",
		"/proc/device-tree/model",
		"/proc/device-tree/compatible",
	} {
		if reads := systemFS.readCount(path); reads != 0 {
			t.Fatalf("cached system path %s read %d times", path, reads)
		}
	}
}

func TestMonitorCadencesSlowCollectorsAndSupportsForcedRefresh(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "generic-linux")
	systemFS := &countingFS{SystemFS: sysfs.MustNew(root)}
	monitor := NewMonitorWithIntervals(systemFS, time.Nanosecond, 2*time.Second, 5*time.Second)
	now := time.Unix(1_700_000_000, 0)
	monitor.now = func() time.Time { return now }

	first := monitor.Snapshot(context.Background())
	if len(first.Processes) != 1 || len(first.Thermals) != 2 {
		t.Fatalf("first snapshot processes=%d thermals=%d", len(first.Processes), len(first.Thermals))
	}

	now = now.Add(time.Second)
	systemFS.reset()
	monitor.Snapshot(context.Background())
	if calls := systemFS.globCount("/proc/[0-9]*"); calls != 0 {
		t.Fatalf("process collection ran inside cadence window: %d globs", calls)
	}
	if reads := systemFS.thermalReads(); reads != 0 {
		t.Fatalf("thermal collection ran inside cadence window: %d reads", reads)
	}

	now = now.Add(2 * time.Second)
	systemFS.reset()
	monitor.Snapshot(context.Background())
	if calls := systemFS.globCount("/proc/[0-9]*"); calls != 1 {
		t.Fatalf("process collection after cadence = %d globs, want 1", calls)
	}
	if reads := systemFS.thermalReads(); reads != 0 {
		t.Fatalf("thermal collection ran before its cadence expired: %d reads", reads)
	}

	monitor.ForceRefresh()
	systemFS.reset()
	monitor.Snapshot(context.Background())
	if calls := systemFS.globCount("/proc/[0-9]*"); calls != 1 {
		t.Fatalf("forced process refresh = %d globs, want 1", calls)
	}
	if reads := systemFS.thermalReads(); reads != 2 {
		t.Fatalf("forced thermal refresh = %d reads, want 2", reads)
	}
}

func TestMonitorCanDisableProcessCollection(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "generic-linux")
	systemFS := &countingFS{SystemFS: sysfs.MustNew(root)}
	monitor := NewMonitor(systemFS, time.Nanosecond)
	monitor.DisableProcesses()
	systemFS.reset()
	snapshot := monitor.Snapshot(context.Background())
	if len(snapshot.Processes) != 0 || snapshot.Processes == nil {
		t.Fatalf("processes = %#v, want non-nil empty slice", snapshot.Processes)
	}
	if calls := systemFS.globCount("/proc/[0-9]*"); calls != 0 {
		t.Fatalf("disabled process collector ran %d times", calls)
	}
}

func TestMonitorRunsOnlyCollectorsInScope(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "generic-linux")
	systemFS := &countingFS{SystemFS: sysfs.MustNew(root)}
	monitor := NewMonitor(systemFS, time.Nanosecond)

	monitor.SetCollectionScope(0)
	systemFS.reset()
	monitor.Snapshot(context.Background())
	if reads, globs := systemFS.totals(); reads != 0 || globs != 0 {
		t.Fatalf("empty scope performed ReadFile=%d Glob=%d", reads, globs)
	}

	monitor.SetCollectionScope(model.CollectMemory)
	systemFS.reset()
	memorySnapshot := monitor.Snapshot(context.Background())
	if memorySnapshot.Memory.Total.Status != model.Available {
		t.Fatalf("memory status = %q", memorySnapshot.Memory.Total.Status)
	}
	if reads, globs := systemFS.totals(); reads != 8 || globs != 1 {
		t.Fatalf("memory scope performed ReadFile=%d Glob=%d", reads, globs)
	}

	monitor.SetCollectionScope(model.CollectPower)
	systemFS.reset()
	powerSnapshot := monitor.Snapshot(context.Background())
	if len(powerSnapshot.Power) != 4 || len(powerSnapshot.Fans) != 1 {
		t.Fatalf("power scope sensors=%d fans=%d", len(powerSnapshot.Power), len(powerSnapshot.Fans))
	}
	if systemFS.readCount("/proc/meminfo") != 0 || systemFS.thermalReads() != 0 {
		t.Fatal("power scope ran an unrelated collector")
	}

	monitor.SetCollectionScope(model.CollectProcesses)
	systemFS.reset()
	processSnapshot := monitor.Snapshot(context.Background())
	if len(processSnapshot.Processes) != 1 {
		t.Fatalf("processes = %d", len(processSnapshot.Processes))
	}
	if calls := systemFS.globCount("/proc/[0-9]*"); calls != 2 {
		t.Fatalf("initial process scope glob calls = %d, want 2", calls)
	}
	if systemFS.readCount("/proc/meminfo") != 0 || systemFS.thermalReads() != 0 {
		t.Fatal("process scope ran an unrelated collector")
	}

	monitor.SetCollectionScope(model.CollectOverview)
	systemFS.reset()
	overviewSnapshot := monitor.Snapshot(context.Background())
	if len(overviewSnapshot.Processes) != 0 {
		t.Fatalf("overview collected %d processes", len(overviewSnapshot.Processes))
	}
	if calls := systemFS.globCount("/proc/[0-9]*"); calls != 0 {
		t.Fatalf("overview scanned processes %d times", calls)
	}
	if overviewSnapshot.CPU.Overall.Status == "" || overviewSnapshot.Memory.Total.Status == "" ||
		len(overviewSnapshot.Accelerators) == 0 || len(overviewSnapshot.Thermals) == 0 ||
		len(overviewSnapshot.Power) == 0 || len(overviewSnapshot.Fans) == 0 {
		t.Fatalf("overview snapshot is incomplete: %#v", overviewSnapshot)
	}

	monitor.SetCollectionScope(model.CollectProcesses)
	systemFS.reset()
	monitor.Snapshot(context.Background())
	if calls := systemFS.globCount("/proc/[0-9]*"); calls != 2 {
		t.Fatalf("re-enabled process scope used stale baseline: %d globs, want 2", calls)
	}
}

type readErrorFS struct {
	sysfs.SystemFS
	path string
	err  error
}

type countingFS struct {
	sysfs.SystemFS
	mu    sync.Mutex
	reads map[string]int
	globs map[string]int
}

func (f *countingFS) ReadFile(path string) ([]byte, error) {
	f.mu.Lock()
	if f.reads == nil {
		f.reads = make(map[string]int)
	}
	f.reads[path]++
	f.mu.Unlock()
	return f.SystemFS.ReadFile(path)
}

func (f *countingFS) Glob(pattern string) ([]string, error) {
	f.mu.Lock()
	if f.globs == nil {
		f.globs = make(map[string]int)
	}
	f.globs[pattern]++
	f.mu.Unlock()
	return f.SystemFS.Glob(pattern)
}

func (f *countingFS) reset() {
	f.mu.Lock()
	f.reads = make(map[string]int)
	f.globs = make(map[string]int)
	f.mu.Unlock()
}

func (f *countingFS) globCount(pattern string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.globs[pattern]
}

func (f *countingFS) thermalReads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads["/sys/class/thermal/thermal_zone0/temp"] +
		f.reads["/sys/class/hwmon/hwmon0/temp1_input"]
}

func (f *countingFS) totals() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var reads, globs int
	for _, count := range f.reads {
		reads += count
	}
	for _, count := range f.globs {
		globs += count
	}
	return reads, globs
}

func (f *countingFS) readCount(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads[path]
}

func (f *readErrorFS) ReadFile(path string) ([]byte, error) {
	if path == f.path {
		return nil, f.err
	}
	return f.SystemFS.ReadFile(path)
}
