package thermal

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

type Collector struct {
	fs sysfs.SystemFS
}

func NewCollector(systemFS sysfs.SystemFS) *Collector {
	return &Collector{fs: systemFS}
}

func (c *Collector) Collect() ([]model.ThermalZone, []model.Warning) {
	timestamp := time.Now()
	var zones []model.ThermalZone
	var warnings []model.Warning

	thermalPaths, err := c.fs.Glob("/sys/class/thermal/thermal_zone*")
	if err != nil {
		warnings = append(warnings, model.Warning{Component: "thermal", Message: err.Error()})
	}
	for _, path := range thermalPaths {
		zone, warning := c.readThermalZone(path, timestamp)
		zones = append(zones, zone)
		if warning != nil {
			warnings = append(warnings, *warning)
		}
	}

	hwmonPaths, err := c.fs.Glob("/sys/class/hwmon/hwmon*")
	if err != nil {
		warnings = append(warnings, model.Warning{Component: "hwmon", Message: err.Error()})
	}
	for _, path := range hwmonPaths {
		found, foundWarnings := c.readHWMon(path, timestamp)
		zones = append(zones, found...)
		warnings = append(warnings, foundWarnings...)
	}

	sort.Slice(zones, func(i, j int) bool {
		if zones[i].Type == zones[j].Type {
			return zones[i].Name < zones[j].Name
		}
		return zones[i].Type < zones[j].Type
	})
	if zones == nil {
		zones = []model.ThermalZone{}
	}
	return zones, warnings
}

func (c *Collector) readThermalZone(path string, timestamp time.Time) (model.ThermalZone, *model.Warning) {
	zoneType := filepath.Base(path)
	if data, err := c.fs.ReadFile(filepath.Join(path, "type")); err == nil {
		if value := strings.TrimSpace(string(data)); value != "" {
			zoneType = value
		}
	}
	temperature, err := c.readTemperature(filepath.Join(path, "temp"), timestamp)
	zone := model.ThermalZone{Name: filepath.Base(path), Type: zoneType, Temperature: temperature}
	if err != nil {
		return zone, &model.Warning{Component: "thermal", Message: path + ": " + err.Error()}
	}
	return zone, nil
}

func (c *Collector) readHWMon(path string, timestamp time.Time) ([]model.ThermalZone, []model.Warning) {
	chipName := filepath.Base(path)
	if data, err := c.fs.ReadFile(filepath.Join(path, "name")); err == nil {
		if value := strings.TrimSpace(string(data)); value != "" {
			chipName = value
		}
	}
	inputs, err := c.fs.Glob(filepath.Join(path, "temp[0-9]*_input"))
	if err != nil {
		return nil, []model.Warning{{Component: "hwmon", Message: path + ": " + err.Error()}}
	}
	var zones []model.ThermalZone
	var warnings []model.Warning
	for _, input := range inputs {
		base := strings.TrimSuffix(filepath.Base(input), "_input")
		label := base
		if data, err := c.fs.ReadFile(filepath.Join(path, base+"_label")); err == nil {
			if value := strings.TrimSpace(string(data)); value != "" {
				label = value
			}
		}
		temperature, readErr := c.readTemperature(input, timestamp)
		zones = append(zones, model.ThermalZone{
			Name: chipName + "/" + label, Type: "hwmon", Temperature: temperature,
		})
		if readErr != nil {
			warnings = append(warnings, model.Warning{
				Component: "hwmon", Message: input + ": " + readErr.Error(),
			})
		}
	}
	return zones, warnings
}

func (c *Collector) readTemperature(path string, timestamp time.Time) (model.Metric, error) {
	data, err := c.fs.ReadFile(path)
	if err != nil {
		return model.MissingMetric(sysfs.StatusForError(err), "°C", path, err.Error(), timestamp), err
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		wrapped := fmt.Errorf("invalid temperature: %w", err)
		return model.MissingMetric(model.Unavailable, "°C", path, wrapped.Error(), timestamp), wrapped
	}
	if value >= 1000 || value <= -1000 {
		value /= 1000
	}
	return model.AvailableMetric(value, "°C", path, timestamp), nil
}
