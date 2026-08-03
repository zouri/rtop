package memory

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

const (
	meminfoPath = "/proc/meminfo"
	loadavgPath = "/proc/loadavg"
)

type LoadValues struct {
	OneMinute      float64
	FiveMinutes    float64
	FifteenMinutes float64
	Runnable       uint64
	Tasks          uint64
}

func ParseMeminfo(data []byte) (map[string]uint64, error) {
	result := make(map[string]uint64)
	for lineNumber, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, rawValue, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("line %d: missing colon", lineNumber+1)
		}
		fields := strings.Fields(rawValue)
		if len(fields) == 0 {
			return nil, fmt.Errorf("%s: missing value", key)
		}
		value, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		multiplier := uint64(1)
		if len(fields) > 1 {
			switch strings.ToLower(fields[1]) {
			case "kb":
				multiplier = 1024
			case "mb":
				multiplier = 1024 * 1024
			case "gb":
				multiplier = 1024 * 1024 * 1024
			default:
				return nil, fmt.Errorf("%s: unsupported unit %q", key, fields[1])
			}
		}
		if value > ^uint64(0)/multiplier {
			return nil, fmt.Errorf("%s: value overflows bytes", key)
		}
		result[key] = value * multiplier
	}
	if _, ok := result["MemTotal"]; !ok {
		return nil, fmt.Errorf("MemTotal is missing")
	}
	return result, nil
}

func ParseLoadavg(data []byte) (LoadValues, error) {
	fields := strings.Fields(string(data))
	if len(fields) < 4 {
		return LoadValues{}, fmt.Errorf("expected at least 4 fields")
	}
	var result LoadValues
	var err error
	if result.OneMinute, err = strconv.ParseFloat(fields[0], 64); err != nil {
		return LoadValues{}, fmt.Errorf("one minute load: %w", err)
	}
	if result.FiveMinutes, err = strconv.ParseFloat(fields[1], 64); err != nil {
		return LoadValues{}, fmt.Errorf("five minute load: %w", err)
	}
	if result.FifteenMinutes, err = strconv.ParseFloat(fields[2], 64); err != nil {
		return LoadValues{}, fmt.Errorf("fifteen minute load: %w", err)
	}
	runnable, tasks, ok := strings.Cut(fields[3], "/")
	if !ok {
		return LoadValues{}, fmt.Errorf("invalid runnable/tasks field %q", fields[3])
	}
	if result.Runnable, err = strconv.ParseUint(runnable, 10, 64); err != nil {
		return LoadValues{}, fmt.Errorf("runnable tasks: %w", err)
	}
	if result.Tasks, err = strconv.ParseUint(tasks, 10, 64); err != nil {
		return LoadValues{}, fmt.Errorf("total tasks: %w", err)
	}
	return result, nil
}

type Collector struct {
	fs sysfs.SystemFS
}

func NewCollector(systemFS sysfs.SystemFS) *Collector {
	return &Collector{fs: systemFS}
}

func (c *Collector) Collect() (model.MemoryInfo, model.LoadInfo, []model.Warning) {
	timestamp := time.Now()
	memoryInfo, memoryWarning := c.collectMemory(timestamp)
	loadInfo, loadWarning := c.collectLoad(timestamp)
	var warnings []model.Warning
	if memoryWarning != nil {
		warnings = append(warnings, *memoryWarning)
	}
	if loadWarning != nil {
		warnings = append(warnings, *loadWarning)
	}
	return memoryInfo, loadInfo, warnings
}

func (c *Collector) collectMemory(timestamp time.Time) (model.MemoryInfo, *model.Warning) {
	data, err := c.fs.ReadFile(meminfoPath)
	if err != nil {
		return unavailableMemory(timestamp, sysfs.StatusForError(err), err.Error()), &model.Warning{
			Component: "memory", Message: meminfoPath + ": " + err.Error(),
		}
	}
	values, err := ParseMeminfo(data)
	if err != nil {
		return unavailableMemory(timestamp, model.Unavailable, err.Error()), &model.Warning{
			Component: "memory", Message: "parse " + meminfoPath + ": " + err.Error(),
		}
	}

	total := values["MemTotal"]
	available, ok := values["MemAvailable"]
	if !ok {
		available = sum(values, "MemFree", "Buffers", "Cached", "SReclaimable")
		if shmem := values["Shmem"]; available >= shmem {
			available -= shmem
		}
	}
	if available > total {
		available = total
	}
	swapTotal := values["SwapTotal"]
	swapFree := values["SwapFree"]
	if swapFree > swapTotal {
		swapFree = swapTotal
	}

	return model.MemoryInfo{
		Total:     bytesMetric(total, meminfoPath, timestamp),
		Used:      bytesMetric(total-available, meminfoPath, timestamp),
		Available: bytesMetric(available, meminfoPath, timestamp),
		SwapTotal: bytesMetric(swapTotal, meminfoPath, timestamp),
		SwapUsed:  bytesMetric(swapTotal-swapFree, meminfoPath, timestamp),
	}, nil
}

func (c *Collector) collectLoad(timestamp time.Time) (model.LoadInfo, *model.Warning) {
	data, err := c.fs.ReadFile(loadavgPath)
	if err != nil {
		return unavailableLoad(timestamp, sysfs.StatusForError(err), err.Error()), &model.Warning{
			Component: "load", Message: loadavgPath + ": " + err.Error(),
		}
	}
	values, err := ParseLoadavg(data)
	if err != nil {
		return unavailableLoad(timestamp, model.Unavailable, err.Error()), &model.Warning{
			Component: "load", Message: "parse " + loadavgPath + ": " + err.Error(),
		}
	}
	return model.LoadInfo{
		OneMinute:      model.AvailableMetric(values.OneMinute, "", loadavgPath, timestamp),
		FiveMinutes:    model.AvailableMetric(values.FiveMinutes, "", loadavgPath, timestamp),
		FifteenMinutes: model.AvailableMetric(values.FifteenMinutes, "", loadavgPath, timestamp),
		Runnable:       model.AvailableMetric(float64(values.Runnable), "tasks", loadavgPath, timestamp),
		Tasks:          model.AvailableMetric(float64(values.Tasks), "tasks", loadavgPath, timestamp),
	}, nil
}

func bytesMetric(value uint64, source string, timestamp time.Time) model.Metric {
	return model.AvailableMetric(float64(value)/(1024*1024), "MiB", source, timestamp)
}

func unavailableMemory(timestamp time.Time, status model.MetricStatus, message string) model.MemoryInfo {
	metric := func() model.Metric {
		return model.MissingMetric(status, "MiB", meminfoPath, message, timestamp)
	}
	return model.MemoryInfo{
		Total: metric(), Used: metric(), Available: metric(), SwapTotal: metric(), SwapUsed: metric(),
	}
}

func unavailableLoad(timestamp time.Time, status model.MetricStatus, message string) model.LoadInfo {
	load := func() model.Metric {
		return model.MissingMetric(status, "", loadavgPath, message, timestamp)
	}
	tasks := func() model.Metric {
		return model.MissingMetric(status, "tasks", loadavgPath, message, timestamp)
	}
	return model.LoadInfo{
		OneMinute: load(), FiveMinutes: load(), FifteenMinutes: load(),
		Runnable: tasks(), Tasks: tasks(),
	}
}

func sum(values map[string]uint64, keys ...string) uint64 {
	var total uint64
	for _, key := range keys {
		total += values[key]
	}
	return total
}
