package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"rtop/internal/config"
	"rtop/internal/doctor"
	"rtop/internal/exporter"
	"rtop/internal/model"
	"rtop/internal/sysfs"
	"rtop/internal/ui"
)

type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

func NewCommand(build BuildInfo, stdout, stderr io.Writer) *cobra.Command {
	runtimeConfig := config.Default()
	command := &cobra.Command{
		Use:           "rtop",
		Short:         "Read-only Rockchip Linux system monitor",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       build.Version,
		RunE: func(command *cobra.Command, _ []string) error {
			return runTUI(command.Context(), runtimeConfig, stdout)
		},
	}
	command.SetOut(stdout)
	command.SetErr(stderr)
	command.SetVersionTemplate(fmt.Sprintf(
		"rtop %s (commit %s, built %s)\n", build.Version, build.Commit, build.Date,
	))
	command.PersistentFlags().StringVar(&runtimeConfig.Root, "root", runtimeConfig.Root, "target system root (for fixtures and containers)")
	command.PersistentFlags().DurationVar(&runtimeConfig.Interval, "interval", runtimeConfig.Interval, "TUI and snapshot watch refresh interval")
	command.PersistentFlags().DurationVar(&runtimeConfig.CPUSampleDuration, "cpu-sample", runtimeConfig.CPUSampleDuration, "CPU usage sampling window")
	command.PersistentFlags().StringVar(&runtimeConfig.LogLevel, "log-level", runtimeConfig.LogLevel, "log level: debug, info, warn, error")

	command.PersistentPreRunE = func(_ *cobra.Command, _ []string) error {
		if runtimeConfig.Interval <= 0 {
			return errors.New("--interval must be greater than zero")
		}
		if runtimeConfig.CPUSampleDuration <= 0 {
			return errors.New("--cpu-sample must be greater than zero")
		}
		level, err := parseLogLevel(runtimeConfig.LogLevel)
		if err != nil {
			return err
		}
		slog.SetDefault(slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level})))
		return nil
	}

	command.AddCommand(newInfoCommand(&runtimeConfig, stdout))
	command.AddCommand(newSnapshotCommand(&runtimeConfig, stdout))
	command.AddCommand(newDoctorCommand(&runtimeConfig, stdout))
	return command
}

func runTUI(ctx context.Context, runtimeConfig config.Runtime, stdout io.Writer) error {
	systemFS, err := sysfs.New(runtimeConfig.Root)
	if err != nil {
		return err
	}
	builder := NewBuilder(systemFS, runtimeConfig.CPUSampleDuration)
	scheduler := NewScheduler(builder, runtimeConfig.Interval, max(runtimeConfig.Interval, time.Second))
	tuiContext, cancel := context.WithCancel(ctx)
	defer cancel()
	refresh := func() {
		builder.Reprobe()
		scheduler.Refresh()
	}
	return ui.Run(tuiContext, scheduler.Run(tuiContext), refresh, stdout)
}

func newInfoCommand(runtimeConfig *config.Runtime, stdout io.Writer) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "info",
		Short: "Show board, SoC, kernel and operating system information",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			systemFS, err := sysfs.New(runtimeConfig.Root)
			if err != nil {
				return err
			}
			builder := NewBuilder(systemFS, runtimeConfig.CPUSampleDuration)
			info, warnings := builder.SystemInfo()
			if asJSON {
				return exporter.WriteJSON(stdout, struct {
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

func newSnapshotCommand(runtimeConfig *config.Runtime, stdout io.Writer) *cobra.Command {
	var asJSON bool
	var watch bool
	command := &cobra.Command{
		Use:   "snapshot",
		Short: "Collect a system snapshot",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runSnapshot(command.Context(), *runtimeConfig, stdout, asJSON || watch, watch)
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	command.Flags().BoolVarP(&watch, "watch", "w", false, "continuously output newline-delimited JSON snapshots")
	return command
}

func newDoctorCommand(runtimeConfig *config.Runtime, stdout io.Writer) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose platform detection, drivers, permissions and metric sources",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			systemFS, err := sysfs.New(runtimeConfig.Root)
			if err != nil {
				return err
			}
			builder := NewBuilder(systemFS, runtimeConfig.CPUSampleDuration)
			report := doctor.Analyze(systemFS, builder.Build(command.Context()))
			if asJSON {
				return exporter.WriteJSON(stdout, report, true)
			}
			return doctor.WriteText(stdout, report)
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return command
}

func runSnapshot(ctx context.Context, runtimeConfig config.Runtime, stdout io.Writer, asJSON, watch bool) error {
	systemFS, err := sysfs.New(runtimeConfig.Root)
	if err != nil {
		return err
	}
	builder := NewBuilder(systemFS, runtimeConfig.CPUSampleDuration)
	if watch {
		scheduler := NewScheduler(builder, runtimeConfig.Interval, max(runtimeConfig.Interval, time.Second))
		for snapshot := range scheduler.Run(ctx) {
			if err := exporter.WriteJSON(stdout, snapshot, false); err != nil {
				return err
			}
		}
		return nil
	}
	for {
		snapshot := builder.Build(ctx)
		if asJSON {
			if err := exporter.WriteJSON(stdout, snapshot, !watch); err != nil {
				return err
			}
		} else {
			printSnapshot(stdout, snapshot)
		}
		return nil
	}
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

func parseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(value) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid --log-level %q", value)
	}
}

func fallback(value, replacement string) string {
	if value == "" {
		return replacement
	}
	return value
}
