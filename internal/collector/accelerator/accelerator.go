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
	"sync"
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

type metricSourceMode uint8

const (
	metricSourceMissing metricSourceMode = iota
	metricSourceSingle
	metricSourceRatio
)

type cachedMetricSource struct {
	mode      metricSourceMode
	primary   string
	secondary string
	missing   model.Metric
}

type cachedFrequencyRange struct {
	minimum model.Metric
	maximum model.Metric
}

type Collector struct {
	fs               sysfs.SystemFS
	devices          map[string]device
	expectedNPUCores int
	probeWarnings    []model.Warning
	loadSources      map[string]cachedMetricSource
	frequencySources map[string]cachedMetricSource
	frequencyRanges  map[string]cachedFrequencyRange
	mu               sync.Mutex
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
		loadSources:      make(map[string]cachedMetricSource, len(found)),
		frequencySources: make(map[string]cachedMetricSource, len(found)),
		frequencyRanges:  make(map[string]cachedFrequencyRange, len(found)),
	}
}

func (c *Collector) Collect() ([]model.Accelerator, []model.Warning) {
	c.mu.Lock()
	defer c.mu.Unlock()

	timestamp := time.Now()
	gpu, hasGPU := c.devices[gpuType]
	npu, hasNPU := c.devices[npuType]
	ddr, hasDDR := c.devices[ddrType]

	gpuInfo, gpuWarnings := c.collectDevice(gpu, hasGPU, gpuType, "Mali GPU", timestamp)
	npuInfo, npuWarnings := c.collectNPU(npu, hasNPU, timestamp)
	ddrInfo, ddrWarnings := c.collectDevice(ddr, hasDDR, ddrType, "DDR/DMC", timestamp)

	warnings := append([]model.Warning{}, c.probeWarnings...)
	warnings = append(warnings, gpuWarnings...)
	warnings = append(warnings, npuWarnings...)
	warnings = append(warnings, ddrWarnings...)
	return []model.Accelerator{gpuInfo, npuInfo, ddrInfo}, warnings
}

func (c *Collector) collectNPU(device device, found bool, timestamp time.Time) (model.Accelerator, []model.Warning) {
	if !found {
		return missingAccelerator(npuType, "RKNPU", timestamp), nil
	}

	runtimeSource := filepath.Join(device.path, "device", "power", "runtime_status")
	runtimeStatus := readTextMetric(c.fs, runtimeSource, timestamp)
	if runtimeStatus.Status == model.Available && runtimeStatus.Value != nil && strings.EqualFold(*runtimeStatus.Value, "suspended") {
		return model.Accelerator{
			Name:          "RKNPU",
			Type:          npuType,
			Usage:         model.AvailableMetric(0, "%", runtimeSource, timestamp),
			Frequency:     model.MissingMetric(model.Stale, "MHz", runtimeSource, "device is runtime suspended; devfreq frequency is cached", timestamp),
			Policy:        c.readFrequencyPolicy(device, timestamp),
			RuntimeStatus: runtimeStatus,
			Cores:         idleCores(max(1, c.expectedNPUCores), runtimeSource, timestamp),
		}, nil
	}

	info, warnings := c.collectDeviceWithRuntime(device, npuType, "RKNPU", runtimeStatus, timestamp)
	cores, coreWarning := c.readNPUCores(c.expectedNPUCores, timestamp)
	info.Cores = cores
	if aggregate, ok := aggregateCoreUsage(cores, timestamp); ok {
		info.Usage = aggregate
		warnings = metricWarnings(npuType, info.Usage, info.Frequency)
	}
	if coreWarning != nil {
		warnings = append(warnings, *coreWarning)
	}
	return info, warnings
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
	runtimeStatus := readTextMetric(c.fs, filepath.Join(device.path, "device", "power", "runtime_status"), timestamp)
	return c.collectDeviceWithRuntime(device, kind, name, runtimeStatus, timestamp)
}

func (c *Collector) collectDeviceWithRuntime(
	device device, kind, name string, runtimeStatus model.TextMetric, timestamp time.Time,
) (model.Accelerator, []model.Warning) {
	usage := c.readLoad(device, timestamp)
	frequency := c.readFrequency(device, timestamp)
	return model.Accelerator{
		Name: name, Type: kind, Usage: usage, Frequency: frequency,
		Policy: c.readFrequencyPolicy(device, timestamp), RuntimeStatus: runtimeStatus,
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
	metric, _ := probeLoad(systemFS, device, timestamp)
	return metric
}

func (c *Collector) readLoad(device device, timestamp time.Time) model.Metric {
	if source, ok := c.loadSources[device.path]; ok {
		return readCachedLoad(c.fs, source, timestamp)
	}
	metric, source := probeLoad(c.fs, device, timestamp)
	c.loadSources[device.path] = source
	return metric
}

func probeLoad(
	systemFS sysfs.SystemFS, device device, timestamp time.Time,
) (model.Metric, cachedMetricSource) {
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
			return model.MissingMetric(model.Unavailable, "%", path, err.Error(), timestamp), cachedMetricSource{
				mode: metricSourceSingle, primary: path,
			}
		}
		return model.AvailableMetric(value, "%", path, timestamp), cachedMetricSource{
			mode: metricSourceSingle, primary: path,
		}
	}

	busyPath := filepath.Join(device.path, "busy_time")
	totalPath := filepath.Join(device.path, "total_time")
	busyData, busyErr := systemFS.ReadFile(busyPath)
	totalData, totalErr := systemFS.ReadFile(totalPath)
	if busyErr == nil && totalErr == nil {
		busy, busyParseErr := parseNumber(busyData)
		total, totalParseErr := parseNumber(totalData)
		if busyParseErr != nil || totalParseErr != nil || total <= 0 || busy < 0 || busy > total {
			return model.MissingMetric(model.Unavailable, "%", busyPath, "invalid busy_time/total_time", timestamp), cachedMetricSource{
				mode: metricSourceRatio, primary: busyPath, secondary: totalPath,
			}
		}
		return model.AvailableMetric(busy*100/total, "%", busyPath, timestamp), cachedMetricSource{
			mode: metricSourceRatio, primary: busyPath, secondary: totalPath,
		}
	}
	bestErr = preferError(bestErr, busyErr)
	bestErr = preferError(bestErr, totalErr)
	if source == "" {
		source = candidates[0]
	}
	metric := missingForError(bestErr, "%", source, timestamp)
	return metric, cachedMetricSource{mode: metricSourceMissing, missing: metric}
}

func readCachedLoad(systemFS sysfs.SystemFS, source cachedMetricSource, timestamp time.Time) model.Metric {
	switch source.mode {
	case metricSourceSingle:
		data, err := systemFS.ReadFile(source.primary)
		if err != nil {
			return missingForError(err, "%", source.primary, timestamp)
		}
		value, err := parseLoad(data)
		if err != nil {
			return model.MissingMetric(model.Unavailable, "%", source.primary, err.Error(), timestamp)
		}
		return model.AvailableMetric(value, "%", source.primary, timestamp)
	case metricSourceRatio:
		busyData, busyErr := systemFS.ReadFile(source.primary)
		totalData, totalErr := systemFS.ReadFile(source.secondary)
		if busyErr != nil || totalErr != nil {
			return missingForError(preferError(busyErr, totalErr), "%", source.primary, timestamp)
		}
		busy, busyParseErr := parseNumber(busyData)
		total, totalParseErr := parseNumber(totalData)
		if busyParseErr != nil || totalParseErr != nil || total <= 0 || busy < 0 || busy > total {
			return model.MissingMetric(model.Unavailable, "%", source.primary, "invalid busy_time/total_time", timestamp)
		}
		return model.AvailableMetric(busy*100/total, "%", source.primary, timestamp)
	default:
		return metricAt(source.missing, timestamp)
	}
}

func readFrequency(systemFS sysfs.SystemFS, device device, timestamp time.Time) model.Metric {
	metric, _ := probeFrequency(systemFS, device, timestamp)
	return metric
}

func (c *Collector) readFrequency(device device, timestamp time.Time) model.Metric {
	if source, ok := c.frequencySources[device.path]; ok {
		return readCachedFrequency(c.fs, source, timestamp)
	}
	metric, source := probeFrequency(c.fs, device, timestamp)
	c.frequencySources[device.path] = source
	return metric
}

func probeFrequency(
	systemFS sysfs.SystemFS, device device, timestamp time.Time,
) (model.Metric, cachedMetricSource) {
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
			return model.MissingMetric(model.Unavailable, "MHz", path, err.Error(), timestamp), cachedMetricSource{
				mode: metricSourceSingle, primary: path,
			}
		}
		return model.AvailableMetric(value/1_000_000, "MHz", path, timestamp), cachedMetricSource{
			mode: metricSourceSingle, primary: path,
		}
	}
	metric := missingForError(bestErr, "MHz", candidates[0], timestamp)
	return metric, cachedMetricSource{mode: metricSourceMissing, missing: metric}
}

func readCachedFrequency(systemFS sysfs.SystemFS, source cachedMetricSource, timestamp time.Time) model.Metric {
	if source.mode == metricSourceMissing {
		return metricAt(source.missing, timestamp)
	}
	data, err := systemFS.ReadFile(source.primary)
	if err != nil {
		return missingForError(err, "MHz", source.primary, timestamp)
	}
	value, err := parseNumber(data)
	if err != nil {
		return model.MissingMetric(model.Unavailable, "MHz", source.primary, err.Error(), timestamp)
	}
	return model.AvailableMetric(value/1_000_000, "MHz", source.primary, timestamp)
}

func readFrequencyPolicy(systemFS sysfs.SystemFS, device device, timestamp time.Time) *model.FrequencyPolicy {
	hardwareMinimum, hardwareMaximum := readAvailableFrequencyRange(systemFS, device, timestamp)
	return &model.FrequencyPolicy{
		Minimum:         readFrequencyPath(systemFS, filepath.Join(device.path, "min_freq"), timestamp),
		Maximum:         readFrequencyPath(systemFS, filepath.Join(device.path, "max_freq"), timestamp),
		HardwareMinimum: hardwareMinimum,
		HardwareMaximum: hardwareMaximum,
		Governor:        readTextMetric(systemFS, filepath.Join(device.path, "governor"), timestamp),
	}
}

func (c *Collector) readFrequencyPolicy(device device, timestamp time.Time) *model.FrequencyPolicy {
	hardware, ok := c.frequencyRanges[device.path]
	if !ok {
		hardware.minimum, hardware.maximum = readAvailableFrequencyRange(c.fs, device, timestamp)
		c.frequencyRanges[device.path] = hardware
	}
	return &model.FrequencyPolicy{
		Minimum:         readFrequencyPath(c.fs, filepath.Join(device.path, "min_freq"), timestamp),
		Maximum:         readFrequencyPath(c.fs, filepath.Join(device.path, "max_freq"), timestamp),
		HardwareMinimum: metricAt(hardware.minimum, timestamp),
		HardwareMaximum: metricAt(hardware.maximum, timestamp),
		Governor:        readTextMetric(c.fs, filepath.Join(device.path, "governor"), timestamp),
	}
}

func metricAt(metric model.Metric, timestamp time.Time) model.Metric {
	metric.Timestamp = timestamp
	return metric
}

func readFrequencyPath(systemFS sysfs.SystemFS, path string, timestamp time.Time) model.Metric {
	data, err := systemFS.ReadFile(path)
	if err != nil {
		return model.MissingMetric(sysfs.StatusForError(err), "MHz", path, err.Error(), timestamp)
	}
	value, err := parseNumber(data)
	if err != nil {
		return model.MissingMetric(model.Unavailable, "MHz", path, err.Error(), timestamp)
	}
	return model.AvailableMetric(value/1_000_000, "MHz", path, timestamp)
}

func readAvailableFrequencyRange(
	systemFS sysfs.SystemFS, device device, timestamp time.Time,
) (model.Metric, model.Metric) {
	path := filepath.Join(device.path, "available_frequencies")
	data, err := systemFS.ReadFile(path)
	if err != nil {
		status := sysfs.StatusForError(err)
		return model.MissingMetric(status, "MHz", path, err.Error(), timestamp),
			model.MissingMetric(status, "MHz", path, err.Error(), timestamp)
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		message := "available_frequencies is empty"
		return model.MissingMetric(model.Unavailable, "MHz", path, message, timestamp),
			model.MissingMetric(model.Unavailable, "MHz", path, message, timestamp)
	}
	minimum, maximum := 0.0, 0.0
	for index, field := range fields {
		value, parseErr := strconv.ParseFloat(field, 64)
		if parseErr != nil || value <= 0 {
			message := fmt.Sprintf("invalid available frequency %q", field)
			return model.MissingMetric(model.Unavailable, "MHz", path, message, timestamp),
				model.MissingMetric(model.Unavailable, "MHz", path, message, timestamp)
		}
		if index == 0 || value < minimum {
			minimum = value
		}
		if index == 0 || value > maximum {
			maximum = value
		}
	}
	return model.AvailableMetric(minimum/1_000_000, "MHz", path, timestamp),
		model.AvailableMetric(maximum/1_000_000, "MHz", path, timestamp)
}

func readText(systemFS sysfs.SystemFS, path string) (string, error) {
	data, err := systemFS.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func readTextMetric(systemFS sysfs.SystemFS, path string, timestamp time.Time) model.TextMetric {
	value, err := readText(systemFS, path)
	if err != nil {
		return model.MissingTextMetric(sysfs.StatusForError(err), path, err.Error(), timestamp)
	}
	if value == "" {
		return model.MissingTextMetric(model.Unavailable, path, "empty text value", timestamp)
	}
	return model.AvailableTextMetric(value, path, timestamp)
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
		Name:          name,
		Type:          kind,
		Usage:         model.MissingMetric(model.Unsupported, "%", source, message, timestamp),
		Frequency:     model.MissingMetric(model.Unsupported, "MHz", source, message, timestamp),
		RuntimeStatus: model.MissingTextMetric(model.Unsupported, source, message, timestamp),
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

func idleCores(count int, source string, timestamp time.Time) []model.AcceleratorCore {
	cores := make([]model.AcceleratorCore, count)
	for id := range cores {
		cores[id] = model.AcceleratorCore{
			ID: id, Usage: model.AvailableMetric(0, "%", source, timestamp),
		}
	}
	return cores
}

func aggregateCoreUsage(cores []model.AcceleratorCore, timestamp time.Time) (model.Metric, bool) {
	if len(cores) == 0 {
		return model.Metric{}, false
	}
	var total float64
	source := ""
	for _, core := range cores {
		if core.Usage.Status != model.Available || core.Usage.Value == nil {
			return model.Metric{}, false
		}
		total += *core.Usage.Value
		if source == "" {
			source = core.Usage.Source
		}
	}
	return model.AvailableMetric(total/float64(len(cores)), "%", source, timestamp), true
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
