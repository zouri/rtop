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
	pageOverview page = iota
	pageEngines
	pageProcesses
	pageThermal
	pageSystem
	pageHelp
)

type processSort int

const (
	sortCPU processSort = iota
	sortMemory
)

type historyPoint struct {
	cpu    float64
	memory float64
	gpu    float64
	npu    float64
	ddr    float64
}

type Model struct {
	snapshots    <-chan model.Snapshot
	refresh      func()
	scopeChanged func(model.CollectionScope)
	latest       model.Snapshot
	hasData      bool
	closed       bool

	width        int
	height       int
	page         page
	previousPage page
	processSort  processSort
	selected     int
	processes    []model.ProcessInfo
	history      []historyPoint

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
		snapshots:    snapshots,
		refresh:      refresh,
		scopeChanged: scopeChanged,
		keys:         newKeyMap(),
		help:         helper,
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
		return m, nil
	case snapshotMsg:
		processesChanged := !sameProcessSlice(m.latest.Processes, message.snapshot.Processes)
		m.latest = message.snapshot
		m.hasData = true
		if processesChanged {
			m.sortProcesses()
		}
		m.addHistory(message.snapshot)
		m.clampSelection()
		return m, waitForSnapshot(m.snapshots)
	case snapshotsClosedMsg:
		m.closed = true
		return m, tea.Quit
	case tea.KeyMsg:
		if key.Matches(message, m.keys.Quit) {
			return m, tea.Quit
		}
		if key.Matches(message, m.keys.Overview) {
			m.setPage(pageOverview)
			return m, m.scopeCommand()
		}
		if key.Matches(message, m.keys.Processes) {
			m.setPage(pageProcesses)
			return m, m.scopeCommand()
		}
		if key.Matches(message, m.keys.Engines) {
			m.setPage(pageEngines)
			return m, m.scopeCommand()
		}
		if key.Matches(message, m.keys.Thermal) {
			m.setPage(pageThermal)
			return m, m.scopeCommand()
		}
		if key.Matches(message, m.keys.System) {
			m.setPage(pageSystem)
			return m, m.scopeCommand()
		}
		if key.Matches(message, m.keys.Help) {
			if m.page == pageHelp {
				m.page = m.previousPage
			} else {
				m.previousPage, m.page = m.page, pageHelp
			}
			return m, m.scopeCommand()
		}
		if key.Matches(message, m.keys.Back) && m.page == pageHelp {
			m.page = m.previousPage
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
			}
		}
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
	if m.page != pageHelp {
		m.previousPage = m.page
	}
	m.page = next
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
	case pageEngines:
		return model.CollectAccelerators
	case pageProcesses:
		return model.CollectProcesses
	case pageThermal:
		return model.CollectThermals
	case pageSystem:
		return model.CollectAll
	case pageHelp:
		return 0
	default:
		return model.CollectOverview
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
}

func (m *Model) addHistory(snapshot model.Snapshot) {
	point := historyPoint{
		cpu: metricNumber(snapshot.CPU.Overall),
		gpu: -1,
		npu: -1,
		ddr: -1,
	}
	if total, used := metricNumber(snapshot.Memory.Total), metricNumber(snapshot.Memory.Used); total > 0 && used >= 0 {
		point.memory = used * 100 / total
	} else {
		point.memory = -1
	}
	for _, accelerator := range snapshot.Accelerators {
		switch accelerator.Type {
		case "gpu":
			point.gpu = metricNumber(accelerator.Usage)
		case "npu":
			point.npu = metricNumber(accelerator.Usage)
		case "ddr":
			point.ddr = metricNumber(accelerator.Usage)
		}
	}
	if point.cpu < 0 && point.memory < 0 && point.gpu < 0 && point.npu < 0 && point.ddr < 0 {
		return
	}
	if len(m.history) < 120 {
		m.history = append(m.history, point)
		return
	}
	copy(m.history, m.history[1:])
	m.history[len(m.history)-1] = point
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
