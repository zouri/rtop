package thermal

import (
	"path/filepath"
	"testing"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

func TestCollectorScansThermalAndHWMon(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "generic-linux")
	zones, warnings := NewCollector(sysfs.MustNew(root)).Collect()
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
}
