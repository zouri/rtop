package thermal

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

type Collector struct {
	fs            sysfs.SystemFS
	sensors       []sensor
	probeWarnings []model.Warning
	discoverOnce  sync.Once
}

type sensor struct {
	name          string
	typeName      string
	path          string
	component     string
	warningSource string
}

func NewCollector(systemFS sysfs.SystemFS) *Collector {
	return &Collector{fs: systemFS}
}

func (c *Collector) Collect() ([]model.ThermalZone, []model.Warning) {
	// Sensor paths and labels are stable on the embedded targets rtop monitors.
	// Keep only temperature input reads on the recurring collection path.
	c.discoverOnce.Do(func() {
		c.sensors, c.probeWarnings = discover(c.fs)
	})
	timestamp := time.Now()
	zones := make([]model.ThermalZone, 0, len(c.sensors))
	warnings := append([]model.Warning(nil), c.probeWarnings...)
	for _, sensor := range c.sensors {
		temperature, err := c.readTemperature(sensor.path, timestamp)
		zones = append(zones, model.ThermalZone{
			Name: sensor.name, Type: sensor.typeName, Temperature: temperature,
		})
		if err != nil {
			warnings = append(warnings, model.Warning{
				Component: sensor.component, Message: sensor.warningSource + ": " + err.Error(),
			})
		}
	}
	return zones, warnings
}

func discover(systemFS sysfs.SystemFS) ([]sensor, []model.Warning) {
	var sensors []sensor
	var warnings []model.Warning
	thermalPaths, err := systemFS.Glob("/sys/class/thermal/thermal_zone*")
	if err != nil {
		warnings = append(warnings, model.Warning{Component: "thermal", Message: err.Error()})
	}
	for _, path := range thermalPaths {
		zoneType := filepath.Base(path)
		if data, readErr := systemFS.ReadFile(filepath.Join(path, "type")); readErr == nil {
			if value := strings.TrimSpace(string(data)); value != "" {
				zoneType = value
			}
		}
		sensors = append(sensors, sensor{
			name: filepath.Base(path), typeName: zoneType,
			path: filepath.Join(path, "temp"), component: "thermal", warningSource: path,
		})
	}

	hwmonPaths, err := systemFS.Glob("/sys/class/hwmon/hwmon*")
	if err != nil {
		warnings = append(warnings, model.Warning{Component: "hwmon", Message: err.Error()})
	}
	for _, path := range hwmonPaths {
		chipName := filepath.Base(path)
		if data, readErr := systemFS.ReadFile(filepath.Join(path, "name")); readErr == nil {
			if value := strings.TrimSpace(string(data)); value != "" {
				chipName = value
			}
		}
		inputs, globErr := systemFS.Glob(filepath.Join(path, "temp[0-9]*_input"))
		if globErr != nil {
			warnings = append(warnings, model.Warning{Component: "hwmon", Message: path + ": " + globErr.Error()})
			continue
		}
		for _, input := range inputs {
			base := strings.TrimSuffix(filepath.Base(input), "_input")
			label := base
			if data, readErr := systemFS.ReadFile(filepath.Join(path, base+"_label")); readErr == nil {
				if value := strings.TrimSpace(string(data)); value != "" {
					label = value
				}
			}
			sensors = append(sensors, sensor{
				name: chipName + "/" + label, typeName: "hwmon", path: input,
				component: "hwmon", warningSource: input,
			})
		}
	}

	sort.Slice(sensors, func(i, j int) bool {
		if sensors[i].typeName == sensors[j].typeName {
			return sensors[i].name < sensors[j].name
		}
		return sensors[i].typeName < sensors[j].typeName
	})
	if sensors == nil {
		sensors = []sensor{}
	}
	return sensors, warnings
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
