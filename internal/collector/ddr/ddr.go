package ddr

import (
	"sync"
	"time"

	"rtop/internal/collector/devfreq"
	"rtop/internal/model"
	"rtop/internal/sysfs"
)

type Collector struct {
	fs sysfs.SystemFS

	mu     sync.RWMutex
	device *devfreq.Device
}

func NewCollector(systemFS sysfs.SystemFS) *Collector {
	collector := &Collector{fs: systemFS}
	collector.Probe()
	return collector
}

func (c *Collector) Probe() []model.Warning {
	devices, warnings := devfreq.Discover(c.fs)
	device, ok := devfreq.Find(devices, devfreq.KindDDR)
	c.mu.Lock()
	if ok {
		c.device = &device
	} else {
		c.device = nil
	}
	c.mu.Unlock()
	return warnings
}

func (c *Collector) Collect() (model.Accelerator, []model.Warning) {
	timestamp := time.Now()
	c.mu.RLock()
	device := c.device
	c.mu.RUnlock()
	if device == nil {
		return devfreq.Missing(devfreq.KindDDR, "DDR/DMC", timestamp), nil
	}
	usage := devfreq.ReadLoad(c.fs, *device, timestamp)
	frequency := devfreq.ReadFrequency(c.fs, *device, timestamp)
	accelerator := model.Accelerator{
		Name: "DDR/DMC", Type: string(devfreq.KindDDR), Usage: usage, Frequency: frequency,
	}
	var warnings []model.Warning
	for _, metric := range []model.Metric{usage, frequency} {
		if warning := devfreq.MetricWarning("ddr", metric); warning != nil {
			warnings = append(warnings, *warning)
		}
	}
	return accelerator, warnings
}

func (c *Collector) Info() (model.DriverInfo, bool) {
	c.mu.RLock()
	if c.device == nil {
		c.mu.RUnlock()
		return model.DriverInfo{}, false
	}
	device := *c.device
	c.mu.RUnlock()
	return devfreq.Info(c.fs, device), true
}
