package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"rtop/internal/model"
	"rtop/internal/tuner"
)

type page int

const (
	pageAll page = iota
	pageCPU
	pageMemory
	pageEngines
	pageProcesses
	pageCTRL
	pageInfo
)

type processSort int

const (
	sortCPU processSort = iota
	sortMemory
)

type tuningInput int

const (
	tuningInputNone tuningInput = iota
	tuningInputSwappiness
	tuningInputSwapSize
)

// TuneRequest is the intentionally small set of mutations exposed by the TUI.
// Exactly one field is set for each request.
type TuneRequest struct {
	CPUProfile    string
	Swappiness    *int
	SwapSizeBytes *uint64
}

type TuneFunc func(TuneRequest) error

type Model struct {
	snapshots     <-chan model.Snapshot
	refresh       func()
	scopeChanged  func(model.CollectionScope)
	tune          TuneFunc
	latest        model.Snapshot
	hasData       bool
	closed        bool
	unprivileged  bool
	accessLimited bool

	width              int
	height             int
	page               page
	showHelp           bool
	tuningEditing      bool
	tuningBusy         bool
	tuningStatus       string
	tuningFailed       bool
	tuningValue        string
	tuningInput        tuningInput
	showProcessDetail  bool
	showProcessPath    bool
	processSort        processSort
	selected           int
	scrollOffset       int
	processes          []model.ProcessInfo
	topCPUProcesses    []model.ProcessInfo
	topMemoryProcesses []model.ProcessInfo

	cpuHistory          []float64
	coreHistory         map[int][]float64
	memoryHistory       []float64
	memoryUsedHistory   []float64
	memorySharedHistory []float64
	memoryBufferHistory []float64
	memoryCacheHistory  []float64
	gpuHistory          []float64
	npuHistory          []float64
	ddrHistory          []float64
	rgaHistory          []float64

	keys keyMap
	help help.Model
}

type snapshotMsg struct {
	snapshot model.Snapshot
}

type snapshotsClosedMsg struct{}

type tuningFinishedMsg struct {
	label string
	err   error
}

func NewModel(snapshots <-chan model.Snapshot, refresh func()) Model {
	return NewModelWithScope(snapshots, refresh, nil)
}

func NewModelWithScope(
	snapshots <-chan model.Snapshot,
	refresh func(),
	scopeChanged func(model.CollectionScope),
) Model {
	return NewModelWithTuning(snapshots, refresh, scopeChanged, nil)
}

func NewModelWithTuning(
	snapshots <-chan model.Snapshot,
	refresh func(),
	scopeChanged func(model.CollectionScope),
	tune TuneFunc,
) Model {
	helper := help.New()
	helper.ShowAll = false
	return Model{
		snapshots: snapshots, refresh: refresh, scopeChanged: scopeChanged, tune: tune,
		keys: newKeyMap(), help: helper, coreHistory: make(map[int][]float64),
		unprivileged: os.Geteuid() != 0,
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
	case tuningFinishedMsg:
		m.tuningBusy = false
		m.tuningEditing = false
		m.tuningInput = tuningInputNone
		m.tuningFailed = message.err != nil
		if message.err != nil {
			m.tuningStatus = "Failed: " + message.err.Error()
			return m, nil
		}
		m.tuningStatus = message.label
		return m, m.refreshCommand()
	case tea.KeyMsg:
		if m.page == pageCTRL && m.tuningBusy && key.Matches(message, m.keys.Quit) {
			return m, nil
		}
		if key.Matches(message, m.keys.Quit) {
			return m, tea.Quit
		}
		if m.page == pageCTRL && (m.tuningEditing || m.tuningBusy) {
			return m.updateCTRL(message)
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
		if !m.showHelp && key.Matches(message, m.keys.Previous) {
			m.setPage((m.page + pageInfo) % (pageInfo + 1))
			return m, m.scopeCommand()
		}
		if !m.showHelp && key.Matches(message, m.keys.Next) {
			m.setPage((m.page + 1) % (pageInfo + 1))
			return m, m.scopeCommand()
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
		if key.Matches(message, m.keys.Control) {
			m.setPage(pageCTRL)
			return m, m.scopeCommand()
		}
		if key.Matches(message, m.keys.Refresh) {
			return m, m.refreshCommand()
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
		if m.page == pageCTRL {
			return m.updateCTRL(message)
		}

		if key.Matches(message, m.keys.Paths) &&
			(m.page == pageAll || m.page == pageProcesses) {
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

func (m Model) updateCTRL(message tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(message, m.keys.Back) {
		if m.tuningBusy {
			return m, nil
		}
		if m.tuningEditing {
			m.tuningEditing = false
			m.tuningInput = tuningInputNone
			m.tuningStatus = ""
			return m, nil
		}
		return m, nil
	}
	if key.Matches(message, m.keys.Up) {
		m.scrollOffset--
		m.clampScroll()
		return m, nil
	}
	if key.Matches(message, m.keys.Down) {
		m.scrollOffset++
		m.clampScroll()
		return m, nil
	}
	if m.tuningBusy {
		return m, nil
	}
	if m.tuningEditing {
		if key.Matches(message, m.keys.Details) {
			if m.tuningInput == tuningInputSwapSize {
				value, err := tuner.ParseSwapSize(m.tuningValue)
				if err != nil {
					m.tuningFailed = true
					m.tuningStatus = "Swap size: " + err.Error()
					return m, nil
				}
				return m.beginTuning(
					TuneRequest{SwapSizeBytes: &value},
					fmt.Sprintf("Total Swap set to %s", tuner.FormatSwapSize(value)),
				)
			}
			value, err := strconv.Atoi(m.tuningValue)
			if err != nil || value < 0 || value > 200 {
				m.tuningFailed = true
				m.tuningStatus = "Swappiness must be an integer from 0 to 200."
				return m, nil
			}
			return m.beginTuning(
				TuneRequest{Swappiness: &value},
				fmt.Sprintf("vm.swappiness set to %d", value),
			)
		}
		if message.Type == tea.KeyBackspace || message.Type == tea.KeyDelete {
			if len(m.tuningValue) > 0 {
				m.tuningValue = m.tuningValue[:len(m.tuningValue)-1]
			}
			return m, nil
		}
		for _, character := range message.Runes {
			if m.tuningInput == tuningInputSwapSize {
				if validSwapSizeRune(character) && len(m.tuningValue) < 32 {
					m.tuningValue += string(character)
				}
			} else if character >= '0' && character <= '9' && len(m.tuningValue) < 3 {
				m.tuningValue += string(character)
			}
		}
		return m, nil
	}

	switch {
	case key.Matches(message, m.keys.Performance):
		return m.beginTuning(TuneRequest{CPUProfile: "performance"}, "CPU mode set to performance")
	case key.Matches(message, m.keys.Powersave):
		return m.beginTuning(TuneRequest{CPUProfile: "powersave"}, "CPU mode set to powersave")
	case key.Matches(message, m.keys.SwapTune):
		m.tuningEditing = true
		m.tuningInput = tuningInputSwappiness
		m.tuningFailed = false
		m.tuningStatus = ""
		m.tuningValue = ""
		return m, nil
	case key.Matches(message, m.keys.SwapSizeTune):
		m.tuningEditing = true
		m.tuningInput = tuningInputSwapSize
		m.tuningFailed = false
		m.tuningStatus = ""
		m.tuningValue = ""
		return m, nil
	}
	return m, nil
}

func validSwapSizeRune(character rune) bool {
	return character >= '0' && character <= '9' || character == '.' ||
		character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
}

func (m Model) beginTuning(request TuneRequest, success string) (tea.Model, tea.Cmd) {
	m.tuningBusy = true
	m.tuningFailed = false
	m.tuningStatus = "Applying…"
	tune := m.tune
	return m, func() tea.Msg {
		if tune == nil {
			return tuningFinishedMsg{err: fmt.Errorf("system control is unavailable in this session")}
		}
		return tuningFinishedMsg{label: success, err: tune(request)}
	}
}

func (m Model) refreshCommand() tea.Cmd {
	refresh := m.refresh
	if refresh == nil {
		return nil
	}
	return func() tea.Msg {
		refresh()
		return nil
	}
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
	return m.scopeChangeCommand(scopeForPage(m.page))
}

func (m Model) scopeChangeCommand(scope model.CollectionScope) tea.Cmd {
	changed := m.scopeChanged
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
		return model.CollectCPU
	case pageMemory:
		return model.CollectMemory | model.CollectAccelerators
	case pageEngines:
		return model.CollectAccelerators
	case pageProcesses:
		return model.CollectProcesses
	case pageInfo:
		return model.CollectOverview
	case pageCTRL:
		return model.CollectCPU | model.CollectMemory
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
	m.accessLimited = m.unprivileged && snapshotHasPermissionDenied(m.latest)
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
	limit := len(m.latest.Processes)
	m.topCPUProcesses = topRankedProcesses(m.latest.Processes, limit, sortCPU)
	m.topMemoryProcesses = topRankedProcesses(m.latest.Processes, limit, sortMemory)
}

func topRankedProcesses(processes []model.ProcessInfo, limit int, order processSort) []model.ProcessInfo {
	if limit <= 0 || len(processes) == 0 {
		return []model.ProcessInfo{}
	}
	limit = min(limit, len(processes))
	result := append([]model.ProcessInfo(nil), processes...)
	sort.SliceStable(result, func(i, j int) bool {
		return processRanksBefore(result[i], result[j], order)
	})
	return result[:limit:limit]
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
		if m.coreHistory == nil {
			m.coreHistory = make(map[int][]float64)
		}
		for _, core := range snapshot.CPU.Cores {
			m.coreHistory[core.ID] = appendHistory(m.coreHistory[core.ID], metricNumber(core.Usage))
		}
	}
	if scope.Includes(model.CollectMemory) {
		m.memoryHistory = appendHistory(m.memoryHistory, memoryPercent(snapshot.Memory))
		breakdown, ok := memoryBreakdownPercentages(snapshot.Memory)
		if !ok {
			breakdown = memoryBreakdown{used: -1, shared: -1, buffers: -1, cache: -1}
		}
		m.memoryUsedHistory = appendHistory(m.memoryUsedHistory, breakdown.used)
		m.memorySharedHistory = appendHistory(m.memorySharedHistory, breakdown.shared)
		m.memoryBufferHistory = appendHistory(m.memoryBufferHistory, breakdown.buffers)
		m.memoryCacheHistory = appendHistory(m.memoryCacheHistory, breakdown.cache)
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

type memoryBreakdown struct {
	used    float64
	shared  float64
	buffers float64
	cache   float64
}

// memoryBreakdownPercentages follows jtop's stacked RAM chart: non-cache used
// memory at the bottom, followed by shared memory, buffers, and cache. Values
// are made disjoint so the stack represents total resident RAM without double
// counting reclaimable memory.
func memoryBreakdownPercentages(memory model.MemoryInfo) (memoryBreakdown, bool) {
	total := metricNumber(memory.Total)
	free := metricNumber(memory.Free)
	shared := metricNumber(memory.Shared)
	buffers := metricNumber(memory.Buffers)
	cache := metricNumber(memory.Cache)
	if total <= 0 || free < 0 || shared < 0 || buffers < 0 || cache < 0 {
		return memoryBreakdown{}, false
	}

	free = min(total, max(0, free))
	occupied := total - free
	cache = min(occupied, max(0, cache))
	buffers = min(occupied-cache, max(0, buffers))
	nonCache := occupied - cache - buffers
	shared = min(nonCache, max(0, shared))
	used := nonCache - shared

	return memoryBreakdown{
		used:    used * 100 / total,
		shared:  shared * 100 / total,
		buffers: buffers * 100 / total,
		cache:   cache * 100 / total,
	}, true
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
	return RunWithTuning(ctx, snapshots, refresh, scopeChanged, nil, output)
}

func RunWithTuning(
	ctx context.Context,
	snapshots <-chan model.Snapshot,
	refresh func(),
	scopeChanged func(model.CollectionScope),
	tune TuneFunc,
	output io.Writer,
) error {
	output = synchronizedOutputForTerminal(output)
	program := tea.NewProgram(
		NewModelWithTuning(snapshots, refresh, scopeChanged, tune),
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
