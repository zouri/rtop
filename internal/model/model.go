package model

import "time"

// CollectionScope selects the dynamic collector groups needed for a snapshot.
type CollectionScope uint32

const (
	CollectCPU CollectionScope = 1 << iota
	CollectMemory
	CollectAccelerators
	CollectProcesses
	CollectThermals

	CollectOverview = CollectCPU | CollectMemory | CollectAccelerators | CollectThermals
	CollectAll      = CollectOverview | CollectProcesses
)

func (s CollectionScope) Includes(group CollectionScope) bool {
	return group != 0 && s&group == group
}

// MetricStatus describes whether a metric can be trusted and displayed.
type MetricStatus string

const (
	Available        MetricStatus = "available"
	Unsupported      MetricStatus = "unsupported"
	PermissionDenied MetricStatus = "permission_denied"
	Unavailable      MetricStatus = "unavailable"
	Stale            MetricStatus = "stale"
)

// Metric is the common representation for numeric measurements.
// Value is nil whenever Status is not Available.
type Metric struct {
	Value     *float64     `json:"value,omitempty"`
	Unit      string       `json:"unit,omitempty"`
	Timestamp time.Time    `json:"timestamp"`
	Source    string       `json:"source,omitempty"`
	Status    MetricStatus `json:"status"`
	Error     string       `json:"error,omitempty"`
}

func AvailableMetric(value float64, unit, source string, timestamp time.Time) Metric {
	return Metric{
		Value:     &value,
		Unit:      unit,
		Timestamp: timestamp,
		Source:    source,
		Status:    Available,
	}
}

func MissingMetric(status MetricStatus, unit, source, message string, timestamp time.Time) Metric {
	return Metric{
		Unit:      unit,
		Timestamp: timestamp,
		Source:    source,
		Status:    status,
		Error:     message,
	}
}

type SystemInfo struct {
	Hostname   string       `json:"hostname,omitempty"`
	OS         string       `json:"os,omitempty"`
	Kernel     string       `json:"kernel,omitempty"`
	Arch       string       `json:"arch,omitempty"`
	Board      string       `json:"board,omitempty"`
	SoC        string       `json:"soc,omitempty"`
	Compatible []string     `json:"compatible,omitempty"`
	Drivers    []DriverInfo `json:"drivers,omitempty"`
}

type DriverInfo struct {
	Type     string `json:"type"`
	Name     string `json:"name,omitempty"`
	Device   string `json:"device,omitempty"`
	Governor string `json:"governor,omitempty"`
}

type CPUCore struct {
	ID        int    `json:"id"`
	Cluster   string `json:"cluster,omitempty"`
	Usage     Metric `json:"usage"`
	Frequency Metric `json:"frequency"`
}

type CPUInfo struct {
	Overall Metric    `json:"overall"`
	Cores   []CPUCore `json:"cores"`
}

type MemoryInfo struct {
	Total     Metric `json:"total"`
	Used      Metric `json:"used"`
	Available Metric `json:"available"`
	SwapTotal Metric `json:"swap_total"`
	SwapUsed  Metric `json:"swap_used"`
}

type LoadInfo struct {
	OneMinute      Metric `json:"one_minute"`
	FiveMinutes    Metric `json:"five_minutes"`
	FifteenMinutes Metric `json:"fifteen_minutes"`
	Runnable       Metric `json:"runnable"`
	Tasks          Metric `json:"tasks"`
}

type ThermalZone struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Temperature Metric `json:"temperature"`
}

type Accelerator struct {
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	Usage     Metric            `json:"usage"`
	Frequency Metric            `json:"frequency"`
	Cores     []AcceleratorCore `json:"cores,omitempty"`
}

type AcceleratorCore struct {
	ID    int    `json:"id"`
	Usage Metric `json:"usage"`
}

type ProcessInfo struct {
	PID     int    `json:"pid"`
	Name    string `json:"name"`
	CPU     Metric `json:"cpu"`
	Memory  Metric `json:"memory"`
	Threads int    `json:"threads"`
}

type Warning struct {
	Component string `json:"component"`
	Message   string `json:"message"`
}

type Snapshot struct {
	Timestamp    time.Time     `json:"timestamp"`
	System       SystemInfo    `json:"system"`
	CPU          CPUInfo       `json:"cpu"`
	Memory       MemoryInfo    `json:"memory"`
	Load         LoadInfo      `json:"load"`
	Thermals     []ThermalZone `json:"thermals"`
	Accelerators []Accelerator `json:"accelerators"`
	Processes    []ProcessInfo `json:"processes"`
	Warnings     []Warning     `json:"warnings"`
}
