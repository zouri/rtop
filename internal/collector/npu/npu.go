package npu

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"rtop/internal/collector/devfreq"
	"rtop/internal/model"
	"rtop/internal/sysfs"
)

var coreLoadPattern = regexp.MustCompile(`(?i)core\s*([0-9]+)\s*(?:load\s*)?[:=]\s*([0-9]+(?:\.[0-9]+)?)\s*%?`)

type Collector struct {
	fs sysfs.SystemFS

	mu            sync.RWMutex
	device        *devfreq.Device
	expectedCores int
}

func NewCollector(systemFS sysfs.SystemFS) *Collector {
	collector := &Collector{fs: systemFS}
	collector.Probe()
	return collector
}

func (c *Collector) Probe() []model.Warning {
	devices, warnings := devfreq.Discover(c.fs)
	device, ok := devfreq.Find(devices, devfreq.KindNPU)
	expectedCores := expectedCoreCount(c.fs)
	c.mu.Lock()
	if ok {
		c.device = &device
	} else {
		c.device = nil
	}
	c.expectedCores = expectedCores
	c.mu.Unlock()
	return warnings
}

func (c *Collector) Collect() (model.Accelerator, []model.Warning) {
	timestamp := time.Now()
	c.mu.RLock()
	device := c.device
	expectedCores := c.expectedCores
	c.mu.RUnlock()
	if device == nil {
		return devfreq.Missing(devfreq.KindNPU, "RKNPU", timestamp), nil
	}

	usage := devfreq.ReadLoad(c.fs, *device, timestamp)
	frequency := devfreq.ReadFrequency(c.fs, *device, timestamp)
	cores, coreWarning := c.readCores(expectedCores, timestamp)
	accelerator := model.Accelerator{
		Name: "RKNPU", Type: string(devfreq.KindNPU),
		Usage: usage, Frequency: frequency, Cores: cores,
	}
	var warnings []model.Warning
	for _, metric := range []model.Metric{usage, frequency} {
		if warning := devfreq.MetricWarning("npu", metric); warning != nil {
			warnings = append(warnings, *warning)
		}
	}
	if coreWarning != nil {
		warnings = append(warnings, *coreWarning)
	}
	return accelerator, warnings
}

func (c *Collector) Info() (model.DriverInfo, bool) {
	c.mu.RLock()
	if c.device == nil {
		c.mu.RUnlock()
		return model.DriverInfo{}, false
	}
	device := *c.device
	c.mu.RUnlock()
	return devfreq.Info(c.fs, device), true
}

func ParseCoreLoad(data []byte) (map[int]float64, error) {
	matches := coreLoadPattern.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		return nil, errors.New("no per-core NPU load values found")
	}
	result := make(map[int]float64, len(matches))
	for _, match := range matches {
		id, err := strconv.Atoi(match[1])
		if err != nil {
			return nil, fmt.Errorf("invalid core id %q: %w", match[1], err)
		}
		value, err := strconv.ParseFloat(match[2], 64)
		if err != nil {
			return nil, fmt.Errorf("invalid core %d load %q: %w", id, match[2], err)
		}
		if value < 0 || value > 100 {
			return nil, fmt.Errorf("core %d load %.2f is outside 0..100", id, value)
		}
		result[id] = value
	}
	return result, nil
}

func (c *Collector) readCores(expected int, timestamp time.Time) ([]model.AcceleratorCore, *model.Warning) {
	candidates := []string{
		"/sys/kernel/debug/rknpu/load",
		"/sys/kernel/debug/rknpu/load_info",
		"/sys/kernel/debug/rknpu/core_load",
	}
	var bestErr error
	bestSource := candidates[0]
	for _, path := range candidates {
		data, err := c.fs.ReadFile(path)
		if err != nil {
			if bestErr == nil ||
				(sysfs.StatusForError(bestErr) != model.PermissionDenied &&
					sysfs.StatusForError(err) == model.PermissionDenied) {
				bestErr, bestSource = err, path
			}
			continue
		}
		loads, parseErr := ParseCoreLoad(data)
		if parseErr != nil {
			metricCores := missingCores(max(1, expected), model.Unavailable, path, parseErr.Error(), timestamp)
			return metricCores, &model.Warning{Component: "npu", Message: path + ": " + parseErr.Error()}
		}
		return availableCores(loads, expected, path, timestamp), nil
	}
	if bestErr == nil {
		bestErr = fs.ErrNotExist
	}
	status := sysfs.StatusForError(bestErr)
	cores := missingCores(max(1, expected), status, bestSource, bestErr.Error(), timestamp)
	if status == model.PermissionDenied || status == model.Unavailable {
		return cores, &model.Warning{Component: "npu", Message: bestSource + ": " + bestErr.Error()}
	}
	return cores, nil
}

func availableCores(loads map[int]float64, expected int, source string, timestamp time.Time) []model.AcceleratorCore {
	ids := make([]int, 0, len(loads))
	for id := range loads {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	count := expected
	if len(ids) > 0 && ids[len(ids)-1]+1 > count {
		count = ids[len(ids)-1] + 1
	}
	result := make([]model.AcceleratorCore, 0, count)
	for id := 0; id < count; id++ {
		value, ok := loads[id]
		metric := model.MissingMetric(model.Unavailable, "%", source, "core is missing from driver output", timestamp)
		if ok {
			metric = model.AvailableMetric(value, "%", source, timestamp)
		}
		result = append(result, model.AcceleratorCore{ID: id, Usage: metric})
	}
	return result
}

func missingCores(count int, status model.MetricStatus, source, message string, timestamp time.Time) []model.AcceleratorCore {
	result := make([]model.AcceleratorCore, 0, count)
	for id := 0; id < count; id++ {
		result = append(result, model.AcceleratorCore{
			ID: id, Usage: model.MissingMetric(status, "%", source, message, timestamp),
		})
	}
	return result
}

func expectedCoreCount(systemFS sysfs.SystemFS) int {
	data, err := systemFS.ReadFile("/proc/device-tree/compatible")
	if err != nil {
		return 0
	}
	compatible := strings.ToLower(string(data))
	switch {
	case strings.Contains(compatible, "rk3588"):
		return 3
	case strings.Contains(compatible, "rk3576"):
		return 2
	case strings.Contains(compatible, "rk3566"), strings.Contains(compatible, "rk3568"):
		return 1
	default:
		return 0
	}
}
