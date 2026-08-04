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
	barFill    = lipgloss.NewStyle().Background(colorGood)
	barTrack   = lipgloss.NewStyle().Background(colorPanel)
)

func (m Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return "rtop: starting…"
	}
	header := m.renderHeader()
	footer := m.renderFooter()
	bodyHeight := max(1, m.height-lipgloss.Height(header)-lipgloss.Height(footer))
	body := m.renderBody(bodyHeight)
	body = lipgloss.NewStyle().
		Width(max(1, m.width)).
		MaxWidth(max(1, m.width)).
		Height(bodyHeight).
		MaxHeight(bodyHeight).
		Render(body)
	return header + body + footer
}

func (m Model) renderHeader() string {
	stamp := "waiting for metrics"
	if m.hasData {
		stamp = m.latest.Timestamp.Format("15:04:05")
	}
	identity := ""
	if m.hasData {
		parts := make([]string, 0, 3)
		if m.latest.System.Hostname != "" {
			parts = append(parts, m.latest.System.Hostname)
		}
		if m.latest.System.Board != "" {
			parts = append(parts, m.latest.System.Board)
		} else if m.latest.System.SoC != "" {
			parts = append(parts, m.latest.System.SoC)
		}
		identity = strings.Join(parts, " · ")
	}
	status := titleStyle.Render("rtop")
	if identity != "" {
		status += "  " + mutedStyle.Render(identity)
	}
	status += "  " + mutedStyle.Render(stamp)
	if len(m.latest.Warnings) > 0 {
		status += "  " + warnStyle.Render(fmt.Sprintf("!%d", len(m.latest.Warnings)))
	}
	status = ansi.Truncate(status, m.width, "")

	tabs := []struct {
		wide   string
		narrow string
		page   page
	}{
		{"1 ALL", "1 ALL", pageAll},
		{"2 CPU", "2 CPU", pageCPU},
		{"3 MEMORY", "3 MEM", pageMemory},
		{"4 ENGINES", "4 ENG", pageEngines},
		{"5 PROCESSES", "5 PROC", pageProcesses},
		{"6 INFO", "6 INFO", pageInfo},
	}
	labels := make([]string, 0, len(tabs))
	for _, tab := range tabs {
		label := tab.wide
		if m.width < 88 {
			label = tab.narrow
		}
		if tab.page == m.page {
			labels = append(labels, titleStyle.Render("["+label+"]"))
		} else {
			labels = append(labels, mutedStyle.Render(label))
		}
	}
	tabLine := strings.Join(labels, "  ")
	if m.width < 56 {
		current := tabs[int(m.page)].narrow
		tabLine = titleStyle.Render("["+current+"]") + mutedStyle.Render("  1-6 pages  ? help")
	}
	tabLine = ansi.Truncate(tabLine, m.width, "")
	return lipgloss.NewStyle().Width(m.width).MaxWidth(m.width).Render(status+"\n"+tabLine) + "\n"
}

func (m Model) renderFooter() string {
	var hint string
	switch {
	case m.showHelp:
		hint = "j/k scroll  esc close  q quit"
	case m.page == pageProcesses:
		hint = "j/k select  c CPU  m MEM  enter details  r refresh  ? help  q quit"
	default:
		hint = "j/k scroll  r refresh  ? help  q quit"
	}
	return "\n" + ansi.Truncate(mutedStyle.Render(hint), m.width, "")
}

func (m Model) renderBody(height int) string {
	if !m.hasData {
		return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, "Collecting Rockchip metrics…")
	}
	content := m.renderPage(height)
	if m.page == pageProcesses && !m.showHelp {
		return content
	}
	return scrollContent(content, height, m.scrollOffset)
}

func (m Model) renderPage(height int) string {
	if m.showHelp {
		return m.renderHelp()
	}
	switch m.page {
	case pageCPU:
		return m.renderCPU(height)
	case pageMemory:
		return m.renderMemory(height)
	case pageEngines:
		return m.renderEngines(height)
	case pageProcesses:
		return m.renderProcesses(height)
	case pageInfo:
		return m.renderInfo(height)
	default:
		return m.renderAll(height)
	}
}

func (m *Model) clampScroll() {
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
	if m.width <= 0 || m.height <= 0 || !m.hasData || (m.page == pageProcesses && !m.showHelp) {
		m.scrollOffset = 0
		return
	}
	headerHeight := lipgloss.Height(m.renderHeader())
	footerHeight := lipgloss.Height(m.renderFooter())
	bodyHeight := max(1, m.height-headerHeight-footerHeight)
	maximum := max(0, lipgloss.Height(m.renderPage(bodyHeight))-bodyHeight)
	if m.scrollOffset > maximum {
		m.scrollOffset = maximum
	}
}

func scrollContent(content string, height, offset int) string {
	lines := strings.Split(content, "\n")
	maximum := max(0, len(lines)-height)
	offset = min(max(0, offset), maximum)
	end := min(len(lines), offset+height)
	return strings.Join(lines[offset:end], "\n")
}

func (m Model) renderAll(_ int) string {
	if m.width >= 72 {
		leftWidth := (m.width - 2) / 2
		rightWidth := m.width - leftWidth - 2
		top := lipgloss.JoinHorizontal(
			lipgloss.Top,
			m.cpuCardCompact(leftWidth), "  ", m.memoryCardCompact(rightWidth),
		)
		bottom := lipgloss.JoinHorizontal(
			lipgloss.Top,
			m.overviewEnginesCard(leftWidth), "  ", m.topProcessesCard(rightWidth, 4, sortCPU),
		)
		return top + "\n" + bottom
	}
	return m.cpuCardCompact(m.width) + "\n" +
		m.memoryCardCompact(m.width) + "\n" +
		m.overviewEnginesCard(m.width) + "\n" +
		m.topProcessesCard(m.width, 4, sortCPU)
}

func (m Model) cpuCardCompact(width int) string {
	inner := max(8, width-4)
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("CPU"))
	builder.WriteString(fmt.Sprintf("  %-8s %s", formatMetricCompact(m.latest.CPU.Overall, 1), sparkline(m.cpuHistory, max(4, inner-17))))
	const coresPerRow = 3
	for index, core := range m.latest.CPU.Cores {
		if index%coresPerRow == 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(fmt.Sprintf("c%d %-7s", core.ID, formatMetricCompact(core.Usage, 0)))
	}
	return panel(builder.String(), width)
}

func (m Model) cpuCard(width int) string {
	inner := max(8, width-4)
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("CPU"))
	builder.WriteString("\n")
	builder.WriteString(fmt.Sprintf("Total %-9s %s", formatMetric(m.latest.CPU.Overall, 1), progress(m.latest.CPU.Overall, max(4, inner-20))))
	if len(m.cpuHistory) > 1 {
		builder.WriteString("\nHistory ")
		builder.WriteString(sparkline(m.cpuHistory, max(4, inner-8)))
	}
	for _, core := range m.latest.CPU.Cores {
		builder.WriteString("\n")
		label := fmt.Sprintf("cpu%d", core.ID)
		if core.Cluster != "" && inner >= 40 {
			label += " " + core.Cluster
		}
		if inner >= 48 {
			barWidth := max(8, inner-32)
			builder.WriteString(fmt.Sprintf("%-13s %s %7s  %8s", label, progress(core.Usage, barWidth), formatMetricCompact(core.Usage, 1), formatMetricCompact(core.Frequency, 0)))
		} else {
			builder.WriteString(fmt.Sprintf("%-13s %7s  %8s", label, formatMetricCompact(core.Usage, 1), formatMetricCompact(core.Frequency, 0)))
			builder.WriteString("\n  " + progress(core.Usage, max(8, inner-2)))
		}
		if summary := frequencyPolicySummary(core.Policy); summary != "" {
			builder.WriteString("\n" + truncateText("  limits "+summary, inner))
		}
	}
	return panel(builder.String(), width)
}

func (m Model) memoryCardCompact(width int) string {
	inner := max(8, width-4)
	percent := memoryPercent(m.latest.Memory)
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("MEMORY"))
	builder.WriteString(fmt.Sprintf("  %s / %s", formatMetric(m.latest.Memory.Used, 0), formatMetric(m.latest.Memory.Total, 0)))
	builder.WriteString("\n")
	builder.WriteString(progressValue(percent, inner))
	builder.WriteString(fmt.Sprintf("\nFree %s  Swap %s", formatMetric(m.latest.Memory.Available, 0), formatMetric(m.latest.Memory.SwapUsed, 0)))
	builder.WriteString(fmt.Sprintf("\nLoad %s %s %s", formatMetricCompact(m.latest.Load.OneMinute, 2), formatMetricCompact(m.latest.Load.FiveMinutes, 2), formatMetricCompact(m.latest.Load.FifteenMinutes, 2)))
	if m.latest.Memory.Pressure.Memory.Some.Avg10.Status == model.Available {
		builder.WriteString(fmt.Sprintf("\nPSI mem %s", formatMetricCompact(m.latest.Memory.Pressure.Memory.Some.Avg10, 2)))
	}
	return panel(builder.String(), width)
}

func (m Model) overviewEnginesCard(width int) string {
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("ROCKCHIP ENGINES"))
	for _, accelerator := range m.latest.Accelerators {
		nameWidth := max(7, min(12, width-25))
		builder.WriteString(fmt.Sprintf("\n%-*s %8s  %8s", nameWidth, truncateText(accelerator.Name, nameWidth), formatMetricCompact(accelerator.Usage, 0), formatMetricCompact(accelerator.Frequency, 0)))
	}
	if zone, ok := hottestThermal(m.latest.Thermals); ok {
		builder.WriteString(fmt.Sprintf("\nHot %-16s %s", truncateText(zone.Name, 16), formatTemperature(zone.Temperature)))
	} else {
		builder.WriteString("\n" + mutedStyle.Render("No thermal source discovered"))
	}
	if sensor, ok := primaryPowerSensor(m.latest.Power); ok {
		builder.WriteString(fmt.Sprintf("\nPower %-14s %s", truncateText(sensor.Name, 14), formatMetricCompact(sensor.Value, 2)))
	}
	if len(m.latest.Fans) > 0 {
		builder.WriteString(fmt.Sprintf("\nFan   %-14s %s", truncateText(m.latest.Fans[0].Name, 14), formatMetricCompact(m.latest.Fans[0].Speed, 0)))
	}
	if len(m.latest.Warnings) > 0 {
		builder.WriteString("\n" + warnStyle.Render(fmt.Sprintf("%d warning(s) · open INFO", len(m.latest.Warnings))))
	}
	return panel(builder.String(), width)
}

func (m Model) topProcessesCard(width, limit int, order processSort) string {
	processes := sortedProcesses(m.latest.Processes, order)
	var builder strings.Builder
	title := "TOP PROCESSES · CPU"
	if order == sortMemory {
		title = "TOP PROCESSES · MEMORY"
	}
	builder.WriteString(titleStyle.Render(title))
	if len(processes) == 0 {
		builder.WriteString("\n" + mutedStyle.Render("Waiting for readable processes"))
		return panel(builder.String(), width)
	}
	nameWidth := max(7, width-23)
	builder.WriteString(fmt.Sprintf("\n%-6s %-*s %8s", "PID", nameWidth, "NAME", metricHeading(order)))
	for _, process := range processes[:min(limit, len(processes))] {
		metric := process.CPU
		if order == sortMemory {
			metric = process.Memory
		}
		builder.WriteString(fmt.Sprintf("\n%-6d %-*s %8s", process.PID, nameWidth, truncateText(process.Name, nameWidth), formatMetricCompact(metric, 1)))
	}
	return panel(builder.String(), width)
}

func (m Model) renderCPU(_ int) string {
	return m.cpuCard(m.width) + "\n" + m.cpuContextCard(m.width)
}

func (m Model) cpuContextCard(width int) string {
	inner := max(8, width-4)
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("LOAD & THERMAL"))
	builder.WriteString(fmt.Sprintf("\nLoad average  %s  %s  %s", formatMetric(m.latest.Load.OneMinute, 2), formatMetric(m.latest.Load.FiveMinutes, 2), formatMetric(m.latest.Load.FifteenMinutes, 2)))
	builder.WriteString(fmt.Sprintf("\nTasks         %s runnable / %s total", formatMetricNumber(m.latest.Load.Runnable, 0), formatMetricNumber(m.latest.Load.Tasks, 0)))
	if len(m.latest.Thermals) == 0 {
		builder.WriteString("\n" + mutedStyle.Render("No thermal sensors discovered"))
	} else {
		limit := min(4, len(m.latest.Thermals))
		for _, zone := range m.latest.Thermals[:limit] {
			line := fmt.Sprintf("%-26s %s", truncateText(zone.Name, 26), formatTemperature(zone.Temperature))
			if trip := thermalTripSummary(zone); trip != "" {
				line += "  " + trip
			}
			builder.WriteString("\n" + truncateText(line, inner))
		}
	}
	if len(m.latest.Cooling) > 0 {
		limit := min(3, len(m.latest.Cooling))
		for _, device := range m.latest.Cooling[:limit] {
			line := fmt.Sprintf("Cooling %-18s %s/%s", truncateText(device.Type, 18),
				formatCoolingState(device.CurrentState), formatMetricNumber(device.MaximumState, 0))
			builder.WriteString("\n" + truncateText(line, inner))
		}
	}
	if sensor, ok := primaryPowerSensor(m.latest.Power); ok {
		line := fmt.Sprintf("Power   %-18s %s", truncateText(sensor.Name, 18), formatMetric(sensor.Value, 2))
		builder.WriteString("\n" + truncateText(line, inner))
	}
	for _, fan := range m.latest.Fans[:min(3, len(m.latest.Fans))] {
		line := fmt.Sprintf("Fan     %-18s %s  PWM %s %s", truncateText(fan.Name, 18),
			formatMetric(fan.Speed, 0), formatMetricCompact(fan.PWM, 0), formatTextMetricCompact(fan.ControlMode))
		builder.WriteString("\n" + truncateText(line, inner))
	}
	return panel(builder.String(), width)
}

func (m Model) renderMemory(_ int) string {
	if m.width >= 88 {
		leftWidth := (m.width - 2) / 2
		rightWidth := m.width - leftWidth - 2
		return lipgloss.JoinHorizontal(
			lipgloss.Top,
			m.memoryDetailCard(leftWidth), "  ", m.topProcessesCard(rightWidth, 8, sortMemory),
		)
	}
	return m.memoryDetailCard(m.width) + "\n" + m.topProcessesCard(m.width, 8, sortMemory)
}

func (m Model) memoryDetailCard(width int) string {
	inner := max(8, width-4)
	percent := memoryPercent(m.latest.Memory)
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("MEMORY"))
	builder.WriteString(fmt.Sprintf("\nUsed       %s / %s", formatMetric(m.latest.Memory.Used, 0), formatMetric(m.latest.Memory.Total, 0)))
	builder.WriteString("\n")
	builder.WriteString(progressValue(percent, inner))
	if len(m.memoryHistory) > 1 {
		builder.WriteString("\nHistory    ")
		builder.WriteString(sparkline(m.memoryHistory, max(4, inner-11)))
	}
	builder.WriteString(fmt.Sprintf("\nAvailable  %s", formatMetric(m.latest.Memory.Available, 0)))
	builder.WriteString(fmt.Sprintf("\nSwap       %s / %s", formatMetric(m.latest.Memory.SwapUsed, 0), formatMetric(m.latest.Memory.SwapTotal, 0)))
	if m.latest.Memory.CMATotal.Status == model.Available {
		builder.WriteString(fmt.Sprintf("\nCMA        %s used / %s total", formatMetric(m.latest.Memory.CMAUsed, 0), formatMetric(m.latest.Memory.CMATotal, 0)))
	}
	builder.WriteString(fmt.Sprintf("\nLoad       %s  %s  %s", formatMetric(m.latest.Load.OneMinute, 2), formatMetric(m.latest.Load.FiveMinutes, 2), formatMetric(m.latest.Load.FifteenMinutes, 2)))
	builder.WriteString(fmt.Sprintf("\nTasks      %s / %s", formatMetricNumber(m.latest.Load.Runnable, 0), formatMetricNumber(m.latest.Load.Tasks, 0)))
	if width >= 62 {
		builder.WriteString(fmt.Sprintf("\nPSI avg10  CPU %s  MEM %s/%s  IO %s/%s",
			formatMetricCompact(m.latest.Memory.Pressure.CPU.Some.Avg10, 2),
			formatMetricCompact(m.latest.Memory.Pressure.Memory.Some.Avg10, 2),
			formatMetricCompact(m.latest.Memory.Pressure.Memory.Full.Avg10, 2),
			formatMetricCompact(m.latest.Memory.Pressure.IO.Some.Avg10, 2),
			formatMetricCompact(m.latest.Memory.Pressure.IO.Full.Avg10, 2),
		))
	} else {
		builder.WriteString(fmt.Sprintf("\nPSI memory %s/%s",
			formatMetricCompact(m.latest.Memory.Pressure.Memory.Some.Avg10, 2),
			formatMetricCompact(m.latest.Memory.Pressure.Memory.Full.Avg10, 2),
		))
	}
	for _, device := range m.latest.Memory.ZRAM {
		line := fmt.Sprintf("ZRAM %-6s %s/%s ratio %s %s", device.Name,
			formatMetricCompact(device.MemoryUsed, 0), formatMetricCompact(device.DiskSize, 0),
			formatMetricCompact(device.CompressionRatio, 2), formatTextMetricCompact(device.Algorithm),
		)
		builder.WriteString("\n" + truncateText(line, inner))
	}
	return panel(builder.String(), width)
}

func (m Model) renderEngines(_ int) string {
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("ROCKCHIP ENGINES"))
	if len(m.latest.Accelerators) == 0 {
		builder.WriteString("\n" + mutedStyle.Render("No GPU, NPU or DDR providers detected"))
		builder.WriteString("\n" + mutedStyle.Render("Run `rtop doctor` for source and permission diagnostics"))
		return builder.String()
	}
	for _, accelerator := range m.latest.Accelerators {
		builder.WriteString("\n")
		builder.WriteString(enginePanel(accelerator, m.engineHistory(accelerator.Type), m.width))
	}
	builder.WriteString("\n")
	builder.WriteString(m.thermalCard(m.width))
	builder.WriteString("\n")
	builder.WriteString(m.powerCard(m.width))
	return builder.String()
}

func enginePanel(accelerator model.Accelerator, history []float64, width int) string {
	inner := max(8, width-4)
	var builder strings.Builder
	builder.WriteString(titleStyle.Render(accelerator.Name))
	builder.WriteString(fmt.Sprintf("  load %s  frequency %s", formatMetric(accelerator.Usage, 1), formatMetric(accelerator.Frequency, 0)))
	metadata := make([]string, 0, 2)
	if runtime := formatTextMetricValue(accelerator.RuntimeStatus); runtime != "" {
		metadata = append(metadata, "power "+runtime)
	}
	if policy := frequencyPolicySummary(accelerator.Policy); policy != "" {
		metadata = append(metadata, "limits "+policy)
	}
	if len(metadata) > 0 {
		builder.WriteString("\n" + truncateText(strings.Join(metadata, " · "), inner))
	}
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
	switch acceleratorType {
	case "gpu":
		return m.gpuHistory
	case "npu":
		return m.npuHistory
	case "ddr":
		return m.ddrHistory
	default:
		return nil
	}
}

func (m Model) renderProcesses(height int) string {
	processes := m.processes
	if len(processes) == 0 {
		return mutedStyle.Render("No readable processes")
	}
	detailHeight := 0
	if m.showProcessDetail {
		detailHeight = 6
	}
	rows := max(1, height-3-detailHeight)
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
	builder.WriteString(titleStyle.Render("PROCESSES"))
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
			row = fmt.Sprintf("%-7d %-*s %8s %11s %7d", process.PID, nameWidth, truncateText(process.Name, nameWidth), formatMetricCompact(process.CPU, 1), formatMetricCompact(process.Memory, 1), process.Threads)
		} else if medium {
			row = fmt.Sprintf("%-7d %-*s %8s %10s", process.PID, nameWidth, truncateText(process.Name, nameWidth), formatMetricCompact(process.CPU, 1), formatMetricCompact(process.Memory, 1))
		} else {
			row = fmt.Sprintf("%-6d %-*s %8s", process.PID, nameWidth, truncateText(process.Name, nameWidth), formatMetricCompact(process.CPU, 1))
		}
		builder.WriteString("\n")
		if index == m.selected {
			builder.WriteString(lipgloss.NewStyle().Reverse(true).Render(truncateText(row, m.width)))
		} else {
			builder.WriteString(truncateText(row, m.width))
		}
	}
	if m.showProcessDetail && m.selected < len(processes) {
		builder.WriteString("\n")
		builder.WriteString(processDetailPanel(processes[m.selected], m.width))
	}
	return builder.String()
}

func processDetailPanel(process model.ProcessInfo, width int) string {
	inner := max(8, width-4)
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("PROCESS DETAIL"))
	builder.WriteString(fmt.Sprintf("  %s", truncateText(process.Name, max(6, inner-24))))
	builder.WriteString(fmt.Sprintf("\nPID %d  Threads %d  CPU %s  Memory %s", process.PID, process.Threads, formatMetric(process.CPU, 1), formatMetric(process.Memory, 1)))
	if process.CPU.Source != "" {
		builder.WriteString("\nCPU source  " + truncateText(process.CPU.Source, max(6, inner-12)))
	}
	return panel(builder.String(), width)
}

func (m Model) thermalCard(width int) string {
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("THERMAL"))
	if len(m.latest.Thermals) == 0 {
		builder.WriteString("\n" + mutedStyle.Render("No thermal or hwmon sensors discovered"))
	} else {
		for _, zone := range m.latest.Thermals {
			if width >= 72 {
				line := fmt.Sprintf("%-24s %-15s %9s", truncateText(zone.Name, 24), truncateText(zone.Type, 15), formatTemperature(zone.Temperature))
				if trip := thermalTripSummary(zone); trip != "" {
					line += "  " + trip
				}
				builder.WriteString("\n" + truncateText(line, max(8, width-4)))
			} else {
				nameWidth := max(8, width-16)
				builder.WriteString(fmt.Sprintf("\n%-*s %10s", nameWidth, truncateText(zone.Name, nameWidth), formatTemperature(zone.Temperature)))
				if trip := thermalTripSummary(zone); trip != "" {
					builder.WriteString("\n  " + truncateText(trip, max(8, width-6)))
				}
			}
		}
	}
	if len(m.latest.Cooling) > 0 {
		builder.WriteString("\n" + titleStyle.Render("COOLING"))
		for _, device := range m.latest.Cooling {
			line := fmt.Sprintf("%-24s %s / %s", truncateText(device.Type, 24),
				formatCoolingState(device.CurrentState), formatMetricNumber(device.MaximumState, 0))
			builder.WriteString("\n" + truncateText(line, max(8, width-4)))
		}
	}
	return panel(builder.String(), width)
}

func (m Model) powerCard(width int) string {
	inner := max(8, width-4)
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("POWER & FANS"))
	if len(m.latest.Power) == 0 && len(m.latest.Fans) == 0 {
		builder.WriteString("\n" + mutedStyle.Render("No hwmon power or fan sensors discovered"))
		return panel(builder.String(), width)
	}
	for _, sensor := range m.latest.Power {
		name := sensor.Name
		if sensor.Chip != "" && width >= 64 {
			name = sensor.Chip + "/" + sensor.Name
		}
		line := fmt.Sprintf("%-9s %-28s %10s", strings.ToUpper(sensor.Kind), truncateText(name, 28), formatMetric(sensor.Value, 3))
		builder.WriteString("\n" + truncateText(line, inner))
	}
	for _, fan := range m.latest.Fans {
		name := fan.Name
		if fan.Chip != "" && width >= 64 {
			name = fan.Chip + "/" + fan.Name
		}
		nameWidth := max(12, min(28, inner-46))
		line := fmt.Sprintf("FAN       %-*s %8s  target %8s  PWM %6s  %s", nameWidth,
			truncateText(name, nameWidth), formatMetric(fan.Speed, 0), formatMetricCompact(fan.TargetSpeed, 0),
			formatMetricCompact(fan.PWM, 0), formatTextMetricCompact(fan.ControlMode))
		builder.WriteString("\n" + truncateText(line, inner))
	}
	return panel(builder.String(), width)
}

func (m Model) renderInfo(_ int) string {
	info := m.latest.System
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("SYSTEM INFO"))
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
		builder.WriteString(titleStyle.Render("DRIVERS"))
		for _, driver := range info.Drivers {
			details := driver.Name
			if driver.Governor != "" {
				details += " / " + driver.Governor
			}
			builder.WriteString(fmt.Sprintf("\n%-13s %s (%s)", strings.ToUpper(driver.Type), details, driver.Device))
		}
	}
	builder.WriteString("\n\n")
	builder.WriteString(titleStyle.Render("METRIC SOURCES"))
	builder.WriteString(fmt.Sprintf("\nCPU          %s", statusText(m.latest.CPU.Overall)))
	builder.WriteString(fmt.Sprintf("\nMemory       %s", statusText(m.latest.Memory.Total)))
	builder.WriteString(fmt.Sprintf("\nMemory PSI   %s", statusText(m.latest.Memory.Pressure.Memory.Some.Avg10)))
	builder.WriteString(fmt.Sprintf("\nCMA          %s", statusText(m.latest.Memory.CMATotal)))
	thermalStatus := mutedStyle.Render("unsupported")
	if len(m.latest.Thermals) > 0 {
		thermalStatus = goodStyle.Render(fmt.Sprintf("available (%d)", len(m.latest.Thermals)))
	}
	builder.WriteString("\nThermal      " + thermalStatus)
	powerStatus := mutedStyle.Render("unsupported")
	if len(m.latest.Power) > 0 {
		powerStatus = goodStyle.Render(fmt.Sprintf("available (%d)", len(m.latest.Power)))
	}
	builder.WriteString("\nPower        " + powerStatus)
	fanStatus := mutedStyle.Render("unsupported")
	if len(m.latest.Fans) > 0 {
		fanStatus = goodStyle.Render(fmt.Sprintf("available (%d)", len(m.latest.Fans)))
	}
	builder.WriteString("\nFans         " + fanStatus)
	for _, accelerator := range m.latest.Accelerators {
		builder.WriteString(fmt.Sprintf("\n%-13s %s", strings.ToUpper(accelerator.Type), statusText(accelerator.Frequency)))
		if accelerator.RuntimeStatus.Status != "" {
			builder.WriteString(fmt.Sprintf(" · %s", formatTextMetricCompact(accelerator.RuntimeStatus)))
		}
	}
	if len(m.latest.Warnings) > 0 {
		builder.WriteString("\n\n")
		builder.WriteString(warnStyle.Render("DIAGNOSTICS"))
		for _, warning := range m.latest.Warnings {
			builder.WriteString(fmt.Sprintf("\n%s: %s", warning.Component, warning.Message))
		}
		builder.WriteString("\n" + mutedStyle.Render("Run `rtop doctor` for detailed checks and remediation guidance."))
	}
	return builder.String()
}

func (m Model) renderHelp() string {
	helper := m.help
	helper.ShowAll = true
	intro := lipgloss.NewStyle().Width(max(1, m.width)).Render(
		"Pages follow the jtop model: ALL for board health, domain pages for drill-down.",
	)
	return titleStyle.Render("HELP") +
		"\n\n" + intro + "\n\n" + helper.View(m.keys)
}

func panel(content string, width int) string {
	return cardStyle.Width(max(1, width-2)).Render(content)
}

func formatMetric(metric model.Metric, precision int) string {
	if metric.Status != model.Available || metric.Value == nil {
		return "[" + fallback(string(metric.Status), "unavailable") + "]"
	}
	return strconv.FormatFloat(*metric.Value, 'f', precision, 64) + metric.Unit
}

func formatMetricCompact(metric model.Metric, precision int) string {
	if metric.Status == model.Available && metric.Value != nil {
		return strconv.FormatFloat(*metric.Value, 'f', precision, 64) + metric.Unit
	}
	switch metric.Status {
	case model.Unsupported:
		return "N/A"
	case model.PermissionDenied:
		return "DENIED"
	case model.Stale:
		return "STALE"
	default:
		return "--"
	}
}

func formatTextMetricCompact(metric model.TextMetric) string {
	if metric.Status == model.Available && metric.Value != nil {
		return *metric.Value
	}
	switch metric.Status {
	case model.Unsupported:
		return "N/A"
	case model.PermissionDenied:
		return "DENIED"
	case model.Stale:
		return "STALE"
	default:
		return "--"
	}
}

func formatTextMetricValue(metric model.TextMetric) string {
	if metric.Status != model.Available || metric.Value == nil {
		return ""
	}
	return *metric.Value
}

func frequencyPolicySummary(policy *model.FrequencyPolicy) string {
	if policy == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	if value := frequencyRange(policy.Minimum, policy.Maximum); value != "" {
		parts = append(parts, value)
	}
	if value := frequencyRange(policy.HardwareMinimum, policy.HardwareMaximum); value != "" {
		parts = append(parts, "hw "+value)
	}
	if governor := formatTextMetricValue(policy.Governor); governor != "" {
		parts = append(parts, governor)
	}
	return strings.Join(parts, " · ")
}

func frequencyRange(minimum, maximum model.Metric) string {
	minimumValue, maximumValue := metricNumber(minimum), metricNumber(maximum)
	switch {
	case minimumValue >= 0 && maximumValue >= 0:
		return fmt.Sprintf("%.0f–%.0fMHz", minimumValue, maximumValue)
	case maximumValue >= 0:
		return fmt.Sprintf("max %.0fMHz", maximumValue)
	case minimumValue >= 0:
		return fmt.Sprintf("min %.0fMHz", minimumValue)
	default:
		return ""
	}
}

func formatMetricNumber(metric model.Metric, precision int) string {
	if metric.Status != model.Available || metric.Value == nil {
		return formatMetricCompact(metric, precision)
	}
	return strconv.FormatFloat(*metric.Value, 'f', precision, 64)
}

func formatTemperature(metric model.Metric) string {
	text := formatMetric(metric, 1)
	if metric.Status != model.Available || metric.Value == nil {
		return text
	}
	switch {
	case *metric.Value >= 85:
		return badStyle.Render(text)
	case *metric.Value >= 70:
		return warnStyle.Render(text)
	default:
		return goodStyle.Render(text)
	}
}

func thermalTripSummary(zone model.ThermalZone) string {
	var selected model.ThermalTrip
	selectedTemperature := -1.0
	for _, trip := range zone.Trips {
		value := metricNumber(trip.Temperature)
		if value >= 0 && (selectedTemperature < 0 || value < selectedTemperature) {
			selected, selectedTemperature = trip, value
		}
	}
	if selectedTemperature < 0 {
		return ""
	}
	label := fallback(selected.Type, fmt.Sprintf("trip%d", selected.ID))
	current := metricNumber(zone.Temperature)
	if current < 0 {
		return fmt.Sprintf("%s %.0f°C", label, selectedTemperature)
	}
	headroom := selectedTemperature - current
	text := fmt.Sprintf("%s %.0f°C (%+.1f°C)", label, selectedTemperature, headroom)
	switch {
	case headroom <= 0:
		return badStyle.Render(text)
	case headroom <= 10:
		return warnStyle.Render(text)
	default:
		return mutedStyle.Render(text)
	}
}

func formatCoolingState(metric model.Metric) string {
	text := formatMetricNumber(metric, 0)
	if metric.Status != model.Available || metric.Value == nil {
		return text
	}
	if *metric.Value > 0 {
		return warnStyle.Render(text)
	}
	return goodStyle.Render(text)
}

func statusText(metric model.Metric) string {
	switch metric.Status {
	case model.Available:
		return goodStyle.Render(string(metric.Status))
	case model.PermissionDenied, model.Stale:
		return warnStyle.Render(string(metric.Status))
	case model.Unsupported:
		return mutedStyle.Render(string(metric.Status))
	default:
		return badStyle.Render(fallback(string(metric.Status), "unavailable"))
	}
}

func progress(metric model.Metric, width int) string {
	return progressValue(metricNumber(metric), width)
}

func progressValue(value float64, width int) string {
	width = max(3, width)
	inner := width - 2
	if value < 0 {
		return mutedStyle.Render("[" + strings.Repeat("·", inner) + "]")
	}
	value = min(100, max(0, value))
	filled := int(value / 100 * float64(inner))
	return mutedStyle.Render("[") +
		barFill.Render(strings.Repeat(" ", filled)) +
		barTrack.Render(strings.Repeat(" ", inner-filled)) +
		mutedStyle.Render("]")
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

func hottestThermal(zones []model.ThermalZone) (model.ThermalZone, bool) {
	var hottest model.ThermalZone
	value := -1.0
	for _, zone := range zones {
		current := metricNumber(zone.Temperature)
		if current > value {
			hottest, value = zone, current
		}
	}
	return hottest, value >= 0
}

func primaryPowerSensor(sensors []model.PowerSensor) (model.PowerSensor, bool) {
	for _, sensor := range sensors {
		if sensor.Kind == "power" && sensor.Value.Status == model.Available {
			return sensor, true
		}
	}
	return model.PowerSensor{}, false
}

func sortedProcesses(processes []model.ProcessInfo, order processSort) []model.ProcessInfo {
	result := append([]model.ProcessInfo(nil), processes...)
	sort.SliceStable(result, func(i, j int) bool {
		left, right := metricNumber(result[i].CPU), metricNumber(result[j].CPU)
		if order == sortMemory {
			left, right = metricNumber(result[i].Memory), metricNumber(result[j].Memory)
		}
		if left == right {
			return result[i].PID < result[j].PID
		}
		return left > right
	})
	return result
}

func metricHeading(order processSort) string {
	if order == sortMemory {
		return "MEMORY"
	}
	return "CPU"
}

func truncateText(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(value, width, "…")
}

func fallback(value, replacement string) string {
	if value == "" {
		return replacement
	}
	return value
}
