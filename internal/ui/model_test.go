package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"rtop/internal/model"
)

func TestModelHandlesSnapshotsNavigationAndNarrowResize(t *testing.T) {
	snapshots := make(chan model.Snapshot)
	uiModel := NewModel(snapshots, nil)
	updated, _ := uiModel.Update(tea.WindowSizeMsg{Width: 36, Height: 12})
	uiModel = updated.(Model)
	updated, _ = uiModel.Update(snapshotMsg{snapshot: testSnapshot()})
	uiModel = updated.(Model)
	if view := uiModel.View(); view == "" {
		t.Fatal("empty view")
	}

	updated, _ = uiModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	uiModel = updated.(Model)
	if uiModel.page != pageProcesses {
		t.Fatalf("page = %d", uiModel.page)
	}
	updated, _ = uiModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	uiModel = updated.(Model)
	if uiModel.processSort != sortMemory {
		t.Fatalf("sort = %d", uiModel.processSort)
	}
	if view := uiModel.View(); view == "" {
		t.Fatal("empty process view")
	}
}

func TestHistoryIsBounded(t *testing.T) {
	uiModel := NewModel(make(chan model.Snapshot), nil)
	for range 150 {
		uiModel.addHistory(testSnapshot())
	}
	if len(uiModel.history) != 120 {
		t.Fatalf("history length = %d", len(uiModel.history))
	}
}

func TestViewNeverExceedsTerminalWidth(t *testing.T) {
	for _, width := range []int{32, 80, 120} {
		uiModel := NewModel(make(chan model.Snapshot), nil)
		updated, _ := uiModel.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		uiModel = updated.(Model)
		updated, _ = uiModel.Update(snapshotMsg{snapshot: testSnapshot()})
		uiModel = updated.(Model)
		for _, currentPage := range []page{pageOverview, pageEngines, pageProcesses, pageThermal, pageSystem, pageHelp} {
			uiModel.page = currentPage
			view := uiModel.View()
			for lineNumber, line := range strings.Split(view, "\n") {
				if got := ansi.StringWidth(line); got > width {
					t.Fatalf("width %d page %d line %d rendered %d cells: %q", width, currentPage, lineNumber+1, got, line)
				}
			}
		}
	}
}

func TestStandardTerminalOverviewIncludesEngines(t *testing.T) {
	uiModel := NewModel(make(chan model.Snapshot), nil)
	updated, _ := uiModel.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	uiModel = updated.(Model)
	updated, _ = uiModel.Update(snapshotMsg{snapshot: testSnapshot()})
	uiModel = updated.(Model)
	view := ansi.Strip(uiModel.View())
	for _, expected := range []string{"CPU", "Memory & Load", "Mali GPU", "RKNPU"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("overview is missing %q:\n%s", expected, view)
		}
	}
}

func testSnapshot() model.Snapshot {
	now := time.Now()
	return model.Snapshot{
		Timestamp: now,
		System: model.SystemInfo{
			Hostname: "fixture", OS: "Linux", Kernel: "6.1", Arch: "arm64", SoC: "RK3588",
		},
		CPU: model.CPUInfo{
			Overall: model.AvailableMetric(25, "%", "/proc/stat", now),
			Cores: []model.CPUCore{{
				ID: 0, Cluster: "little",
				Usage:     model.AvailableMetric(25, "%", "/proc/stat", now),
				Frequency: model.AvailableMetric(1800, "MHz", "/sys/cpu0", now),
			}},
		},
		Memory: model.MemoryInfo{
			Total:     model.AvailableMetric(8000, "MiB", "/proc/meminfo", now),
			Used:      model.AvailableMetric(4000, "MiB", "/proc/meminfo", now),
			Available: model.AvailableMetric(4000, "MiB", "/proc/meminfo", now),
			SwapTotal: model.AvailableMetric(0, "MiB", "/proc/meminfo", now),
			SwapUsed:  model.AvailableMetric(0, "MiB", "/proc/meminfo", now),
		},
		Processes: []model.ProcessInfo{{
			PID: 100, Name: "fixture worker",
			CPU:     model.AvailableMetric(12, "%", "/proc/100/stat", now),
			Memory:  model.AvailableMetric(20, "MiB", "/proc/100/status", now),
			Threads: 4,
		}},
		Accelerators: []model.Accelerator{
			{
				Name: "Mali GPU", Type: "gpu",
				Usage:     model.AvailableMetric(20, "%", "/sys/gpu/load", now),
				Frequency: model.AvailableMetric(1000, "MHz", "/sys/gpu/cur_freq", now),
			},
			{
				Name: "RKNPU", Type: "npu",
				Usage:     model.AvailableMetric(40, "%", "/sys/npu/load", now),
				Frequency: model.AvailableMetric(1000, "MHz", "/sys/npu/cur_freq", now),
				Cores: []model.AcceleratorCore{{
					ID: 0, Usage: model.AvailableMetric(40, "%", "/sys/debug/rknpu/load", now),
				}},
			},
		},
		Warnings: []model.Warning{},
	}
}
