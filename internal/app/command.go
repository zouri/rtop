package app

import (
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"rtop/internal/doctor"
	"rtop/internal/model"
	"rtop/internal/sysfs"
	"rtop/internal/ui"
)

type runtimeConfig struct {
	root              string
	interval          time.Duration
	cpuSampleDuration time.Duration
	processInterval   time.Duration
	thermalInterval   time.Duration
}

type snapshotOptions struct {
	asJSON      bool
	watch       bool
	noProcesses bool
	top         int
}

func NewCommand(version, commit, date string, stdout, stderr io.Writer) *cobra.Command {
	config := runtimeConfig{
		root:              "/",
		interval:          time.Second,
		cpuSampleDuration: 200 * time.Millisecond,
		processInterval:   2 * time.Second,
		thermalInterval:   5 * time.Second,
	}
	command := &cobra.Command{
		Use:           "rtop",
		Short:         "Read-only Rockchip Linux system monitor",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		RunE: func(command *cobra.Command, _ []string) error {
			return runTUI(command.Context(), config, stdout)
		},
	}
	command.SetOut(stdout)
	command.SetErr(stderr)
	command.SetVersionTemplate(fmt.Sprintf(
		"rtop %s (commit %s, built %s)\n", version, commit, date,
	))
	command.PersistentFlags().StringVar(&config.root, "root", config.root, "target system root (for fixtures and containers)")
	command.PersistentFlags().DurationVar(&config.interval, "interval", config.interval, "TUI and snapshot watch refresh interval")
	command.PersistentFlags().DurationVar(&config.cpuSampleDuration, "cpu-sample", config.cpuSampleDuration, "initial CPU and process usage sampling window")
	command.PersistentFlags().DurationVar(&config.processInterval, "process-interval", config.processInterval, "process collection interval")
	command.PersistentFlags().DurationVar(&config.thermalInterval, "thermal-interval", config.thermalInterval, "thermal collection interval")

	command.AddCommand(newInfoCommand(&config, stdout))
	command.AddCommand(newSnapshotCommand(&config, stdout))
	command.AddCommand(newDoctorCommand(&config, stdout))
	return command
}

func runTUI(ctx context.Context, config runtimeConfig, stdout io.Writer) error {
	if err := validateSampling(config, true, true); err != nil {
		return err
	}
	systemFS, err := sysfs.New(config.root)
	if err != nil {
		return err
	}
	monitor := NewMonitorWithIntervals(
		systemFS, config.cpuSampleDuration, config.processInterval, config.thermalInterval,
	)
	// The default ALL page mirrors jtop's board-wide view and includes a small
	// top-process list. Slower collectors still keep their independent cadence.
	monitor.SetCollectionScope(model.CollectAll)
	scheduler := NewScheduler(monitor.Snapshot, config.interval, periodicSnapshotTimeout(config))
	tuiContext, cancel := context.WithCancel(ctx)
	defer cancel()
	refresh := func() {
		monitor.ForceRefresh()
		scheduler.Refresh()
	}
	scopeChanged := func(scope model.CollectionScope) {
		monitor.SetCollectionScope(scope)
		monitor.ForceRefresh()
		scheduler.Refresh()
	}
	return ui.RunWithScope(tuiContext, scheduler.Run(tuiContext), refresh, scopeChanged, stdout)
}

func newInfoCommand(config *runtimeConfig, stdout io.Writer) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "info",
		Short: "Show board, SoC, kernel and operating system information",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			systemFS, err := sysfs.New(config.root)
			if err != nil {
				return err
			}
			monitor := NewMonitor(systemFS, config.cpuSampleDuration)
			info, warnings := monitor.SystemInfo()
			if asJSON {
				return writeJSON(stdout, struct {
					System   model.SystemInfo `json:"system"`
					Warnings []model.Warning  `json:"warnings"`
				}{System: info, Warnings: warnings}, true)
			}
			printSystemInfo(stdout, info)
			for _, warning := range warnings {
				fmt.Fprintf(stdout, "warning: %s: %s\n", warning.Component, warning.Message)
			}
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return command
}

func newSnapshotCommand(config *runtimeConfig, stdout io.Writer) *cobra.Command {
	var asJSON bool
	var watch bool
	var noProcesses bool
	var top int
	command := &cobra.Command{
		Use:   "snapshot",
		Short: "Collect a system snapshot",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runSnapshot(command.Context(), *config, stdout, snapshotOptions{
				asJSON: asJSON || watch, watch: watch, noProcesses: noProcesses, top: top,
			})
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	command.Flags().BoolVarP(&watch, "watch", "w", false, "continuously output newline-delimited JSON snapshots")
	command.Flags().BoolVar(&noProcesses, "no-processes", false, "omit process data and skip process collection")
	command.Flags().IntVar(&top, "top", 0, "limit process output to the top N by CPU (0 keeps all)")
	return command
}

func newDoctorCommand(config *runtimeConfig, stdout io.Writer) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose platform detection, drivers, permissions and metric sources",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := validateSampling(*config, false, true); err != nil {
				return err
			}
			systemFS, err := sysfs.New(config.root)
			if err != nil {
				return err
			}
			monitor := NewMonitor(systemFS, config.cpuSampleDuration)
			report := doctor.Analyze(systemFS, monitor.Snapshot(command.Context()))
			if asJSON {
				return writeJSON(stdout, report, true)
			}
			return doctor.WriteText(stdout, report)
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return command
}

func runSnapshot(ctx context.Context, config runtimeConfig, stdout io.Writer, options snapshotOptions) error {
	if options.top < 0 {
		return errors.New("--top must be zero or greater")
	}
	if err := validateSampling(config, options.watch, !options.noProcesses); err != nil {
		return err
	}
	systemFS, err := sysfs.New(config.root)
	if err != nil {
		return err
	}
	if options.watch {
		monitor := NewMonitorWithIntervals(
			systemFS, config.cpuSampleDuration, config.processInterval, config.thermalInterval,
		)
		if options.noProcesses {
			monitor.DisableProcesses()
		}
		scheduler := NewScheduler(monitor.Snapshot, config.interval, periodicSnapshotTimeout(config))
		for snapshot := range scheduler.Run(ctx) {
			snapshot.Processes = topProcessesByCPU(snapshot.Processes, options.top)
			if err := writeJSON(stdout, snapshot, false); err != nil {
				return err
			}
		}
		return nil
	}
	monitor := NewMonitor(systemFS, config.cpuSampleDuration)
	if options.noProcesses {
		monitor.DisableProcesses()
	}
	snapshot := monitor.Snapshot(ctx)
	if options.asJSON {
		snapshot.Processes = topProcessesByCPU(snapshot.Processes, options.top)
		return writeJSON(stdout, snapshot, true)
	}
	printSnapshot(stdout, snapshot)
	return nil
}

func validateSampling(config runtimeConfig, requireInterval, requireProcesses bool) error {
	if requireInterval && config.interval <= 0 {
		return errors.New("--interval must be greater than zero")
	}
	if config.cpuSampleDuration <= 0 {
		return errors.New("--cpu-sample must be greater than zero")
	}
	if requireInterval && requireProcesses && config.processInterval <= 0 {
		return errors.New("--process-interval must be greater than zero")
	}
	if requireInterval && config.thermalInterval <= 0 {
		return errors.New("--thermal-interval must be greater than zero")
	}
	return nil
}

type processMinHeap []model.ProcessInfo

func (h processMinHeap) Len() int           { return len(h) }
func (h processMinHeap) Less(i, j int) bool { return processBetter(h[j], h[i]) }
func (h processMinHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *processMinHeap) Push(value any)    { *h = append(*h, value.(model.ProcessInfo)) }
func (h *processMinHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func topProcessesByCPU(processes []model.ProcessInfo, limit int) []model.ProcessInfo {
	if limit <= 0 || len(processes) == 0 {
		return processes
	}
	limit = min(limit, len(processes))
	selected := make(processMinHeap, 0, limit)
	for _, process := range processes {
		if len(selected) < limit {
			heap.Push(&selected, process)
			continue
		}
		if processBetter(process, selected[0]) {
			selected[0] = process
			heap.Fix(&selected, 0)
		}
	}
	sort.Slice(selected, func(i, j int) bool {
		return processBetter(selected[i], selected[j])
	})
	return []model.ProcessInfo(selected)
}

func processBetter(left, right model.ProcessInfo) bool {
	leftCPU, rightCPU := processCPUValue(left), processCPUValue(right)
	if leftCPU == rightCPU {
		return left.PID < right.PID
	}
	return leftCPU > rightCPU
}

func processCPUValue(process model.ProcessInfo) float64 {
	if process.CPU.Status != model.Available || process.CPU.Value == nil ||
		math.IsNaN(*process.CPU.Value) || math.IsInf(*process.CPU.Value, 0) {
		return -1
	}
	return *process.CPU.Value
}

func periodicSnapshotTimeout(config runtimeConfig) time.Duration {
	return max(config.interval, config.cpuSampleDuration+time.Second)
}

func writeJSON(writer io.Writer, value any, pretty bool) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	if pretty {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(value)
}

func printSystemInfo(writer io.Writer, info model.SystemInfo) {
	fmt.Fprintf(writer, "Hostname: %s\n", fallback(info.Hostname, "unknown"))
	fmt.Fprintf(writer, "OS:       %s\n", fallback(info.OS, "unknown"))
	fmt.Fprintf(writer, "Kernel:   %s\n", fallback(info.Kernel, "unknown"))
	fmt.Fprintf(writer, "Arch:     %s\n", fallback(info.Arch, "unknown"))
	fmt.Fprintf(writer, "Board:    %s\n", fallback(info.Board, "unknown"))
	fmt.Fprintf(writer, "SoC:      %s\n", fallback(info.SoC, "unknown"))
	if len(info.Compatible) > 0 {
		fmt.Fprintf(writer, "DT:       %s\n", strings.Join(info.Compatible, ", "))
	}
	for _, driver := range info.Drivers {
		fmt.Fprintf(writer, "Driver:   %-4s %-18s device=%s", strings.ToUpper(driver.Type), driver.Name, driver.Device)
		if driver.Governor != "" {
			fmt.Fprintf(writer, " governor=%s", driver.Governor)
		}
		fmt.Fprintln(writer)
	}
}

func printSnapshot(writer io.Writer, snapshot model.Snapshot) {
	fmt.Fprintf(writer, "%s  %s  kernel %s\n",
		snapshot.Timestamp.Format(time.RFC3339),
		fallback(snapshot.System.Hostname, "unknown"),
		fallback(snapshot.System.Kernel, "unknown"),
	)
	fmt.Fprintf(writer, "CPU: %s", formatMetric(snapshot.CPU.Overall, 1))
	for _, core := range snapshot.CPU.Cores {
		fmt.Fprintf(writer, "  cpu%d %s %s", core.ID, formatMetric(core.Usage, 1), formatMetric(core.Frequency, 0))
	}
	fmt.Fprintln(writer)
	for _, core := range snapshot.CPU.Cores {
		if policy := formatFrequencyPolicy(core.Policy); policy != "" {
			fmt.Fprintf(writer, "  cpu%d policy: %s\n", core.ID, policy)
		}
	}
	fmt.Fprintf(writer, "Memory: %s total  %s used  %s free  %s shared  %s buff/cache  %s available\n",
		formatMetric(snapshot.Memory.Total, 0),
		formatMetric(snapshot.Memory.Used, 0),
		formatMetric(snapshot.Memory.Free, 0),
		formatMetric(snapshot.Memory.Shared, 0),
		formatMetric(snapshot.Memory.BufferCache, 0),
		formatMetric(snapshot.Memory.Available, 0),
	)
	fmt.Fprintf(writer, "Cache: %s buffers  %s cache\n",
		formatMetric(snapshot.Memory.Buffers, 0),
		formatMetric(snapshot.Memory.Cache, 0),
	)
	fmt.Fprintf(writer, "Swap: %s total  %s used  %s free  %s cached\n",
		formatMetric(snapshot.Memory.SwapTotal, 0),
		formatMetric(snapshot.Memory.SwapUsed, 0),
		formatMetric(snapshot.Memory.SwapFree, 0),
		formatMetric(snapshot.Memory.SwapCached, 0),
	)
	if snapshot.Memory.CMATotal.Status == model.Available {
		fmt.Fprintf(writer, "CMA: %s used / %s total\n",
			formatMetric(snapshot.Memory.CMAUsed, 0), formatMetric(snapshot.Memory.CMATotal, 0))
	}
	fmt.Fprintf(writer, "Pressure avg10: CPU %s  Memory %s/%s  IO %s/%s (some/full)\n",
		formatMetric(snapshot.Memory.Pressure.CPU.Some.Avg10, 2),
		formatMetric(snapshot.Memory.Pressure.Memory.Some.Avg10, 2),
		formatMetric(snapshot.Memory.Pressure.Memory.Full.Avg10, 2),
		formatMetric(snapshot.Memory.Pressure.IO.Some.Avg10, 2),
		formatMetric(snapshot.Memory.Pressure.IO.Full.Avg10, 2),
	)
	for _, device := range snapshot.Memory.ZRAM {
		fmt.Fprintf(writer, "ZRAM %s: %s used / %s, ratio %s, algorithm %s\n",
			device.Name, formatMetric(device.MemoryUsed, 0), formatMetric(device.DiskSize, 0),
			formatMetric(device.CompressionRatio, 2), formatTextMetric(device.Algorithm))
	}
	fmt.Fprintf(writer, "Load: %s %s %s\n",
		formatMetric(snapshot.Load.OneMinute, 2),
		formatMetric(snapshot.Load.FiveMinutes, 2),
		formatMetric(snapshot.Load.FifteenMinutes, 2),
	)
	for _, accelerator := range snapshot.Accelerators {
		fmt.Fprintf(writer, "%s: load %s  frequency %s", accelerator.Name,
			formatMetric(accelerator.Usage, 1), formatMetric(accelerator.Frequency, 0))
		if runtime := formatTextMetricValue(accelerator.RuntimeStatus); runtime != "" {
			fmt.Fprintf(writer, "  power %s", runtime)
		}
		fmt.Fprintln(writer)
		if policy := formatFrequencyPolicy(accelerator.Policy); policy != "" {
			fmt.Fprintf(writer, "  policy: %s\n", policy)
		}
		for _, session := range accelerator.Sessions {
			fmt.Fprintf(writer, "  session %d: status %s  TGID %d  process %s\n",
				session.Session, session.Status, session.TGID, session.Process)
		}
	}
	for _, sensor := range snapshot.Power {
		name := sensor.Name
		if sensor.Chip != "" {
			name = sensor.Chip + "/" + sensor.Name
		}
		fmt.Fprintf(writer, "Power %s (%s): %s\n", name, sensor.Kind, formatMetric(sensor.Value, 3))
	}
	for _, fan := range snapshot.Fans {
		name := fan.Name
		if fan.Chip != "" {
			name = fan.Chip + "/" + fan.Name
		}
		fmt.Fprintf(writer, "Fan %s: %s, target %s, PWM %s, mode %s\n", name,
			formatMetric(fan.Speed, 0), formatMetric(fan.TargetSpeed, 0),
			formatMetric(fan.PWM, 1), formatTextMetric(fan.ControlMode))
	}
	if len(snapshot.Thermals) > 0 {
		fmt.Fprint(writer, "Thermal:")
		for _, zone := range snapshot.Thermals {
			fmt.Fprintf(writer, "  %s %s", zone.Name, formatMetric(zone.Temperature, 1))
			if trip := firstThermalTrip(zone); trip != nil {
				fmt.Fprintf(writer, " next=%s:%s", fallback(trip.Type, "trip"), formatMetric(trip.Temperature, 0))
			}
		}
		fmt.Fprintln(writer)
	}
	for _, device := range snapshot.Cooling {
		fmt.Fprintf(writer, "Cooling %s: state %s / %s\n", fallback(device.Type, device.Name),
			formatMetricNumber(device.CurrentState, 0), formatMetricNumber(device.MaximumState, 0))
	}
	for _, warning := range snapshot.Warnings {
		fmt.Fprintf(writer, "warning: %s: %s\n", warning.Component, warning.Message)
	}
}

func formatMetric(metric model.Metric, precision int) string {
	if metric.Status != model.Available || metric.Value == nil {
		return "[" + string(metric.Status) + "]"
	}
	value := *metric.Value
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "[unavailable]"
	}
	return strconv.FormatFloat(value, 'f', precision, 64) + metric.Unit
}

func formatMetricNumber(metric model.Metric, precision int) string {
	if metric.Status != model.Available || metric.Value == nil {
		return "[" + string(metric.Status) + "]"
	}
	value := *metric.Value
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "[unavailable]"
	}
	return strconv.FormatFloat(value, 'f', precision, 64)
}

func formatTextMetric(metric model.TextMetric) string {
	if metric.Status != model.Available || metric.Value == nil {
		return "[" + string(metric.Status) + "]"
	}
	return *metric.Value
}

func formatTextMetricValue(metric model.TextMetric) string {
	if metric.Status != model.Available || metric.Value == nil {
		return ""
	}
	return *metric.Value
}

func formatFrequencyPolicy(policy *model.FrequencyPolicy) string {
	if policy == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	if minimum, maximum := metricValue(policy.Minimum), metricValue(policy.Maximum); minimum >= 0 || maximum >= 0 {
		parts = append(parts, formatFrequencyRange(minimum, maximum))
	}
	if minimum, maximum := metricValue(policy.HardwareMinimum), metricValue(policy.HardwareMaximum); minimum >= 0 || maximum >= 0 {
		parts = append(parts, "hardware "+formatFrequencyRange(minimum, maximum))
	}
	if governor := formatTextMetricValue(policy.Governor); governor != "" {
		parts = append(parts, "governor "+governor)
	}
	return strings.Join(parts, ", ")
}

func formatFrequencyRange(minimum, maximum float64) string {
	switch {
	case minimum >= 0 && maximum >= 0:
		return fmt.Sprintf("%.0f-%.0fMHz", minimum, maximum)
	case maximum >= 0:
		return fmt.Sprintf("max %.0fMHz", maximum)
	default:
		return fmt.Sprintf("min %.0fMHz", minimum)
	}
}

func metricValue(metric model.Metric) float64 {
	if metric.Status != model.Available || metric.Value == nil ||
		math.IsNaN(*metric.Value) || math.IsInf(*metric.Value, 0) {
		return -1
	}
	return *metric.Value
}

func firstThermalTrip(zone model.ThermalZone) *model.ThermalTrip {
	selected := -1
	selectedTemperature := -1.0
	for index := range zone.Trips {
		value := metricValue(zone.Trips[index].Temperature)
		if value >= 0 && (selected < 0 || value < selectedTemperature) {
			selected, selectedTemperature = index, value
		}
	}
	if selected < 0 {
		return nil
	}
	return &zone.Trips[selected]
}

func fallback(value, replacement string) string {
	if value == "" {
		return replacement
	}
	return value
}
