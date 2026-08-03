package ui

import (
	"context"
	"io"

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
	snapshots <-chan model.Snapshot
	refresh   func()
	latest    model.Snapshot
	hasData   bool
	closed    bool

	width        int
	height       int
	page         page
	previousPage page
	processSort  processSort
	selected     int
	history      []historyPoint

	keys keyMap
	help help.Model
}

type snapshotMsg struct {
	snapshot model.Snapshot
}

type snapshotsClosedMsg struct{}

func NewModel(snapshots <-chan model.Snapshot, refresh func()) Model {
	helper := help.New()
	helper.ShowAll = false
	return Model{
		snapshots: snapshots,
		refresh:   refresh,
		keys:      newKeyMap(),
		help:      helper,
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
		m.latest = message.snapshot
		m.hasData = true
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
			return m, nil
		}
		if key.Matches(message, m.keys.Processes) {
			m.setPage(pageProcesses)
			return m, nil
		}
		if key.Matches(message, m.keys.Engines) {
			m.setPage(pageEngines)
			return m, nil
		}
		if key.Matches(message, m.keys.Thermal) {
			m.setPage(pageThermal)
			return m, nil
		}
		if key.Matches(message, m.keys.System) {
			m.setPage(pageSystem)
			return m, nil
		}
		if key.Matches(message, m.keys.Help) {
			if m.page == pageHelp {
				m.page = m.previousPage
			} else {
				m.previousPage, m.page = m.page, pageHelp
			}
			return m, nil
		}
		if key.Matches(message, m.keys.Back) && m.page == pageHelp {
			m.page = m.previousPage
			return m, nil
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
				m.processSort = sortCPU
			case key.Matches(message, m.keys.SortMemory):
				m.processSort = sortMemory
			}
		}
	}
	return m, nil
}

func (m *Model) setPage(next page) {
	if m.page != pageHelp {
		m.previousPage = m.page
	}
	m.page = next
}

func (m *Model) clampSelection() {
	maximum := len(m.latest.Processes) - 1
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
	m.history = append(m.history, point)
	if len(m.history) > 120 {
		m.history = append([]historyPoint(nil), m.history[len(m.history)-120:]...)
	}
}

func Run(
	ctx context.Context,
	snapshots <-chan model.Snapshot,
	refresh func(),
	output io.Writer,
) error {
	program := tea.NewProgram(
		NewModel(snapshots, refresh),
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
