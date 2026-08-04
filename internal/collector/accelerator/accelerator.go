package accelerator

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

const (
	gpuType = "gpu"
	npuType = "npu"
	ddrType = "ddr"
)

var coreLoadPattern = regexp.MustCompile(`(?i)core\s*([0-9]+)\s*(?:load\s*)?[:=]\s*([0-9]+(?:\.[0-9]+)?)\s*%?`)

type device struct {
	path   string
	name   string
	driver string
	kind   string
}

type Collector struct {
	fs               sysfs.SystemFS
	devices          map[string]device
	expectedNPUCores int
	probeWarnings    []model.Warning
}

func NewCollector(systemFS sysfs.SystemFS) *Collector {
	devices, warnings := discover(systemFS)
	found := make(map[string]device, 3)
	for _, device := range devices {
		if device.kind == gpuType || device.kind == npuType || device.kind == ddrType {
			if _, exists := found[device.kind]; !exists {
				found[device.kind] = device
			}
		}
	}
	return &Collector{
		fs:               systemFS,
		devices:          found,
		expectedNPUCores: expectedCoreCount(systemFS),
		probeWarnings:    warnings,
	}
}

func (c *Collector) Collect() ([]model.Accelerator, []model.Warning) {
	timestamp := time.Now()
	gpu, hasGPU := c.devices[gpuType]
	npu, hasNPU := c.devices[npuType]
	ddr, hasDDR := c.devices[ddrType]

	gpuInfo, gpuWarnings := c.collectDevice(gpu, hasGPU, gpuType, "Mali GPU", timestamp)
	npuInfo, npuWarnings := c.collectDevice(npu, hasNPU, npuType, "RKNPU", timestamp)
	ddrInfo, ddrWarnings := c.collectDevice(ddr, hasDDR, ddrType, "DDR/DMC", timestamp)
	if hasNPU {
		cores, warning := c.readNPUCores(c.expectedNPUCores, timestamp)
		npuInfo.Cores = cores
		if warning != nil {
			npuWarnings = append(npuWarnings, *warning)
		}
	}

	warnings := append([]model.Warning{}, c.probeWarnings...)
	warnings = append(warnings, gpuWarnings...)
	warnings = append(warnings, npuWarnings...)
	warnings = append(warnings, ddrWarnings...)
	return []model.Accelerator{gpuInfo, npuInfo, ddrInfo}, warnings
}

func (c *Collector) Drivers() []model.DriverInfo {
	drivers := make([]model.DriverInfo, 0, len(c.devices))
	for _, kind := range []string{gpuType, npuType, ddrType} {
		if device, ok := c.devices[kind]; ok {
			drivers = append(drivers, driverInfo(c.fs, device))
		}
	}
	return drivers
}

func (c *Collector) collectDevice(device device, found bool, kind, name string, timestamp time.Time) (model.Accelerator, []model.Warning) {
	if !found {
		return missingAccelerator(kind, name, timestamp), nil
	}
	usage := readLoad(c.fs, device, timestamp)
	frequency := readFrequency(c.fs, device, timestamp)
	return model.Accelerator{
		Name: name, Type: kind, Usage: usage, Frequency: frequency,
	}, metricWarnings(kind, usage, frequency)
}

func discover(systemFS sysfs.SystemFS) ([]device, []model.Warning) {
	paths, err := systemFS.Glob("/sys/class/devfreq/*")
	if err != nil {
		return nil, []model.Warning{{Component: "devfreq", Message: err.Error()}}
	}
	devices := make([]device, 0, len(paths))
	for _, path := range paths {
		name := filepath.Base(path)
		if data, readErr := systemFS.ReadFile(filepath.Join(path, "name")); readErr == nil {
			if value := strings.TrimSpace(string(data)); value != "" {
				name = value
			}
		}
		driver := ""
		if target, readErr := systemFS.Readlink(filepath.Join(path, "device", "driver")); readErr == nil {
			driver = filepath.Base(target)
		}
		devices = append(devices, device{
			path: path, name: name, driver: driver, kind: classify(name, path, driver),
		})
	}
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].kind == devices[j].kind {
			return devices[i].name < devices[j].name
		}
		return devices[i].kind < devices[j].kind
	})
	return devices, nil
}

func classify(values ...string) string {
	value := strings.ToLower(strings.Join(values, " "))
	switch {
	case containsAny(value, "rknpu", ".npu", "/npu", " npu"):
		return npuType
	case containsAny(value, "mali", "panfrost", "panthor", ".gpu", "/gpu", " gpu"):
		return gpuType
	case containsAny(value, "rockchip-dmc", "/dmc", " dmc", "ddr", "dram"):
		return ddrType
	default:
		return "other"
	}
}

func readLoad(systemFS sysfs.SystemFS, device device, timestamp time.Time) model.Metric {
	candidates := []string{
		filepath.Join(device.path, "load"),
		filepath.Join(device.path, "utilization"),
		filepath.Join(device.path, "utilisation"),
	}
	var bestErr error
	var source string
	for _, path := range candidates {
		data, err := systemFS.ReadFile(path)
		if err != nil {
			bestErr = preferError(bestErr, err)
			if source == "" || sysfs.StatusForError(err) == model.PermissionDenied {
				source = path
			}
			continue
		}
		value, err := parseLoad(data)
		if err != nil {
			return model.MissingMetric(model.Unavailable, "%", path, err.Error(), timestamp)
		}
		return model.AvailableMetric(value, "%", path, timestamp)
	}

	busyPath := filepath.Join(device.path, "busy_time")
	totalPath := filepath.Join(device.path, "total_time")
	busyData, busyErr := systemFS.ReadFile(busyPath)
	totalData, totalErr := systemFS.ReadFile(totalPath)
	if busyErr == nil && totalErr == nil {
		busy, busyParseErr := parseNumber(busyData)
		total, totalParseErr := parseNumber(totalData)
		if busyParseErr != nil || totalParseErr != nil || total <= 0 || busy < 0 || busy > total {
			return model.MissingMetric(model.Unavailable, "%", busyPath, "invalid busy_time/total_time", timestamp)
		}
		return model.AvailableMetric(busy*100/total, "%", busyPath, timestamp)
	}
	bestErr = preferError(bestErr, busyErr)
	bestErr = preferError(bestErr, totalErr)
	if source == "" {
		source = candidates[0]
	}
	return missingForError(bestErr, "%", source, timestamp)
}

func readFrequency(systemFS sysfs.SystemFS, device device, timestamp time.Time) model.Metric {
	candidates := []string{
		filepath.Join(device.path, "cur_freq"),
		filepath.Join(device.path, "target_freq"),
	}
	var bestErr error
	for _, path := range candidates {
		data, err := systemFS.ReadFile(path)
		if err != nil {
			bestErr = preferError(bestErr, err)
			continue
		}
		value, err := parseNumber(data)
		if err != nil {
			return model.MissingMetric(model.Unavailable, "MHz", path, err.Error(), timestamp)
		}
		return model.AvailableMetric(value/1_000_000, "MHz", path, timestamp)
	}
	return missingForError(bestErr, "MHz", candidates[0], timestamp)
}

func parseLoad(data []byte) (float64, error) {
	value := strings.TrimSpace(string(data))
	if value == "" {
		return 0, errors.New("empty load value")
	}
	if before, _, found := strings.Cut(value, "@"); found {
		value = before
	}
	value = strings.TrimSpace(strings.TrimSuffix(value, "%"))
	fields := strings.Fields(value)
	if len(fields) == 2 {
		busy, busyErr := strconv.ParseFloat(fields[0], 64)
		total, totalErr := strconv.ParseFloat(fields[1], 64)
		if busyErr == nil && totalErr == nil && total > 0 && busy >= 0 && busy <= total {
			return busy * 100 / total, nil
		}
	}
	if len(fields) == 0 {
		return 0, errors.New("empty load value")
	}
	load, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid load %q: %w", value, err)
	}
	if load < 0 || load > 100 {
		return 0, fmt.Errorf("load %.2f is outside 0..100", load)
	}
	return load, nil
}

func driverInfo(systemFS sysfs.SystemFS, device device) model.DriverInfo {
	governor := ""
	if data, err := systemFS.ReadFile(filepath.Join(device.path, "governor")); err == nil {
		governor = strings.TrimSpace(string(data))
	}
	name := device.driver
	if name == "" {
		name = device.name
	}
	return model.DriverInfo{Type: device.kind, Name: name, Device: device.name, Governor: governor}
}

func missingAccelerator(kind, name string, timestamp time.Time) model.Accelerator {
	source := "/sys/class/devfreq"
	message := fmt.Sprintf("%s devfreq device was not discovered", kind)
	return model.Accelerator{
		Name:      name,
		Type:      kind,
		Usage:     model.MissingMetric(model.Unsupported, "%", source, message, timestamp),
		Frequency: model.MissingMetric(model.Unsupported, "MHz", source, message, timestamp),
	}
}

func metricWarnings(component string, metrics ...model.Metric) []model.Warning {
	var warnings []model.Warning
	for _, metric := range metrics {
		if metric.Status == model.PermissionDenied || metric.Status == model.Unavailable {
			warnings = append(warnings, model.Warning{
				Component: component,
				Message:   metric.Source + ": " + metric.Error,
			})
		}
	}
	return warnings
}

func (c *Collector) readNPUCores(expected int, timestamp time.Time) ([]model.AcceleratorCore, *model.Warning) {
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
		loads, parseErr := parseCoreLoad(data)
		if parseErr != nil {
			cores := missingCores(max(1, expected), model.Unavailable, path, parseErr.Error(), timestamp)
			return cores, &model.Warning{Component: npuType, Message: path + ": " + parseErr.Error()}
		}
		return availableCores(loads, expected, path, timestamp), nil
	}
	if bestErr == nil {
		bestErr = fs.ErrNotExist
	}
	status := sysfs.StatusForError(bestErr)
	cores := missingCores(max(1, expected), status, bestSource, bestErr.Error(), timestamp)
	if status == model.PermissionDenied || status == model.Unavailable {
		return cores, &model.Warning{Component: npuType, Message: bestSource + ": " + bestErr.Error()}
	}
	return cores, nil
}

func parseCoreLoad(data []byte) (map[int]float64, error) {
	matches := coreLoadPattern.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		return nil, errors.New("no per-core NPU load values found")
	}
	loads := make(map[int]float64, len(matches))
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
		loads[id] = value
	}
	return loads, nil
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
	cores := make([]model.AcceleratorCore, 0, count)
	for id := 0; id < count; id++ {
		value, ok := loads[id]
		metric := model.MissingMetric(model.Unavailable, "%", source, "core is missing from driver output", timestamp)
		if ok {
			metric = model.AvailableMetric(value, "%", source, timestamp)
		}
		cores = append(cores, model.AcceleratorCore{ID: id, Usage: metric})
	}
	return cores
}

func missingCores(count int, status model.MetricStatus, source, message string, timestamp time.Time) []model.AcceleratorCore {
	cores := make([]model.AcceleratorCore, 0, count)
	for id := 0; id < count; id++ {
		cores = append(cores, model.AcceleratorCore{
			ID: id, Usage: model.MissingMetric(status, "%", source, message, timestamp),
		})
	}
	return cores
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

func missingForError(err error, unit, source string, timestamp time.Time) model.Metric {
	if err == nil {
		err = fs.ErrNotExist
	}
	return model.MissingMetric(sysfs.StatusForError(err), unit, source, err.Error(), timestamp)
}

func preferError(current, candidate error) error {
	if candidate == nil {
		return current
	}
	if current == nil || sysfs.StatusForError(candidate) == model.PermissionDenied {
		return candidate
	}
	return current
}

func parseNumber(data []byte) (float64, error) {
	value := strings.TrimSpace(string(data))
	result, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q: %w", value, err)
	}
	return result, nil
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
