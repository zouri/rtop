package app

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	swapinfo "rtop/internal/swap"
	"rtop/internal/sysfs"
	"rtop/internal/tuner"
)

func newTuneCommand(config *runtimeConfig, stdout io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use:   "tune",
		Short: "Explicitly adjust supported system controls",
		Args:  cobra.NoArgs,
	}
	command.AddCommand(newTuneCPUCommand(config, stdout))
	command.AddCommand(newTuneSwapCommand(config, stdout))
	return command
}

func newTuneCPUCommand(config *runtimeConfig, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:       "cpu <performance|powersave>",
		Short:     "Set every CPUFreq policy to performance or powersave",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"performance", "powersave"},
		RunE: func(_ *cobra.Command, args []string) error {
			profile, err := tuner.ParseCPUProfile(args[0])
			if err != nil {
				return err
			}
			controller, err := newTuningController(config.root)
			if err != nil {
				return err
			}
			result, err := controller.SetCPUProfile(profile)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "CPU mode set to %s (%d/%d policies changed)\n",
				result.Profile, result.Changed, len(result.Policies))
			return nil
		},
	}
}

func newTuneSwapCommand(config *runtimeConfig, stdout io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use:   "swap",
		Short: "Inspect or adjust runtime Swap policy and total capacity",
		Args:  cobra.NoArgs,
	}
	command.AddCommand(newTuneSwapListCommand(config, stdout))
	command.AddCommand(newTuneSwapSizeCommand(config, stdout))
	command.AddCommand(newTuneSwappinessCommand(config, stdout))
	return command
}

func newTuneSwapListCommand(config *runtimeConfig, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List active Swap backends detected from /proc/swaps",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			systemFS, err := sysfs.New(config.root)
			if err != nil {
				return err
			}
			entries, err := swapinfo.Discover(systemFS)
			if err != nil {
				return fmt.Errorf("discover active swap: %w", err)
			}
			total, err := swapinfo.TotalBytes(entries)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "Total Swap: %s (%d active backend%s)\n",
				tuner.FormatSwapSize(total), len(entries), pluralSuffix(len(entries)))
			if len(entries) == 0 {
				return nil
			}
			fmt.Fprintln(stdout, "TYPE\tSIZE\tUSED\tPRIORITY\tPATH")
			for _, entry := range entries {
				fmt.Fprintf(stdout, "%s\t%s\t%s\t%d\t%s\n",
					entry.Kind, tuner.FormatSwapSize(entry.SizeBytes), tuner.FormatSwapSize(entry.UsedBytes),
					entry.Priority, printableSwapPath(entry.Path),
				)
			}
			return nil
		},
	}
}

func newTuneSwapSizeCommand(config *runtimeConfig, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "size <total-size>",
		Short: "Set total runtime Swap capacity (for example 512M, 4G, 1.5GiB, or 0)",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			bytes, err := tuner.ParseSwapSize(args[0])
			if err != nil {
				return err
			}
			controller, err := newTuningController(config.root)
			if err != nil {
				return err
			}
			result, err := controller.SetSwapSize(command.Context(), bytes)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "Total Swap: %s -> %s (%s; %d active backend%s)\n",
				tuner.FormatSwapSize(result.BeforeBytes), tuner.FormatSwapSize(result.AfterBytes),
				result.Strategy, len(result.Devices), pluralSuffix(len(result.Devices)),
			)
			return nil
		},
	}
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func printableSwapPath(path string) string {
	if strings.IndexFunc(path, func(character rune) bool {
		return character <= ' ' || character == 0x7f
	}) >= 0 {
		return strconv.QuoteToASCII(path)
	}
	return path
}

func newTuneSwappinessCommand(config *runtimeConfig, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "swappiness <0-200>",
		Short: "Set runtime vm.swappiness in the Linux range 0-200",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			value, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("swappiness must be an integer between %d and %d", tuner.SwappinessMinimum, tuner.SwappinessMaximum)
			}
			controller, err := newTuningController(config.root)
			if err != nil {
				return err
			}
			if err := controller.SetSwappiness(value); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "vm.swappiness set to %d\n", value)
			return nil
		},
	}
}

func newTuningController(root string) (*tuner.Controller, error) {
	systemFS, err := sysfs.New(root)
	if err != nil {
		return nil, err
	}
	executor := tuner.NewLinuxSwapExecutor(root, systemFS)
	return tuner.NewWithSwapExecutor(systemFS, executor), nil
}
