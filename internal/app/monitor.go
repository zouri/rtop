package app

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"rtop/internal/collector/accelerator"
	"rtop/internal/collector/cpu"
	"rtop/internal/collector/memory"
	processcollector "rtop/internal/collector/process"
	"rtop/internal/collector/thermal"
	"rtop/internal/model"
	"rtop/internal/platform"
	"rtop/internal/sysfs"
)

type Monitor struct {
	systemInfo     model.SystemInfo
	systemWarnings []model.Warning
	cpu            *cpu.Collector
	accelerator    *accelerator.Collector
	memory         *memory.Collector
	process        *processcollector.Collector
	thermal        *thermal.Collector

	processInterval time.Duration
	thermalInterval time.Duration
	now             func() time.Time
	forceSlow       atomic.Bool
	processDisabled atomic.Bool
	collectionScope atomic.Uint32

	processMu          sync.Mutex
	processCached      bool
	processCollectedAt time.Time
	processes          []model.ProcessInfo
	processWarnings    []model.Warning

	thermalMu          sync.Mutex
	thermalCached      bool
	thermalCollectedAt time.Time
	thermals           []model.ThermalZone
	thermalWarnings    []model.Warning
}

func NewMonitor(systemFS sysfs.SystemFS, cpuSampleDuration time.Duration) *Monitor {
	return NewMonitorWithIntervals(systemFS, cpuSampleDuration, 0, 0)
}

func NewMonitorWithIntervals(
	systemFS sysfs.SystemFS,
	cpuSampleDuration, processInterval, thermalInterval time.Duration,
) *Monitor {
	platformDetector := platform.NewDetector(systemFS)
	acceleratorCollector := accelerator.NewCollector(systemFS)
	// Platform identity and accelerator driver metadata are effectively static
	// for the lifetime of an interactive monitoring session.
	systemInfo, systemWarnings := platformDetector.Detect()
	systemInfo.Drivers = acceleratorCollector.Drivers()
	monitor := &Monitor{
		systemInfo:      systemInfo,
		systemWarnings:  systemWarnings,
		cpu:             cpu.NewCollector(systemFS, cpuSampleDuration),
		accelerator:     acceleratorCollector,
		memory:          memory.NewCollector(systemFS),
		process:         processcollector.NewCollector(systemFS, cpuSampleDuration),
		thermal:         thermal.NewCollector(systemFS),
		processInterval: processInterval,
		thermalInterval: thermalInterval,
		now:             time.Now,
	}
	monitor.collectionScope.Store(uint32(model.CollectAll))
	return monitor
}

func (m *Monitor) SystemInfo() (model.SystemInfo, []model.Warning) {
	return m.systemInfo, m.systemWarnings
}

func (m *Monitor) Snapshot(ctx context.Context) model.Snapshot {
	timestamp := m.now()
	forceSlow := m.forceSlow.Swap(false)
	scope := model.CollectionScope(m.collectionScope.Load())
	var cpuInfo model.CPUInfo
	var memoryInfo model.MemoryInfo
	var loadInfo model.LoadInfo
	zones := []model.ThermalZone{}
	processes := []model.ProcessInfo{}
	accelerators := []model.Accelerator{}
	var cpuWarnings, memoryWarnings, thermalWarnings, processWarnings, acceleratorWarnings []model.Warning
	var wait sync.WaitGroup
	collectProcess := scope.Includes(model.CollectProcesses) && !m.processDisabled.Load()
	collectorCount := 0
	if scope.Includes(model.CollectCPU) {
		collectorCount++
	}
	if scope.Includes(model.CollectMemory) {
		collectorCount++
	}
	if scope.Includes(model.CollectThermals) {
		collectorCount++
	}
	if collectProcess {
		collectorCount++
	}
	if scope.Includes(model.CollectAccelerators) {
		collectorCount++
	}
	wait.Add(collectorCount)
	if scope.Includes(model.CollectCPU) {
		go func() {
			defer wait.Done()
			cpuInfo, cpuWarnings = m.cpu.Collect(ctx)
		}()
	}
	if scope.Includes(model.CollectMemory) {
		go func() {
			defer wait.Done()
			memoryInfo, loadInfo, memoryWarnings = m.memory.Collect()
		}()
	}
	if scope.Includes(model.CollectThermals) {
		go func() {
			defer wait.Done()
			zones, thermalWarnings = m.collectThermals(timestamp, forceSlow)
		}()
	}
	if collectProcess {
		go func() {
			defer wait.Done()
			processes, processWarnings = m.collectProcesses(ctx, timestamp, forceSlow)
		}()
	}
	if scope.Includes(model.CollectAccelerators) {
		go func() {
			defer wait.Done()
			accelerators, acceleratorWarnings = m.accelerator.Collect()
		}()
	}
	wait.Wait()

	warnings := append([]model.Warning{}, m.systemWarnings...)
	warnings = append(warnings, cpuWarnings...)
	warnings = append(warnings, memoryWarnings...)
	warnings = append(warnings, thermalWarnings...)
	warnings = append(warnings, processWarnings...)
	warnings = append(warnings, acceleratorWarnings...)

	return model.Snapshot{
		Collected:    scope,
		Timestamp:    timestamp,
		System:       m.systemInfo,
		CPU:          cpuInfo,
		Memory:       memoryInfo,
		Load:         loadInfo,
		Thermals:     zones,
		Accelerators: accelerators,
		Processes:    processes,
		Warnings:     warnings,
	}
}

// ForceRefresh makes the next snapshot bypass the process and thermal cadence
// caches. Fast collectors are sampled on every snapshot regardless.
func (m *Monitor) ForceRefresh() {
	m.forceSlow.Store(true)
}

// SetCollectionScope changes which dynamic collector groups future snapshots
// execute. Static system information is always included from the session cache.
func (m *Monitor) SetCollectionScope(scope model.CollectionScope) {
	previous := model.CollectionScope(m.collectionScope.Swap(uint32(scope)))
	if scope.Includes(model.CollectCPU) && !previous.Includes(model.CollectCPU) {
		m.cpu.Reset()
	}
	if scope.Includes(model.CollectProcesses) && !previous.Includes(model.CollectProcesses) {
		m.processMu.Lock()
		m.process.Reset()
		m.processCached = false
		m.processes = nil
		m.processWarnings = nil
		m.processMu.Unlock()
	}
}

// DisableProcesses omits process data and avoids scanning /proc on future
// snapshots. It is intended for headless summary/watch use.
func (m *Monitor) DisableProcesses() {
	m.processDisabled.Store(true)
	m.processMu.Lock()
	m.processCached = false
	m.processes = nil
	m.processWarnings = nil
	m.processMu.Unlock()
}

func (m *Monitor) collectProcesses(
	ctx context.Context, timestamp time.Time, force bool,
) ([]model.ProcessInfo, []model.Warning) {
	m.processMu.Lock()
	defer m.processMu.Unlock()
	if !force && m.processCached && !collectionDue(timestamp, m.processCollectedAt, m.processInterval) {
		return m.processes, m.processWarnings
	}
	m.processes, m.processWarnings = m.process.Collect(ctx)
	m.processCollectedAt = timestamp
	m.processCached = true
	return m.processes, m.processWarnings
}

func (m *Monitor) collectThermals(
	timestamp time.Time, force bool,
) ([]model.ThermalZone, []model.Warning) {
	m.thermalMu.Lock()
	defer m.thermalMu.Unlock()
	if !force && m.thermalCached && !collectionDue(timestamp, m.thermalCollectedAt, m.thermalInterval) {
		return m.thermals, m.thermalWarnings
	}
	m.thermals, m.thermalWarnings = m.thermal.Collect()
	m.thermalCollectedAt = timestamp
	m.thermalCached = true
	return m.thermals, m.thermalWarnings
}

func collectionDue(now, previous time.Time, interval time.Duration) bool {
	if interval <= 0 || now.Before(previous) {
		return true
	}
	return now.Sub(previous) >= interval
}
