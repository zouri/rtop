package ui

import (
	"fmt"
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
	if len(uiModel.cpuHistory) != 120 || len(uiModel.memoryHistory) != 120 || len(uiModel.gpuHistory) != 120 || len(uiModel.rgaHistory) != 120 {
		t.Fatalf("history lengths cpu=%d memory=%d gpu=%d rga=%d", len(uiModel.cpuHistory), len(uiModel.memoryHistory), len(uiModel.gpuHistory), len(uiModel.rgaHistory))
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
	uiModel.setPage(pageProcesses)
	updated, _ := uiModel.Update(snapshotMsg{snapshot: snapshot})
	uiModel = updated.(Model)
	if got := []int{uiModel.processes[0].PID, uiModel.processes[1].PID, uiModel.processes[2].PID}; got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("CPU order = %v", got)
	}
	if snapshot.Processes[0].PID != 3 {
		t.Fatal("sorting mutated the snapshot process order")
	}

	uiModel.setPage(pageProcesses)
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

func TestTopRankedProcessesKeepsOnlyBestEntries(t *testing.T) {
	now := time.Now()
	processes := []model.ProcessInfo{
		{PID: 4, CPU: model.AvailableMetric(40, "%", "cpu", now), Memory: model.AvailableMetric(10, "MiB", "memory", now)},
		{PID: 2, CPU: model.AvailableMetric(20, "%", "cpu", now), Memory: model.AvailableMetric(40, "MiB", "memory", now)},
		{PID: 1, CPU: model.AvailableMetric(40, "%", "cpu", now), Memory: model.AvailableMetric(20, "MiB", "memory", now)},
		{PID: 3, CPU: model.AvailableMetric(30, "%", "cpu", now), Memory: model.AvailableMetric(30, "MiB", "memory", now)},
	}
	cpu := topRankedProcesses(processes, 3, sortCPU)
	memory := topRankedProcesses(processes, 3, sortMemory)
	if got := []int{cpu[0].PID, cpu[1].PID, cpu[2].PID}; got[0] != 1 || got[1] != 4 || got[2] != 3 {
		t.Fatalf("CPU ranking = %v", got)
	}
	if got := []int{memory[0].PID, memory[1].PID, memory[2].PID}; got[0] != 2 || got[1] != 3 || got[2] != 1 {
		t.Fatalf("memory ranking = %v", got)
	}
	if processes[0].PID != 4 {
		t.Fatal("ranking mutated its input")
	}
}

func TestPageCollectionScopesAndNotifications(t *testing.T) {
	tests := []struct {
		page page
		want model.CollectionScope
	}{
		{pageAll, model.CollectAll},
		{pageCPU, model.CollectCPU | model.CollectMemory | model.CollectThermals | model.CollectPower | model.CollectProcesses},
		{pageMemory, model.CollectMemory | model.CollectProcesses},
		{pageEngines, model.CollectAccelerators | model.CollectThermals | model.CollectPower},
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
	if len(uiModel.cpuHistory) != 0 || len(uiModel.memoryHistory) != 0 || len(uiModel.gpuHistory) != 0 || len(uiModel.rgaHistory) != 0 {
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
	if len(uiModel.latest.Accelerators) != 3 {
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

func TestProcessPathShortcutTogglesExecutablePaths(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Processes = append(snapshot.Processes, model.ProcessInfo{
		PID: 101, Name: "rcuos/6",
		CPU:    model.AvailableMetric(1, "%", "/proc/101/stat", snapshot.Timestamp),
		Memory: model.AvailableMetric(0, "MiB", "/proc/101/stat", snapshot.Timestamp),
	})
	uiModel := NewModel(make(chan model.Snapshot), nil)
	updated, _ := uiModel.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	uiModel = updated.(Model)
	updated, _ = uiModel.Update(snapshotMsg{snapshot: snapshot})
	uiModel = updated.(Model)
	uiModel.setPage(pageProcesses)

	if view := ansi.Strip(uiModel.View()); strings.Contains(view, "/usr/bin/fixture-worker") {
		t.Fatalf("process path is visible before toggling:\n%s", view)
	}
	updated, _ = uiModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	uiModel = updated.(Model)
	if !uiModel.showProcessPath {
		t.Fatal("process path toggle was not enabled")
	}
	view := ansi.Strip(uiModel.View())
	for _, expected := range []string{"PATH", "/usr/bin/fixture-worker", "[rcuos/6]", "p paths"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("process page is missing %q after path toggle:\n%s", expected, view)
		}
	}
	if strings.Contains(view, "N/A") {
		t.Fatalf("named process without an executable path rendered as N/A:\n%s", view)
	}

	updated, _ = uiModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	uiModel = updated.(Model)
	if uiModel.showProcessPath || strings.Contains(ansi.Strip(uiModel.View()), "/usr/bin/fixture-worker") {
		t.Fatal("process path toggle was not disabled")
	}
}

func TestViewNeverExceedsTerminalWidth(t *testing.T) {
	for _, width := range []int{1, 8, 20, 24, 32, 47, 48, 55, 56, 65, 66, 71, 72, 87, 88, 120} {
		for _, height := range []int{1, 2, 3, 5, 7, 8, 12, 24, 40} {
			uiModel := NewModel(make(chan model.Snapshot), nil)
			updated, _ := uiModel.Update(tea.WindowSizeMsg{Width: width, Height: height})
			uiModel = updated.(Model)
			updated, _ = uiModel.Update(snapshotMsg{snapshot: testSnapshot()})
			uiModel = updated.(Model)
			for _, currentPage := range []page{pageAll, pageCPU, pageMemory, pageEngines, pageProcesses, pageInfo} {
				uiModel.setPage(currentPage)
				for _, showPath := range []bool{false, true} {
					uiModel.showProcessPath = showPath
					assertViewFitsTerminal(t, uiModel)
				}
			}
			uiModel.showHelp = true
			assertViewFitsTerminal(t, uiModel)
		}
	}
}

func assertViewFitsTerminal(t *testing.T, uiModel Model) {
	t.Helper()
	view := uiModel.View()
	for lineNumber, line := range strings.Split(view, "\n") {
		if got := ansi.StringWidth(line); got > uiModel.width {
			t.Fatalf("%dx%d page %d line %d rendered %d cells: %q", uiModel.width, uiModel.height, uiModel.page, lineNumber+1, got, line)
		}
	}
	if got := lipgloss.Height(view); got > uiModel.height {
		t.Fatalf("%dx%d page %d rendered %d lines", uiModel.width, uiModel.height, uiModel.page, got)
	}
}

func TestSmallTerminalShowsResizeGuidance(t *testing.T) {
	uiModel := NewModel(make(chan model.Snapshot), nil)
	updated, _ := uiModel.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
	uiModel = updated.(Model)
	view := ansi.Strip(uiModel.View())
	for _, expected := range []string{"rtop", "Terminal 20x5", "Minimum 24x8"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("small-terminal view is missing %q:\n%s", expected, view)
		}
	}
	assertViewFitsTerminal(t, uiModel)

	updated, _ = uiModel.Update(tea.WindowSizeMsg{Width: 24, Height: 8})
	uiModel = updated.(Model)
	updated, _ = uiModel.Update(snapshotMsg{snapshot: testSnapshot()})
	uiModel = updated.(Model)
	view = ansi.Strip(uiModel.View())
	if strings.Contains(view, "Minimum 24x8") || !strings.Contains(view, "CPU") {
		t.Fatalf("normal UI was not restored after resize:\n%s", view)
	}
	assertViewFitsTerminal(t, uiModel)
}

func TestPageTabsRenderOnBottomLine(t *testing.T) {
	uiModel := NewModel(make(chan model.Snapshot), nil)
	updated, _ := uiModel.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	uiModel = updated.(Model)
	updated, _ = uiModel.Update(snapshotMsg{snapshot: testSnapshot()})
	uiModel = updated.(Model)

	for _, test := range []struct {
		current page
		label   string
	}{
		{current: pageAll, label: "[1 ALL]"},
		{current: pageProcesses, label: "[5 PROC]"},
	} {
		uiModel.setPage(test.current)
		lines := strings.Split(ansi.Strip(uiModel.View()), "\n")
		last := strings.TrimSpace(lines[len(lines)-1])
		if !strings.Contains(last, test.label) {
			t.Fatalf("page %d bottom line is missing %q: %q", test.current, test.label, last)
		}
	}
}

func TestProcessSelectionRemainsVisibleAfterResize(t *testing.T) {
	now := time.Now()
	snapshot := testSnapshot()
	snapshot.Processes = make([]model.ProcessInfo, 12)
	for index := range snapshot.Processes {
		snapshot.Processes[index] = model.ProcessInfo{
			PID:  index + 1,
			Name: fmt.Sprintf("proc%02d", index+1),
			CPU:  model.AvailableMetric(float64(12-index), "%", fmt.Sprintf("/proc/%d/stat", index+1), now),
		}
	}

	uiModel := NewModel(make(chan model.Snapshot), nil)
	updated, _ := uiModel.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	uiModel = updated.(Model)
	updated, _ = uiModel.Update(snapshotMsg{snapshot: snapshot})
	uiModel = updated.(Model)
	uiModel.setPage(pageProcesses)
	uiModel.selected = len(snapshot.Processes) - 1
	uiModel.showProcessDetail = true

	updated, _ = uiModel.Update(tea.WindowSizeMsg{Width: 24, Height: 8})
	uiModel = updated.(Model)
	view := ansi.Strip(uiModel.View())
	if !strings.Contains(view, "proc12") {
		t.Fatalf("selected process disappeared after resize:\n%s", view)
	}
	if uiModel.selected != 11 {
		t.Fatalf("selected process changed after resize: %d", uiModel.selected)
	}
	assertViewFitsTerminal(t, uiModel)
}

func TestStandardTerminalOverviewIncludesEngines(t *testing.T) {
	uiModel := NewModel(make(chan model.Snapshot), nil)
	updated, _ := uiModel.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	uiModel = updated.(Model)
	updated, _ = uiModel.Update(snapshotMsg{snapshot: testSnapshot()})
	uiModel = updated.(Model)
	view := ansi.Strip(uiModel.View())
	for _, expected := range []string{"CPU", "c0", "MEMORY", "TOP PROCESSES", "Mali GPU", "RKNPU", "Rockchip RGA"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("overview is missing %q:\n%s", expected, view)
		}
	}
}

func TestAllPageRendersTopFiveProcessesOnSeparateRows(t *testing.T) {
	now := time.Now()
	snapshot := testSnapshot()
	snapshot.Processes = make([]model.ProcessInfo, 6)
	for index := range snapshot.Processes {
		rank := index + 1
		snapshot.Processes[index] = model.ProcessInfo{
			PID:  100 + rank,
			Name: fmt.Sprintf("cpu-rank-%d", rank),
			CPU:  model.AvailableMetric(float64(100-rank), "%", fmt.Sprintf("/proc/%d/stat", 100+rank), now),
			Memory: model.AvailableMetric(
				float64(rank*100), "MiB", fmt.Sprintf("/proc/%d/status", 100+rank), now,
			),
		}
	}

	uiModel := NewModel(make(chan model.Snapshot), nil)
	updated, _ := uiModel.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	uiModel = updated.(Model)
	updated, _ = uiModel.Update(snapshotMsg{snapshot: snapshot})
	uiModel = updated.(Model)
	view := ansi.Strip(uiModel.renderAll(0))

	for _, line := range strings.Split(view, "\n") {
		titles := 0
		for _, title := range []string{"ROCKCHIP ENGINES", "TOP PROCESSES · CPU", "TOP PROCESSES · MEMORY"} {
			if strings.Contains(line, title) {
				titles++
			}
		}
		if titles > 1 {
			t.Fatalf("overview cards share a row: %q", line)
		}
	}
	cpuStart := strings.Index(view, "TOP PROCESSES · CPU")
	memoryStart := strings.Index(view, "TOP PROCESSES · MEMORY")
	if cpuStart < 0 || memoryStart < 0 || memoryStart <= cpuStart {
		t.Fatalf("ALL page process cards are missing or out of order:\n%s", view)
	}
	cpuView := view[cpuStart:memoryStart]
	memoryView := view[memoryStart:]
	for rank := 1; rank <= 5; rank++ {
		if name := fmt.Sprintf("cpu-rank-%d", rank); !strings.Contains(cpuView, name) {
			t.Fatalf("CPU process card is missing %q:\n%s", name, cpuView)
		}
	}
	if strings.Contains(cpuView, "cpu-rank-6") {
		t.Fatalf("CPU process card rendered more than five processes:\n%s", cpuView)
	}
	for rank := 2; rank <= 6; rank++ {
		if name := fmt.Sprintf("cpu-rank-%d", rank); !strings.Contains(memoryView, name) {
			t.Fatalf("memory process card is missing %q:\n%s", name, memoryView)
		}
	}
	if strings.Contains(memoryView, "cpu-rank-1") {
		t.Fatalf("memory process card rendered more than five processes:\n%s", memoryView)
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

func TestCPUCardGroupsCoresAndShowsPolicyOnce(t *testing.T) {
	now := time.Now()
	policy := func(minimum, maximum float64) *model.FrequencyPolicy {
		return &model.FrequencyPolicy{
			Minimum:         model.AvailableMetric(minimum, "MHz", "min", now),
			Maximum:         model.AvailableMetric(maximum, "MHz", "max", now),
			HardwareMinimum: model.AvailableMetric(minimum, "MHz", "hw_min", now),
			HardwareMaximum: model.AvailableMetric(maximum, "MHz", "hw_max", now),
			Governor:        model.AvailableTextMetric("schedutil", "governor", now),
		}
	}
	clusters := []struct {
		name        string
		first, last int
		maximum     float64
	}{
		{name: "little", first: 0, last: 3, maximum: 1800},
		{name: "middle", first: 4, last: 5, maximum: 2400},
		{name: "big", first: 6, last: 7, maximum: 2400},
	}
	snapshot := testSnapshot()
	snapshot.CPU.Cores = nil
	for _, cluster := range clusters {
		clusterPolicy := policy(408, cluster.maximum)
		for id := cluster.first; id <= cluster.last; id++ {
			snapshot.CPU.Cores = append(snapshot.CPU.Cores, model.CPUCore{
				ID: id, Cluster: cluster.name,
				Usage:     model.AvailableMetric(float64(10+id*8), "%", "/proc/stat", now),
				Frequency: model.AvailableMetric(cluster.maximum, "MHz", "frequency", now),
				Policy:    clusterPolicy,
			})
		}
	}

	uiModel := NewModel(make(chan model.Snapshot), nil)
	uiModel.latest = snapshot
	view := ansi.Strip(uiModel.cpuCard(100))
	for _, expected := range []string{
		"LITTLE · cpu0-3", "MIDDLE · cpu4-5", "BIG · cpu6-7",
	} {
		if !strings.Contains(view, expected) {
			t.Fatalf("grouped CPU card is missing %q:\n%s", expected, view)
		}
	}
	if count := strings.Count(view, "408–1800MHz · schedutil"); count != 1 {
		t.Fatalf("little policy rendered %d times, want once:\n%s", count, view)
	}
	for id := range 8 {
		core := fmt.Sprintf("cpu%d", id)
		found := false
		for _, line := range strings.Split(view, "\n") {
			if strings.Contains(line, "│ "+core+" ") &&
				strings.Contains(line, "[") && strings.Contains(line, "]") &&
				strings.Contains(line, "MHz") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("grouped CPU card has no load/frequency row for %s:\n%s", core, view)
		}
	}
}

func TestCPUCardFallsBackToFrequencyPolicyGroups(t *testing.T) {
	now := time.Now()
	policy := func(maximum float64) *model.FrequencyPolicy {
		return &model.FrequencyPolicy{
			Minimum:  model.AvailableMetric(400, "MHz", "min", now),
			Maximum:  model.AvailableMetric(maximum, "MHz", "max", now),
			Governor: model.AvailableTextMetric("schedutil", "governor", now),
		}
	}
	groups := groupCPUCores([]model.CPUCore{
		{ID: 0, Policy: policy(1800)},
		{ID: 1, Policy: policy(1800)},
		{ID: 2, Policy: policy(2400)},
	})
	if len(groups) != 2 || groups[0].label != "POLICY 1" || groups[1].label != "POLICY 2" {
		t.Fatalf("fallback policy groups = %#v", groups)
	}
	if got := cpuCoreRange(groups[0].cores); got != "cpu0-1" {
		t.Fatalf("first policy core range = %q", got)
	}
}

func TestCPUCoreRowsAndGroupHeadersFillAvailableWidth(t *testing.T) {
	now := time.Now()
	policy := &model.FrequencyPolicy{
		Minimum:  model.AvailableMetric(408, "MHz", "min", now),
		Maximum:  model.AvailableMetric(1800, "MHz", "max", now),
		Governor: model.AvailableTextMetric("schedutil", "governor", now),
	}
	core := model.CPUCore{
		ID: 0, Cluster: "little",
		Usage:     model.AvailableMetric(42, "%", "/proc/stat", now),
		Frequency: model.AvailableMetric(1800, "MHz", "frequency", now),
		Policy:    policy,
	}
	for _, width := range []int{34, 52, 76, 96} {
		row := renderCPUCore(core, width)
		if got := ansi.StringWidth(row); got != width {
			t.Fatalf("CPU core row width = %d, want %d: %q", got, width, ansi.Strip(row))
		}
		if !strings.HasSuffix(ansi.Strip(row), "1800MHz") {
			t.Fatalf("CPU frequency is not right aligned: %q", ansi.Strip(row))
		}
	}

	header := renderCPUGroupHeader(cpuCoreGroup{
		label: "LITTLE", cores: []model.CPUCore{core}, policy: policy,
	}, 76)
	if got := ansi.StringWidth(header); got != 76 {
		t.Fatalf("CPU group header width = %d, want 76: %q", got, ansi.Strip(header))
	}
}

func TestCPUAndMemoryPagesRenderProcessRankings(t *testing.T) {
	uiModel := NewModel(make(chan model.Snapshot), nil)
	updated, _ := uiModel.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	uiModel = updated.(Model)
	updated, _ = uiModel.Update(snapshotMsg{snapshot: testSnapshot()})
	uiModel = updated.(Model)

	for _, test := range []struct {
		current page
		title   string
	}{
		{current: pageCPU, title: "TOP PROCESSES · CPU"},
		{current: pageMemory, title: "TOP PROCESSES · MEMORY"},
	} {
		uiModel.setPage(test.current)
		uiModel.showProcessPath = false
		view := ansi.Strip(uiModel.View())
		for _, expected := range []string{test.title, "fixture worker", "p paths"} {
			if !strings.Contains(view, expected) {
				t.Fatalf("page %d is missing %q:\n%s", test.current, expected, view)
			}
		}

		updated, _ = uiModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
		uiModel = updated.(Model)
		view = ansi.Strip(uiModel.View())
		for _, expected := range []string{"PATH", "/usr/bin/fixture-worker"} {
			if !strings.Contains(view, expected) {
				t.Fatalf("page %d is missing %q after path toggle:\n%s", test.current, expected, view)
			}
		}
	}
}

func TestMemoryPageUsesFullWidthRows(t *testing.T) {
	uiModel := NewModel(make(chan model.Snapshot), nil)
	updated, _ := uiModel.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	uiModel = updated.(Model)
	updated, _ = uiModel.Update(snapshotMsg{snapshot: testSnapshot()})
	uiModel = updated.(Model)

	view := ansi.Strip(uiModel.renderMemory(40))
	memoryLine, processLine := -1, -1
	fullWidthCards := 0
	for index, line := range strings.Split(view, "\n") {
		switch {
		case strings.Contains(line, "│ MEMORY"):
			memoryLine = index
		case strings.Contains(line, "│ TOP PROCESSES · MEMORY"):
			processLine = index
		}
		if strings.HasPrefix(line, "╭") && ansi.StringWidth(line) == 100 {
			fullWidthCards++
		}
	}
	if memoryLine < 0 || processLine <= memoryLine {
		t.Fatalf("memory cards are not arranged in top/bottom rows:\n%s", view)
	}
	if fullWidthCards != 2 {
		t.Fatalf("full-width memory cards = %d, want 2:\n%s", fullWidthCards, view)
	}
}

func TestCPUProcessRankingIsReachableInStandardTerminal(t *testing.T) {
	uiModel := NewModel(make(chan model.Snapshot), nil)
	updated, _ := uiModel.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	uiModel = updated.(Model)
	updated, _ = uiModel.Update(snapshotMsg{snapshot: testSnapshot()})
	uiModel = updated.(Model)
	uiModel.setPage(pageCPU)

	for range 64 {
		updated, _ = uiModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		uiModel = updated.(Model)
	}
	view := ansi.Strip(uiModel.View())
	for _, expected := range []string{"TOP PROCESSES · CPU", "fixture worker", "[2 CPU]"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("standard CPU page is missing %q after scrolling:\n%s", expected, view)
		}
	}
	assertViewFitsTerminal(t, uiModel)
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

func TestDetailPagesRenderDVFSPressureAndThermalControls(t *testing.T) {
	uiModel := NewModel(make(chan model.Snapshot), nil)
	updated, _ := uiModel.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	uiModel = updated.(Model)
	updated, _ = uiModel.Update(snapshotMsg{snapshot: testSnapshot()})
	uiModel = updated.(Model)

	for currentPage, expected := range map[page][]string{
		pageCPU:     {"LITTLE · cpu0", "600–1800MHz", "schedutil", "board-fan"},
		pageMemory:  {"CMA", "PSI avg10", "ZRAM"},
		pageEngines: {"power active", "simple_ondemand", "Rockchip RGA", "rga3", "rga2", "passive", "COOLING", "POWER & FANS", "board-fan"},
	} {
		uiModel.page = currentPage
		view := ansi.Strip(uiModel.View())
		for _, value := range expected {
			if !strings.Contains(view, value) {
				t.Fatalf("page %d is missing %q:\n%s", currentPage, value, view)
			}
		}
	}
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
				Policy: &model.FrequencyPolicy{
					Minimum:         model.AvailableMetric(600, "MHz", "/sys/cpu0/min", now),
					Maximum:         model.AvailableMetric(1800, "MHz", "/sys/cpu0/max", now),
					HardwareMinimum: model.AvailableMetric(408, "MHz", "/sys/cpu0/hwmin", now),
					HardwareMaximum: model.AvailableMetric(1800, "MHz", "/sys/cpu0/hwmax", now),
					Governor:        model.AvailableTextMetric("schedutil", "/sys/cpu0/governor", now),
				},
			}},
		},
		Memory: model.MemoryInfo{
			Total:     model.AvailableMetric(8000, "MiB", "/proc/meminfo", now),
			Used:      model.AvailableMetric(4000, "MiB", "/proc/meminfo", now),
			Available: model.AvailableMetric(4000, "MiB", "/proc/meminfo", now),
			SwapTotal: model.AvailableMetric(0, "MiB", "/proc/meminfo", now),
			SwapUsed:  model.AvailableMetric(0, "MiB", "/proc/meminfo", now),
			CMATotal:  model.AvailableMetric(500, "MiB", "/proc/meminfo", now),
			CMAUsed:   model.AvailableMetric(125, "MiB", "/proc/meminfo", now),
			CMAFree:   model.AvailableMetric(375, "MiB", "/proc/meminfo", now),
			Pressure: model.PressureInfo{
				CPU: model.PressureResource{Some: model.PressureWindow{Avg10: model.AvailableMetric(0.25, "%", "/proc/pressure/cpu", now)}},
				Memory: model.PressureResource{
					Some: model.PressureWindow{Avg10: model.AvailableMetric(1.25, "%", "/proc/pressure/memory", now)},
					Full: model.PressureWindow{Avg10: model.AvailableMetric(0.40, "%", "/proc/pressure/memory", now)},
				},
				IO: model.PressureResource{
					Some: model.PressureWindow{Avg10: model.AvailableMetric(2.50, "%", "/proc/pressure/io", now)},
					Full: model.PressureWindow{Avg10: model.AvailableMetric(0.80, "%", "/proc/pressure/io", now)},
				},
			},
			ZRAM: []model.ZRAMDevice{{
				Name: "zram0", DiskSize: model.AvailableMetric(1024, "MiB", "/sys/block/zram0/disksize", now),
				MemoryUsed:       model.AvailableMetric(224, "MiB", "/sys/block/zram0/mm_stat", now),
				CompressionRatio: model.AvailableMetric(2.29, "x", "/sys/block/zram0/mm_stat", now),
				Algorithm:        model.AvailableTextMetric("zstd", "/sys/block/zram0/comp_algorithm", now),
			}},
		},
		Thermals: []model.ThermalZone{{
			Name: "soc", Type: "cpu-thermal", Temperature: model.AvailableMetric(52, "°C", "/sys/thermal/temp", now),
			Trips: []model.ThermalTrip{{
				ID: 0, Type: "passive", Temperature: model.AvailableMetric(80, "°C", "/sys/thermal/trip0", now),
			}},
		}},
		Cooling: []model.CoolingDevice{{
			Name: "cooling_device0", Type: "thermal-cpufreq-0",
			CurrentState: model.AvailableMetric(1, "state", "/sys/thermal/cooling/cur", now),
			MaximumState: model.AvailableMetric(4, "state", "/sys/thermal/cooling/max", now),
		}},
		Power: []model.PowerSensor{{
			Name: "system", Chip: "fixture-sensor", Kind: "power",
			Value: model.AvailableMetric(9, "W", "/sys/hwmon/power1_input", now),
		}},
		Fans: []model.FanInfo{{
			Name: "board-fan", Chip: "fixture-sensor",
			Speed:       model.AvailableMetric(2400, "RPM", "/sys/hwmon/fan1_input", now),
			TargetSpeed: model.AvailableMetric(3000, "RPM", "/sys/hwmon/fan1_target", now),
			PWM:         model.AvailableMetric(50.2, "%", "/sys/hwmon/pwm1", now),
			ControlMode: model.AvailableTextMetric("automatic", "/sys/hwmon/pwm1_enable", now),
		}},
		Processes: []model.ProcessInfo{{
			PID: 100, Name: "fixture worker", Path: "/usr/bin/fixture-worker",
			CPU:     model.AvailableMetric(12, "%", "/proc/100/stat", now),
			Memory:  model.AvailableMetric(20, "MiB", "/proc/100/status", now),
			Threads: 4,
		}},
		Accelerators: []model.Accelerator{
			{
				Name: "Mali GPU", Type: "gpu",
				Usage:     model.AvailableMetric(20, "%", "/sys/gpu/load", now),
				Frequency: model.AvailableMetric(1000, "MHz", "/sys/gpu/cur_freq", now),
				Policy: &model.FrequencyPolicy{
					Minimum:  model.AvailableMetric(200, "MHz", "/sys/gpu/min", now),
					Maximum:  model.AvailableMetric(1000, "MHz", "/sys/gpu/max", now),
					Governor: model.AvailableTextMetric("simple_ondemand", "/sys/gpu/governor", now),
				},
				RuntimeStatus: model.AvailableTextMetric("active", "/sys/gpu/runtime_status", now),
			},
			{
				Name: "RKNPU", Type: "npu",
				Usage:     model.AvailableMetric(40, "%", "/sys/npu/load", now),
				Frequency: model.AvailableMetric(1000, "MHz", "/sys/npu/cur_freq", now),
				Cores: []model.AcceleratorCore{{
					ID: 0, Usage: model.AvailableMetric(40, "%", "/sys/debug/rknpu/load", now),
				}},
			},
			{
				Name: "Rockchip RGA", Type: "rga",
				Usage:     model.AvailableMetric(30, "%", "/sys/kernel/debug/rkrga/load", now),
				Frequency: model.MissingMetric(model.Unsupported, "MHz", "/sys/kernel/debug/rkrga/load", "not exposed", now),
				Cores: []model.AcceleratorCore{
					{ID: 0, Name: "rga3", Usage: model.AvailableMetric(40, "%", "/sys/kernel/debug/rkrga/load", now)},
					{ID: 1, Name: "rga2", Usage: model.AvailableMetric(20, "%", "/sys/kernel/debug/rkrga/load", now)},
				},
			},
		},
		Warnings: []model.Warning{},
	}
}
