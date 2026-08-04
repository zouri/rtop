package power

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

type sensorSource struct {
	name    string
	chip    string
	kind    string
	path    string
	unit    string
	divisor float64
}

type fanSource struct {
	index      int
	name       string
	chip       string
	speedPath  string
	targetPath string
	pwmPath    string
	modePath   string
}

type Collector struct {
	fs            sysfs.SystemFS
	sensors       []sensorSource
	fans          []fanSource
	probeWarnings []model.Warning
	discoverOnce  sync.Once
}

func NewCollector(systemFS sysfs.SystemFS) *Collector {
	return &Collector{fs: systemFS}
}

func (c *Collector) Collect() ([]model.PowerSensor, []model.FanInfo, []model.Warning) {
	c.discoverOnce.Do(func() {
		c.sensors, c.fans, c.probeWarnings = discover(c.fs)
	})
	timestamp := time.Now()
	sensors := make([]model.PowerSensor, 0, len(c.sensors))
	warnings := append([]model.Warning(nil), c.probeWarnings...)
	for _, source := range c.sensors {
		metric, err := readScaledMetric(c.fs, source.path, source.unit, source.divisor, timestamp)
		sensors = append(sensors, model.PowerSensor{
			Name: source.name, Chip: source.chip, Kind: source.kind, Value: metric,
		})
		if err != nil {
			warnings = append(warnings, model.Warning{
				Component: "power", Message: source.path + ": " + err.Error(),
			})
		}
	}

	fans := make([]model.FanInfo, 0, len(c.fans))
	for _, source := range c.fans {
		fan := model.FanInfo{Name: source.name, Chip: source.chip}
		fan.Speed = readOptionalMetric(c.fs, source.speedPath, "RPM", 1, timestamp)
		fan.TargetSpeed = readOptionalMetric(c.fs, source.targetPath, "RPM", 1, timestamp)
		fan.PWM = readPWM(c.fs, source.pwmPath, timestamp)
		fan.ControlMode = readPWMMode(c.fs, source.modePath, timestamp)
		fans = append(fans, fan)
		for _, result := range []struct {
			path   string
			status model.MetricStatus
			err    string
		}{
			{path: source.speedPath, status: fan.Speed.Status, err: fan.Speed.Error},
			{path: source.targetPath, status: fan.TargetSpeed.Status, err: fan.TargetSpeed.Error},
			{path: source.pwmPath, status: fan.PWM.Status, err: fan.PWM.Error},
			{path: source.modePath, status: fan.ControlMode.Status, err: fan.ControlMode.Error},
		} {
			if result.path != "" && (result.status == model.PermissionDenied || result.status == model.Unavailable) {
				warnings = append(warnings, model.Warning{
					Component: "fan", Message: result.path + ": " + result.err,
				})
			}
		}
	}
	return sensors, fans, warnings
}

func discover(systemFS sysfs.SystemFS) ([]sensorSource, []fanSource, []model.Warning) {
	hwmonPaths, err := systemFS.Glob("/sys/class/hwmon/hwmon*")
	if err != nil {
		return []sensorSource{}, []fanSource{}, []model.Warning{{Component: "hwmon", Message: err.Error()}}
	}
	var sensors []sensorSource
	var fans []fanSource
	var warnings []model.Warning
	for _, path := range hwmonPaths {
		chip := filepath.Base(path)
		if data, readErr := systemFS.ReadFile(filepath.Join(path, "name")); readErr == nil {
			if value := strings.TrimSpace(string(data)); value != "" {
				chip = value
			}
		}
		discovered, sensorWarnings := discoverSensors(systemFS, path, chip)
		sensors = append(sensors, discovered...)
		warnings = append(warnings, sensorWarnings...)
		discoveredFans, fanWarnings := discoverFans(systemFS, path, chip)
		fans = append(fans, discoveredFans...)
		warnings = append(warnings, fanWarnings...)
	}
	sort.Slice(sensors, func(i, j int) bool {
		if sensors[i].chip != sensors[j].chip {
			return sensors[i].chip < sensors[j].chip
		}
		if sensors[i].kind != sensors[j].kind {
			return sensors[i].kind < sensors[j].kind
		}
		return sensors[i].name < sensors[j].name
	})
	sort.Slice(fans, func(i, j int) bool {
		if fans[i].chip != fans[j].chip {
			return fans[i].chip < fans[j].chip
		}
		return fans[i].index < fans[j].index
	})
	if sensors == nil {
		sensors = []sensorSource{}
	}
	if fans == nil {
		fans = []fanSource{}
	}
	return sensors, fans, warnings
}

func discoverSensors(
	systemFS sysfs.SystemFS, hwmonPath, chip string,
) ([]sensorSource, []model.Warning) {
	definitions := []struct {
		prefix  string
		kind    string
		unit    string
		divisor float64
		suffix  string
	}{
		{prefix: "in", kind: "voltage", unit: "V", divisor: 1000, suffix: "_input"},
		{prefix: "curr", kind: "current", unit: "A", divisor: 1000, suffix: "_input"},
		{prefix: "power", kind: "power", unit: "W", divisor: 1_000_000, suffix: "_input"},
		{prefix: "power", kind: "power", unit: "W", divisor: 1_000_000, suffix: "_average"},
		{prefix: "energy", kind: "energy", unit: "J", divisor: 1_000_000, suffix: "_input"},
	}
	seen := make(map[string]struct{})
	var sensors []sensorSource
	var warnings []model.Warning
	for _, definition := range definitions {
		paths, err := systemFS.Glob(filepath.Join(hwmonPath, definition.prefix+"[0-9]*"+definition.suffix))
		if err != nil {
			warnings = append(warnings, model.Warning{Component: "hwmon", Message: hwmonPath + ": " + err.Error()})
			continue
		}
		for _, path := range paths {
			index, ok := channelIndex(filepath.Base(path), definition.prefix, definition.suffix)
			if !ok {
				continue
			}
			key := definition.kind + ":" + strconv.Itoa(index)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			base := definition.prefix + strconv.Itoa(index)
			name := readLabel(systemFS, filepath.Join(hwmonPath, base+"_label"), base)
			sensors = append(sensors, sensorSource{
				name: name, chip: chip, kind: definition.kind, path: path,
				unit: definition.unit, divisor: definition.divisor,
			})
		}
	}
	return sensors, warnings
}

func discoverFans(systemFS sysfs.SystemFS, hwmonPath, chip string) ([]fanSource, []model.Warning) {
	sources := make(map[int]*fanSource)
	var warnings []model.Warning
	discover := func(pattern, prefix, suffix string, setPath func(*fanSource, string)) {
		paths, err := systemFS.Glob(filepath.Join(hwmonPath, pattern))
		if err != nil {
			warnings = append(warnings, model.Warning{Component: "hwmon", Message: hwmonPath + ": " + err.Error()})
			return
		}
		for _, path := range paths {
			index, ok := channelIndex(filepath.Base(path), prefix, suffix)
			if !ok {
				continue
			}
			source := sources[index]
			if source == nil {
				source = &fanSource{index: index, chip: chip}
				sources[index] = source
			}
			setPath(source, path)
		}
	}
	discover("fan[0-9]*_input", "fan", "_input", func(source *fanSource, path string) { source.speedPath = path })
	discover("fan[0-9]*_target", "fan", "_target", func(source *fanSource, path string) { source.targetPath = path })
	discover("pwm[0-9]*", "pwm", "", func(source *fanSource, path string) { source.pwmPath = path })

	indices := make([]int, 0, len(sources))
	for index := range sources {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	fans := make([]fanSource, 0, len(indices))
	for _, index := range indices {
		source := sources[index]
		base := "fan" + strconv.Itoa(index)
		source.name = readLabel(systemFS, filepath.Join(hwmonPath, base+"_label"), base)
		if source.pwmPath != "" {
			source.modePath = filepath.Join(hwmonPath, "pwm"+strconv.Itoa(index)+"_enable")
		}
		fans = append(fans, *source)
	}
	return fans, warnings
}

func channelIndex(filename, prefix, suffix string) (int, bool) {
	if !strings.HasPrefix(filename, prefix) || !strings.HasSuffix(filename, suffix) {
		return 0, false
	}
	value := strings.TrimSuffix(strings.TrimPrefix(filename, prefix), suffix)
	if value == "" {
		return 0, false
	}
	index, err := strconv.Atoi(value)
	return index, err == nil
}

func readLabel(systemFS sysfs.SystemFS, path, fallback string) string {
	data, err := systemFS.ReadFile(path)
	if err != nil {
		return fallback
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return fallback
	}
	return value
}

func readScaledMetric(
	systemFS sysfs.SystemFS, path, unit string, divisor float64, timestamp time.Time,
) (model.Metric, error) {
	data, err := systemFS.ReadFile(path)
	if err != nil {
		return model.MissingMetric(sysfs.StatusForError(err), unit, path, err.Error(), timestamp), err
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		wrapped := fmt.Errorf("invalid numeric value")
		if err != nil {
			wrapped = fmt.Errorf("invalid numeric value: %w", err)
		}
		return model.MissingMetric(model.Unavailable, unit, path, wrapped.Error(), timestamp), wrapped
	}
	return model.AvailableMetric(value/divisor, unit, path, timestamp), nil
}

func readOptionalMetric(
	systemFS sysfs.SystemFS, path, unit string, divisor float64, timestamp time.Time,
) model.Metric {
	if path == "" {
		return model.MissingMetric(model.Unsupported, unit, "", "source is not exposed", timestamp)
	}
	metric, _ := readScaledMetric(systemFS, path, unit, divisor, timestamp)
	return metric
}

func readPWM(systemFS sysfs.SystemFS, path string, timestamp time.Time) model.Metric {
	if path == "" {
		return model.MissingMetric(model.Unsupported, "%", "", "PWM control is not exposed", timestamp)
	}
	data, err := systemFS.ReadFile(path)
	if err != nil {
		return model.MissingMetric(sysfs.StatusForError(err), "%", path, err.Error(), timestamp)
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 255 {
		return model.MissingMetric(model.Unavailable, "%", path, "PWM value is outside 0..255", timestamp)
	}
	return model.AvailableMetric(value*100/255, "%", path, timestamp)
}

func readPWMMode(systemFS sysfs.SystemFS, path string, timestamp time.Time) model.TextMetric {
	if path == "" {
		return model.MissingTextMetric(model.Unsupported, "", "PWM control mode is not exposed", timestamp)
	}
	data, err := systemFS.ReadFile(path)
	if err != nil {
		return model.MissingTextMetric(sysfs.StatusForError(err), path, err.Error(), timestamp)
	}
	value, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return model.MissingTextMetric(model.Unavailable, path, err.Error(), timestamp)
	}
	mode := "mode-" + strconv.Itoa(value)
	switch value {
	case 0:
		mode = "disabled"
	case 1:
		mode = "manual"
	case 2:
		mode = "automatic"
	}
	return model.AvailableTextMetric(mode, path, timestamp)
}
