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
	CollectPower

	CollectOverview = CollectCPU | CollectMemory | CollectAccelerators | CollectThermals | CollectPower
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

// TextMetric is the status-aware representation for textual runtime values
// such as governors and runtime power states.
type TextMetric struct {
	Value     *string      `json:"value,omitempty"`
	Timestamp time.Time    `json:"timestamp"`
	Source    string       `json:"source,omitempty"`
	Status    MetricStatus `json:"status"`
	Error     string       `json:"error,omitempty"`
}

func AvailableTextMetric(value, source string, timestamp time.Time) TextMetric {
	return TextMetric{
		Value:     &value,
		Timestamp: timestamp,
		Source:    source,
		Status:    Available,
	}
}

func MissingTextMetric(status MetricStatus, source, message string, timestamp time.Time) TextMetric {
	return TextMetric{
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
	ID        int              `json:"id"`
	Cluster   string           `json:"cluster,omitempty"`
	Usage     Metric           `json:"usage"`
	Frequency Metric           `json:"frequency"`
	Policy    *FrequencyPolicy `json:"policy,omitempty"`
}

type CPUInfo struct {
	Overall Metric    `json:"overall"`
	Cores   []CPUCore `json:"cores"`
}

// MemoryInfo exposes the core column set used by procps free. Cache contains
// Cached + SReclaimable, while BufferCache additionally includes Buffers.
type MemoryInfo struct {
	Total       Metric       `json:"total"`
	Used        Metric       `json:"used"`
	Free        Metric       `json:"free"`
	Shared      Metric       `json:"shared"`
	Buffers     Metric       `json:"buffers"`
	Cache       Metric       `json:"cache"`
	BufferCache Metric       `json:"buff_cache"`
	Available   Metric       `json:"available"`
	SwapTotal   Metric       `json:"swap_total"`
	SwapUsed    Metric       `json:"swap_used"`
	SwapFree    Metric       `json:"swap_free"`
	SwapCached  Metric       `json:"swap_cached"`
	Swappiness  Metric       `json:"swappiness"`
	SwapDevices []SwapDevice `json:"swap_devices"`
	CMATotal    Metric       `json:"cma_total"`
	CMAUsed     Metric       `json:"cma_used"`
	CMAFree     Metric       `json:"cma_free"`
	Pressure    PressureInfo `json:"pressure"`
	ZRAM        []ZRAMDevice `json:"zram"`
}

type SwapKind string

const (
	SwapZRAM  SwapKind = "zram"
	SwapFile  SwapKind = "file"
	SwapBlock SwapKind = "block"
)

// SwapDevice is one currently active entry from /proc/swaps. Size and Used
// describe usable swap capacity, which can be one page smaller than the
// backing zram device or file that contains the swap header.
type SwapDevice struct {
	Path       string   `json:"path"`
	Kind       SwapKind `json:"kind"`
	KernelType string   `json:"kernel_type"`
	Size       Metric   `json:"size"`
	Used       Metric   `json:"used"`
	Priority   int      `json:"priority"`
}

// FrequencyPolicy describes the active software limits and the immutable
// hardware range for a CPU or devfreq performance domain.
type FrequencyPolicy struct {
	Minimum         Metric     `json:"minimum"`
	Maximum         Metric     `json:"maximum"`
	HardwareMinimum Metric     `json:"hardware_minimum"`
	HardwareMaximum Metric     `json:"hardware_maximum"`
	Governor        TextMetric `json:"governor"`
}

type PressureInfo struct {
	CPU    PressureResource `json:"cpu"`
	Memory PressureResource `json:"memory"`
	IO     PressureResource `json:"io"`
}

type PressureResource struct {
	Some PressureWindow `json:"some"`
	Full PressureWindow `json:"full"`
}

type PressureWindow struct {
	Avg10  Metric `json:"avg10"`
	Avg60  Metric `json:"avg60"`
	Avg300 Metric `json:"avg300"`
	Total  Metric `json:"total"`
}

type ZRAMDevice struct {
	Name             string     `json:"name"`
	DiskSize         Metric     `json:"disk_size"`
	OriginalData     Metric     `json:"original_data"`
	CompressedData   Metric     `json:"compressed_data"`
	MemoryUsed       Metric     `json:"memory_used"`
	CompressionRatio Metric     `json:"compression_ratio"`
	Algorithm        TextMetric `json:"algorithm"`
}

type LoadInfo struct {
	OneMinute      Metric `json:"one_minute"`
	FiveMinutes    Metric `json:"five_minutes"`
	FifteenMinutes Metric `json:"fifteen_minutes"`
	Runnable       Metric `json:"runnable"`
	Tasks          Metric `json:"tasks"`
}

type ThermalZone struct {
	Name        string        `json:"name"`
	Type        string        `json:"type,omitempty"`
	Temperature Metric        `json:"temperature"`
	Trips       []ThermalTrip `json:"trips,omitempty"`
}

type ThermalTrip struct {
	ID          int    `json:"id"`
	Type        string `json:"type,omitempty"`
	Temperature Metric `json:"temperature"`
	Hysteresis  Metric `json:"hysteresis"`
}

type CoolingDevice struct {
	Name         string `json:"name"`
	Type         string `json:"type,omitempty"`
	CurrentState Metric `json:"current_state"`
	MaximumState Metric `json:"maximum_state"`
}

type PowerSensor struct {
	Name  string `json:"name"`
	Chip  string `json:"chip,omitempty"`
	Kind  string `json:"kind"`
	Value Metric `json:"value"`
}

type FanInfo struct {
	Name        string     `json:"name"`
	Chip        string     `json:"chip,omitempty"`
	Speed       Metric     `json:"speed"`
	TargetSpeed Metric     `json:"target_speed"`
	PWM         Metric     `json:"pwm"`
	ControlMode TextMetric `json:"control_mode"`
}

type Accelerator struct {
	Name          string               `json:"name"`
	Type          string               `json:"type"`
	Usage         Metric               `json:"usage"`
	Frequency     Metric               `json:"frequency"`
	Policy        *FrequencyPolicy     `json:"policy,omitempty"`
	RuntimeStatus TextMetric           `json:"runtime_status"`
	DriverVersion *TextMetric          `json:"driver_version,omitempty"`
	Cores         []AcceleratorCore    `json:"cores,omitempty"`
	Sessions      []AcceleratorSession `json:"sessions,omitempty"`
}

type AcceleratorCore struct {
	ID    int    `json:"id"`
	Name  string `json:"name,omitempty"`
	Usage Metric `json:"usage"`
}

type AcceleratorSession struct {
	Session int    `json:"session"`
	Status  string `json:"status"`
	TGID    int    `json:"tgid"`
	Process string `json:"process"`
}

type ProcessInfo struct {
	PID     int    `json:"pid"`
	Name    string `json:"name"`
	Path    string `json:"path,omitempty"`
	CPU     Metric `json:"cpu"`
	Memory  Metric `json:"memory"`
	Threads int    `json:"threads"`
}

type Warning struct {
	Component string `json:"component"`
	Message   string `json:"message"`
}

type Snapshot struct {
	// Collected records which dynamic groups are present in this snapshot. It is
	// runtime metadata for interactive consumers and is intentionally omitted
	// from the stable JSON snapshot format.
	Collected    CollectionScope `json:"-"`
	Timestamp    time.Time       `json:"timestamp"`
	System       SystemInfo      `json:"system"`
	CPU          CPUInfo         `json:"cpu"`
	Memory       MemoryInfo      `json:"memory"`
	Load         LoadInfo        `json:"load"`
	Thermals     []ThermalZone   `json:"thermals"`
	Cooling      []CoolingDevice `json:"cooling_devices"`
	Power        []PowerSensor   `json:"power_sensors"`
	Fans         []FanInfo       `json:"fans"`
	Accelerators []Accelerator   `json:"accelerators"`
	Processes    []ProcessInfo   `json:"processes"`
	Warnings     []Warning       `json:"warnings"`
}
