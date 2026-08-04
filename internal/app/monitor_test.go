package app

import (
	"context"
	"os"
	"path/filepath"
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
	if len(snapshot.Warnings) == 0 {
		t.Fatal("expected a CPU warning")
	}
}

type readErrorFS struct {
	sysfs.SystemFS
	path string
	err  error
}

func (f *readErrorFS) ReadFile(path string) ([]byte, error) {
	if path == f.path {
		return nil, f.err
	}
	return f.SystemFS.ReadFile(path)
}
