package thermal

import (
	"path/filepath"
	"strings"
	"testing"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

func TestCollectorScansThermalAndHWMon(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "generic-linux")
	systemFS := &countingFS{SystemFS: sysfs.MustNew(root)}
	collector := NewCollector(systemFS)
	if systemFS.globCalls != 0 || systemFS.metadataReads != 0 {
		t.Fatal("collector construction eagerly discovered thermal sensors")
	}
	zones, warnings := collector.Collect()
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	if len(zones) != 2 {
		t.Fatalf("zones = %#v", zones)
	}
	for _, zone := range zones {
		if zone.Temperature.Status != model.Available || zone.Temperature.Value == nil {
			t.Fatalf("zone = %#v", zone)
		}
	}
	collector.Collect()
	if systemFS.globCalls != 3 {
		t.Fatalf("sensor discovery glob calls = %d, want 3", systemFS.globCalls)
	}
	if systemFS.metadataReads != 3 {
		t.Fatalf("sensor metadata reads = %d, want 3", systemFS.metadataReads)
	}
}

type countingFS struct {
	sysfs.SystemFS
	globCalls     int
	metadataReads int
}

func (f *countingFS) Glob(pattern string) ([]string, error) {
	f.globCalls++
	return f.SystemFS.Glob(pattern)
}

func (f *countingFS) ReadFile(path string) ([]byte, error) {
	if strings.HasSuffix(path, "/type") || strings.HasSuffix(path, "/name") || strings.HasSuffix(path, "_label") {
		f.metadataReads++
	}
	return f.SystemFS.ReadFile(path)
}
