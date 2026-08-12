package ui

import (
	"context"
	"io"
	"sort"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"rtop/internal/model"
)

type page int

const (
	pageAll page = iota
	pageCPU
	pageMemory
	pageEngines
	pageProcesses
	pageInfo
)

type processSort int

const (
	sortCPU processSort = iota
	sortMemory
)

type Model struct {
	snapshots    <-chan model.Snapshot
	refresh      func()
	scopeChanged func(model.CollectionScope)
	latest       model.Snapshot
	hasData      bool
	closed       bool

	width              int
	height             int
	page               page
	showHelp           bool
	showProcessDetail  bool
	showProcessPath    bool
	processSort        processSort
	selected           int
	scrollOffset       int
	processes          []model.ProcessInfo
	topCPUProcesses    []model.ProcessInfo
	topMemoryProcesses []model.ProcessInfo

	cpuHistory    []float64
	memoryHistory []float64
	gpuHistory    []float64
	npuHistory    []float64
	ddrHistory    []float64
	rgaHistory    []float64

	keys keyMap
	help help.Model
}

type snapshotMsg struct {
	snapshot model.Snapshot
}

type snapshotsClosedMsg struct{}

func NewModel(snapshots <-chan model.Snapshot, refresh func()) Model {
	return NewModelWithScope(snapshots, refresh, nil)
}

func NewModelWithScope(
	snapshots <-chan model.Snapshot,
	refresh func(),
	scopeChanged func(model.CollectionScope),
) Model {
	helper := help.New()
	helper.ShowAll = false
	return Model{
		snapshots: snapshots, refresh: refresh, scopeChanged: scopeChanged,
		keys: newKeyMap(), help: helper,
	}
}

func (m Model) Init() tea.Cmd {
	return waitForSnapshot(m.snapshots)
}

func waitForSnapshot(snapshots <-chan model.Snapshot) tea.Cmd {
	return func() tea.Msg {
		snapshot, ok := <-snapshots
		if !ok {
			return snapshotsClosedMsg{}
		}
		return snapshotMsg{snapshot: snapshot}
	}
}

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = message.Width, message.Height
		m.help.Width = max(1, message.Width-4)
		m.clampScroll()
		return m, nil
	case snapshotMsg:
		scope := collectedScope(message.snapshot)
		processesChanged := scope.Includes(model.CollectProcesses) &&
			!sameProcessSlice(m.latest.Processes, message.snapshot.Processes)
		m.mergeSnapshot(message.snapshot, scope)
		m.hasData = true
		if processesChanged {
			m.updateProcessRankings()
			if m.page == pageProcesses {
				m.sortProcesses()
			}
		}
		m.addHistory(message.snapshot)
		m.clampSelection()
		m.clampScroll()
		return m, waitForSnapshot(m.snapshots)
	case snapshotsClosedMsg:
		m.closed = true
		return m, tea.Quit
	case tea.KeyMsg:
		if key.Matches(message, m.keys.Quit) {
			return m, tea.Quit
		}
		if key.Matches(message, m.keys.Help) {
			m.showHelp = !m.showHelp
			m.scrollOffset = 0
			return m, nil
		}
		if key.Matches(message, m.keys.Back) && m.showHelp {
			m.showHelp = false
			m.scrollOffset = 0
			return m, nil
		}

		if key.Matches(message, m.keys.All) {
			m.setPage(pageAll)
			return m, m.scopeCommand()
		}
		if key.Matches(message, m.keys.CPU) {
			m.setPage(pageCPU)
			return m, m.scopeCommand()
		}
		if key.Matches(message, m.keys.Memory) {
			m.setPage(pageMemory)
			return m, m.scopeCommand()
		}
		if key.Matches(message, m.keys.Engines) {
			m.setPage(pageEngines)
			return m, m.scopeCommand()
		}
		if key.Matches(message, m.keys.Processes) {
			m.setPage(pageProcesses)
			return m, m.scopeCommand()
		}
		if key.Matches(message, m.keys.Info) {
			m.setPage(pageInfo)
			return m, m.scopeCommand()
		}
		if key.Matches(message, m.keys.Refresh) {
			refresh := m.refresh
			return m, func() tea.Msg {
				if refresh != nil {
					refresh()
				}
				return nil
			}
		}

		if m.showHelp {
			switch {
			case key.Matches(message, m.keys.Up):
				m.scrollOffset--
			case key.Matches(message, m.keys.Down):
				m.scrollOffset++
			}
			m.clampScroll()
			return m, nil
		}

		if key.Matches(message, m.keys.Paths) &&
			(m.page == pageAll || m.page == pageCPU || m.page == pageMemory || m.page == pageProcesses) {
			m.showProcessPath = !m.showProcessPath
			return m, nil
		}

		if m.page == pageProcesses {
			switch {
			case key.Matches(message, m.keys.Up):
				m.selected--
				m.clampSelection()
			case key.Matches(message, m.keys.Down):
				m.selected++
				m.clampSelection()
			case key.Matches(message, m.keys.SortCPU):
				if m.processSort != sortCPU {
					m.processSort = sortCPU
					m.sortProcesses()
				}
			case key.Matches(message, m.keys.SortMemory):
				if m.processSort != sortMemory {
					m.processSort = sortMemory
					m.sortProcesses()
				}
			case key.Matches(message, m.keys.Details):
				m.showProcessDetail = !m.showProcessDetail
			}
			return m, nil
		}

		switch {
		case key.Matches(message, m.keys.Up):
			m.scrollOffset--
		case key.Matches(message, m.keys.Down):
			m.scrollOffset++
		}
		m.clampScroll()
	}
	return m, nil
}

func sameProcessSlice(left, right []model.ProcessInfo) bool {
	if len(left) != len(right) {
		return false
	}
	return len(left) == 0 || &left[0] == &right[0]
}

func (m *Model) setPage(next page) {
	previous := m.page
	m.page = next
	m.showHelp = false
	m.scrollOffset = 0
	if next == pageProcesses && previous != pageProcesses {
		m.sortProcesses()
		m.clampSelection()
	}
}

func (m Model) scopeCommand() tea.Cmd {
	changed := m.scopeChanged
	scope := scopeForPage(m.page)
	if changed == nil {
		return nil
	}
	return func() tea.Msg {
		changed(scope)
		return nil
	}
}

func scopeForPage(current page) model.CollectionScope {
	switch current {
	case pageCPU:
		return model.CollectCPU | model.CollectMemory | model.CollectThermals | model.CollectPower | model.CollectProcesses
	case pageMemory:
		return model.CollectMemory | model.CollectProcesses
	case pageEngines:
		return model.CollectAccelerators | model.CollectThermals | model.CollectPower
	case pageProcesses:
		return model.CollectProcesses
	case pageInfo:
		return model.CollectOverview
	default:
		return model.CollectAll
	}
}

func collectedScope(snapshot model.Snapshot) model.CollectionScope {
	if snapshot.Collected != 0 {
		return snapshot.Collected
	}
	var scope model.CollectionScope
	if snapshot.CPU.Overall.Status != "" || snapshot.CPU.Cores != nil {
		scope |= model.CollectCPU
	}
	if snapshot.Memory.Total.Status != "" || snapshot.Load.OneMinute.Status != "" {
		scope |= model.CollectMemory
	}
	if len(snapshot.Accelerators) > 0 {
		scope |= model.CollectAccelerators
	}
	if len(snapshot.Processes) > 0 {
		scope |= model.CollectProcesses
	}
	if len(snapshot.Thermals) > 0 || len(snapshot.Cooling) > 0 {
		scope |= model.CollectThermals
	}
	if len(snapshot.Power) > 0 || len(snapshot.Fans) > 0 {
		scope |= model.CollectPower
	}
	return scope
}

func (m *Model) mergeSnapshot(snapshot model.Snapshot, scope model.CollectionScope) {
	m.latest.Collected = scope
	m.latest.Timestamp = snapshot.Timestamp
	m.latest.System = snapshot.System
	m.latest.Warnings = snapshot.Warnings
	if scope.Includes(model.CollectCPU) {
		m.latest.CPU = snapshot.CPU
	}
	if scope.Includes(model.CollectMemory) {
		m.latest.Memory = snapshot.Memory
		m.latest.Load = snapshot.Load
	}
	if scope.Includes(model.CollectAccelerators) {
		m.latest.Accelerators = snapshot.Accelerators
	}
	if scope.Includes(model.CollectProcesses) {
		m.latest.Processes = snapshot.Processes
	}
	if scope.Includes(model.CollectThermals) {
		m.latest.Thermals = snapshot.Thermals
		m.latest.Cooling = snapshot.Cooling
	}
	if scope.Includes(model.CollectPower) {
		m.latest.Power = snapshot.Power
		m.latest.Fans = snapshot.Fans
	}
}

func (m *Model) clampSelection() {
	maximum := len(m.processes) - 1
	if maximum < 0 {
		m.selected = 0
		return
	}
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected > maximum {
		m.selected = maximum
	}
}

func (m *Model) sortProcesses() {
	selectedPID := -1
	if m.selected >= 0 && m.selected < len(m.processes) {
		selectedPID = m.processes[m.selected].PID
	}
	previous := m.processes
	m.processes = append(previous[:0], m.latest.Processes...)
	if len(previous) > len(m.processes) {
		clear(previous[len(m.processes):])
	}
	sort.SliceStable(m.processes, func(i, j int) bool {
		var left, right float64
		if m.processSort == sortMemory {
			left, right = metricNumber(m.processes[i].Memory), metricNumber(m.processes[j].Memory)
		} else {
			left, right = metricNumber(m.processes[i].CPU), metricNumber(m.processes[j].CPU)
		}
		if left == right {
			return m.processes[i].PID < m.processes[j].PID
		}
		return left > right
	})
	if selectedPID >= 0 {
		for index, process := range m.processes {
			if process.PID == selectedPID {
				m.selected = index
				break
			}
		}
	}
}

func (m *Model) updateProcessRankings() {
	const maximumOverviewProcesses = 8
	m.topCPUProcesses = topRankedProcesses(m.latest.Processes, maximumOverviewProcesses, sortCPU)
	m.topMemoryProcesses = topRankedProcesses(m.latest.Processes, maximumOverviewProcesses, sortMemory)
}

func topRankedProcesses(processes []model.ProcessInfo, limit int, order processSort) []model.ProcessInfo {
	if limit <= 0 || len(processes) == 0 {
		return []model.ProcessInfo{}
	}
	limit = min(limit, len(processes))
	result := make([]model.ProcessInfo, 0, limit)
	for _, process := range processes {
		index := sort.Search(len(result), func(index int) bool {
			return processRanksBefore(process, result[index], order)
		})
		if index >= limit {
			continue
		}
		if len(result) < limit {
			result = append(result, model.ProcessInfo{})
			copy(result[index+1:], result[index:len(result)-1])
		} else {
			copy(result[index+1:], result[index:len(result)-1])
		}
		result[index] = process
	}
	return result
}

func processRanksBefore(left, right model.ProcessInfo, order processSort) bool {
	leftValue, rightValue := metricNumber(left.CPU), metricNumber(right.CPU)
	if order == sortMemory {
		leftValue, rightValue = metricNumber(left.Memory), metricNumber(right.Memory)
	}
	if leftValue == rightValue {
		return left.PID < right.PID
	}
	return leftValue > rightValue
}

func (m *Model) addHistory(snapshot model.Snapshot) {
	scope := collectedScope(snapshot)
	if scope.Includes(model.CollectCPU) {
		m.cpuHistory = appendHistory(m.cpuHistory, metricNumber(snapshot.CPU.Overall))
	}
	if scope.Includes(model.CollectMemory) {
		m.memoryHistory = appendHistory(m.memoryHistory, memoryPercent(snapshot.Memory))
	}
	if scope.Includes(model.CollectAccelerators) {
		values := map[string]float64{"gpu": -1, "npu": -1, "ddr": -1, "rga": -1}
		for _, accelerator := range snapshot.Accelerators {
			values[accelerator.Type] = metricNumber(accelerator.Usage)
		}
		m.gpuHistory = appendHistory(m.gpuHistory, values["gpu"])
		m.npuHistory = appendHistory(m.npuHistory, values["npu"])
		m.ddrHistory = appendHistory(m.ddrHistory, values["ddr"])
		m.rgaHistory = appendHistory(m.rgaHistory, values["rga"])
	}
}

func appendHistory(history []float64, value float64) []float64 {
	if value < 0 && len(history) == 0 {
		return history
	}
	if len(history) < 120 {
		return append(history, value)
	}
	copy(history, history[1:])
	history[len(history)-1] = value
	return history
}

func Run(
	ctx context.Context,
	snapshots <-chan model.Snapshot,
	refresh func(),
	output io.Writer,
) error {
	return RunWithScope(ctx, snapshots, refresh, nil, output)
}

func RunWithScope(
	ctx context.Context,
	snapshots <-chan model.Snapshot,
	refresh func(),
	scopeChanged func(model.CollectionScope),
	output io.Writer,
) error {
	output = synchronizedOutputForTerminal(output)
	program := tea.NewProgram(
		NewModelWithScope(snapshots, refresh, scopeChanged),
		tea.WithContext(ctx),
		tea.WithOutput(output),
		tea.WithAltScreen(),
	)
	_, err := program.Run()
	if ctx.Err() != nil {
		return nil
	}
	return err
}
