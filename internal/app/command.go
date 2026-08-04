package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
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
}

func NewCommand(version, commit, date string, stdout, stderr io.Writer) *cobra.Command {
	config := runtimeConfig{
		root:              "/",
		interval:          time.Second,
		cpuSampleDuration: 200 * time.Millisecond,
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
	command.PersistentFlags().DurationVar(&config.cpuSampleDuration, "cpu-sample", config.cpuSampleDuration, "CPU usage sampling window")

	command.AddCommand(newInfoCommand(&config, stdout))
	command.AddCommand(newSnapshotCommand(&config, stdout))
	command.AddCommand(newDoctorCommand(&config, stdout))
	return command
}

func runTUI(ctx context.Context, config runtimeConfig, stdout io.Writer) error {
	if err := validateSampling(config, true); err != nil {
		return err
	}
	systemFS, err := sysfs.New(config.root)
	if err != nil {
		return err
	}
	monitor := NewMonitor(systemFS, config.cpuSampleDuration)
	scheduler := NewScheduler(monitor.Snapshot, config.interval, periodicSnapshotTimeout(config))
	tuiContext, cancel := context.WithCancel(ctx)
	defer cancel()
	return ui.Run(tuiContext, scheduler.Run(tuiContext), scheduler.Refresh, stdout)
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
	command := &cobra.Command{
		Use:   "snapshot",
		Short: "Collect a system snapshot",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runSnapshot(command.Context(), *config, stdout, asJSON || watch, watch)
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	command.Flags().BoolVarP(&watch, "watch", "w", false, "continuously output newline-delimited JSON snapshots")
	return command
}

func newDoctorCommand(config *runtimeConfig, stdout io.Writer) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose platform detection, drivers, permissions and metric sources",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := validateSampling(*config, false); err != nil {
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

func runSnapshot(ctx context.Context, config runtimeConfig, stdout io.Writer, asJSON, watch bool) error {
	if err := validateSampling(config, watch); err != nil {
		return err
	}
	systemFS, err := sysfs.New(config.root)
	if err != nil {
		return err
	}
	monitor := NewMonitor(systemFS, config.cpuSampleDuration)
	if watch {
		scheduler := NewScheduler(monitor.Snapshot, config.interval, periodicSnapshotTimeout(config))
		for snapshot := range scheduler.Run(ctx) {
			if err := writeJSON(stdout, snapshot, false); err != nil {
				return err
			}
		}
		return nil
	}
	snapshot := monitor.Snapshot(ctx)
	if asJSON {
		return writeJSON(stdout, snapshot, true)
	}
	printSnapshot(stdout, snapshot)
	return nil
}

func validateSampling(config runtimeConfig, requireInterval bool) error {
	if requireInterval && config.interval <= 0 {
		return errors.New("--interval must be greater than zero")
	}
	if config.cpuSampleDuration <= 0 {
		return errors.New("--cpu-sample must be greater than zero")
	}
	return nil
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
	fmt.Fprintf(writer, "Memory: %s used / %s total  Swap: %s used\n",
		formatMetric(snapshot.Memory.Used, 0),
		formatMetric(snapshot.Memory.Total, 0),
		formatMetric(snapshot.Memory.SwapUsed, 0),
	)
	fmt.Fprintf(writer, "Load: %s %s %s\n",
		formatMetric(snapshot.Load.OneMinute, 2),
		formatMetric(snapshot.Load.FiveMinutes, 2),
		formatMetric(snapshot.Load.FifteenMinutes, 2),
	)
	if len(snapshot.Thermals) > 0 {
		fmt.Fprint(writer, "Thermal:")
		for _, zone := range snapshot.Thermals {
			fmt.Fprintf(writer, "  %s %s", zone.Name, formatMetric(zone.Temperature, 1))
		}
		fmt.Fprintln(writer)
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

func fallback(value, replacement string) string {
	if value == "" {
		return replacement
	}
	return value
}
