package cpu

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

const procStatPath = "/proc/stat"

type Times struct {
	User      uint64
	Nice      uint64
	System    uint64
	Idle      uint64
	IOWait    uint64
	IRQ       uint64
	SoftIRQ   uint64
	Steal     uint64
	Guest     uint64
	GuestNice uint64
}

func (t Times) total() uint64 {
	return t.User + t.Nice + t.System + t.Idle + t.IOWait + t.IRQ + t.SoftIRQ + t.Steal
}

// Total returns the aggregate scheduler ticks represented by this sample.
func (t Times) Total() uint64 {
	return t.total()
}

func (t Times) idle() uint64 {
	return t.Idle + t.IOWait
}

func ParseStat(data []byte) (map[string]Times, error) {
	result := make(map[string]Times)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || (fields[0] != "cpu" && !isCPUCore(fields[0])) {
			continue
		}
		if len(fields) < 5 {
			return nil, fmt.Errorf("%s: expected at least 4 counters", fields[0])
		}
		values := make([]uint64, 10)
		for i := 1; i < len(fields) && i <= 10; i++ {
			value, err := strconv.ParseUint(fields[i], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%s counter %d: %w", fields[0], i, err)
			}
			values[i-1] = value
		}
		result[fields[0]] = Times{
			User: values[0], Nice: values[1], System: values[2], Idle: values[3],
			IOWait: values[4], IRQ: values[5], SoftIRQ: values[6], Steal: values[7],
			Guest: values[8], GuestNice: values[9],
		}
	}
	if _, ok := result["cpu"]; !ok {
		return nil, errors.New("aggregate cpu line not found")
	}
	return result, nil
}

func Usage(previous, current Times) (float64, error) {
	previousTotal, currentTotal := previous.total(), current.total()
	previousIdle, currentIdle := previous.idle(), current.idle()
	if currentTotal < previousTotal || currentIdle < previousIdle {
		return 0, errors.New("cpu counters moved backwards")
	}
	totalDelta := currentTotal - previousTotal
	if totalDelta == 0 {
		return 0, errors.New("cpu counters did not advance")
	}
	idleDelta := currentIdle - previousIdle
	if idleDelta > totalDelta {
		return 0, errors.New("idle delta exceeds total delta")
	}
	return float64(totalDelta-idleDelta) * 100 / float64(totalDelta), nil
}

type Collector struct {
	fs             sysfs.SystemFS
	sampleDuration time.Duration
}

func NewCollector(systemFS sysfs.SystemFS, sampleDuration time.Duration) *Collector {
	if sampleDuration <= 0 {
		sampleDuration = 200 * time.Millisecond
	}
	return &Collector{fs: systemFS, sampleDuration: sampleDuration}
}

func (c *Collector) Collect(ctx context.Context) (model.CPUInfo, []model.Warning) {
	started := time.Now()
	firstData, err := c.fs.ReadFile(procStatPath)
	if err != nil {
		return c.unavailableCPU(started, err), []model.Warning{{
			Component: "cpu",
			Message:   procStatPath + ": " + err.Error(),
		}}
	}
	first, err := ParseStat(firstData)
	if err != nil {
		return c.unavailableCPU(started, err), []model.Warning{{
			Component: "cpu",
			Message:   "parse " + procStatPath + ": " + err.Error(),
		}}
	}

	timer := time.NewTimer(c.sampleDuration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return c.unavailableCPU(time.Now(), ctx.Err()), []model.Warning{{
			Component: "cpu",
			Message:   ctx.Err().Error(),
		}}
	case <-timer.C:
	}

	secondData, err := c.fs.ReadFile(procStatPath)
	if err != nil {
		return c.unavailableCPU(time.Now(), err), []model.Warning{{
			Component: "cpu",
			Message:   procStatPath + ": " + err.Error(),
		}}
	}
	second, err := ParseStat(secondData)
	if err != nil {
		return c.unavailableCPU(time.Now(), err), []model.Warning{{
			Component: "cpu",
			Message:   "parse " + procStatPath + ": " + err.Error(),
		}}
	}

	timestamp := time.Now()
	info := model.CPUInfo{
		Overall: usageMetric(first["cpu"], second["cpu"], timestamp),
	}
	frequencies, clusters := c.readFrequencies(timestamp)
	coreNames := make([]string, 0, len(second))
	for name := range second {
		if isCPUCore(name) {
			coreNames = append(coreNames, name)
		}
	}
	sort.Slice(coreNames, func(i, j int) bool {
		return coreID(coreNames[i]) < coreID(coreNames[j])
	})
	for _, name := range coreNames {
		id := coreID(name)
		usage := model.MissingMetric(model.Unavailable, "%", procStatPath, "previous sample unavailable", timestamp)
		if previous, ok := first[name]; ok {
			usage = usageMetric(previous, second[name], timestamp)
		}
		frequency, ok := frequencies[id]
		if !ok {
			frequency = model.MissingMetric(model.Unsupported, "MHz", cpuFreqBase(id), "cpufreq is not exposed", timestamp)
		}
		info.Cores = append(info.Cores, model.CPUCore{
			ID: id, Cluster: clusters[id], Usage: usage, Frequency: frequency,
		})
	}
	return info, nil
}

func (c *Collector) unavailableCPU(timestamp time.Time, err error) model.CPUInfo {
	status := sysfs.StatusForError(err)
	if status == model.Available {
		status = model.Unavailable
	}
	return model.CPUInfo{
		Overall: model.MissingMetric(status, "%", procStatPath, err.Error(), timestamp),
	}
}

func (c *Collector) readFrequencies(timestamp time.Time) (map[int]model.Metric, map[int]string) {
	result := make(map[int]model.Metric)
	maximums := make(map[int]float64)
	paths, _ := c.fs.Glob("/sys/devices/system/cpu/cpu[0-9]*/cpufreq/scaling_cur_freq")
	fallback, _ := c.fs.Glob("/sys/devices/system/cpu/cpu[0-9]*/cpufreq/cpuinfo_cur_freq")
	for _, path := range fallback {
		id, ok := coreIDFromPath(path)
		if ok {
			if _, exists := result[id]; !exists {
				result[id] = c.readFrequency(path, timestamp)
			}
		}
	}
	for _, path := range paths {
		id, ok := coreIDFromPath(path)
		if ok {
			result[id] = c.readFrequency(path, timestamp)
		}
	}
	maxPaths, _ := c.fs.Glob("/sys/devices/system/cpu/cpu[0-9]*/cpufreq/cpuinfo_max_freq")
	for _, path := range maxPaths {
		id, ok := coreIDFromPath(path)
		if !ok {
			continue
		}
		data, err := c.fs.ReadFile(path)
		if err != nil {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
		if err == nil {
			maximums[id] = value
		}
	}
	return result, classifyClusters(maximums)
}

func (c *Collector) readFrequency(path string, timestamp time.Time) model.Metric {
	data, err := c.fs.ReadFile(path)
	if err != nil {
		return model.MissingMetric(sysfs.StatusForError(err), "MHz", path, err.Error(), timestamp)
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return model.MissingMetric(model.Unavailable, "MHz", path, err.Error(), timestamp)
	}
	return model.AvailableMetric(value/1000, "MHz", path, timestamp)
}

func usageMetric(previous, current Times, timestamp time.Time) model.Metric {
	value, err := Usage(previous, current)
	if err != nil {
		return model.MissingMetric(model.Unavailable, "%", procStatPath, err.Error(), timestamp)
	}
	return model.AvailableMetric(value, "%", procStatPath, timestamp)
}

func isCPUCore(name string) bool {
	if !strings.HasPrefix(name, "cpu") || len(name) == 3 {
		return false
	}
	_, err := strconv.Atoi(name[3:])
	return err == nil
}

func coreID(name string) int {
	id, _ := strconv.Atoi(strings.TrimPrefix(name, "cpu"))
	return id
}

func coreIDFromPath(path string) (int, bool) {
	for _, part := range strings.Split(path, "/") {
		if isCPUCore(part) {
			return coreID(part), true
		}
	}
	return 0, false
}

func cpuFreqBase(id int) string {
	return fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq", id)
}

func classifyClusters(maximums map[int]float64) map[int]string {
	uniqueMap := make(map[float64]struct{})
	for _, value := range maximums {
		uniqueMap[value] = struct{}{}
	}
	unique := make([]float64, 0, len(uniqueMap))
	for value := range uniqueMap {
		unique = append(unique, value)
	}
	sort.Float64s(unique)
	labels := make(map[float64]string)
	switch len(unique) {
	case 1:
		labels[unique[0]] = "single"
	case 2:
		labels[unique[0]], labels[unique[1]] = "little", "big"
	case 3:
		labels[unique[0]], labels[unique[1]], labels[unique[2]] = "little", "middle", "big"
	default:
		for index, value := range unique {
			labels[value] = fmt.Sprintf("cluster-%d", index+1)
		}
	}
	result := make(map[int]string)
	for id, value := range maximums {
		result[id] = labels[value]
	}
	return result
}
