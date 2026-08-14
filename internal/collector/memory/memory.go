package memory

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
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

type PressureValues struct {
	Avg10  float64
	Avg60  float64
	Avg300 float64
	Total  uint64
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

func ParsePressure(data []byte) (map[string]PressureValues, error) {
	result := make(map[string]PressureValues)
	for lineNumber, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] != "some" && fields[0] != "full" {
			return nil, fmt.Errorf("line %d: unknown pressure class %q", lineNumber+1, fields[0])
		}
		values := make(map[string]string, len(fields)-1)
		for _, field := range fields[1:] {
			key, value, ok := strings.Cut(field, "=")
			if !ok {
				return nil, fmt.Errorf("line %d: invalid field %q", lineNumber+1, field)
			}
			values[key] = value
		}
		var pressure PressureValues
		var err error
		if pressure.Avg10, err = parsePressureFloat(values, "avg10"); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNumber+1, err)
		}
		if pressure.Avg60, err = parsePressureFloat(values, "avg60"); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNumber+1, err)
		}
		if pressure.Avg300, err = parsePressureFloat(values, "avg300"); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNumber+1, err)
		}
		if pressure.Total, err = strconv.ParseUint(values["total"], 10, 64); err != nil {
			return nil, fmt.Errorf("line %d: total: %w", lineNumber+1, err)
		}
		result[fields[0]] = pressure
	}
	if _, ok := result["some"]; !ok {
		return nil, fmt.Errorf("some pressure line is missing")
	}
	return result, nil
}

func parsePressureFloat(values map[string]string, key string) (float64, error) {
	raw, ok := values[key]
	if !ok {
		return 0, fmt.Errorf("%s is missing", key)
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if value < 0 {
		return 0, fmt.Errorf("%s is negative", key)
	}
	return value, nil
}

type Collector struct {
	fs        sysfs.SystemFS
	zramOnce  sync.Once
	zramPaths []string
}

func NewCollector(systemFS sysfs.SystemFS) *Collector {
	return &Collector{fs: systemFS}
}

func (c *Collector) Collect() (model.MemoryInfo, model.LoadInfo, []model.Warning) {
	timestamp := time.Now()
	memoryInfo, memoryWarning := c.collectMemory(timestamp)
	loadInfo, loadWarning := c.collectLoad(timestamp)
	pressure, pressureWarnings := c.collectPressure(timestamp)
	zram, zramWarnings := c.collectZRAM(timestamp)
	memoryInfo.Pressure = pressure
	memoryInfo.ZRAM = zram
	var warnings []model.Warning
	if memoryWarning != nil {
		warnings = append(warnings, *memoryWarning)
	}
	if loadWarning != nil {
		warnings = append(warnings, *loadWarning)
	}
	warnings = append(warnings, pressureWarnings...)
	warnings = append(warnings, zramWarnings...)
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
	_, freeOK := values["MemFree"]
	buffers, buffersOK := values["Buffers"]
	cached, cachedOK := values["Cached"]
	cache := cached + values["SReclaimable"]
	bufferCache := buffers + cache

	available, availableOK := values["MemAvailable"]
	if !availableOK && freeOK && buffersOK && cachedOK {
		available = sum(values, "MemFree", "Buffers", "Cached", "SReclaimable")
		if shmem := values["Shmem"]; available >= shmem {
			available -= shmem
		}
		availableOK = true
	}
	if available > total {
		available = total
	}

	usedMetric := missingMeminfoMetric("MemAvailable and fallback fields are not exposed", timestamp)
	availableMetric := missingMeminfoMetric("MemAvailable and fallback fields are not exposed", timestamp)
	if availableOK {
		usedMetric = bytesMetric(total-available, meminfoPath, timestamp)
		availableMetric = bytesMetric(available, meminfoPath, timestamp)
	}
	cacheMetric := missingMeminfoMetric("Cached is not exposed", timestamp)
	if cachedOK {
		cacheMetric = bytesMetric(cache, meminfoPath, timestamp)
	}
	bufferCacheMetric := missingMeminfoMetric("Buffers or Cached is not exposed", timestamp)
	if buffersOK && cachedOK {
		bufferCacheMetric = bytesMetric(bufferCache, meminfoPath, timestamp)
	}

	swapTotal, swapTotalOK := values["SwapTotal"]
	swapFree, swapFreeOK := values["SwapFree"]
	if swapTotalOK && swapFreeOK && swapFree > swapTotal {
		swapFree = swapTotal
	}
	swapUsedMetric := missingMeminfoMetric("SwapTotal or SwapFree is not exposed", timestamp)
	if swapTotalOK && swapFreeOK {
		swapUsedMetric = bytesMetric(swapTotal-swapFree, meminfoPath, timestamp)
	}

	cmaTotalMetric := model.MissingMetric(model.Unsupported, "MiB", meminfoPath, "CmaTotal is not exposed", timestamp)
	cmaUsedMetric := model.MissingMetric(model.Unsupported, "MiB", meminfoPath, "CMA accounting is not exposed", timestamp)
	cmaFreeMetric := model.MissingMetric(model.Unsupported, "MiB", meminfoPath, "CmaFree is not exposed", timestamp)
	if cmaTotal, totalOK := values["CmaTotal"]; totalOK {
		cmaTotalMetric = bytesMetric(cmaTotal, meminfoPath, timestamp)
		if cmaFree, freeOK := values["CmaFree"]; freeOK {
			if cmaFree > cmaTotal {
				cmaFree = cmaTotal
			}
			cmaFreeMetric = bytesMetric(cmaFree, meminfoPath, timestamp)
			cmaUsedMetric = bytesMetric(cmaTotal-cmaFree, meminfoPath, timestamp)
		}
	}

	return model.MemoryInfo{
		Total:       bytesMetric(total, meminfoPath, timestamp),
		Used:        usedMetric,
		Free:        meminfoBytesMetric(values, "MemFree", timestamp),
		Shared:      meminfoBytesMetric(values, "Shmem", timestamp),
		Buffers:     meminfoBytesMetric(values, "Buffers", timestamp),
		Cache:       cacheMetric,
		BufferCache: bufferCacheMetric,
		Available:   availableMetric,
		SwapTotal:   meminfoBytesMetric(values, "SwapTotal", timestamp),
		SwapUsed:    swapUsedMetric,
		SwapFree:    metricOrMissing(swapFree, swapFreeOK, "SwapFree is not exposed", timestamp),
		SwapCached:  meminfoBytesMetric(values, "SwapCached", timestamp),
		CMATotal:    cmaTotalMetric,
		CMAUsed:     cmaUsedMetric,
		CMAFree:     cmaFreeMetric,
	}, nil
}

func (c *Collector) collectPressure(timestamp time.Time) (model.PressureInfo, []model.Warning) {
	resources := []struct {
		path   string
		target *model.PressureResource
	}{
		{path: "/proc/pressure/cpu"},
		{path: "/proc/pressure/memory"},
		{path: "/proc/pressure/io"},
	}
	var pressure model.PressureInfo
	resources[0].target = &pressure.CPU
	resources[1].target = &pressure.Memory
	resources[2].target = &pressure.IO
	var warnings []model.Warning
	for _, resource := range resources {
		data, err := c.fs.ReadFile(resource.path)
		if err != nil {
			status := sysfs.StatusForError(err)
			*resource.target = missingPressure(status, resource.path, err.Error(), timestamp)
			if status == model.PermissionDenied || status == model.Unavailable {
				warnings = append(warnings, model.Warning{Component: "pressure", Message: resource.path + ": " + err.Error()})
			}
			continue
		}
		values, err := ParsePressure(data)
		if err != nil {
			*resource.target = missingPressure(model.Unavailable, resource.path, err.Error(), timestamp)
			warnings = append(warnings, model.Warning{Component: "pressure", Message: resource.path + ": " + err.Error()})
			continue
		}
		resource.target.Some = pressureWindow(values["some"], resource.path, timestamp)
		if full, ok := values["full"]; ok {
			resource.target.Full = pressureWindow(full, resource.path, timestamp)
		} else {
			resource.target.Full = missingPressureWindow(model.Unsupported, resource.path, "full pressure line is not exposed", timestamp)
		}
	}
	return pressure, warnings
}

func pressureWindow(values PressureValues, source string, timestamp time.Time) model.PressureWindow {
	return model.PressureWindow{
		Avg10:  model.AvailableMetric(values.Avg10, "%", source, timestamp),
		Avg60:  model.AvailableMetric(values.Avg60, "%", source, timestamp),
		Avg300: model.AvailableMetric(values.Avg300, "%", source, timestamp),
		Total:  model.AvailableMetric(float64(values.Total), "µs", source, timestamp),
	}
}

func missingPressure(
	status model.MetricStatus, source, message string, timestamp time.Time,
) model.PressureResource {
	return model.PressureResource{
		Some: missingPressureWindow(status, source, message, timestamp),
		Full: missingPressureWindow(status, source, message, timestamp),
	}
}

func missingPressureWindow(
	status model.MetricStatus, source, message string, timestamp time.Time,
) model.PressureWindow {
	metric := func(unit string) model.Metric {
		return model.MissingMetric(status, unit, source, message, timestamp)
	}
	return model.PressureWindow{
		Avg10: metric("%"), Avg60: metric("%"), Avg300: metric("%"), Total: metric("µs"),
	}
}

func (c *Collector) collectZRAM(timestamp time.Time) ([]model.ZRAMDevice, []model.Warning) {
	c.zramOnce.Do(func() {
		c.zramPaths, _ = c.fs.Glob("/sys/block/zram*")
		sort.Strings(c.zramPaths)
	})
	devices := make([]model.ZRAMDevice, 0, len(c.zramPaths))
	var warnings []model.Warning
	for _, path := range c.zramPaths {
		device := model.ZRAMDevice{Name: filepath.Base(path)}
		device.DiskSize = c.readByteMetric(filepath.Join(path, "disksize"), timestamp)
		mmStatPath := filepath.Join(path, "mm_stat")
		data, err := c.fs.ReadFile(mmStatPath)
		if err != nil {
			status := sysfs.StatusForError(err)
			device.OriginalData = model.MissingMetric(status, "MiB", mmStatPath, err.Error(), timestamp)
			device.CompressedData = model.MissingMetric(status, "MiB", mmStatPath, err.Error(), timestamp)
			device.MemoryUsed = model.MissingMetric(status, "MiB", mmStatPath, err.Error(), timestamp)
			device.CompressionRatio = model.MissingMetric(status, "x", mmStatPath, err.Error(), timestamp)
			warnings = append(warnings, model.Warning{Component: "zram", Message: mmStatPath + ": " + err.Error()})
		} else {
			original, compressed, memoryUsed, parseErr := parseZRAMMMStat(data)
			if parseErr != nil {
				device.OriginalData = model.MissingMetric(model.Unavailable, "MiB", mmStatPath, parseErr.Error(), timestamp)
				device.CompressedData = model.MissingMetric(model.Unavailable, "MiB", mmStatPath, parseErr.Error(), timestamp)
				device.MemoryUsed = model.MissingMetric(model.Unavailable, "MiB", mmStatPath, parseErr.Error(), timestamp)
				device.CompressionRatio = model.MissingMetric(model.Unavailable, "x", mmStatPath, parseErr.Error(), timestamp)
				warnings = append(warnings, model.Warning{Component: "zram", Message: mmStatPath + ": " + parseErr.Error()})
			} else {
				device.OriginalData = rawBytesMetric(original, mmStatPath, timestamp)
				device.CompressedData = rawBytesMetric(compressed, mmStatPath, timestamp)
				device.MemoryUsed = rawBytesMetric(memoryUsed, mmStatPath, timestamp)
				ratio := 0.0
				if compressed > 0 {
					ratio = float64(original) / float64(compressed)
				}
				device.CompressionRatio = model.AvailableMetric(ratio, "x", mmStatPath, timestamp)
			}
		}
		device.Algorithm = c.readZRAMAlgorithm(filepath.Join(path, "comp_algorithm"), timestamp)
		devices = append(devices, device)
	}
	return devices, warnings
}

func parseZRAMMMStat(data []byte) (uint64, uint64, uint64, error) {
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, 0, 0, fmt.Errorf("expected at least 3 mm_stat fields")
	}
	values := make([]uint64, 3)
	for index := range values {
		value, err := strconv.ParseUint(fields[index], 10, 64)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("mm_stat field %d: %w", index+1, err)
		}
		values[index] = value
	}
	return values[0], values[1], values[2], nil
}

func (c *Collector) readByteMetric(path string, timestamp time.Time) model.Metric {
	data, err := c.fs.ReadFile(path)
	if err != nil {
		return model.MissingMetric(sysfs.StatusForError(err), "MiB", path, err.Error(), timestamp)
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return model.MissingMetric(model.Unavailable, "MiB", path, err.Error(), timestamp)
	}
	return rawBytesMetric(value, path, timestamp)
}

func rawBytesMetric(value uint64, source string, timestamp time.Time) model.Metric {
	return model.AvailableMetric(float64(value)/(1024*1024), "MiB", source, timestamp)
}

func (c *Collector) readZRAMAlgorithm(path string, timestamp time.Time) model.TextMetric {
	data, err := c.fs.ReadFile(path)
	if err != nil {
		return model.MissingTextMetric(sysfs.StatusForError(err), path, err.Error(), timestamp)
	}
	for _, field := range strings.Fields(string(data)) {
		if strings.HasPrefix(field, "[") && strings.HasSuffix(field, "]") {
			return model.AvailableTextMetric(strings.Trim(field, "[]"), path, timestamp)
		}
	}
	return model.MissingTextMetric(model.Unavailable, path, "active compression algorithm is not marked", timestamp)
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
	return rawBytesMetric(value, source, timestamp)
}

func meminfoBytesMetric(values map[string]uint64, key string, timestamp time.Time) model.Metric {
	value, ok := values[key]
	return metricOrMissing(value, ok, key+" is not exposed", timestamp)
}

func metricOrMissing(value uint64, ok bool, message string, timestamp time.Time) model.Metric {
	if !ok {
		return missingMeminfoMetric(message, timestamp)
	}
	return bytesMetric(value, meminfoPath, timestamp)
}

func missingMeminfoMetric(message string, timestamp time.Time) model.Metric {
	return model.MissingMetric(model.Unsupported, "MiB", meminfoPath, message, timestamp)
}

func unavailableMemory(timestamp time.Time, status model.MetricStatus, message string) model.MemoryInfo {
	metric := func() model.Metric {
		return model.MissingMetric(status, "MiB", meminfoPath, message, timestamp)
	}
	return model.MemoryInfo{
		Total: metric(), Used: metric(), Free: metric(), Shared: metric(), Buffers: metric(),
		Cache: metric(), BufferCache: metric(), Available: metric(), SwapTotal: metric(),
		SwapUsed: metric(), SwapFree: metric(), SwapCached: metric(), CMATotal: metric(),
		CMAUsed: metric(), CMAFree: metric(), ZRAM: []model.ZRAMDevice{},
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
