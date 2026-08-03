package devfreq

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

type Kind string

const (
	KindGPU   Kind = "gpu"
	KindNPU   Kind = "npu"
	KindDDR   Kind = "ddr"
	KindOther Kind = "other"
)

type Device struct {
	Path   string
	Name   string
	Driver string
	Kind   Kind
}

func Discover(systemFS sysfs.SystemFS) ([]Device, []model.Warning) {
	paths, err := systemFS.Glob("/sys/class/devfreq/*")
	if err != nil {
		return nil, []model.Warning{{Component: "devfreq", Message: err.Error()}}
	}
	devices := make([]Device, 0, len(paths))
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
		devices = append(devices, Device{
			Path: path, Name: name, Driver: driver, Kind: Classify(name, path, driver),
		})
	}
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].Kind == devices[j].Kind {
			return devices[i].Name < devices[j].Name
		}
		return devices[i].Kind < devices[j].Kind
	})
	return devices, nil
}

func Classify(values ...string) Kind {
	value := strings.ToLower(strings.Join(values, " "))
	switch {
	case containsAny(value, "rknpu", ".npu", "/npu", " npu"):
		return KindNPU
	case containsAny(value, "mali", "panfrost", "panthor", ".gpu", "/gpu", " gpu"):
		return KindGPU
	case containsAny(value, "rockchip-dmc", "/dmc", " dmc", "ddr", "dram"):
		return KindDDR
	default:
		return KindOther
	}
}

func Find(devices []Device, kind Kind) (Device, bool) {
	for _, device := range devices {
		if device.Kind == kind {
			return device, true
		}
	}
	return Device{}, false
}

func ReadLoad(systemFS sysfs.SystemFS, device Device, timestamp time.Time) model.Metric {
	candidates := []string{
		filepath.Join(device.Path, "load"),
		filepath.Join(device.Path, "utilization"),
		filepath.Join(device.Path, "utilisation"),
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
		value, err := ParseLoad(data)
		if err != nil {
			return model.MissingMetric(model.Unavailable, "%", path, err.Error(), timestamp)
		}
		return model.AvailableMetric(value, "%", path, timestamp)
	}

	busyPath := filepath.Join(device.Path, "busy_time")
	totalPath := filepath.Join(device.Path, "total_time")
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

func ReadFrequency(systemFS sysfs.SystemFS, device Device, timestamp time.Time) model.Metric {
	candidates := []string{
		filepath.Join(device.Path, "cur_freq"),
		filepath.Join(device.Path, "target_freq"),
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

func Info(systemFS sysfs.SystemFS, device Device) model.DriverInfo {
	governor := ""
	if data, err := systemFS.ReadFile(filepath.Join(device.Path, "governor")); err == nil {
		governor = strings.TrimSpace(string(data))
	}
	name := device.Driver
	if name == "" {
		name = device.Name
	}
	return model.DriverInfo{
		Type: string(device.Kind), Name: name, Device: device.Name, Governor: governor,
	}
}

func ParseLoad(data []byte) (float64, error) {
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

func Missing(kind Kind, name string, timestamp time.Time) model.Accelerator {
	source := "/sys/class/devfreq"
	message := fmt.Sprintf("%s devfreq device was not discovered", kind)
	return model.Accelerator{
		Name:      name,
		Type:      string(kind),
		Usage:     model.MissingMetric(model.Unsupported, "%", source, message, timestamp),
		Frequency: model.MissingMetric(model.Unsupported, "MHz", source, message, timestamp),
	}
}

func MetricWarning(component string, metric model.Metric) *model.Warning {
	if metric.Status != model.PermissionDenied && metric.Status != model.Unavailable {
		return nil
	}
	message := metric.Source + ": " + metric.Error
	return &model.Warning{Component: component, Message: message}
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
