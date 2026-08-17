package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"rtop/internal/model"
)

// This file contains the screen composition used by View.  It follows jtop's
// curses layout: a one-line board header, dense unboxed gauges, a highlighted
// process-table header, bottom summary panes on ALL, and a one-line reverse
// video page menu. The older card renderers remain useful as small, testable
// formatting units, but are deliberately not used to compose the screen.

var (
	jtopLabelStyle        = lipgloss.NewStyle().Bold(true).Foreground(colorPrimary)
	jtopHeaderStyle       = lipgloss.NewStyle().Bold(true).Reverse(true).Foreground(colorGood)
	jtopAxisStyle         = lipgloss.NewStyle().Foreground(colorMuted)
	jtopChartStyle        = lipgloss.NewStyle().Foreground(colorPrimary)
	jtopMemorySharedStyle = lipgloss.NewStyle().Foreground(colorGood)
	jtopMemoryBufferStyle = lipgloss.NewStyle().Foreground(colorBuffer)
	jtopMemoryCacheStyle  = lipgloss.NewStyle().Foreground(colorCache)
)

const (
	jtopGaugeValueWidth = 6
	jtopGaugeRightWidth = 8
)

func fitLine(left, right string, width int) string {
	if width <= 0 {
		return ""
	}
	leftWidth, rightWidth := ansi.StringWidth(left), ansi.StringWidth(right)
	if right == "" || leftWidth+rightWidth+1 > width {
		return ansi.Truncate(left, width, "")
	}
	return left + strings.Repeat(" ", width-leftWidth-rightWidth) + right
}

func fitLines(content string, width int) string {
	lines := strings.Split(content, "\n")
	for index, line := range lines {
		lines[index] = ansi.Truncate(line, max(1, width), "")
	}
	return strings.Join(lines, "\n")
}

func joinJtopColumns(left, right string, leftWidth, rightWidth int, separator bool) string {
	leftLines, rightLines := strings.Split(left, "\n"), strings.Split(right, "\n")
	height := max(len(leftLines), len(rightLines))
	lines := make([]string, 0, height)
	divider := ""
	if separator {
		divider = mutedStyle.Render("│")
	}
	for row := 0; row < height; row++ {
		leftLine, rightLine := "", ""
		if row < len(leftLines) {
			leftLine = ansi.Truncate(leftLines[row], leftWidth, "")
		}
		if row < len(rightLines) {
			rightLine = ansi.Truncate(rightLines[row], rightWidth, "")
		}
		leftLine += strings.Repeat(" ", max(0, leftWidth-ansi.StringWidth(leftLine)))
		lines = append(lines, leftLine+divider+rightLine)
	}
	return strings.Join(lines, "\n")
}

func jtopGauge(name string, value float64, valueText, right string, width int) string {
	width = max(1, width)
	labelWidth := min(max(3, len(name)), 9)
	label := fmt.Sprintf("%-*s", labelWidth, truncateText(name, labelWidth))
	valueWidth := gaugeFieldWidth(valueText, jtopGaugeValueWidth)
	rightWidth := gaugeFieldWidth(right, jtopGaugeRightWidth)
	reserved := labelWidth + valueWidth + rightWidth + 6
	barWidth := max(3, width-reserved)
	if width < 28 {
		barWidth = max(3, width-labelWidth-4)
		valueText, right = "", ""
	}
	var bar string
	if value < 0 {
		bar = badStyle.Render("OFF" + strings.Repeat(" ", max(0, barWidth-3)))
	} else {
		value = min(100, max(0, value))
		filled := int(value / 100 * float64(barWidth))
		bar = goodStyle.Render(strings.Repeat("|", filled)) +
			mutedStyle.Render(strings.Repeat("·", barWidth-filled))
	}
	line := jtopLabelStyle.Render(label) + mutedStyle.Render("[") + bar + mutedStyle.Render("]")
	if valueText != "" {
		line += " " + padGaugeField(valueText, valueWidth)
	}
	if right != "" {
		line += " " + padGaugeField(right, rightWidth)
	}
	return ansi.Truncate(line, width, "")
}

func gaugeFieldWidth(value string, minimum int) int {
	if value == "" {
		return 0
	}
	return max(minimum, ansi.StringWidth(value))
}

func padGaugeField(value string, width int) string {
	return strings.Repeat(" ", max(0, width-ansi.StringWidth(value))) + value
}

type jtopGaugeSegment struct {
	percent float64
	style   lipgloss.Style
}

func jtopStackedGauge(name string, segments []jtopGaugeSegment, valueText, right string, width int) string {
	width = max(1, width)
	labelWidth := min(max(3, len(name)), 9)
	label := fmt.Sprintf("%-*s", labelWidth, truncateText(name, labelWidth))
	valueWidth := gaugeFieldWidth(valueText, jtopGaugeValueWidth)
	rightWidth := gaugeFieldWidth(right, jtopGaugeRightWidth)
	reserved := labelWidth + valueWidth + rightWidth + 6
	barWidth := max(3, width-reserved)
	if width < 28 {
		barWidth = max(3, width-labelWidth-4)
		valueText, right = "", ""
	}

	var bar strings.Builder
	position := 0
	cumulative := 0.0
	for _, segment := range segments {
		cumulative = min(100, cumulative+max(0, segment.percent))
		end := int(cumulative / 100 * float64(barWidth))
		if end > position {
			bar.WriteString(segment.style.Render(strings.Repeat("|", end-position)))
			position = end
		}
	}
	bar.WriteString(mutedStyle.Render(strings.Repeat("·", max(0, barWidth-position))))

	line := jtopLabelStyle.Render(label) + mutedStyle.Render("[") + bar.String() + mutedStyle.Render("]")
	if valueText != "" {
		line += " " + padGaugeField(valueText, valueWidth)
	}
	if right != "" {
		line += " " + padGaugeField(right, rightWidth)
	}
	return ansi.Truncate(line, width, "")
}

func jtopMemoryGauge(memory model.MemoryInfo, width int) string {
	valueText := formatMetricPairCompact(memory.Used, memory.Total, 0)
	breakdown, ok := memoryBreakdownPercentages(memory)
	if !ok {
		return jtopGauge("Mem", -1, valueText, "", width)
	}
	return jtopStackedGauge("Mem", []jtopGaugeSegment{
		{percent: breakdown.used, style: jtopChartStyle},
		{percent: breakdown.shared, style: jtopMemorySharedStyle},
		{percent: breakdown.buffers, style: jtopMemoryBufferStyle},
		{percent: breakdown.cache, style: jtopMemoryCacheStyle},
	}, valueText, "", width)
}

func jtopMetricGauge(name string, metric model.Metric, right string, width int) string {
	return jtopGauge(name, metricNumber(metric), formatMetricCompact(metric, 1), right, width)
}

func formatMetricPairCompact(value, total model.Metric, precision int) string {
	valueText := formatMetricCompact(value, precision)
	totalText := formatMetricCompact(total, precision)
	fieldWidth := max(ansi.StringWidth(valueText), ansi.StringWidth(totalText))
	return padGaugeField(valueText, fieldWidth) + "/" + totalText
}

func (m Model) compactCPUs(width int) string {
	cores := m.latest.CPU.Cores
	if len(cores) == 0 {
		return jtopMetricGauge("CPU", m.latest.CPU.Overall, "", width)
	}
	columns := 1
	if width >= 64 && len(cores) > 1 {
		columns = 2
	}
	columnWidth := width
	if columns == 2 {
		columnWidth = width / 2
	}
	rows := (len(cores) + columns - 1) / columns
	lines := make([]string, 0, rows)
	for row := 0; row < rows; row++ {
		parts := make([]string, 0, columns)
		for column := 0; column < columns; column++ {
			index := row + column*rows
			if index >= len(cores) {
				parts = append(parts, strings.Repeat(" ", columnWidth))
				continue
			}
			core := cores[index]
			frequency := formatMetricCompact(core.Frequency, 0)
			parts = append(parts, jtopMetricGauge(fmt.Sprintf("%d", core.ID+1), core.Usage, frequency, columnWidth))
		}
		lines = append(lines, strings.Join(parts, ""))
	}
	return strings.Join(lines, "\n")
}

func (m Model) compactMemory(width int) string {
	lines := []string{
		jtopMemoryGauge(m.latest.Memory, width),
		jtopGauge("Swp", ratioMetric(m.latest.Memory.SwapUsed, m.latest.Memory.SwapTotal),
			formatMetricPairCompact(m.latest.Memory.SwapUsed, m.latest.Memory.SwapTotal, 0), "", width),
	}
	if m.latest.Memory.CMATotal.Status == model.Available {
		lines = append(lines, jtopGauge("CMA", ratioMetric(m.latest.Memory.CMAUsed, m.latest.Memory.CMATotal),
			formatMetricPairCompact(m.latest.Memory.CMAUsed, m.latest.Memory.CMATotal, 0), "", width))
	}
	return strings.Join(lines, "\n")
}

func (m Model) compactStatus(width int) string {
	lines := make([]string, 0, 3)
	for _, fan := range m.latest.Fans[:min(2, len(m.latest.Fans))] {
		lines = append(lines, jtopMetricGauge("FAN", fan.PWM, formatMetricCompact(fan.Speed, 0), width))
	}
	if len(lines) == 0 {
		lines = append(lines, jtopGauge("FAN", -1, "", "", width))
	}
	load := fmt.Sprintf("%s %s %s", formatMetricCompact(m.latest.Load.OneMinute, 2),
		formatMetricCompact(m.latest.Load.FiveMinutes, 2), formatMetricCompact(m.latest.Load.FifteenMinutes, 2))
	lines = append(lines, fitLine(jtopLabelStyle.Render("Load"), load, width))
	lines = append(lines, fitLine(jtopLabelStyle.Render("Updated"), m.latest.Timestamp.Format("15:04:05"), width))
	return strings.Join(lines, "\n")
}

func ratioMetric(used, total model.Metric) float64 {
	usedValue, totalValue := metricNumber(used), metricNumber(total)
	if usedValue < 0 || totalValue <= 0 {
		return -1
	}
	return usedValue * 100 / totalValue
}

func engineShortName(accelerator model.Accelerator) string {
	if accelerator.Type != "" {
		return strings.ToUpper(accelerator.Type)
	}
	return accelerator.Name
}

func (m Model) renderJtopAll(height int) string {
	width := m.width
	sections := make([]string, 0, 5)
	sections = append(sections, m.compactCPUs(width))

	leftWidth := width / 2
	rightWidth := width - leftWidth
	sections = append(sections, joinJtopColumns(
		m.compactMemory(leftWidth), m.compactStatus(rightWidth), leftWidth, rightWidth, false,
	))

	used := 0
	for _, section := range sections {
		used += lipgloss.Height(section)
	}
	miniHeight := 0
	if width >= 60 && height-used >= 4 {
		miniHeight = min(6, max(4, height-used-2))
	}
	processHeight := max(0, height-used-miniHeight)
	if processHeight > 0 {
		sections = append(sections, m.jtopProcessTable(processHeight, width, sortCPU, false))
	}
	if miniHeight > 0 {
		sections = append(sections, m.jtopSummaryPanes(miniHeight, width))
	}
	return fitLines(strings.Join(sections, "\n"), width)
}

func (m Model) jtopSummaryPanes(height, width int) string {
	columnWidth := width / 3
	widths := []int{columnWidth, columnWidth, width - 2*columnWidth}
	engineLines := make([]string, 0, len(m.latest.Accelerators))
	for _, engine := range m.latest.Accelerators {
		value := summaryMetricValue(engine.Usage) + " " + summaryMetricValue(engine.Frequency)
		engineLines = append(engineLines, fitLine(engineShortName(engine), value, max(1, widths[0]-2)))
	}
	temperatureLines := make([]string, 0, len(m.latest.Thermals))
	for _, zone := range m.latest.Thermals {
		temperatureLines = append(temperatureLines, fitLine(truncateText(zone.Name, max(3, widths[1]-11)),
			formatTemperature(zone.Temperature), max(1, widths[1]-2)))
	}
	powerLines := make([]string, 0, len(m.latest.Power)+len(m.latest.Fans))
	for _, sensor := range m.latest.Power {
		powerLines = append(powerLines, fitLine(truncateText(sensor.Name, max(3, widths[2]-12)),
			formatMetricCompact(sensor.Value, 2), max(1, widths[2]-2)))
	}
	for _, fan := range m.latest.Fans {
		powerLines = append(powerLines, fitLine(truncateText(fan.Name, max(3, widths[2]-12)),
			formatMetricCompact(fan.Speed, 0), max(1, widths[2]-2)))
	}
	left := jtopBox("HW engines", engineLines, widths[0], height)
	middle := jtopBox("Sensor", temperatureLines, widths[1], height)
	right := jtopBox("Power", powerLines, widths[2], height)
	return joinJtopColumns(joinJtopColumns(left, middle, widths[0], widths[1], false), right,
		widths[0]+widths[1], widths[2], false)
}

func summaryMetricValue(metric model.Metric) string {
	if metric.Status != model.Available || metric.Value == nil {
		return "-"
	}
	return formatMetricCompact(metric, 0)
}

func jtopBox(title string, content []string, width, height int) string {
	width, height = max(4, width), max(2, height)
	inner := width - 2
	titleText := "[" + title + "]"
	leftRule := max(0, (inner-ansi.StringWidth(titleText))/2)
	rightRule := max(0, inner-leftRule-ansi.StringWidth(titleText))
	lines := []string{mutedStyle.Render("┌"+strings.Repeat("─", leftRule)) + titleStyle.Render(titleText) + mutedStyle.Render(strings.Repeat("─", rightRule)+"┐")}
	for row := 0; row < height-2; row++ {
		line := ""
		if row < len(content) {
			line = ansi.Truncate(content[row], inner, "")
		}
		line += strings.Repeat(" ", max(0, inner-ansi.StringWidth(line)))
		lines = append(lines, mutedStyle.Render("│")+line+mutedStyle.Render("│"))
	}
	lines = append(lines, mutedStyle.Render("└"+strings.Repeat("─", inner)+"┘"))
	return strings.Join(lines, "\n")
}

func verticalChart(values []float64, width, height int) string {
	width, height = max(2, width), max(1, height)
	if len(values) > width {
		values = values[len(values)-width:]
	}
	missing := width - len(values)
	lines := make([]string, height)
	for row := 0; row < height; row++ {
		threshold := float64(height-row-1) * 100 / float64(height)
		var builder strings.Builder
		builder.WriteString(strings.Repeat(" ", missing))
		for _, value := range values {
			if value >= threshold {
				builder.WriteString(jtopChartStyle.Render("█"))
			} else {
				builder.WriteString(" ")
			}
		}
		label := fmt.Sprintf(" %3.0f%%", threshold)
		lines[row] = builder.String() + jtopAxisStyle.Render(label)
	}
	return strings.Join(lines, "\n")
}

type memoryChartLayer uint8

const (
	memoryChartEmpty memoryChartLayer = iota
	memoryChartUsed
	memoryChartShared
	memoryChartBuffers
	memoryChartCache
)

func stackedMemoryChart(used, shared, buffers, cache []float64, width, height int) string {
	width, height = max(2, width), max(1, height)
	count := min(width, len(used), len(shared), len(buffers), len(cache))
	missing := width - count
	lines := make([]string, height)
	for row := 0; row < height; row++ {
		threshold := float64(height-row-1) * 100 / float64(height)
		var builder strings.Builder
		builder.WriteString(strings.Repeat(" ", missing))
		for index := 0; index < count; index++ {
			point := memoryBreakdown{
				used:    used[len(used)-count+index],
				shared:  shared[len(shared)-count+index],
				buffers: buffers[len(buffers)-count+index],
				cache:   cache[len(cache)-count+index],
			}
			builder.WriteString(renderMemoryChartLayer(memoryChartLayerAt(point, threshold)))
		}
		label := fmt.Sprintf(" %3.0f%%", threshold)
		lines[row] = builder.String() + jtopAxisStyle.Render(label)
	}
	return strings.Join(lines, "\n")
}

func memoryChartLayerAt(point memoryBreakdown, threshold float64) memoryChartLayer {
	if point.used < 0 || point.shared < 0 || point.buffers < 0 || point.cache < 0 {
		return memoryChartEmpty
	}
	cumulative := point.used
	if cumulative > threshold {
		return memoryChartUsed
	}
	cumulative += point.shared
	if cumulative > threshold {
		return memoryChartShared
	}
	cumulative += point.buffers
	if cumulative > threshold {
		return memoryChartBuffers
	}
	cumulative += point.cache
	if cumulative > threshold {
		return memoryChartCache
	}
	return memoryChartEmpty
}

func renderMemoryChartLayer(layer memoryChartLayer) string {
	switch layer {
	case memoryChartUsed:
		return jtopChartStyle.Render("█")
	case memoryChartShared:
		return jtopMemorySharedStyle.Render("█")
	case memoryChartBuffers:
		return jtopMemoryBufferStyle.Render("█")
	case memoryChartCache:
		return jtopMemoryCacheStyle.Render("█")
	default:
		return " "
	}
}

func memoryChartLegend(width int) string {
	items := []string{
		jtopChartStyle.Render("■") + " used",
		jtopMemorySharedStyle.Render("■") + " shared",
		jtopMemoryBufferStyle.Render("■") + " buffers",
		jtopMemoryCacheStyle.Render("■") + " cache",
	}
	return strings.Join(wrapJtopItems(items, width), "\n")
}

func jtopFrequencyGauge(name string, frequency model.Metric, policy *model.FrequencyPolicy, width int) string {
	if frequency.Status != model.Available || frequency.Value == nil {
		return jtopUnavailableGauge(name, formatMetricCompact(frequency, 0), width)
	}
	value := metricNumber(frequency)
	maximum := -1.0
	minimum := 0.0
	if policy != nil {
		maximum = metricNumber(policy.Maximum)
		minimum = metricNumber(policy.Minimum)
		if maximum <= 0 {
			maximum = metricNumber(policy.HardwareMaximum)
		}
		if minimum < 0 {
			minimum = metricNumber(policy.HardwareMinimum)
		}
	}
	percent := -1.0
	if value >= 0 && maximum > minimum {
		percent = (value - minimum) * 100 / (maximum - minimum)
	}
	rangeText := frequencyRange(model.AvailableMetric(minimum, "MHz", "", frequency.Timestamp),
		model.AvailableMetric(maximum, "MHz", "", frequency.Timestamp))
	if maximum <= 0 {
		rangeText = ""
	}
	if percent < 0 {
		return jtopUnknownRangeGauge(name, rangeText, formatMetricCompact(frequency, 0), width)
	}
	return jtopGauge(name, percent, rangeText, formatMetricCompact(frequency, 0), width)
}

func jtopUnavailableGauge(name, status string, width int) string {
	width = max(1, width)
	labelWidth := min(max(3, len(name)), 9)
	label := fmt.Sprintf("%-*s", labelWidth, truncateText(name, labelWidth))
	barWidth := max(3, width-labelWidth-8)
	bar := " " + status + strings.Repeat(" ", max(0, barWidth-ansi.StringWidth(status)-1))
	line := jtopLabelStyle.Render(label) + mutedStyle.Render("[") + mutedStyle.Render(bar) + mutedStyle.Render("]")
	return ansi.Truncate(line+" "+status, width, "")
}

func jtopUnknownRangeGauge(name, rangeText, current string, width int) string {
	width = max(1, width)
	labelWidth := min(max(3, len(name)), 9)
	label := fmt.Sprintf("%-*s", labelWidth, truncateText(name, labelWidth))
	reserved := labelWidth + ansi.StringWidth(rangeText) + ansi.StringWidth(current) + 6
	barWidth := max(3, width-reserved)
	line := jtopLabelStyle.Render(label) + mutedStyle.Render("[") +
		mutedStyle.Render(strings.Repeat("·", barWidth)) + mutedStyle.Render("]")
	if rangeText != "" {
		line += " " + rangeText
	}
	line += " " + current
	return ansi.Truncate(line, width, "")
}

func (m Model) renderJtopCPU(height int) string {
	width := m.width
	sections := []string{jtopMetricGauge("ALL", m.latest.CPU.Overall, "", width)}
	cores := m.latest.CPU.Cores
	if len(cores) == 0 {
		return sections[0]
	}
	columns := jtopCPUColumns(len(cores), max(1, height-1), width)
	rows := (len(cores) + columns - 1) / columns
	tileHeight := max(4, max(1, height-1)/rows)
	gridWidth := max(1, width-columns+1)
	baseWidth := max(1, gridWidth/columns)
	for row := 0; row < rows; row++ {
		rowContent := ""
		usedWidth := 0
		for column := 0; column < columns; column++ {
			index := column*rows + row
			tileWidth := baseWidth
			if column == columns-1 {
				tileWidth = gridWidth - baseWidth*(columns-1)
			}
			tile := ""
			if index < len(cores) {
				tile = m.jtopCPUCoreChart(cores[index], tileWidth, tileHeight)
			} else {
				tileLines := make([]string, tileHeight)
				for line := range tileLines {
					tileLines[line] = strings.Repeat(" ", tileWidth)
				}
				tile = strings.Join(tileLines, "\n")
			}
			if column == 0 {
				rowContent = tile
				usedWidth = tileWidth
			} else {
				rowContent = joinJtopColumns(rowContent, tile, usedWidth, tileWidth, true)
				usedWidth += tileWidth + 1
			}
		}
		sections = append(sections, rowContent)
	}
	return fitLines(strings.Join(sections, "\n"), width)
}

func jtopCPUColumns(count, height, width int) int {
	bestColumns := 1
	bestScore := 1e9
	for columns := 1; columns <= count; columns++ {
		rows := (count + columns - 1) / columns
		tileHeight := max(1, height/rows)
		tileWidth := max(1, (width-columns+1)/columns)
		if tileHeight < 5 || tileWidth < 18 {
			continue
		}
		ratio := float64(tileWidth) / float64(tileHeight)
		score := ratio - 6
		if score < 0 {
			score = -score
		}
		score += float64(rows*columns-count) * .2
		if score < bestScore {
			bestScore, bestColumns = score, columns
		}
	}
	return bestColumns
}

func (m Model) jtopCPUCoreChart(core model.CPUCore, width, height int) string {
	governor := ""
	if core.Policy != nil {
		governor = formatTextMetricValue(core.Policy.Governor)
	}
	value := formatMetricCompact(core.Usage, 0)
	header := fitLine(jtopLabelStyle.Render(fmt.Sprintf("%d  %s", core.ID+1, value)), governor, width)
	chartWidth := max(2, width-6)
	chart := verticalChart(m.coreHistory[core.ID], chartWidth, max(1, height-3))
	axis := jtopTimeAxis(width)
	frequency := jtopFrequencyGauge("Frq", core.Frequency, core.Policy, width)
	return strings.Join([]string{header, chart, axis, frequency}, "\n")
}

func jtopTimeAxis(width int) string {
	if width < 12 {
		return mutedStyle.Render(strings.Repeat("─", max(1, width)))
	}
	axis := "└ -8s   └ -6s   └ -4s   └ -2s   0"
	return mutedStyle.Render(ansi.Truncate(axis, width, ""))
}

func (m Model) renderJtopMemory(height int) string {
	width := m.width
	summary := jtopMemorySummary(m.latest.Memory, width)
	activity := m.jtopMemoryActivity(width)
	fixedHeight := lipgloss.Height(summary) + lipgloss.Height(activity)

	ddr := ""
	for _, accelerator := range m.latest.Accelerators {
		if accelerator.Type == "ddr" {
			ddr = jtopMetricGauge("DDR", accelerator.Usage,
				formatMetricCompact(accelerator.Frequency, 0), width)
			fixedHeight++
			break
		}
	}

	chartHeight := max(4, height-fixedHeight)
	chartHeading := fitLine(
		titleStyle.Render("RAM HISTORY"),
		fmt.Sprintf("Used: %s / %s", formatMetricCompact(m.latest.Memory.Used, 1), formatMetricCompact(m.latest.Memory.Total, 1)),
		width,
	)
	chartHeading += "\n" + memoryChartLegend(width)
	plotHeight := max(1, chartHeight-lipgloss.Height(chartHeading)-1)
	chart := chartHeading + "\n" + stackedMemoryChart(
		m.memoryUsedHistory,
		m.memorySharedHistory,
		m.memoryBufferHistory,
		m.memoryCacheHistory,
		max(2, width-6),
		plotHeight,
	) +
		"\n" + jtopTimeAxis(width)

	sections := []string{summary, chart}
	if ddr != "" {
		sections = append(sections, ddr)
	}
	sections = append(sections, activity)
	return fitLines(strings.Join(sections, "\n"), width)
}

func jtopMemorySummary(memory model.MemoryInfo, width int) string {
	width = max(1, width)
	if width >= 72 {
		const labelWidth = 6
		columnWidth := (width - labelWidth) / 6
		headings := []string{"total", "used", "free", "shared", "buff/cache", "available"}
		header := jtopLabelStyle.Render(fmt.Sprintf("%-*s", labelWidth, "RAM"))
		for _, heading := range headings {
			header += mutedStyle.Render(fmt.Sprintf("%*s", columnWidth, heading))
		}
		row := func(label string, metrics ...model.Metric) string {
			line := jtopLabelStyle.Render(fmt.Sprintf("%-*s", labelWidth, label))
			for index := range headings {
				value := ""
				if index < len(metrics) {
					value = truncateText(formatMetricCompact(metrics[index], 0), columnWidth)
				}
				line += fmt.Sprintf("%*s", columnWidth, value)
			}
			return ansi.Truncate(line, width, "")
		}
		lines := []string{
			ansi.Truncate(header, width, ""),
			row("Mem:", memory.Total, memory.Used, memory.Free, memory.Shared, memory.BufferCache, memory.Available),
			row("SWAP:", memory.SwapTotal, memory.SwapUsed, memory.SwapFree),
		}
		lines = append(lines, wrapJtopItems(memoryDetailItems(memory, true), width)...)
		return strings.Join(lines, "\n")
	}

	lines := []string{titleStyle.Render("RAM")}
	lines = append(lines, wrapJtopItems([]string{
		jtopMemoryMetric("Total", memory.Total),
		jtopMemoryMetric("Used", memory.Used),
		jtopMemoryMetric("Free", memory.Free),
		jtopMemoryMetric("Shared", memory.Shared),
		jtopMemoryMetric("Buff/cache", memory.BufferCache),
		jtopMemoryMetric("Available", memory.Available),
	}, width)...)
	lines = append(lines, wrapJtopItems(memoryDetailItems(memory, false), width)...)
	lines = append(lines, titleStyle.Render("SWAP"))
	lines = append(lines, wrapJtopItems([]string{
		jtopMemoryMetric("Total", memory.SwapTotal),
		jtopMemoryMetric("Used", memory.SwapUsed),
		jtopMemoryMetric("Free", memory.SwapFree),
		jtopMemoryMetric("Cached", memory.SwapCached),
	}, width)...)
	return strings.Join(lines, "\n")
}

func memoryDetailItems(memory model.MemoryInfo, includeSwapCache bool) []string {
	items := []string{
		jtopMemoryMetric("Buffers", memory.Buffers),
		jtopMemoryMetric("Cache", memory.Cache),
	}
	if includeSwapCache {
		items = append(items, jtopMemoryMetric("Swap cache", memory.SwapCached))
	}
	if memory.CMATotal.Status == model.Available {
		items = append(items, jtopLabelStyle.Render("CMA:")+" "+
			fmt.Sprintf("%s/%s", formatMetricCompact(memory.CMAUsed, 0), formatMetricCompact(memory.CMATotal, 0)))
	}
	return items
}

func jtopMemoryMetric(label string, metric model.Metric) string {
	return jtopLabelStyle.Render(label+":") + " " + formatMetricCompact(metric, 0)
}

func wrapJtopItems(items []string, width int) []string {
	width = max(1, width)
	lines := make([]string, 0, len(items))
	line := ""
	for _, item := range items {
		item = ansi.Truncate(item, width, "")
		if line == "" {
			line = item
			continue
		}
		if ansi.StringWidth(line)+2+ansi.StringWidth(item) <= width {
			line += "  " + item
			continue
		}
		lines = append(lines, line)
		line = item
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func (m Model) jtopMemoryActivity(width int) string {
	inner := max(2, width)
	lines := make([]string, 0, len(m.latest.Memory.ZRAM)+2)
	if len(m.latest.Memory.ZRAM) > 0 {
		lines = append(lines, titleStyle.Render("ZRAM"))
	}
	for _, device := range m.latest.Memory.ZRAM {
		line := jtopGauge(device.Name, ratioMetric(device.MemoryUsed, device.DiskSize),
			formatMetricPairCompact(device.MemoryUsed, device.DiskSize, 0), "", inner)
		lines = append(lines, line)
	}
	lines = append(lines,
		fmt.Sprintf("PSI cpu %s  mem %s/%s  io %s/%s",
			formatMetricCompact(m.latest.Memory.Pressure.CPU.Some.Avg10, 2),
			formatMetricCompact(m.latest.Memory.Pressure.Memory.Some.Avg10, 2),
			formatMetricCompact(m.latest.Memory.Pressure.Memory.Full.Avg10, 2),
			formatMetricCompact(m.latest.Memory.Pressure.IO.Some.Avg10, 2),
			formatMetricCompact(m.latest.Memory.Pressure.IO.Full.Avg10, 2)))
	return strings.Join(lines, "\n")
}

func (m Model) renderJtopEngines(_ int) string {
	if len(m.latest.Accelerators) == 0 {
		return mutedStyle.Render("No GPU, NPU, DDR or RGA providers detected\nRun `rtop doctor` for diagnostics")
	}
	sections := make([]string, 0, len(m.latest.Accelerators))
	for _, accelerator := range m.latest.Accelerators {
		lines := []string{jtopMetricGauge(engineShortName(accelerator), accelerator.Usage,
			formatMetricCompact(accelerator.Frequency, 0), m.width)}
		meta := make([]string, 0, 3)
		if policy := frequencyPolicySummary(accelerator.Policy); policy != "" {
			meta = append(meta, policy)
		}
		if runtime := formatTextMetricValue(accelerator.RuntimeStatus); runtime != "" {
			meta = append(meta, "power "+runtime)
		}
		if accelerator.DriverVersion != nil {
			if version := formatTextMetricValue(*accelerator.DriverVersion); version != "" {
				meta = append(meta, version)
			}
		}
		if len(meta) > 0 {
			lines = append(lines, "  "+mutedStyle.Render(strings.Join(meta, " · ")))
		}
		for _, core := range accelerator.Cores {
			name := fallback(core.Name, fmt.Sprintf("Core %d", core.ID))
			lines = append(lines, "  "+jtopMetricGauge(name, core.Usage, "", max(1, m.width-2)))
		}
		lines = append(lines, acceleratorSessionLines(accelerator.Sessions, m.width)...)
		sections = append(sections, strings.Join(lines, "\n"))
	}
	return fitLines(strings.Join(sections, "\n"), m.width)
}

func (m Model) jtopProcessTable(height, width int, order processSort, selectable bool) string {
	if height <= 0 {
		return ""
	}
	processes := m.topCPUProcesses
	if selectable {
		processes = m.processes
	} else if order == sortMemory {
		processes = m.topMemoryProcesses
	}
	full := width >= 66
	medium := width >= 48
	nameWidth := max(6, width-38)
	headingName := processNameHeading(m.showProcessPath)
	var heading string
	if full {
		heading = fmt.Sprintf("%-7s %-*s %8s %11s %7s", "PID", nameWidth, headingName, "CPU%", "MEM", "THREADS")
	} else if medium {
		nameWidth = max(6, width-27)
		heading = fmt.Sprintf("%-7s %-*s %8s %10s", "PID", nameWidth, headingName, "CPU%", "MEM")
	} else {
		nameWidth = max(4, width-17)
		heading = fmt.Sprintf("%-6s %-*s %7s", "PID", nameWidth, headingName, "CPU%")
	}
	if order == sortCPU {
		heading = strings.Replace(heading, "CPU%", "[CPU%]", 1)
	} else {
		heading = strings.Replace(heading, "MEM", "[MEM]", 1)
	}
	lines := []string{jtopHeaderStyle.Width(width).MaxWidth(width).Render(ansi.Truncate(heading, width, ""))}
	rows := min(len(processes), max(0, height-1))
	start := 0
	if selectable && m.selected >= rows && rows > 0 {
		start = m.selected - rows + 1
	}
	for index := start; index < min(len(processes), start+rows); index++ {
		process := processes[index]
		name := process.Name
		if m.showProcessPath {
			name = processPathDisplay(process)
		}
		var row string
		if full {
			row = fmt.Sprintf("%-7d %-*s %8s %11s %7d", process.PID, nameWidth, truncateText(name, nameWidth),
				formatMetricCompact(process.CPU, 1), formatMetricCompact(process.Memory, 1), process.Threads)
		} else if medium {
			row = fmt.Sprintf("%-7d %-*s %8s %10s", process.PID, nameWidth, truncateText(name, nameWidth),
				formatMetricCompact(process.CPU, 1), formatMetricCompact(process.Memory, 1))
		} else {
			row = fmt.Sprintf("%-6d %-*s %7s", process.PID, nameWidth, truncateText(name, nameWidth), formatMetricCompact(process.CPU, 1))
		}
		row = ansi.Truncate(row, width, "")
		if selectable && index == m.selected {
			row = lipgloss.NewStyle().Reverse(true).Render(row)
		}
		lines = append(lines, row)
	}
	// On ALL the process table is the flexible middle region. Pad it to its
	// assigned height so the fixed summary panes stay anchored to the bottom,
	// even when there are only a few processes to display.
	if !selectable {
		for len(lines) < height {
			lines = append(lines, strings.Repeat(" ", max(1, width)))
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderJtopProcesses(height int) string {
	detailHeight := 0
	if m.showProcessDetail && len(m.processes) > 0 {
		detailHeight = 6
		if m.showProcessPath {
			detailHeight++
		}
	}
	table := m.jtopProcessTable(max(1, height-detailHeight), m.width, m.processSort, true)
	if detailHeight == 0 || m.selected >= len(m.processes) {
		return table
	}
	return table + "\n" + jtopProcessDetail(m.processes[m.selected], m.width, m.showProcessPath)
}

func jtopProcessDetail(process model.ProcessInfo, width int, showPath bool) string {
	lines := []string{
		jtopHeaderStyle.Width(width).MaxWidth(width).Render("PROCESS DETAIL"),
		fmt.Sprintf("PID %-7d %-*s", process.PID, max(1, width-12), process.Name),
		fmt.Sprintf("Threads %-5d CPU %-9s MEM %s", process.Threads,
			formatMetricCompact(process.CPU, 1), formatMetricCompact(process.Memory, 1)),
	}
	if showPath {
		lines = append(lines, "Path: "+processPathDisplay(process))
	}
	if process.CPU.Source != "" {
		lines = append(lines, "CPU:  "+process.CPU.Source)
	}
	return fitLines(strings.Join(lines, "\n"), width)
}

func (m Model) renderJtopInfo(_ int) string {
	left := []string{
		titleStyle.Render("Platform"),
		"  System: " + fallback(m.latest.System.OS, "--"),
		"  Kernel: " + fallback(m.latest.System.Kernel, "--"),
		"  Arch:   " + fallback(m.latest.System.Arch, "--"),
		"",
		titleStyle.Render("Runtime"),
		"  Updated: " + m.latest.Timestamp.Format("2006-01-02 15:04:05"),
		fmt.Sprintf("  Tasks:   %s", formatMetricNumber(m.latest.Load.Tasks, 0)),
	}
	right := []string{
		titleStyle.Render("Hardware"),
		"  Model:    " + fallback(m.latest.System.Board, "--"),
		"  SoC:      " + fallback(m.latest.System.SoC, "--"),
		"  Hostname: " + fallback(m.latest.System.Hostname, "--"),
		"",
		titleStyle.Render("Drivers"),
	}
	for _, driver := range m.latest.System.Drivers {
		right = append(right, fmt.Sprintf("  %-8s %s", driver.Type+":", fallback(driver.Name, driver.Device)))
	}
	if m.accessLimited {
		left = append(left,
			"",
			warnStyle.Render("Access"),
			"  Mode:   standard user",
			"  Status: limited",
			"  Hint:   sudo rtop for more metrics",
			"  Check:  rtop doctor",
		)
	}
	if len(m.latest.Warnings) > 0 {
		left = append(left, "", warnStyle.Render("Diagnostics"))
		for _, warning := range m.latest.Warnings {
			left = append(left, "  "+warning.Component+": "+warning.Message)
		}
	}
	leftWidth := m.width
	if m.width < 60 {
		return fitLines(strings.Join(append(left, append([]string{""}, right...)...), "\n"), m.width)
	}
	leftWidth = m.width / 2
	rightWidth := m.width - leftWidth
	return fitLines(joinJtopColumns(strings.Join(left, "\n"), strings.Join(right, "\n"), leftWidth, rightWidth, false), m.width)
}
