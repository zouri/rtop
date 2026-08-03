package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"rtop/internal/model"
)

var (
	colorPrimary = lipgloss.AdaptiveColor{Light: "#005F87", Dark: "#5FD7FF"}
	colorMuted   = lipgloss.AdaptiveColor{Light: "#626262", Dark: "#808080"}
	colorGood    = lipgloss.AdaptiveColor{Light: "#00875F", Dark: "#5FD787"}
	colorWarn    = lipgloss.AdaptiveColor{Light: "#AF5F00", Dark: "#FFAF5F"}
	colorBad     = lipgloss.AdaptiveColor{Light: "#AF0000", Dark: "#FF5F5F"}
	colorPanel   = lipgloss.AdaptiveColor{Light: "#D7D7D7", Dark: "#444444"}

	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(colorPrimary)
	mutedStyle = lipgloss.NewStyle().Foreground(colorMuted)
	goodStyle  = lipgloss.NewStyle().Foreground(colorGood)
	warnStyle  = lipgloss.NewStyle().Foreground(colorWarn)
	badStyle   = lipgloss.NewStyle().Foreground(colorBad)
	cardStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorPanel).Padding(0, 1)
)

func (m Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return "rtop: starting…"
	}
	header := m.renderHeader()
	foot := m.renderFooter()
	bodyHeight := max(1, m.height-lipgloss.Height(header)-lipgloss.Height(foot))
	body := m.renderBody(bodyHeight)
	body = lipgloss.NewStyle().
		Width(max(1, m.width)).
		MaxWidth(max(1, m.width)).
		Height(bodyHeight).
		MaxHeight(bodyHeight).
		Render(body)
	return header + body + foot
}

func (m Model) renderHeader() string {
	name := titleStyle.Render("rtop")
	stamp := "waiting for first snapshot"
	if m.hasData {
		stamp = m.latest.Timestamp.Format("15:04:05")
	}
	status := mutedStyle.Render(stamp)
	top := lipgloss.JoinHorizontal(lipgloss.Top, name, "  ", status)
	if m.width >= 72 {
		tabs := []struct {
			label string
			page  page
		}{
			{"1 Overview", pageOverview},
			{"2 Engines", pageEngines},
			{"3 Processes", pageProcesses},
			{"4 Thermal", pageThermal},
			{"5 System", pageSystem},
			{"? Help", pageHelp},
		}
		labels := make([]string, 0, len(tabs))
		for _, tab := range tabs {
			style := mutedStyle
			if tab.page == m.page {
				style = titleStyle
			}
			labels = append(labels, style.Render(tab.label))
		}
		top += "\n" + strings.Join(labels, "  ")
	} else {
		top += "\n" + mutedStyle.Render("1 O  2 E  3 P  4 T  5 S  ?")
	}
	return lipgloss.NewStyle().Width(m.width).MaxWidth(m.width).Render(top) + "\n"
}

func (m Model) renderFooter() string {
	if m.page == pageHelp {
		return ""
	}
	return "\n" + ansi.Truncate(mutedStyle.Render(m.help.View(m.keys)), m.width, "")
}

func (m Model) renderBody(height int) string {
	if !m.hasData {
		return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, "Collecting system metrics…")
	}
	switch m.page {
	case pageEngines:
		return m.renderEngines(height)
	case pageProcesses:
		return m.renderProcesses(height)
	case pageThermal:
		return m.renderThermal(height)
	case pageSystem:
		return m.renderSystem(height)
	case pageHelp:
		return m.renderHelp()
	default:
		return m.renderOverview(height)
	}
}

func (m Model) renderOverview(height int) string {
	contentWidth := max(20, m.width)
	if m.width >= 72 && height < 26 {
		leftWidth := (m.width - 2) / 2
		rightWidth := m.width - leftWidth - 2
		left := m.cpuCardCompact(leftWidth)
		right := m.memoryCardCompact(rightWidth)
		top := lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)
		return top + "\n" + m.summaryCard(contentWidth)
	}
	if m.width >= 88 {
		leftWidth := (m.width - 2) / 2
		rightWidth := m.width - leftWidth - 2
		left := m.cpuCard(leftWidth)
		right := m.memoryCard(rightWidth)
		top := lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)
		bottom := m.summaryCard(contentWidth)
		return top + "\n" + bottom
	}
	return m.cpuCard(contentWidth) + "\n" + m.memoryCard(contentWidth) + "\n" + m.summaryCard(contentWidth)
}

func (m Model) cpuCardCompact(width int) string {
	inner := max(8, width-4)
	values := make([]float64, 0, len(m.history))
	for _, point := range m.history {
		values = append(values, point.cpu)
	}
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("CPU"))
	builder.WriteString(fmt.Sprintf("\nTotal %-8s %s", formatMetric(m.latest.CPU.Overall, 1), sparkline(values, max(4, inner-16))))
	const coresPerRow = 3
	for index, core := range m.latest.CPU.Cores {
		if index%coresPerRow == 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(fmt.Sprintf("c%d %-6s", core.ID, formatMetric(core.Usage, 0)))
	}
	return panel(builder.String(), width)
}

func (m Model) cpuCard(width int) string {
	inner := max(8, width-4)
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("CPU"))
	builder.WriteString("\n")
	builder.WriteString(fmt.Sprintf("Total %-9s %s", formatMetric(m.latest.CPU.Overall, 1), progress(m.latest.CPU.Overall, max(4, inner-20))))
	values := make([]float64, 0, len(m.history))
	for _, point := range m.history {
		values = append(values, point.cpu)
	}
	if len(values) > 1 {
		builder.WriteString("\nHistory ")
		builder.WriteString(sparkline(values, max(4, inner-8)))
	}
	for _, core := range m.latest.CPU.Cores {
		builder.WriteString("\n")
		label := fmt.Sprintf("cpu%d", core.ID)
		if core.Cluster != "" && inner >= 40 {
			label += " " + core.Cluster
		}
		builder.WriteString(fmt.Sprintf("%-13s %7s  %8s", label, formatMetric(core.Usage, 1), formatMetric(core.Frequency, 0)))
	}
	return panel(builder.String(), width)
}

func (m Model) memoryCard(width int) string {
	inner := max(8, width-4)
	percent := memoryPercent(m.latest.Memory)
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("Memory & Load"))
	builder.WriteString("\n")
	builder.WriteString(fmt.Sprintf("Used  %s / %s", formatMetric(m.latest.Memory.Used, 0), formatMetric(m.latest.Memory.Total, 0)))
	builder.WriteString("\n")
	builder.WriteString(progressValue(percent, max(4, inner)))
	builder.WriteString("\n")
	builder.WriteString(fmt.Sprintf("Free  %s", formatMetric(m.latest.Memory.Available, 0)))
	builder.WriteString("\n")
	builder.WriteString(fmt.Sprintf("Swap  %s / %s", formatMetric(m.latest.Memory.SwapUsed, 0), formatMetric(m.latest.Memory.SwapTotal, 0)))
	builder.WriteString("\n\n")
	builder.WriteString(titleStyle.Render("Load Average"))
	builder.WriteString("\n")
	builder.WriteString(fmt.Sprintf("1m %s   5m %s   15m %s", formatMetric(m.latest.Load.OneMinute, 2), formatMetric(m.latest.Load.FiveMinutes, 2), formatMetric(m.latest.Load.FifteenMinutes, 2)))
	builder.WriteString("\n")
	builder.WriteString(fmt.Sprintf("Tasks %s / %s", formatMetric(m.latest.Load.Runnable, 0), formatMetric(m.latest.Load.Tasks, 0)))
	return panel(builder.String(), width)
}

func (m Model) memoryCardCompact(width int) string {
	inner := max(8, width-4)
	percent := memoryPercent(m.latest.Memory)
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("Memory & Load"))
	builder.WriteString(fmt.Sprintf("\n%s / %s", formatMetric(m.latest.Memory.Used, 0), formatMetric(m.latest.Memory.Total, 0)))
	builder.WriteString("\n")
	builder.WriteString(progressValue(percent, inner))
	builder.WriteString(fmt.Sprintf("\nLoad %s %s %s", formatMetric(m.latest.Load.OneMinute, 2), formatMetric(m.latest.Load.FiveMinutes, 2), formatMetric(m.latest.Load.FifteenMinutes, 2)))
	builder.WriteString(fmt.Sprintf("\nSwap %s", formatMetric(m.latest.Memory.SwapUsed, 0)))
	return panel(builder.String(), width)
}

func (m Model) summaryCard(width int) string {
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("Engines"))
	for _, accelerator := range m.latest.Accelerators {
		builder.WriteString(fmt.Sprintf("\n%-10s %9s  %9s", accelerator.Name, formatMetric(accelerator.Usage, 1), formatMetric(accelerator.Frequency, 0)))
	}
	builder.WriteString("\n\n")
	builder.WriteString(titleStyle.Render("Thermal summary"))
	if len(m.latest.Thermals) == 0 {
		builder.WriteString("\n")
		builder.WriteString(mutedStyle.Render("No thermal source discovered"))
	} else {
		limit := min(2, len(m.latest.Thermals))
		for _, zone := range m.latest.Thermals[:limit] {
			builder.WriteString(fmt.Sprintf("\n%-24s %s", truncateText(zone.Name, 24), formatMetric(zone.Temperature, 1)))
		}
	}
	if len(m.latest.Warnings) > 0 {
		builder.WriteString("\n")
		builder.WriteString(warnStyle.Render(fmt.Sprintf("%d collection warning(s); see System", len(m.latest.Warnings))))
	}
	return panel(builder.String(), width)
}

func (m Model) renderEngines(_ int) string {
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("Engines"))
	if len(m.latest.Accelerators) == 0 {
		builder.WriteString("\n")
		builder.WriteString(mutedStyle.Render("No accelerator providers configured"))
		return builder.String()
	}
	for _, accelerator := range m.latest.Accelerators {
		builder.WriteString("\n")
		builder.WriteString(enginePanel(accelerator, m.engineHistory(accelerator.Type), m.width))
	}
	return builder.String()
}

func enginePanel(accelerator model.Accelerator, history []float64, width int) string {
	inner := max(8, width-4)
	var builder strings.Builder
	builder.WriteString(titleStyle.Render(accelerator.Name))
	builder.WriteString(fmt.Sprintf("  load %s  frequency %s", formatMetric(accelerator.Usage, 1), formatMetric(accelerator.Frequency, 0)))
	if width >= 48 {
		builder.WriteString("\n")
		builder.WriteString(progress(accelerator.Usage, inner))
	}
	if len(history) > 1 {
		builder.WriteString("\nHistory ")
		builder.WriteString(sparkline(history, max(4, inner-8)))
	}
	if len(accelerator.Cores) > 0 && width >= 70 {
		builder.WriteString("\n")
		for index, core := range accelerator.Cores {
			if index > 0 {
				builder.WriteString("  ")
			}
			builder.WriteString(fmt.Sprintf("C%d %s", core.ID, formatMetric(core.Usage, 1)))
		}
	} else {
		for _, core := range accelerator.Cores {
			builder.WriteString(fmt.Sprintf("\nCore %-2d  %-12s %s", core.ID, formatMetric(core.Usage, 1), statusText(core.Usage)))
		}
	}
	return panel(builder.String(), width)
}

func (m Model) engineHistory(acceleratorType string) []float64 {
	values := make([]float64, 0, len(m.history))
	for _, point := range m.history {
		switch acceleratorType {
		case "gpu":
			values = append(values, point.gpu)
		case "npu":
			values = append(values, point.npu)
		case "ddr":
			values = append(values, point.ddr)
		}
	}
	return values
}

func (m Model) renderProcesses(height int) string {
	processes := append([]model.ProcessInfo(nil), m.latest.Processes...)
	sort.SliceStable(processes, func(i, j int) bool {
		var left, right float64
		if m.processSort == sortMemory {
			left, right = metricNumber(processes[i].Memory), metricNumber(processes[j].Memory)
		} else {
			left, right = metricNumber(processes[i].CPU), metricNumber(processes[j].CPU)
		}
		if left == right {
			return processes[i].PID < processes[j].PID
		}
		return left > right
	})
	if len(processes) == 0 {
		return mutedStyle.Render("No readable processes")
	}
	rows := max(1, height-3)
	start := 0
	if m.selected >= rows {
		start = m.selected - rows + 1
	}
	end := min(len(processes), start+rows)
	nameWidth := max(8, m.width-39)
	full := m.width >= 66
	medium := m.width >= 48
	var builder strings.Builder
	sortName := "CPU"
	if m.processSort == sortMemory {
		sortName = "memory"
	}
	builder.WriteString(titleStyle.Render("Processes"))
	builder.WriteString(mutedStyle.Render("  sorted by " + sortName))
	builder.WriteString("\n")
	if full {
		builder.WriteString(fmt.Sprintf("%-7s %-*s %8s %11s %7s", "PID", nameWidth, "NAME", "CPU", "MEMORY", "THREADS"))
	} else if medium {
		nameWidth = max(8, m.width-27)
		builder.WriteString(fmt.Sprintf("%-7s %-*s %8s %10s", "PID", nameWidth, "NAME", "CPU", "MEMORY"))
	} else {
		nameWidth = max(6, m.width-18)
		builder.WriteString(fmt.Sprintf("%-6s %-*s %8s", "PID", nameWidth, "NAME", "CPU"))
	}
	for index := start; index < end; index++ {
		process := processes[index]
		var row string
		if full {
			row = fmt.Sprintf("%-7d %-*s %8s %11s %7d", process.PID, nameWidth, truncateText(process.Name, nameWidth), formatMetric(process.CPU, 1), formatMetric(process.Memory, 1), process.Threads)
		} else if medium {
			row = fmt.Sprintf("%-7d %-*s %8s %10s", process.PID, nameWidth, truncateText(process.Name, nameWidth), formatMetric(process.CPU, 1), formatMetric(process.Memory, 1))
		} else {
			row = fmt.Sprintf("%-6d %-*s %8s", process.PID, nameWidth, truncateText(process.Name, nameWidth), formatMetric(process.CPU, 1))
		}
		builder.WriteString("\n")
		if index == m.selected {
			builder.WriteString(lipgloss.NewStyle().Reverse(true).Render(truncateText(row, m.width)))
		} else {
			builder.WriteString(truncateText(row, m.width))
		}
	}
	return builder.String()
}

func (m Model) renderThermal(_ int) string {
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("Thermal"))
	if len(m.latest.Thermals) == 0 {
		builder.WriteString("\n")
		builder.WriteString(mutedStyle.Render("No thermal or hwmon sensors discovered"))
		return builder.String()
	}
	for _, zone := range m.latest.Thermals {
		if m.width >= 72 {
			builder.WriteString(fmt.Sprintf("\n%-28s %-18s %9s  %s", truncateText(zone.Name, 28), truncateText(zone.Type, 18), formatMetric(zone.Temperature, 1), statusText(zone.Temperature)))
		} else {
			nameWidth := max(8, m.width-14)
			builder.WriteString(fmt.Sprintf("\n%-*s %10s", nameWidth, truncateText(zone.Name, nameWidth), formatMetric(zone.Temperature, 1)))
		}
	}
	return builder.String()
}

func (m Model) renderSystem(_ int) string {
	info := m.latest.System
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("System"))
	rows := [][2]string{
		{"Hostname", info.Hostname},
		{"OS", info.OS},
		{"Kernel", info.Kernel},
		{"Architecture", info.Arch},
		{"Board", info.Board},
		{"SoC", info.SoC},
		{"Compatible", strings.Join(info.Compatible, ", ")},
	}
	for _, row := range rows {
		builder.WriteString(fmt.Sprintf("\n%-13s %s", row[0], fallback(row[1], "unknown")))
	}
	if len(info.Drivers) > 0 {
		builder.WriteString("\n\n")
		builder.WriteString(titleStyle.Render("Drivers"))
		for _, driver := range info.Drivers {
			details := driver.Name
			if driver.Governor != "" {
				details += " / " + driver.Governor
			}
			builder.WriteString(fmt.Sprintf("\n%-13s %s (%s)", strings.ToUpper(driver.Type), details, driver.Device))
		}
	}
	builder.WriteString("\n\n")
	builder.WriteString(titleStyle.Render("Capabilities"))
	builder.WriteString(fmt.Sprintf("\nCPU          %s", statusText(m.latest.CPU.Overall)))
	builder.WriteString(fmt.Sprintf("\nMemory       %s", statusText(m.latest.Memory.Total)))
	thermalStatus := mutedStyle.Render("unsupported")
	if len(m.latest.Thermals) > 0 {
		thermalStatus = goodStyle.Render("available")
	}
	builder.WriteString("\nThermal      " + thermalStatus)
	builder.WriteString(fmt.Sprintf("\nProcesses    %s", goodStyle.Render(strconv.Itoa(len(m.latest.Processes))+" readable")))
	for _, accelerator := range m.latest.Accelerators {
		label := strings.ToUpper(accelerator.Type)
		builder.WriteString(fmt.Sprintf("\n%-13s %s", label, statusText(accelerator.Frequency)))
	}
	if len(m.latest.Warnings) > 0 {
		builder.WriteString("\n\n")
		builder.WriteString(warnStyle.Render("Warnings"))
		for _, warning := range m.latest.Warnings {
			builder.WriteString(fmt.Sprintf("\n%s: %s", warning.Component, warning.Message))
		}
	}
	return builder.String()
}

func (m Model) renderHelp() string {
	helper := m.help
	helper.ShowAll = true
	return titleStyle.Render("Help") + "\n\n" + helper.View(m.keys)
}

func panel(content string, width int) string {
	// Lip Gloss Width includes padding but not the two border cells.
	return cardStyle.Width(max(1, width-2)).Render(content)
}

func formatMetric(metric model.Metric, precision int) string {
	if metric.Status != model.Available || metric.Value == nil {
		return "[" + string(metric.Status) + "]"
	}
	return strconv.FormatFloat(*metric.Value, 'f', precision, 64) + metric.Unit
}

func statusText(metric model.Metric) string {
	switch metric.Status {
	case model.Available:
		return goodStyle.Render(string(metric.Status))
	case model.PermissionDenied:
		return warnStyle.Render(string(metric.Status))
	case model.Unsupported:
		return mutedStyle.Render(string(metric.Status))
	default:
		return badStyle.Render(string(metric.Status))
	}
}

func progress(metric model.Metric, width int) string {
	return progressValue(metricNumber(metric), width)
}

func progressValue(value float64, width int) string {
	width = max(1, width)
	if value < 0 {
		return mutedStyle.Render(strings.Repeat("·", width))
	}
	value = min(100, max(0, value))
	filled := int(value / 100 * float64(width))
	return goodStyle.Render(strings.Repeat("█", filled)) + mutedStyle.Render(strings.Repeat("░", width-filled))
}

func sparkline(values []float64, width int) string {
	const blocks = "▁▂▃▄▅▆▇█"
	blockRunes := []rune(blocks)
	if width <= 0 || len(values) == 0 {
		return ""
	}
	if len(values) > width {
		values = values[len(values)-width:]
	}
	var builder strings.Builder
	for _, value := range values {
		if value < 0 {
			builder.WriteRune('·')
			continue
		}
		value = min(100, max(0, value))
		index := int(value / 100 * float64(len(blockRunes)-1))
		builder.WriteRune(blockRunes[index])
	}
	return titleStyle.Render(builder.String())
}

func memoryPercent(memory model.MemoryInfo) float64 {
	total, used := metricNumber(memory.Total), metricNumber(memory.Used)
	if total <= 0 || used < 0 {
		return -1
	}
	return used * 100 / total
}

func metricNumber(metric model.Metric) float64 {
	if metric.Status != model.Available || metric.Value == nil {
		return -1
	}
	return *metric.Value
}

func truncateText(value string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

func fallback(value, replacement string) string {
	if value == "" {
		return replacement
	}
	return value
}
