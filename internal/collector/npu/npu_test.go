package npu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

func TestParseCoreLoadFormats(t *testing.T) {
	for _, content := range []string{
		"NPU load: Core0: 10%, Core1: 20%, Core2: 30%,",
		"Core0 load: 10%\nCore1 load: 20%\nCore2 load: 30%\n",
	} {
		loads, err := ParseCoreLoad([]byte(content))
		if err != nil {
			t.Fatal(err)
		}
		if len(loads) != 3 || loads[0] != 10 || loads[1] != 20 || loads[2] != 30 {
			t.Fatalf("loads = %#v", loads)
		}
	}
	if _, err := ParseCoreLoad([]byte("NPU idle")); err == nil {
		t.Fatal("malformed core load succeeded")
	}
}

func TestCollectorExposesPerCoreLoad(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "rk3588-kernel61")
	accelerator, warnings := NewCollector(sysfs.MustNew(root)).Collect()
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if accelerator.Usage.Value == nil || *accelerator.Usage.Value != 60 {
		t.Fatalf("usage = %#v", accelerator.Usage)
	}
	if len(accelerator.Cores) != 3 {
		t.Fatalf("cores = %#v", accelerator.Cores)
	}
	for id, want := range []float64{10, 20, 30} {
		metric := accelerator.Cores[id].Usage
		if metric.Status != model.Available || metric.Value == nil || *metric.Value != want {
			t.Fatalf("core %d = %#v", id, metric)
		}
	}
}

func TestCollectorParsesBSP510CoreFormat(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "rk3588-bsp510")
	accelerator, warnings := NewCollector(sysfs.MustNew(root)).Collect()
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if accelerator.Frequency.Value == nil || *accelerator.Frequency.Value != 800 {
		t.Fatalf("frequency = %#v", accelerator.Frequency)
	}
	for id, want := range []float64{5, 15, 25} {
		metric := accelerator.Cores[id].Usage
		if metric.Value == nil || *metric.Value != want {
			t.Fatalf("core %d = %#v", id, metric)
		}
	}
}

func TestCollectorReportsDebugFSPermissionPerCore(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "rk3588-kernel61")
	base := sysfs.MustNew(root)
	accelerator, warnings := NewCollector(&debugPermissionFS{SystemFS: base}).Collect()
	if len(warnings) == 0 {
		t.Fatal("expected permission warning")
	}
	if len(accelerator.Cores) != 3 {
		t.Fatalf("cores = %#v", accelerator.Cores)
	}
	for _, core := range accelerator.Cores {
		if core.Usage.Status != model.PermissionDenied || core.Usage.Value != nil {
			t.Fatalf("core = %#v", core)
		}
	}
	if accelerator.Usage.Status != model.Available {
		t.Fatalf("aggregate usage = %#v", accelerator.Usage)
	}
}

type debugPermissionFS struct {
	sysfs.SystemFS
}

func (f *debugPermissionFS) ReadFile(path string) ([]byte, error) {
	if strings.HasPrefix(path, "/sys/kernel/debug/") {
		return nil, os.ErrPermission
	}
	return f.SystemFS.ReadFile(path)
}
