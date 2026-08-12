package doctor

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"time"

	"rtop/internal/model"
	"rtop/internal/sysfs"
)

type CheckStatus string

const (
	StatusPass    CheckStatus = "pass"
	StatusWarning CheckStatus = "warning"
	StatusFail    CheckStatus = "fail"
	StatusInfo    CheckStatus = "info"
)

type Check struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Status     CheckStatus `json:"status"`
	Message    string      `json:"message"`
	Source     string      `json:"source,omitempty"`
	Suggestion string      `json:"suggestion,omitempty"`
}

type Summary struct {
	Passed   int `json:"passed"`
	Warnings int `json:"warnings"`
	Failed   int `json:"failed"`
	Info     int `json:"info"`
}

type Report struct {
	Timestamp time.Time        `json:"timestamp"`
	System    model.SystemInfo `json:"system"`
	Checks    []Check          `json:"checks"`
	Summary   Summary          `json:"summary"`
}

func Analyze(systemFS sysfs.SystemFS, snapshot model.Snapshot) Report {
	report := Report{Timestamp: time.Now(), System: snapshot.System}
	report.Checks = append(report.Checks, platformChecks(snapshot.System)...)
	report.Checks = append(report.Checks, kernelBuildCheck(systemFS))
	report.Checks = append(report.Checks, driverChecks(snapshot.System)...)
	report.Checks = append(report.Checks, debugFSChecks(systemFS)...)
	report.Checks = append(report.Checks, snapshotChecks(snapshot)...)
	for _, check := range report.Checks {
		switch check.Status {
		case StatusPass:
			report.Summary.Passed++
		case StatusWarning:
			report.Summary.Warnings++
		case StatusFail:
			report.Summary.Failed++
		case StatusInfo:
			report.Summary.Info++
		}
	}
	return report
}

func kernelBuildCheck(systemFS sysfs.SystemFS) Check {
	for _, path := range []string{"/proc/version_signature", "/proc/version"} {
		data, err := systemFS.ReadFile(path)
		if err != nil {
			continue
		}
		value := strings.TrimSpace(string(data))
		if value != "" {
			return Check{
				ID: "platform.kernel_build", Name: "Kernel build", Status: StatusInfo,
				Message: value, Source: path,
			}
		}
	}
	return Check{
		ID: "platform.kernel_build", Name: "Kernel build", Status: StatusInfo,
		Message: "kernel build or BSP signature is not exposed",
		Source:  "/proc/version_signature, /proc/version",
	}
}

func platformChecks(info model.SystemInfo) []Check {
	var checks []Check
	if info.SoC != "" {
		checks = append(checks, Check{
			ID: "platform.soc", Name: "SoC detection", Status: StatusPass,
			Message: info.SoC + " on " + fallback(info.Board, "unknown board"),
			Source:  "/proc/device-tree/compatible",
		})
	} else {
		checks = append(checks, Check{
			ID: "platform.soc", Name: "SoC detection", Status: StatusWarning,
			Message:    "Rockchip SoC was not identified",
			Source:     "/proc/device-tree/compatible",
			Suggestion: "Confirm that Device Tree compatible is exposed to userspace.",
		})
	}
	if info.Kernel != "" {
		checks = append(checks, Check{
			ID: "platform.kernel", Name: "Kernel", Status: StatusPass,
			Message: info.Kernel + " (" + fallback(info.Arch, "unknown architecture") + ")",
			Source:  "/proc/sys/kernel/osrelease",
		})
	} else {
		checks = append(checks, Check{
			ID: "platform.kernel", Name: "Kernel", Status: StatusFail,
			Message: "kernel release is unavailable", Source: "/proc/sys/kernel/osrelease",
		})
	}
	return checks
}

func driverChecks(info model.SystemInfo) []Check {
	drivers := make(map[string]model.DriverInfo)
	for _, driver := range info.Drivers {
		drivers[driver.Type] = driver
	}
	var checks []Check
	for _, driverType := range []string{"gpu", "npu", "ddr"} {
		driver, ok := drivers[driverType]
		if !ok {
			checks = append(checks, Check{
				ID: "driver." + driverType, Name: strings.ToUpper(driverType) + " driver",
				Status: StatusWarning, Message: "no matching devfreq device was discovered",
				Source:     "/sys/class/devfreq",
				Suggestion: "Check the Device Tree node, kernel driver and devfreq registration.",
			})
			continue
		}
		message := driver.Name + " on " + driver.Device
		if driver.Governor != "" {
			message += ", governor " + driver.Governor
		}
		checks = append(checks, Check{
			ID: "driver." + driverType, Name: strings.ToUpper(driverType) + " driver",
			Status: StatusPass, Message: message,
			Source: "/sys/class/devfreq/" + driver.Device,
		})
	}
	return checks
}

func debugFSChecks(systemFS sysfs.SystemFS) []Check {
	mounted, source, err := debugFSMounted(systemFS)
	var checks []Check
	if err != nil {
		checks = append(checks, Check{
			ID: "debugfs.mount", Name: "debugfs mount", Status: StatusWarning,
			Message: err.Error(), Source: source,
			Suggestion: "Inspect /proc/mounts or /proc/self/mountinfo.",
		})
	} else if !mounted {
		checks = append(checks, Check{
			ID: "debugfs.mount", Name: "debugfs mount", Status: StatusWarning,
			Message: "debugfs is not mounted", Source: source,
			Suggestion: "Mount debugfs at /sys/kernel/debug if per-core NPU or RGA diagnostics are required.",
		})
	} else {
		checks = append(checks, Check{
			ID: "debugfs.mount", Name: "debugfs mount", Status: StatusPass,
			Message: "debugfs is mounted at /sys/kernel/debug", Source: source,
		})
	}

	entries, readErr := systemFS.ReadDir("/sys/kernel/debug")
	switch {
	case readErr == nil:
		checks = append(checks, Check{
			ID: "debugfs.access", Name: "debugfs access", Status: StatusPass,
			Message: fmt.Sprintf("current user can list debugfs (%d entries)", len(entries)),
			Source:  "/sys/kernel/debug",
		})
	case errors.Is(readErr, fs.ErrPermission):
		checks = append(checks, Check{
			ID: "debugfs.access", Name: "debugfs access", Status: StatusWarning,
			Message:    "debugfs is mounted but not readable by the current user",
			Source:     "/sys/kernel/debug",
			Suggestion: "Grant narrowly scoped read access to required nodes or run with suitable privileges; avoid making all debugfs files world-readable.",
		})
	default:
		checks = append(checks, Check{
			ID: "debugfs.access", Name: "debugfs access", Status: StatusWarning,
			Message: readErr.Error(), Source: "/sys/kernel/debug",
		})
	}
	return checks
}

func snapshotChecks(snapshot model.Snapshot) []Check {
	checks := []Check{
		metricCheck("metric.cpu", "CPU usage", snapshot.CPU.Overall, true),
		metricCheck("metric.memory", "Memory", snapshot.Memory.Total, true),
		metricCheck("metric.load", "Load average", snapshot.Load.OneMinute, true),
		capabilityMetricCheck("metric.memory.cma", "CMA accounting", snapshot.Memory.CMATotal),
		capabilityMetricCheck("metric.pressure.memory", "Memory pressure (PSI)", snapshot.Memory.Pressure.Memory.Some.Avg10),
	}
	for _, core := range snapshot.CPU.Cores {
		if core.Policy == nil {
			continue
		}
		prefix := fmt.Sprintf("metric.cpu.core%d.policy", core.ID)
		checks = append(checks,
			capabilityMetricCheck(prefix+".minimum", fmt.Sprintf("CPU %d minimum frequency", core.ID), core.Policy.Minimum),
			capabilityMetricCheck(prefix+".maximum", fmt.Sprintf("CPU %d maximum frequency", core.ID), core.Policy.Maximum),
			capabilityTextMetricCheck(prefix+".governor", fmt.Sprintf("CPU %d governor", core.ID), core.Policy.Governor),
		)
	}
	availableThermals := 0
	availableTrips := 0
	for _, zone := range snapshot.Thermals {
		if zone.Temperature.Status == model.Available {
			availableThermals++
		}
		for _, trip := range zone.Trips {
			if trip.Temperature.Status == model.Available {
				availableTrips++
			}
		}
	}
	if availableThermals > 0 {
		checks = append(checks, Check{
			ID: "metric.thermal", Name: "Thermal sensors", Status: StatusPass,
			Message: fmt.Sprintf("%d readable temperature sources, %d trip points, %d cooling devices", availableThermals, availableTrips, len(snapshot.Cooling)),
			Source:  snapshot.Thermals[0].Temperature.Source,
		})
	} else {
		checks = append(checks, Check{
			ID: "metric.thermal", Name: "Thermal sensors", Status: StatusWarning,
			Message: "no readable thermal or hwmon temperature source",
			Source:  "/sys/class/thermal, /sys/class/hwmon",
		})
	}
	for index, device := range snapshot.Cooling {
		prefix := fmt.Sprintf("metric.cooling.device%d", index)
		name := fallback(device.Type, device.Name)
		checks = append(checks,
			metricCheck(prefix+".current_state", name+" current state", device.CurrentState, false),
			metricCheck(prefix+".maximum_state", name+" maximum state", device.MaximumState, false),
		)
	}
	if len(snapshot.Power) == 0 {
		checks = append(checks, Check{
			ID: "metric.power", Name: "Power sensors", Status: StatusInfo,
			Message: "no hwmon voltage, current, power or energy source was discovered",
			Source:  "/sys/class/hwmon",
		})
	} else {
		for index, sensor := range snapshot.Power {
			checks = append(checks, metricCheck(
				fmt.Sprintf("metric.power.sensor%d", index),
				fmt.Sprintf("%s %s", fallback(sensor.Name, sensor.Kind), sensor.Kind),
				sensor.Value, false,
			))
		}
	}
	for index, fan := range snapshot.Fans {
		prefix := fmt.Sprintf("metric.fan.device%d", index)
		checks = append(checks,
			capabilityMetricCheck(prefix+".speed", fan.Name+" speed", fan.Speed),
			capabilityMetricCheck(prefix+".target", fan.Name+" target speed", fan.TargetSpeed),
			capabilityMetricCheck(prefix+".pwm", fan.Name+" PWM", fan.PWM),
			capabilityTextMetricCheck(prefix+".control_mode", fan.Name+" control mode", fan.ControlMode),
		)
	}
	if len(snapshot.Processes) > 0 {
		checks = append(checks, Check{
			ID: "metric.process", Name: "Processes", Status: StatusPass,
			Message: fmt.Sprintf("%d readable processes", len(snapshot.Processes)),
			Source:  "/proc/[0-9]*",
		})
	} else {
		checks = append(checks, Check{
			ID: "metric.process", Name: "Processes", Status: StatusWarning,
			Message: "no processes were readable", Source: "/proc/[0-9]*",
			Suggestion: "Check procfs hidepid settings and current user permissions.",
		})
	}

	accelerators := append([]model.Accelerator(nil), snapshot.Accelerators...)
	sort.SliceStable(accelerators, func(i, j int) bool {
		return accelerators[i].Type < accelerators[j].Type
	})
	for _, accelerator := range accelerators {
		prefix := "metric." + accelerator.Type
		usageCheck := metricCheck(prefix+".usage", accelerator.Name+" usage", accelerator.Usage, false)
		if accelerator.Type == "rga" && accelerator.Usage.Status == model.PermissionDenied {
			usageCheck.Suggestion = "Grant narrowly scoped read access to /sys/kernel/debug/rkrga/load or use the procfs rkrga interface."
		}
		checks = append(checks, usageCheck)
		frequencyCheck := metricCheck(prefix+".frequency", accelerator.Name+" frequency", accelerator.Frequency, false)
		if accelerator.Type == "rga" && accelerator.Frequency.Status == model.Unsupported {
			frequencyCheck.Status = StatusInfo
		}
		checks = append(checks, frequencyCheck,
			capabilityTextMetricCheck(prefix+".runtime_status", accelerator.Name+" runtime power state", accelerator.RuntimeStatus))
		if accelerator.DriverVersion != nil {
			checks = append(checks, capabilityTextMetricCheck(prefix+".driver_version", accelerator.Name+" driver version", *accelerator.DriverVersion))
		}
		if accelerator.Policy != nil {
			checks = append(checks,
				capabilityMetricCheck(prefix+".policy.minimum", accelerator.Name+" minimum frequency", accelerator.Policy.Minimum),
				capabilityMetricCheck(prefix+".policy.maximum", accelerator.Name+" maximum frequency", accelerator.Policy.Maximum),
				capabilityTextMetricCheck(prefix+".policy.governor", accelerator.Name+" governor", accelerator.Policy.Governor),
			)
		}
		for _, core := range accelerator.Cores {
			coreName := fmt.Sprintf("core %d", core.ID)
			if core.Name != "" {
				coreName = core.Name
			}
			check := metricCheck(
				fmt.Sprintf("%s.core%d", prefix, core.ID),
				fmt.Sprintf("%s %s", accelerator.Name, coreName),
				core.Usage,
				false,
			)
			if core.Usage.Status == model.PermissionDenied && accelerator.Type == "npu" {
				check.Suggestion = "Grant read access to the RKNPU debugfs load node if per-core metrics are required."
			} else if core.Usage.Status == model.PermissionDenied && accelerator.Type == "rga" {
				check.Suggestion = "Grant read access to the rkrga load node if RGA scheduler metrics are required."
			}
			checks = append(checks, check)
		}
	}
	return checks
}

func capabilityMetricCheck(id, name string, metric model.Metric) Check {
	check := metricCheck(id, name, metric, false)
	if metric.Status == "" || metric.Status == model.Unsupported {
		check.Status = StatusInfo
	}
	return check
}

func capabilityTextMetricCheck(id, name string, metric model.TextMetric) Check {
	check := Check{ID: id, Name: name, Source: metric.Source}
	switch metric.Status {
	case model.Available:
		check.Status = StatusPass
		check.Message = "selected source is readable"
		if metric.Value != nil {
			check.Message = *metric.Value
		}
	case model.PermissionDenied:
		check.Status = StatusWarning
		check.Message = fallback(metric.Error, "permission denied")
		check.Suggestion = "Review read permissions for the selected source."
	case "", model.Unsupported:
		check.Status = StatusInfo
		check.Message = fallback(metric.Error, "source is not exposed")
	case model.Stale:
		check.Status = StatusWarning
		check.Message = fallback(metric.Error, "metric is stale")
	default:
		check.Status = StatusWarning
		check.Message = fallback(metric.Error, "metric is unavailable")
	}
	return check
}

func metricCheck(id, name string, metric model.Metric, required bool) Check {
	check := Check{ID: id, Name: name, Source: metric.Source}
	switch metric.Status {
	case model.Available:
		check.Status = StatusPass
		check.Message = "selected source is readable"
	case model.PermissionDenied:
		check.Status = StatusWarning
		check.Message = fallback(metric.Error, "permission denied")
		check.Suggestion = "Review read permissions for the selected source."
	case model.Unsupported:
		check.Status = StatusWarning
		check.Message = fallback(metric.Error, "no supported source was discovered")
	case model.Stale:
		check.Status = StatusWarning
		check.Message = fallback(metric.Error, "metric is stale")
	default:
		if required {
			check.Status = StatusFail
		} else {
			check.Status = StatusWarning
		}
		check.Message = fallback(metric.Error, "metric is unavailable")
	}
	return check
}

func debugFSMounted(systemFS sysfs.SystemFS) (bool, string, error) {
	if data, err := systemFS.ReadFile("/proc/mounts"); err == nil {
		return parseProcMounts(data), "/proc/mounts", nil
	}
	data, err := systemFS.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false, "/proc/mounts, /proc/self/mountinfo", err
	}
	return parseMountInfo(data), "/proc/self/mountinfo", nil
}

func parseProcMounts(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[2] == "debugfs" && unescapeMount(fields[1]) == "/sys/kernel/debug" {
			return true
		}
	}
	return false
}

func parseMountInfo(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		left, right, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		leftFields, rightFields := strings.Fields(left), strings.Fields(right)
		if len(leftFields) >= 5 && len(rightFields) >= 1 &&
			rightFields[0] == "debugfs" && unescapeMount(leftFields[4]) == "/sys/kernel/debug" {
			return true
		}
	}
	return false
}

func unescapeMount(value string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\134`, `\`)
	return replacer.Replace(value)
}

func fallback(value, replacement string) string {
	if value == "" {
		return replacement
	}
	return value
}

func WriteText(writer io.Writer, report Report) error {
	if _, err := fmt.Fprintf(writer, "rtop doctor — %s\n", report.Timestamp.Format(time.RFC3339)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Platform: %s / %s / kernel %s\n\n",
		fallback(report.System.SoC, "unknown SoC"),
		fallback(report.System.Board, "unknown board"),
		fallback(report.System.Kernel, "unknown"),
	); err != nil {
		return err
	}
	for _, check := range report.Checks {
		if _, err := fmt.Fprintf(writer, "[%-7s] %-24s %s\n", strings.ToUpper(string(check.Status)), check.Name, check.Message); err != nil {
			return err
		}
		if check.Source != "" {
			if _, err := fmt.Fprintf(writer, "          source: %s\n", check.Source); err != nil {
				return err
			}
		}
		if check.Suggestion != "" {
			if _, err := fmt.Fprintf(writer, "          suggestion: %s\n", check.Suggestion); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintf(writer, "\nSummary: %d passed, %d warnings, %d failed, %d info\n",
		report.Summary.Passed, report.Summary.Warnings, report.Summary.Failed, report.Summary.Info,
	)
	return err
}
