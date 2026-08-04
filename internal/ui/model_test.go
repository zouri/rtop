package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

	updated, _ = uiModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
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
	if len(uiModel.cpuHistory) != 120 || len(uiModel.memoryHistory) != 120 || len(uiModel.gpuHistory) != 120 {
		t.Fatalf("history lengths cpu=%d memory=%d gpu=%d", len(uiModel.cpuHistory), len(uiModel.memoryHistory), len(uiModel.gpuHistory))
	}
}

func TestProcessesAreSortedOnlyWhenDataOrSortModeChanges(t *testing.T) {
	now := time.Now()
	snapshot := testSnapshot()
	snapshot.Processes = []model.ProcessInfo{
		{PID: 3, CPU: model.AvailableMetric(10, "%", "/proc/3/stat", now), Memory: model.AvailableMetric(30, "MiB", "/proc/3/stat", now)},
		{PID: 1, CPU: model.AvailableMetric(30, "%", "/proc/1/stat", now), Memory: model.AvailableMetric(10, "MiB", "/proc/1/stat", now)},
		{PID: 2, CPU: model.AvailableMetric(20, "%", "/proc/2/stat", now), Memory: model.AvailableMetric(20, "MiB", "/proc/2/stat", now)},
	}
	uiModel := NewModel(make(chan model.Snapshot), nil)
	updated, _ := uiModel.Update(snapshotMsg{snapshot: snapshot})
	uiModel = updated.(Model)
	if got := []int{uiModel.processes[0].PID, uiModel.processes[1].PID, uiModel.processes[2].PID}; got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("CPU order = %v", got)
	}
	if snapshot.Processes[0].PID != 3 {
		t.Fatal("sorting mutated the snapshot process order")
	}

	uiModel.page = pageProcesses
	updated, _ = uiModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	uiModel = updated.(Model)
	if got := []int{uiModel.processes[0].PID, uiModel.processes[1].PID, uiModel.processes[2].PID}; got[0] != 3 || got[1] != 2 || got[2] != 1 {
		t.Fatalf("memory order = %v", got)
	}
}

func TestSameProcessSliceDetection(t *testing.T) {
	processes := make([]model.ProcessInfo, 2)
	if !sameProcessSlice(processes, processes) {
		t.Fatal("same slice was reported as changed")
	}
	if sameProcessSlice(processes, append([]model.ProcessInfo(nil), processes...)) {
		t.Fatal("copied slice was reported as unchanged")
	}
	if sameProcessSlice(processes, processes[:1]) {
		t.Fatal("different length was reported as unchanged")
	}
}

func TestPageCollectionScopesAndNotifications(t *testing.T) {
	tests := []struct {
		page page
		want model.CollectionScope
	}{
		{pageAll, model.CollectAll},
		{pageCPU, model.CollectCPU | model.CollectMemory | model.CollectThermals},
		{pageMemory, model.CollectMemory | model.CollectProcesses},
		{pageEngines, model.CollectAccelerators | model.CollectThermals},
		{pageProcesses, model.CollectProcesses},
		{pageInfo, model.CollectOverview},
	}
	for _, test := range tests {
		if got := scopeForPage(test.page); got != test.want {
			t.Fatalf("page %d scope = %b, want %b", test.page, got, test.want)
		}
	}

	var notified model.CollectionScope
	uiModel := NewModelWithScope(make(chan model.Snapshot), nil, func(scope model.CollectionScope) {
		notified = scope
	})
	updated, command := uiModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	uiModel = updated.(Model)
	if command == nil {
		t.Fatal("page switch did not return a scope command")
	}
	command()
	if uiModel.page != pageProcesses || notified != model.CollectProcesses {
		t.Fatalf("page=%d notified scope=%b", uiModel.page, notified)
	}
}

func TestHistoryIgnoresSnapshotsWithoutTrackedMetrics(t *testing.T) {
	uiModel := NewModel(make(chan model.Snapshot), nil)
	uiModel.addHistory(model.Snapshot{})
	if len(uiModel.cpuHistory) != 0 || len(uiModel.memoryHistory) != 0 || len(uiModel.gpuHistory) != 0 {
		t.Fatalf("unexpected history for empty snapshot")
	}
}

func TestSparseSnapshotsRetainLastValuesFromOtherPages(t *testing.T) {
	uiModel := NewModel(make(chan model.Snapshot), nil)
	updated, _ := uiModel.Update(snapshotMsg{snapshot: testSnapshot()})
	uiModel = updated.(Model)

	now := time.Now()
	processOnly := model.Snapshot{
		Collected: model.CollectProcesses,
		Timestamp: now,
		System:    uiModel.latest.System,
		Processes: []model.ProcessInfo{{
			PID: 200, Name: "new worker",
			CPU: model.AvailableMetric(20, "%", "/proc/200/stat", now),
		}},
	}
	updated, _ = uiModel.Update(snapshotMsg{snapshot: processOnly})
	uiModel = updated.(Model)
	if got := metricNumber(uiModel.latest.CPU.Overall); got != 25 {
		t.Fatalf("CPU value after process-only snapshot = %v", got)
	}
	if len(uiModel.latest.Accelerators) != 2 {
		t.Fatalf("accelerators after process-only snapshot = %d", len(uiModel.latest.Accelerators))
	}
}

func TestHelpIsOverlayAndDoesNotChangeCollectionScope(t *testing.T) {
	called := false
	uiModel := NewModelWithScope(make(chan model.Snapshot), nil, func(model.CollectionScope) {
		called = true
	})
	updated, command := uiModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	uiModel = updated.(Model)
	if !uiModel.showHelp || command != nil || called {
		t.Fatalf("help overlay show=%v command=%v called=%v", uiModel.showHelp, command != nil, called)
	}
}

func TestViewNeverExceedsTerminalWidth(t *testing.T) {
	for _, width := range []int{32, 80, 120} {
		uiModel := NewModel(make(chan model.Snapshot), nil)
		updated, _ := uiModel.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		uiModel = updated.(Model)
		updated, _ = uiModel.Update(snapshotMsg{snapshot: testSnapshot()})
		uiModel = updated.(Model)
		for _, currentPage := range []page{pageAll, pageCPU, pageMemory, pageEngines, pageProcesses, pageInfo} {
			uiModel.page = currentPage
			view := uiModel.View()
			for lineNumber, line := range strings.Split(view, "\n") {
				if got := ansi.StringWidth(line); got > width {
					t.Fatalf("width %d page %d line %d rendered %d cells: %q", width, currentPage, lineNumber+1, got, line)
				}
			}
			if got := lipgloss.Height(view); got > uiModel.height {
				t.Fatalf("width %d page %d rendered %d lines for height %d", width, currentPage, got, uiModel.height)
			}
		}
		uiModel.showHelp = true
		helpView := uiModel.View()
		for lineNumber, line := range strings.Split(helpView, "\n") {
			if got := ansi.StringWidth(line); got > width {
				t.Fatalf("width %d help line %d rendered %d cells: %q", width, lineNumber+1, got, line)
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
	for _, expected := range []string{"CPU", "c0", "MEMORY", "TOP PROCESSES", "Mali GPU", "RKNPU"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("overview is missing %q:\n%s", expected, view)
		}
	}
}

func TestCPUPageRendersPerCoreUsageGraph(t *testing.T) {
	uiModel := NewModel(make(chan model.Snapshot), nil)
	updated, _ := uiModel.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	uiModel = updated.(Model)
	updated, _ = uiModel.Update(snapshotMsg{snapshot: testSnapshot()})
	uiModel = updated.(Model)
	uiModel.page = pageCPU
	view := ansi.Strip(uiModel.View())
	coreGraph := false
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "cpu0") && strings.Contains(line, "[") && strings.Contains(line, "]") {
			coreGraph = true
			break
		}
	}
	if !coreGraph {
		t.Fatalf("CPU page is missing a per-core usage graph:\n%s", view)
	}
}

func TestAllPageKeepsPerCoreUsageCompact(t *testing.T) {
	uiModel := NewModel(make(chan model.Snapshot), nil)
	updated, _ := uiModel.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	uiModel = updated.(Model)
	updated, _ = uiModel.Update(snapshotMsg{snapshot: testSnapshot()})
	uiModel = updated.(Model)
	view := ansi.Strip(uiModel.View())
	for _, line := range strings.Split(view, "\n") {
		if start := strings.Index(line, "c0"); start >= 0 {
			coreSection := line[start:]
			if end := strings.Index(coreSection, "│"); end >= 0 {
				coreSection = coreSection[:end]
			}
			if strings.Contains(coreSection, "[") || strings.Contains(coreSection, "]") {
				t.Fatalf("ALL page core row contains a load bar: %q", line)
			}
			return
		}
	}
	t.Fatal("ALL page is missing the compact core row")
}

func testSnapshot() model.Snapshot {
	now := time.Now()
	return model.Snapshot{
		Collected: model.CollectAll,
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
