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
	cooling       []coolingSource
	probeWarnings []model.Warning
	discoverOnce  sync.Once
}

type sensor struct {
	name          string
	typeName      string
	path          string
	component     string
	warningSource string
	trips         []tripSource
}

type tripSource struct {
	id              int
	typeName        string
	temperaturePath string
	hysteresisPath  string
}

type coolingSource struct {
	name             string
	typeName         string
	currentStatePath string
	maximumStatePath string
}

func NewCollector(systemFS sysfs.SystemFS) *Collector {
	return &Collector{fs: systemFS}
}

func (c *Collector) Collect() ([]model.ThermalZone, []model.CoolingDevice, []model.Warning) {
	// Sensor paths and labels are stable on the embedded targets rtop monitors.
	// Keep only temperature input reads on the recurring collection path.
	c.discoverOnce.Do(func() {
		c.sensors, c.cooling, c.probeWarnings = discover(c.fs)
	})
	timestamp := time.Now()
	zones := make([]model.ThermalZone, 0, len(c.sensors))
	warnings := append([]model.Warning(nil), c.probeWarnings...)
	for _, sensor := range c.sensors {
		temperature, err := c.readTemperature(sensor.path, timestamp)
		zone := model.ThermalZone{Name: sensor.name, Type: sensor.typeName, Temperature: temperature}
		for _, trip := range sensor.trips {
			tripTemperature, tripErr := c.readTemperature(trip.temperaturePath, timestamp)
			hysteresis, hysteresisErr := c.readTemperature(trip.hysteresisPath, timestamp)
			zone.Trips = append(zone.Trips, model.ThermalTrip{
				ID: trip.id, Type: trip.typeName, Temperature: tripTemperature, Hysteresis: hysteresis,
			})
			if tripErr != nil {
				warnings = append(warnings, model.Warning{
					Component: "thermal", Message: trip.temperaturePath + ": " + tripErr.Error(),
				})
			}
			if hysteresisErr != nil && sysfs.StatusForError(hysteresisErr) != model.Unsupported {
				warnings = append(warnings, model.Warning{
					Component: "thermal", Message: trip.hysteresisPath + ": " + hysteresisErr.Error(),
				})
			}
		}
		zones = append(zones, zone)
		if err != nil {
			warnings = append(warnings, model.Warning{
				Component: sensor.component, Message: sensor.warningSource + ": " + err.Error(),
			})
		}
	}
	cooling := make([]model.CoolingDevice, 0, len(c.cooling))
	for _, source := range c.cooling {
		currentState, currentErr := c.readState(source.currentStatePath, timestamp)
		maximumState, maximumErr := c.readState(source.maximumStatePath, timestamp)
		cooling = append(cooling, model.CoolingDevice{
			Name: source.name, Type: source.typeName,
			CurrentState: currentState, MaximumState: maximumState,
		})
		for _, result := range []struct {
			path string
			err  error
		}{
			{path: source.currentStatePath, err: currentErr},
			{path: source.maximumStatePath, err: maximumErr},
		} {
			if result.err != nil {
				warnings = append(warnings, model.Warning{
					Component: "cooling", Message: result.path + ": " + result.err.Error(),
				})
			}
		}
	}
	return zones, cooling, warnings
}

func discover(systemFS sysfs.SystemFS) ([]sensor, []coolingSource, []model.Warning) {
	var sensors []sensor
	var cooling []coolingSource
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
		source := sensor{
			name: filepath.Base(path), typeName: zoneType,
			path: filepath.Join(path, "temp"), component: "thermal", warningSource: path,
		}
		tripPaths, globErr := systemFS.Glob(filepath.Join(path, "trip_point_*_temp"))
		if globErr != nil {
			warnings = append(warnings, model.Warning{Component: "thermal", Message: path + ": " + globErr.Error()})
		}
		for _, tripPath := range tripPaths {
			id, ok := tripID(tripPath)
			if !ok {
				warnings = append(warnings, model.Warning{Component: "thermal", Message: tripPath + ": invalid trip point name"})
				continue
			}
			prefix := strings.TrimSuffix(tripPath, "_temp")
			tripType := "trip"
			if data, readErr := systemFS.ReadFile(prefix + "_type"); readErr == nil {
				if value := strings.TrimSpace(string(data)); value != "" {
					tripType = value
				}
			}
			source.trips = append(source.trips, tripSource{
				id: id, typeName: tripType, temperaturePath: tripPath,
				hysteresisPath: prefix + "_hyst",
			})
		}
		sort.Slice(source.trips, func(i, j int) bool { return source.trips[i].id < source.trips[j].id })
		sensors = append(sensors, source)
	}

	coolingPaths, err := systemFS.Glob("/sys/class/thermal/cooling_device*")
	if err != nil {
		warnings = append(warnings, model.Warning{Component: "cooling", Message: err.Error()})
	}
	for _, path := range coolingPaths {
		typeName := filepath.Base(path)
		if data, readErr := systemFS.ReadFile(filepath.Join(path, "type")); readErr == nil {
			if value := strings.TrimSpace(string(data)); value != "" {
				typeName = value
			}
		}
		cooling = append(cooling, coolingSource{
			name: filepath.Base(path), typeName: typeName,
			currentStatePath: filepath.Join(path, "cur_state"),
			maximumStatePath: filepath.Join(path, "max_state"),
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
	if cooling == nil {
		cooling = []coolingSource{}
	}
	return sensors, cooling, warnings
}

func tripID(path string) (int, bool) {
	base := filepath.Base(path)
	value := strings.TrimSuffix(strings.TrimPrefix(base, "trip_point_"), "_temp")
	id, err := strconv.Atoi(value)
	return id, err == nil
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

func (c *Collector) readState(path string, timestamp time.Time) (model.Metric, error) {
	data, err := c.fs.ReadFile(path)
	if err != nil {
		return model.MissingMetric(sysfs.StatusForError(err), "state", path, err.Error(), timestamp), err
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		wrapped := fmt.Errorf("invalid cooling state: %w", err)
		return model.MissingMetric(model.Unavailable, "state", path, wrapped.Error(), timestamp), wrapped
	}
	return model.AvailableMetric(value, "state", path, timestamp), nil
}
