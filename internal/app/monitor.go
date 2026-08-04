package app

import (
	"context"
	"sync"
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
	platform    *platform.Detector
	cpu         *cpu.Collector
	accelerator *accelerator.Collector
	memory      *memory.Collector
	process     *processcollector.Collector
	thermal     *thermal.Collector
}

func NewMonitor(systemFS sysfs.SystemFS, cpuSampleDuration time.Duration) *Monitor {
	return &Monitor{
		platform:    platform.NewDetector(systemFS),
		cpu:         cpu.NewCollector(systemFS, cpuSampleDuration),
		accelerator: accelerator.NewCollector(systemFS),
		memory:      memory.NewCollector(systemFS),
		process:     processcollector.NewCollector(systemFS, cpuSampleDuration),
		thermal:     thermal.NewCollector(systemFS),
	}
}

func (m *Monitor) SystemInfo() (model.SystemInfo, []model.Warning) {
	info, warnings := m.platform.Detect()
	info.Drivers = m.accelerator.Drivers()
	return info, warnings
}

func (m *Monitor) Snapshot(ctx context.Context) model.Snapshot {
	timestamp := time.Now()
	systemInfo, systemWarnings := m.platform.Detect()
	systemInfo.Drivers = m.accelerator.Drivers()

	var cpuInfo model.CPUInfo
	var memoryInfo model.MemoryInfo
	var loadInfo model.LoadInfo
	var zones []model.ThermalZone
	var processes []model.ProcessInfo
	var accelerators []model.Accelerator
	var cpuWarnings, memoryWarnings, thermalWarnings, processWarnings, acceleratorWarnings []model.Warning
	var wait sync.WaitGroup
	wait.Add(5)
	go func() {
		defer wait.Done()
		cpuInfo, cpuWarnings = m.cpu.Collect(ctx)
	}()
	go func() {
		defer wait.Done()
		memoryInfo, loadInfo, memoryWarnings = m.memory.Collect()
	}()
	go func() {
		defer wait.Done()
		zones, thermalWarnings = m.thermal.Collect()
	}()
	go func() {
		defer wait.Done()
		processes, processWarnings = m.process.Collect(ctx)
	}()
	go func() {
		defer wait.Done()
		accelerators, acceleratorWarnings = m.accelerator.Collect()
	}()
	wait.Wait()

	warnings := append([]model.Warning{}, systemWarnings...)
	warnings = append(warnings, cpuWarnings...)
	warnings = append(warnings, memoryWarnings...)
	warnings = append(warnings, thermalWarnings...)
	warnings = append(warnings, processWarnings...)
	warnings = append(warnings, acceleratorWarnings...)

	return model.Snapshot{
		Timestamp:    timestamp,
		System:       systemInfo,
		CPU:          cpuInfo,
		Memory:       memoryInfo,
		Load:         loadInfo,
		Thermals:     zones,
		Accelerators: accelerators,
		Processes:    processes,
		Warnings:     warnings,
	}
}
