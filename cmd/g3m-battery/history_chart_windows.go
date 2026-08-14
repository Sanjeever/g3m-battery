//go:build windows

package main

import (
	"fmt"
	"syscall"
	"time"
)

func historyDrawChart(hdc syscall.Handle, card historyRect, data historyData, now time.Time, window *historyWindow) {
	historyDrawText(hdc, "电量趋势", historyRect{Left: card.Left + 18, Top: card.Top + 14, Right: card.Left + 170, Bottom: card.Top + 38}, historyColorInk, 13, 600, historyDTLeft|historyDTSingleLine|historyDTVCenter)
	historyDrawLegend(hdc, card)

	plot := historyRect{Left: card.Left + 58, Top: card.Top + 52, Right: card.Right - 22, Bottom: card.Bottom - 42}
	window.plotRect = plot
	start := now.Add(-window.windowRange).Unix()
	end := now.Unix()

	historyDrawUnobservedBands(hdc, plot, data, start, end)
	historyDrawChargeBands(hdc, plot, data, start, end)
	for _, incident := range data.Incidents {
		incidentEnd := incident.EndedAt
		if incidentEnd == 0 || incidentEnd > end {
			incidentEnd = end
		}
		left := incident.StartedAt
		if left < start {
			left = start
		}
		if incidentEnd > left {
			historyFill(hdc, historyRect{Left: historyChartX(left, start, end, plot), Top: plot.Top, Right: historyChartX(incidentEnd, start, end, plot), Bottom: plot.Bottom}, historyColorIncidentFill)
		}
	}

	for _, level := range []int{0, 20, 40, 60, 80, 100} {
		y := plot.Bottom - int32(float64(level)/100*float64(plot.Bottom-plot.Top))
		historyDrawLine(hdc, plot.Left, y, plot.Right, y, historyColorGrid, 1, historyPSolid)
		historyDrawText(hdc, fmt.Sprintf("%d%%", level), historyRect{Left: card.Left + 10, Top: y - 9, Right: plot.Left - 8, Bottom: y + 9}, historyColorMuted, 11, 400, historyDTRight|historyDTSingleLine|historyDTVCenter)
	}

	historyDrawText(hdc, historyTime(start), historyRect{Left: plot.Left, Top: plot.Bottom + 10, Right: plot.Left + 150, Bottom: plot.Bottom + 29}, historyColorMuted, 11, 400, historyDTLeft|historyDTSingleLine|historyDTVCenter)
	historyDrawText(hdc, historyTime(start+(end-start)/2), historyRect{Left: (plot.Left+plot.Right)/2 - 75, Top: plot.Bottom + 10, Right: (plot.Left+plot.Right)/2 + 75, Bottom: plot.Bottom + 29}, historyColorMuted, 11, 400, historyDTCenter|historyDTSingleLine|historyDTVCenter)
	historyDrawText(hdc, historyTime(end), historyRect{Left: plot.Right - 150, Top: plot.Bottom + 10, Right: plot.Right, Bottom: plot.Bottom + 29}, historyColorMuted, 11, 400, historyDTRight|historyDTSingleLine|historyDTVCenter)

	segments := historyChartSegments(data, start, end)
	if len(segments) == 0 {
		historyDrawText(hdc, "此时间段没有有效采样数据", historyRect{Left: plot.Left + 80, Top: (plot.Top+plot.Bottom)/2 - 18, Right: plot.Right - 80, Bottom: (plot.Top+plot.Bottom)/2 + 18}, historyColorMuted, 14, 600, historyDTCenter|historyDTSingleLine|historyDTVCenter)
	}
	for _, segment := range segments {
		if len(segment) == 0 {
			continue
		}
		historySelectPenAndDraw(hdc, historyColorBlue, 2, historyPSolid, func() {
			first := segment[0]
			historyMoveToEx.Call(uintptr(hdc), uintptr(historyChartX(first.At, start, end, plot)), uintptr(historyChartY(first.Percent, plot)), 0)
			for _, sample := range segment[1:] {
				historyLineTo.Call(uintptr(hdc), uintptr(historyChartX(sample.At, start, end, plot)), uintptr(historyChartY(sample.Percent, plot)))
			}
		})
		for _, sample := range segment {
			historyDrawPoint(hdc, historyChartX(sample.At, start, end, plot), historyChartY(sample.Percent, plot), historyChargeColor(sample.Charge))
		}
	}

	if window.hasHover && window.hoverSample.At >= start && window.hoverSample.At <= end {
		x := historyChartX(window.hoverSample.At, start, end, plot)
		historyDrawLine(hdc, x, plot.Top, x, plot.Bottom, historyColorBlueMuted, 1, historyPDot)
		historyDrawText(hdc, fmt.Sprintf("%s · %s", historyTime(window.hoverSample.At), historySampleText(window.hoverSample)), historyRect{Left: card.Right - 370, Top: card.Top + 14, Right: card.Right - 18, Bottom: card.Top + 38}, historyColorBlue, 11, 600, historyDTRight|historyDTSingleLine|historyDTVCenter|historyDTEndEllipsis)
	}
}

func historyDrawLegend(hdc syscall.Handle, card historyRect) {
	x := card.Right - 310
	y := card.Top + 19
	historyDrawLegendItem(hdc, x, y, historyColorBlue, "连续观测")
	historyDrawLegendItem(hdc, x+78, y, historyColorOrange, "充电")
	historyDrawLegendItem(hdc, x+126, y, historyColorRed, "异常")
	historyDrawLegendItem(hdc, x+174, y, historyColorUnobserved, "未观测")
}

func historyDrawLegendItem(hdc syscall.Handle, x, y int32, color uint32, text string) {
	historyFill(hdc, historyRect{Left: x, Top: y - 5, Right: x + 10, Bottom: y + 5}, color)
	historyDrawText(hdc, text, historyRect{Left: x + 14, Top: y - 10, Right: x + 72, Bottom: y + 10}, historyColorMuted, 10, 400, historyDTLeft|historyDTSingleLine|historyDTVCenter)
}

func historyDrawChargeBands(hdc syscall.Handle, plot historyRect, data historyData, start, end int64) {
	plotEnd := end
	var previous historySample
	var hasPrevious bool
	var activeStart int64
	lastAt := int64(0)
	for _, sample := range data.Samples {
		lastAt = sample.At
		if hasPrevious && !historySamplesContinuous(data, previous, sample) {
			if activeStart != 0 {
				historyDrawTimeBand(hdc, plot, activeStart, previous.At, start, end, historyColorChargingFill)
				activeStart = 0
			}
		}
		if sample.Charge == ChargeCharging {
			if activeStart == 0 {
				activeStart = sample.At
			}
		} else if activeStart != 0 {
			historyDrawTimeBand(hdc, plot, activeStart, sample.At, start, end, historyColorChargingFill)
			activeStart = 0
		}
		previous = sample
		hasPrevious = true
	}
	if activeStart != 0 {
		bandEnd := plotEnd
		if lastAt < bandEnd {
			bandEnd = lastAt
		}
		historyDrawTimeBand(hdc, plot, activeStart, bandEnd, start, plotEnd, historyColorChargingFill)
	}
}

func historyDrawUnobservedBands(hdc syscall.Handle, plot historyRect, data historyData, start, end int64) {
	for index := 1; index < len(data.Samples); index++ {
		before := data.Samples[index-1]
		after := data.Samples[index]
		if historySamplesContinuous(data, before, after) {
			continue
		}
		left := before.At
		if left < start {
			left = start
		}
		right := after.At
		if right > end {
			right = end
		}
		if right > left {
			historyDrawTimeBand(hdc, plot, left, right, start, end, historyColorUnobservedFill)
		}
	}
}

func historyDrawTimeBand(hdc syscall.Handle, plot historyRect, from, to, start, end int64, color uint32) {
	if to <= start || from >= end {
		return
	}
	if from < start {
		from = start
	}
	if to > end {
		to = end
	}
	if to <= from {
		return
	}
	historyFill(hdc, historyRect{Left: historyChartX(from, start, end, plot), Top: plot.Top, Right: historyChartX(to, start, end, plot), Bottom: plot.Bottom}, color)
}

func historyChartSegments(data historyData, start, end int64) [][]historySample {
	segments := make([][]historySample, 0)
	var segment []historySample
	var previous historySample
	hasPrevious := false
	for _, sample := range data.Samples {
		if sample.At < start || sample.At > end {
			continue
		}
		if hasPrevious && !historySamplesContinuous(data, previous, sample) {
			if len(segment) > 0 {
				segments = append(segments, segment)
			}
			segment = nil
		}
		segment = append(segment, sample)
		previous = sample
		hasPrevious = true
	}
	if len(segment) > 0 {
		segments = append(segments, segment)
	}
	return segments
}

func nearestHistorySample(data historyData, window time.Duration, now time.Time, x int32, plot historyRect) (historySample, bool) {
	start := now.Add(-window).Unix()
	end := now.Unix()
	if len(data.Samples) == 0 || plot.Right <= plot.Left {
		return historySample{}, false
	}
	var nearest historySample
	bestDistance := int64(1<<63 - 1)
	found := false
	for _, sample := range data.Samples {
		if sample.At < start || sample.At > end {
			continue
		}
		sampleX := historyChartX(sample.At, start, end, plot)
		distance := int64(abs(int(sampleX - x)))
		if !found || distance < bestDistance {
			nearest = sample
			bestDistance = distance
			found = true
		}
	}
	return nearest, found
}

func historyChartX(at, start, end int64, plot historyRect) int32 {
	if at <= start {
		return plot.Left
	}
	if at >= end {
		return plot.Right
	}
	ratio := float64(at-start) / float64(end-start)
	return plot.Left + int32(ratio*float64(plot.Right-plot.Left))
}

func historyChartY(percent int, plot historyRect) int32 {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return plot.Bottom - int32(float64(percent)/100*float64(plot.Bottom-plot.Top))
}

func historyChargeColor(charge ChargeState) uint32 {
	switch charge {
	case ChargeCharging:
		return historyColorOrange
	case ChargeFull:
		return historyColorGreen
	default:
		return historyColorBlue
	}
}
