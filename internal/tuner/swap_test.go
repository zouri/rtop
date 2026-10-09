package tuner

import (
	"math"
	"strings"
	"testing"

	"rtop/internal/model"
	swapinfo "rtop/internal/swap"
)

func TestBuildSwapPlanChoosesRuntimeBackend(t *testing.T) {
	zram := swapinfo.Entry{Path: "/dev/zram0", Kind: model.SwapZRAM, SizeBytes: 2 << 30, Priority: 100}
	file := swapinfo.Entry{Path: "/swapfile", Kind: model.SwapFile, SizeBytes: 2 << 30, Priority: -2}
	block := swapinfo.Entry{Path: "/dev/mmcblk0p3", Kind: model.SwapBlock, SizeBytes: 4 << 30, Priority: -3}

	plan, err := BuildSwapPlan([]swapinfo.Entry{zram}, 3<<30)
	if err != nil || plan.Strategy != SwapResize || plan.Resize == nil ||
		plan.Resize.Path != zram.Path || plan.ResizeBytes != 3<<30 {
		t.Fatalf("zram plan=%#v error=%v", plan, err)
	}

	plan, err = BuildSwapPlan([]swapinfo.Entry{block}, 6<<30)
	if err != nil || plan.Strategy != SwapManaged || plan.ManagedBytes != 2<<30 || len(plan.Keep) != 1 {
		t.Fatalf("block growth plan=%#v error=%v", plan, err)
	}

	plan, err = BuildSwapPlan([]swapinfo.Entry{block, file}, 3<<30)
	if err != nil || plan.Strategy != SwapResize || plan.Resize == nil ||
		plan.Resize.Path != file.Path || plan.ResizeBytes != 3<<30 || len(plan.Disable) != 1 ||
		plan.Disable[0].Path != block.Path {
		t.Fatalf("mixed shrink plan=%#v error=%v", plan, err)
	}
}

func TestBuildSwapPlanHandlesMultipleBackendsAndZero(t *testing.T) {
	entries := []swapinfo.Entry{
		{Path: "/dev/block0", Kind: model.SwapBlock, SizeBytes: 2 << 30, Priority: 5},
		{Path: "/dev/block1", Kind: model.SwapBlock, SizeBytes: 4 << 30, Priority: 1},
	}
	plan, err := BuildSwapPlan(entries, 3<<30)
	if err != nil || plan.Strategy != SwapManaged || len(plan.Keep) != 1 ||
		plan.Keep[0].Path != "/dev/block0" || plan.ManagedBytes != 1<<30 || len(plan.Disable) != 1 {
		t.Fatalf("partition plan=%#v error=%v", plan, err)
	}
	plan, err = BuildSwapPlan(entries, 0)
	if err != nil || plan.Strategy != SwapDisable || len(plan.Disable) != 2 {
		t.Fatalf("zero plan=%#v error=%v", plan, err)
	}
}

func TestBuildSwapPlanFindsBestFixedBackendCombination(t *testing.T) {
	entries := []swapinfo.Entry{
		{Path: "/dev/block0", Kind: model.SwapBlock, SizeBytes: 6 << 30, Priority: 3},
		{Path: "/dev/block1", Kind: model.SwapBlock, SizeBytes: 4 << 30, Priority: 2},
		{Path: "/dev/block2", Kind: model.SwapBlock, SizeBytes: 4 << 30, Priority: 1},
	}
	plan, err := BuildSwapPlan(entries, 8<<30)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Strategy != SwapDisable || plan.ManagedBytes != 0 || len(plan.Keep) != 2 ||
		plan.Keep[0].Path != "/dev/block1" || plan.Keep[1].Path != "/dev/block2" {
		t.Fatalf("combination plan=%#v", plan)
	}
}

func TestBuildSwapPlanDoesNotCreateSubMinimumResidual(t *testing.T) {
	entries := []swapinfo.Entry{
		{Path: "/dev/block0", Kind: model.SwapBlock, SizeBytes: 2 << 30, Priority: 2},
		{Path: "/dev/block1", Kind: model.SwapBlock, SizeBytes: 2 << 30, Priority: 1},
	}
	plan, err := BuildSwapPlan(entries, (2<<30)+(512<<10))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Strategy != SwapDisable || plan.ManagedBytes != 0 || plan.Resize != nil ||
		len(plan.Keep) != 1 || len(plan.Disable) != 1 {
		t.Fatalf("sub-minimum residual plan=%#v", plan)
	}
}

func TestBuildSwapPlanKeepsExactResizableResidualOnline(t *testing.T) {
	entries := []swapinfo.Entry{
		{Path: "/dev/block0", Kind: model.SwapBlock, SizeBytes: 2 << 30, Priority: 2},
		{Path: "/dev/block1", Kind: model.SwapBlock, SizeBytes: 4 << 30, Priority: 1},
		{Path: "/swapfile", Kind: model.SwapFile, SizeBytes: 1 << 30, Priority: -2},
	}
	plan, err := BuildSwapPlan(entries, 3<<30)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Strategy != SwapDisable || plan.Resize != nil || len(plan.Keep) != 2 ||
		len(plan.Disable) != 1 || plan.Disable[0].Path != "/dev/block1" {
		t.Fatalf("exact residual plan=%#v", plan)
	}
}

func TestBuildSwapPlanMinimizesUsedSwapEvacuationForEqualCapacity(t *testing.T) {
	entries := []swapinfo.Entry{
		{Path: "/dev/empty", Kind: model.SwapBlock, SizeBytes: 4 << 30, Priority: 10},
		{Path: "/dev/used", Kind: model.SwapBlock, SizeBytes: 4 << 30, UsedBytes: 1 << 30, Priority: 1},
	}
	plan, err := BuildSwapPlan(entries, 4<<30)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Keep) != 1 || plan.Keep[0].Path != "/dev/used" ||
		len(plan.Disable) != 1 || plan.Disable[0].Path != "/dev/empty" {
		t.Fatalf("migration-aware plan=%#v", plan)
	}
}

func TestBuildSwapPlanRejectsCapacityOverflow(t *testing.T) {
	_, err := BuildSwapPlan([]swapinfo.Entry{
		{Path: "/dev/block0", Kind: model.SwapBlock, SizeBytes: math.MaxUint64},
		{Path: "/dev/block1", Kind: model.SwapBlock, SizeBytes: 1},
	}, 1<<30)
	if err == nil || !strings.Contains(err.Error(), "overflows") {
		t.Fatalf("overflow error=%v", err)
	}
	if _, err := BuildSwapPlan(nil, uint64(1)<<63); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("target overflow error=%v", err)
	}
}

func TestBuildSwapPlanPrefersZRAMAndNoopsWithinHeaderPage(t *testing.T) {
	entries := []swapinfo.Entry{
		{Path: "/swapfile", Kind: model.SwapFile, SizeBytes: 1 << 30, Priority: -2},
		{Path: "/dev/zram0", Kind: model.SwapZRAM, SizeBytes: (1 << 30) - 4096, Priority: 100},
	}
	plan, err := BuildSwapPlan(entries, 3<<30)
	if err != nil || plan.Resize == nil || plan.Resize.Path != "/dev/zram0" {
		t.Fatalf("preferred plan=%#v error=%v", plan, err)
	}
	plan, err = BuildSwapPlan([]swapinfo.Entry{entries[1]}, 1<<30)
	if err != nil || plan.Strategy != SwapNoChange {
		t.Fatalf("header tolerance plan=%#v error=%v", plan, err)
	}
}

func TestParseAndFormatSwapSize(t *testing.T) {
	for _, test := range []struct {
		input string
		want  uint64
	}{
		{"0", 0}, {"512", 512 << 20}, {"1.5GiB", 1536 << 20}, {"2 GB", 2 << 30},
	} {
		got, err := ParseSwapSize(test.input)
		if err != nil || got != test.want {
			t.Errorf("ParseSwapSize(%q)=%d, %v; want %d", test.input, got, err, test.want)
		}
	}
	for _, input := range []string{"", "size", "1XB", "0.5MiB", "0.1B", "-1G"} {
		if _, err := ParseSwapSize(input); err == nil {
			t.Errorf("ParseSwapSize(%q) succeeded", input)
		}
	}
	if got := FormatSwapSize(1536 << 20); got != "1.50GiB" {
		t.Fatalf("formatted size=%q", got)
	}
}
