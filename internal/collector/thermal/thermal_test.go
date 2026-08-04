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
	zones, cooling, warnings := collector.Collect()
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
	if len(zones[0].Trips) != 2 || zones[0].Trips[0].Type != "passive" {
		t.Fatalf("trips = %#v", zones[0].Trips)
	}
	if len(cooling) != 1 || cooling[0].CurrentState.Value == nil || *cooling[0].CurrentState.Value != 1 {
		t.Fatalf("cooling = %#v", cooling)
	}
	collector.Collect()
	if systemFS.globCalls != 5 {
		t.Fatalf("sensor discovery glob calls = %d, want 5", systemFS.globCalls)
	}
	if systemFS.metadataReads != 6 {
		t.Fatalf("sensor metadata reads = %d, want 6", systemFS.metadataReads)
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
	if strings.HasSuffix(path, "/type") || strings.HasSuffix(path, "/name") || strings.HasSuffix(path, "_label") || strings.HasSuffix(path, "_type") {
		f.metadataReads++
	}
	return f.SystemFS.ReadFile(path)
}
