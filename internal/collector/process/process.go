package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"rtop/internal/collector/cpu"
	"rtop/internal/model"
	"rtop/internal/sysfs"
)

const (
	procStatPath = "/proc/stat"
	procGlob     = "/proc/[0-9]*"
)

type Stat struct {
	PID       int
	Name      string
	Ticks     uint64
	Threads   int
	StartTime uint64
	RSSPages  int64
}

// ParseStat parses the fields used by rtop from /proc/PID/stat. The command
// name is isolated using the outer parentheses because it may contain spaces
// and closing parentheses.
func ParseStat(data []byte) (Stat, error) {
	line := strings.TrimSpace(string(data))
	open := strings.IndexByte(line, '(')
	close := strings.LastIndexByte(line, ')')
	if open <= 0 || close <= open {
		return Stat{}, errors.New("invalid comm field")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line[:open]))
	if err != nil || pid <= 0 {
		return Stat{}, fmt.Errorf("invalid pid %q", strings.TrimSpace(line[:open]))
	}
	fields := strings.Fields(strings.TrimSpace(line[close+1:]))
	if len(fields) < 22 {
		return Stat{}, fmt.Errorf("expected at least 22 fields after comm, got %d", len(fields))
	}
	utime, err := parseUintField(fields, 11, "utime")
	if err != nil {
		return Stat{}, err
	}
	stime, err := parseUintField(fields, 12, "stime")
	if err != nil {
		return Stat{}, err
	}
	if ^uint64(0)-utime < stime {
		return Stat{}, errors.New("utime + stime overflows")
	}
	threads64, err := strconv.ParseInt(fields[17], 10, 32)
	if err != nil || threads64 < 0 {
		return Stat{}, fmt.Errorf("invalid num_threads %q", fields[17])
	}
	startTime, err := parseUintField(fields, 19, "starttime")
	if err != nil {
		return Stat{}, err
	}
	rssPages, err := strconv.ParseInt(fields[21], 10, 64)
	if err != nil {
		return Stat{}, fmt.Errorf("invalid rss %q: %w", fields[21], err)
	}
	return Stat{
		PID:       pid,
		Name:      line[open+1 : close],
		Ticks:     utime + stime,
		Threads:   int(threads64),
		StartTime: startTime,
		RSSPages:  rssPages,
	}, nil
}

func parseUintField(fields []string, index int, name string) (uint64, error) {
	value, err := strconv.ParseUint(fields[index], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", name, fields[index], err)
	}
	return value, nil
}

type sample struct {
	stat         Stat
	rssBytes     uint64
	statPath     string
	memorySource string
}

type capture struct {
	totalTicks uint64
	cores      int
	processes  map[int]sample
	timestamp  time.Time
}

type Collector struct {
	fs             sysfs.SystemFS
	sampleDuration time.Duration
	pageSize       uint64

	mu       sync.Mutex
	previous *capture
}

func NewCollector(systemFS sysfs.SystemFS, sampleDuration time.Duration) *Collector {
	if sampleDuration <= 0 {
		sampleDuration = 200 * time.Millisecond
	}
	return &Collector{
		fs:             systemFS,
		sampleDuration: sampleDuration,
		pageSize:       uint64(os.Getpagesize()),
	}
}

// Reset discards the delta baseline so the next collection measures current
// process activity over the configured initial sampling window.
func (c *Collector) Reset() {
	c.mu.Lock()
	c.previous = nil
	c.mu.Unlock()
}

func (c *Collector) Collect(ctx context.Context) ([]model.ProcessInfo, []model.Warning) {
	c.mu.Lock()
	defer c.mu.Unlock()

	current, err := c.capture()
	if err != nil {
		return []model.ProcessInfo{}, []model.Warning{{
			Component: "process",
			Message:   err.Error(),
		}}
	}
	previous := c.previous
	if previous == nil {
		timer := time.NewTimer(c.sampleDuration)
		select {
		case <-ctx.Done():
			timer.Stop()
			c.previous = current
			return c.build(current, nil), []model.Warning{{
				Component: "process",
				Message:   ctx.Err().Error(),
			}}
		case <-timer.C:
		}
		previous = current
		current, err = c.capture()
		if err != nil {
			c.previous = previous
			return c.build(previous, nil), []model.Warning{{
				Component: "process",
				Message:   err.Error(),
			}}
		}
	}
	c.previous = current
	return c.build(current, previous), nil
}

func (c *Collector) capture() (*capture, error) {
	statData, err := c.fs.ReadFile(procStatPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", procStatPath, err)
	}
	times, err := cpu.ParseStat(statData)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", procStatPath, err)
	}
	paths, err := c.fs.Glob(procGlob)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", procGlob, err)
	}
	result := &capture{
		totalTicks: times["cpu"].Total(),
		cores:      len(times) - 1,
		processes:  make(map[int]sample),
		timestamp:  time.Now(),
	}
	if result.cores < 1 {
		result.cores = 1
	}
	for _, path := range paths {
		statPath := path + "/stat"
		data, readErr := c.fs.ReadFile(statPath)
		if readErr != nil {
			continue
		}
		processStat, parseErr := ParseStat(data)
		if parseErr != nil {
			continue
		}
		var rssBytes uint64
		if processStat.RSSPages > 0 {
			pages := uint64(processStat.RSSPages)
			if pages <= ^uint64(0)/c.pageSize {
				rssBytes = pages * c.pageSize
			}
		}
		result.processes[processStat.PID] = sample{
			stat: processStat, rssBytes: rssBytes, statPath: statPath, memorySource: statPath,
		}
	}
	return result, nil
}

func (c *Collector) build(current, previous *capture) []model.ProcessInfo {
	result := make([]model.ProcessInfo, 0, len(current.processes))
	for pid, currentProcess := range current.processes {
		cpuMetric := model.MissingMetric(
			model.Unavailable, "%", currentProcess.statPath, "previous sample unavailable", current.timestamp,
		)
		if previous != nil {
			if previousProcess, ok := previous.processes[pid]; ok &&
				previousProcess.stat.StartTime == currentProcess.stat.StartTime {
				cpuMetric = processCPU(previousProcess, currentProcess, previous, current)
			}
		}
		memoryMetric := model.AvailableMetric(
			float64(currentProcess.rssBytes)/(1024*1024), "MiB", currentProcess.memorySource, current.timestamp,
		)
		result = append(result, model.ProcessInfo{
			PID: currentProcess.stat.PID, Name: currentProcess.stat.Name,
			CPU: cpuMetric, Memory: memoryMetric, Threads: currentProcess.stat.Threads,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].PID < result[j].PID
	})
	return result
}

func processCPU(previousProcess, currentProcess sample, previous, current *capture) model.Metric {
	if current.totalTicks <= previous.totalTicks || currentProcess.stat.Ticks < previousProcess.stat.Ticks {
		return model.MissingMetric(
			model.Unavailable, "%", currentProcess.statPath, "process counters did not advance", current.timestamp,
		)
	}
	totalDelta := current.totalTicks - previous.totalTicks
	processDelta := currentProcess.stat.Ticks - previousProcess.stat.Ticks
	value := float64(processDelta) * float64(current.cores) * 100 / float64(totalDelta)
	return model.AvailableMetric(value, "%", currentProcess.statPath, current.timestamp)
}
