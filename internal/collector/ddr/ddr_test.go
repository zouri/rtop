package ddr

import (
	"path/filepath"
	"testing"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

func TestCollectorFromKernel61Fixture(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "rk3588-kernel61")
	accelerator, warnings := NewCollector(sysfs.MustNew(root)).Collect()
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if accelerator.Usage.Status != model.Available || accelerator.Usage.Value == nil || *accelerator.Usage.Value != 28 {
		t.Fatalf("usage = %#v", accelerator.Usage)
	}
	if accelerator.Frequency.Value == nil || *accelerator.Frequency.Value != 528 {
		t.Fatalf("frequency = %#v", accelerator.Frequency)
	}
}

func TestBSP510KeepsFrequencyWhenLoadIsUnsupported(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "rk3588-bsp510")
	accelerator, warnings := NewCollector(sysfs.MustNew(root)).Collect()
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if accelerator.Usage.Status != model.Unsupported {
		t.Fatalf("usage = %#v", accelerator.Usage)
	}
	if accelerator.Frequency.Value == nil || *accelerator.Frequency.Value != 1560 {
		t.Fatalf("frequency = %#v", accelerator.Frequency)
	}
}
