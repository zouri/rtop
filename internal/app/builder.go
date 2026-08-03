package app

import (
	"context"
	"time"

	"rtop/internal/collector/cpu"
	"rtop/internal/collector/ddr"
	"rtop/internal/collector/gpu"
	"rtop/internal/collector/memory"
	"rtop/internal/collector/npu"
	processcollector "rtop/internal/collector/process"
	"rtop/internal/collector/thermal"
	"rtop/internal/model"
	"rtop/internal/platform"
	"rtop/internal/sysfs"
)

type Builder struct {
	platform *platform.Detector
	cpu      *cpu.Collector
	gpu      *gpu.Collector
	npu      *npu.Collector
	ddr      *ddr.Collector
	memory   *memory.Collector
	process  *processcollector.Collector
	thermal  *thermal.Collector
}

func NewBuilder(systemFS sysfs.SystemFS, cpuSampleDuration time.Duration) *Builder {
	return &Builder{
		platform: platform.NewDetector(systemFS),
		cpu:      cpu.NewCollector(systemFS, cpuSampleDuration),
		gpu:      gpu.NewCollector(systemFS),
		npu:      npu.NewCollector(systemFS),
		ddr:      ddr.NewCollector(systemFS),
		memory:   memory.NewCollector(systemFS),
		process:  processcollector.NewCollector(systemFS, cpuSampleDuration),
		thermal:  thermal.NewCollector(systemFS),
	}
}

func (b *Builder) SystemInfo() (model.SystemInfo, []model.Warning) {
	info, warnings := b.platform.Detect()
	return b.enrichSystemInfo(info), warnings
}

func (b *Builder) Reprobe() []model.Warning {
	var warnings []model.Warning
	warnings = append(warnings, b.gpu.Probe()...)
	warnings = append(warnings, b.npu.Probe()...)
	warnings = append(warnings, b.ddr.Probe()...)
	return warnings
}

func (b *Builder) Build(ctx context.Context) model.Snapshot {
	timestamp := time.Now()
	systemInfo, systemWarnings := b.platform.Detect()
	systemInfo = b.enrichSystemInfo(systemInfo)

	type cpuResult struct {
		info     model.CPUInfo
		warnings []model.Warning
	}
	type memoryResult struct {
		memory   model.MemoryInfo
		load     model.LoadInfo
		warnings []model.Warning
	}
	type thermalResult struct {
		zones    []model.ThermalZone
		warnings []model.Warning
	}
	type processResult struct {
		processes []model.ProcessInfo
		warnings  []model.Warning
	}
	type acceleratorResult struct {
		accelerators []model.Accelerator
		warnings     []model.Warning
	}

	cpuResults := make(chan cpuResult, 1)
	memoryResults := make(chan memoryResult, 1)
	thermalResults := make(chan thermalResult, 1)
	processResults := make(chan processResult, 1)
	acceleratorResults := make(chan acceleratorResult, 1)

	go func() {
		info, warnings := b.cpu.Collect(ctx)
		cpuResults <- cpuResult{info: info, warnings: warnings}
	}()
	go func() {
		memoryInfo, loadInfo, warnings := b.memory.Collect()
		memoryResults <- memoryResult{memory: memoryInfo, load: loadInfo, warnings: warnings}
	}()
	go func() {
		zones, warnings := b.thermal.Collect()
		thermalResults <- thermalResult{zones: zones, warnings: warnings}
	}()
	go func() {
		processes, warnings := b.process.Collect(ctx)
		processResults <- processResult{processes: processes, warnings: warnings}
	}()
	go func() {
		gpuInfo, gpuWarnings := b.gpu.Collect()
		npuInfo, npuWarnings := b.npu.Collect()
		ddrInfo, ddrWarnings := b.ddr.Collect()
		warnings := append([]model.Warning{}, gpuWarnings...)
		warnings = append(warnings, npuWarnings...)
		warnings = append(warnings, ddrWarnings...)
		acceleratorResults <- acceleratorResult{
			accelerators: []model.Accelerator{gpuInfo, npuInfo, ddrInfo},
			warnings:     warnings,
		}
	}()

	cpuData := <-cpuResults
	memoryData := <-memoryResults
	thermalData := <-thermalResults
	processData := <-processResults
	acceleratorData := <-acceleratorResults
	warnings := append([]model.Warning{}, systemWarnings...)
	warnings = append(warnings, cpuData.warnings...)
	warnings = append(warnings, memoryData.warnings...)
	warnings = append(warnings, thermalData.warnings...)
	warnings = append(warnings, processData.warnings...)
	warnings = append(warnings, acceleratorData.warnings...)

	return model.Snapshot{
		Timestamp:    timestamp,
		System:       systemInfo,
		CPU:          cpuData.info,
		Memory:       memoryData.memory,
		Load:         memoryData.load,
		Thermals:     thermalData.zones,
		Accelerators: acceleratorData.accelerators,
		Processes:    processData.processes,
		Warnings:     warnings,
	}
}

func (b *Builder) enrichSystemInfo(info model.SystemInfo) model.SystemInfo {
	info.Drivers = nil
	if driver, ok := b.gpu.Info(); ok {
		info.Drivers = append(info.Drivers, driver)
	}
	if driver, ok := b.npu.Info(); ok {
		info.Drivers = append(info.Drivers, driver)
	}
	if driver, ok := b.ddr.Info(); ok {
		info.Drivers = append(info.Drivers, driver)
	}
	return info
}
