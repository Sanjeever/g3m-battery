//go:build windows

package main

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

var (
	historyShowWindow       = user32IconDLL.NewProc("ShowWindow")
	historyUpdateWindow     = user32IconDLL.NewProc("UpdateWindow")
	historyInvalidateRect   = user32IconDLL.NewProc("InvalidateRect")
	historyGetClientRect    = user32IconDLL.NewProc("GetClientRect")
	historyBeginPaint       = user32IconDLL.NewProc("BeginPaint")
	historyEndPaint         = user32IconDLL.NewProc("EndPaint")
	historySetTimer         = user32IconDLL.NewProc("SetTimer")
	historyKillTimer        = user32IconDLL.NewProc("KillTimer")
	historyTrackMouseEvent  = user32IconDLL.NewProc("TrackMouseEvent")
	historyScreenToClient   = user32IconDLL.NewProc("ScreenToClient")
	historyGetSysColor      = user32IconDLL.NewProc("GetSysColor")
	historyFillRect         = user32IconDLL.NewProc("FillRect")
	historyFrameRect        = user32IconDLL.NewProc("FrameRect")
	historyDrawTextW        = user32IconDLL.NewProc("DrawTextW")
	historyCreateSolidBrush = gdi32DLL.NewProc("CreateSolidBrush")
	historyCreatePen        = gdi32DLL.NewProc("CreatePen")
	historyCreateFontW      = gdi32DLL.NewProc("CreateFontW")
	historyGetDeviceCaps    = gdi32DLL.NewProc("GetDeviceCaps")
	historySelectObject     = gdi32DLL.NewProc("SelectObject")
	historySetTextColor     = gdi32DLL.NewProc("SetTextColor")
	historySetBkMode        = gdi32DLL.NewProc("SetBkMode")
	historyMoveToEx         = gdi32DLL.NewProc("MoveToEx")
	historyLineTo           = gdi32DLL.NewProc("LineTo")
	historyEllipse          = gdi32DLL.NewProc("Ellipse")
)

const (
	historyWindowStyle  = 0x00CF0000 | 0x10000000
	historyWindowTimer  = 1
	historyWindowWidth  = 1080
	historyWindowHeight = 860

	historyShow          = 5
	historyRestore       = 9
	historyMouseLeave    = 0x02A3
	historyMouseMove     = 0x0200
	historyMouseWheel    = 0x020A
	historyPaint         = 0x000F
	historyEraseBkgnd    = 0x0014
	historySize          = 0x0005
	historyTimer         = 0x0113
	historyClose         = 0x0010
	historyGetMinMaxInfo = 0x0024
	historyTrackLeave    = 0x00000002

	historyPSolid = 0
	historyPDot   = 2

	historyTransparent = 1

	historyDTLeft        = 0x00000000
	historyDTCenter      = 0x00000001
	historyDTRight       = 0x00000002
	historyDTVCenter     = 0x00000004
	historyDTSingleLine  = 0x00000020
	historyDTNoPrefix    = 0x00000800
	historyDTEndEllipsis = 0x00008000

	historyDefaultCharset          = 1
	historyClearTypeNaturalQuality = 6
	historyLogPixelsY              = 90
)

var historyWindowClassName = syscall.StringToUTF16Ptr("G3MBatteryHistoryWindow")

type historyRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type historyMinMaxInfo struct {
	Reserved     point
	MaxSize      point
	MaxPosition  point
	MinTrackSize point
	MaxTrackSize point
}

type historyPaintStruct struct {
	Hdc       syscall.Handle
	Erase     uint32
	PaintRect historyRect
	Restore   uint32
	IncUpdate uint32
	Reserved  [32]byte
}

type historyTrackMouseEventStruct struct {
	Size      uint32
	Flags     uint32
	HwndTrack syscall.Handle
	HoverTime uint32
}

type historyWindow struct {
	store       *historyStore
	hwnd        syscall.Handle
	windowRange time.Duration
	eventOffset int

	tab24Rect  historyRect
	tab7Rect   historyRect
	plotRect   historyRect
	eventsRect historyRect

	hoverSample historySample
	hasHover    bool
	tracking    bool
}

var (
	activeHistoryWindow *historyWindow
	historyWindowProc   = syscall.NewCallback(historyWindowMessageProc)
)

func showHistoryWindow(store *historyStore) error {
	if activeHistoryWindow != nil && activeHistoryWindow.hwnd != 0 {
		historyShowWindow.Call(uintptr(activeHistoryWindow.hwnd), historyRestore)
		setForegroundWindow.Call(uintptr(activeHistoryWindow.hwnd))
		historyInvalidateRect.Call(uintptr(activeHistoryWindow.hwnd), 0, 1)
		return nil
	}

	instance, _, callErr := getModuleHandleW.Call(0)
	if instance == 0 {
		return fmt.Errorf("GetModuleHandleW: %w", callErr)
	}

	class := wndClassEx{
		CbSize:    uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc:   historyWindowProc,
		HInstance: syscall.Handle(instance),
		ClassName: historyWindowClassName,
	}
	atom, _, callErr := registerClassExW.Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 && callErr != syscall.Errno(1410) {
		return fmt.Errorf("RegisterClassExW: %w", callErr)
	}

	window := &historyWindow{
		store:       store,
		windowRange: 24 * time.Hour,
	}
	activeHistoryWindow = window

	title := syscall.StringToUTF16Ptr("G3M Pro 电量历史")
	hwnd, _, callErr := createWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(historyWindowClassName)),
		uintptr(unsafe.Pointer(title)),
		historyWindowStyle,
		uintptr(0x80000000),
		uintptr(0x80000000),
		historyWindowWidth,
		historyWindowHeight,
		0,
		0,
		instance,
		0,
	)
	if hwnd == 0 {
		activeHistoryWindow = nil
		return fmt.Errorf("CreateWindowExW: %w", callErr)
	}

	window.hwnd = syscall.Handle(hwnd)
	historySetTimer.Call(hwnd, historyWindowTimer, 5000, 0)
	historyShowWindow.Call(hwnd, historyShow)
	historyUpdateWindow.Call(hwnd)
	setForegroundWindow.Call(hwnd)
	return nil
}

func closeHistoryWindow() {
	if activeHistoryWindow == nil || activeHistoryWindow.hwnd == 0 {
		return
	}
	hwnd := activeHistoryWindow.hwnd
	historyKillTimer.Call(uintptr(hwnd), historyWindowTimer)
	destroyWindow.Call(uintptr(hwnd))
}

func historyWindowMessageProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	window := activeHistoryWindow
	if window != nil && window.hwnd == 0 {
		window.hwnd = syscall.Handle(hwnd)
	}

	switch message {
	case historyEraseBkgnd:
		return 1
	case historyPaint:
		if window == nil {
			break
		}
		var paint historyPaintStruct
		hdc, _, _ := historyBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&paint)))
		if hdc != 0 {
			window.paint(syscall.Handle(hdc))
			historyEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&paint)))
		}
		return 0
	case historySize:
		if window != nil {
			historyInvalidateRect.Call(hwnd, 0, 1)
		}
		return 0
	case historyGetMinMaxInfo:
		if lParam != 0 {
			info := (*historyMinMaxInfo)(unsafe.Pointer(lParam))
			info.MinTrackSize = point{X: 900, Y: 720}
		}
		return 0
	case historyTimer:
		if window != nil {
			historyInvalidateRect.Call(hwnd, 0, 1)
		}
		return 0
	case wmLButtonUp:
		if window != nil {
			window.handleClick(historyMessageX(lParam), historyMessageY(lParam))
		}
		return 0
	case historyMouseMove:
		if window != nil {
			window.handleMouseMove(historyMessageX(lParam), historyMessageY(lParam))
		}
		return 0
	case historyMouseLeave:
		if window != nil {
			window.tracking = false
			if window.hasHover {
				window.hasHover = false
				historyInvalidateRect.Call(hwnd, 0, 1)
			}
		}
		return 0
	case historyMouseWheel:
		if window != nil {
			window.handleMouseWheel(historyMessageX(lParam), historyMessageY(lParam), int16(wParam>>16))
		}
		return 0
	case historyClose:
		destroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		if window != nil {
			historyKillTimer.Call(hwnd, historyWindowTimer)
		}
		activeHistoryWindow = nil
		return 0
	}

	ret, _, _ := defWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return ret
}

func (w *historyWindow) handleClick(x, y int32) {
	switch {
	case historyRectContains(w.tab24Rect, x, y):
		w.windowRange = 24 * time.Hour
		w.eventOffset = 0
		w.hasHover = false
	case historyRectContains(w.tab7Rect, x, y):
		w.windowRange = 7 * 24 * time.Hour
		w.eventOffset = 0
		w.hasHover = false
	}
	historyInvalidateRect.Call(uintptr(w.hwnd), 0, 1)
}

func (w *historyWindow) handleMouseMove(x, y int32) {
	if !historyRectContains(w.plotRect, x, y) {
		if w.hasHover {
			w.hasHover = false
			historyInvalidateRect.Call(uintptr(w.hwnd), 0, 1)
		}
		return
	}

	if !w.tracking {
		tracking := historyTrackMouseEventStruct{
			Size:      uint32(unsafe.Sizeof(historyTrackMouseEventStruct{})),
			Flags:     historyTrackLeave,
			HwndTrack: w.hwnd,
		}
		historyTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tracking)))
		w.tracking = true
	}

	data := w.store.snapshot()
	sample, ok := nearestHistorySample(data, w.windowRange, time.Now(), x, w.plotRect)
	if w.hasHover == ok && (!ok || w.hoverSample.At == sample.At) {
		return
	}
	w.hasHover = ok
	if ok {
		w.hoverSample = sample
	}
	historyInvalidateRect.Call(uintptr(w.hwnd), 0, 1)
}

func (w *historyWindow) handleMouseWheel(x, y int32, delta int16) {
	cursor := point{X: x, Y: y}
	historyScreenToClient.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&cursor)))
	x = cursor.X
	y = cursor.Y
	if !historyRectContains(w.eventsRect, x, y) {
		return
	}
	data := w.store.snapshot()
	visibleRows := int((w.eventsRect.Bottom - w.eventsRect.Top - 42) / 30)
	if visibleRows < 1 {
		visibleRows = 1
	}
	maxOffset := len(data.Incidents) - visibleRows
	if maxOffset < 0 {
		maxOffset = 0
	}
	if delta > 0 {
		w.eventOffset -= 3
	} else if delta < 0 {
		w.eventOffset += 3
	}
	if w.eventOffset < 0 {
		w.eventOffset = 0
	}
	if w.eventOffset > maxOffset {
		w.eventOffset = maxOffset
	}
	historyInvalidateRect.Call(uintptr(w.hwnd), 0, 1)
}

func (w *historyWindow) paint(hdc syscall.Handle) {
	var client historyRect
	historyGetClientRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&client)))
	historyFill(hdc, client, historyColorBackground)

	data := w.store.snapshot()
	now := time.Now()
	metrics := buildHistoryMetrics(data, now)

	margin := int32(28)
	contentRight := client.Right - margin

	historyDrawText(hdc, "G3M Pro 电量历史", historyRect{Left: margin, Top: 22, Right: contentRight - 220, Bottom: 54}, historyColorInk, 18, 600, historyDTLeft|historyDTSingleLine|historyDTVCenter)
	historyDrawText(hdc, "实时状态的连续记录 · 窗口会自动更新", historyRect{Left: margin, Top: 56, Right: contentRight - 220, Bottom: 78}, historyColorMuted, 12, 400, historyDTLeft|historyDTSingleLine|historyDTVCenter)

	w.tab24Rect = historyRect{Left: contentRight - 174, Top: 28, Right: contentRight - 94, Bottom: 60}
	w.tab7Rect = historyRect{Left: contentRight - 88, Top: 28, Right: contentRight - 8, Bottom: 60}
	historyDrawButton(hdc, w.tab24Rect, "24 小时", w.windowRange == 24*time.Hour)
	historyDrawButton(hdc, w.tab7Rect, "7 天", w.windowRange == 7*24*time.Hour)

	statusRect := historyRect{Left: margin, Top: 94, Right: contentRight, Bottom: 184}
	historyDrawCard(hdc, statusRect, historyColorSurface, historyColorBorder)
	w.drawStatus(hdc, statusRect, data, metrics, now)

	chartTop := int32(200)
	chartHeight := int32(328)
	if client.Bottom < 800 {
		chartHeight = client.Bottom - 472
		if chartHeight < 230 {
			chartHeight = 230
		}
	}
	chartRect := historyRect{Left: margin, Top: chartTop, Right: contentRight, Bottom: chartTop + chartHeight}
	historyDrawCard(hdc, chartRect, historyColorSurface, historyColorBorder)
	historyDrawChart(hdc, chartRect, data, now, w)

	metricsTop := chartRect.Bottom + 16
	metricHeight := int32(96)
	metricGap := int32(12)
	metricWidth := (contentRight - margin - metricGap*2) / 3
	for index := 0; index < 3; index++ {
		left := margin + int32(index)*(metricWidth+metricGap)
		metricRect := historyRect{Left: left, Top: metricsTop, Right: left + metricWidth, Bottom: metricsTop + metricHeight}
		historyDrawCard(hdc, metricRect, historyColorSurface, historyColorBorder)
		switch index {
		case 0:
			value := "暂无估算"
			hint := historyEstimateHint(data, metrics, now)
			if metrics.hasRemaining {
				value = "约 " + formatHistoryDuration(metrics.remaining)
				hint = "基于最近连续放电数据"
			}
			historyDrawMetric(hdc, metricRect, "续航估算（约）", value, hint)
		case 1:
			value := "暂无完整记录"
			hint := "需要从充电开始到结束的连续数据"
			if metrics.hasCurrentCharging {
				value = "充电中 · " + formatHistoryDuration(metrics.currentCharging)
				hint = "当前连续充电观测"
			} else if metrics.hasLastCharging {
				value = formatHistoryDuration(metrics.lastCharging)
				hint = "最近一次连续充电观测"
			}
			historyDrawMetric(hdc, metricRect, "充电观测", value, hint)
		case 2:
			value := "暂无完整记录"
			hint := "从满电到 20% 的连续观测"
			if metrics.hasLastDischarge {
				value = formatHistoryDuration(metrics.lastDischarge)
				hint = "从 100% 到 20% 的观测时长"
			}
			historyDrawMetric(hdc, metricRect, "完整放电观测", value, hint)
		}
	}

	eventsTop := metricsTop + metricHeight + 16
	w.eventsRect = historyRect{Left: margin, Top: eventsTop, Right: contentRight, Bottom: client.Bottom - margin}
	historyDrawCard(hdc, w.eventsRect, historyColorSurface, historyColorBorder)
	historyDrawEvents(hdc, w.eventsRect, data, w.eventOffset)
}

func (w *historyWindow) drawStatus(hdc syscall.Handle, rect historyRect, data historyData, metrics historyMetrics, now time.Time) {
	title, detail, color := historyWindowStatus(data, metrics, now)
	historyDrawText(hdc, "当前状态", historyRect{Left: rect.Left + 18, Top: rect.Top + 12, Right: rect.Left + 160, Bottom: rect.Top + 32}, historyColorMuted, 12, 600, historyDTLeft|historyDTSingleLine|historyDTVCenter)
	historyDrawText(hdc, title, historyRect{Left: rect.Left + 18, Top: rect.Top + 35, Right: rect.Left + 470, Bottom: rect.Top + 64}, color, 20, 700, historyDTLeft|historyDTSingleLine|historyDTVCenter|historyDTEndEllipsis)
	historyDrawText(hdc, detail, historyRect{Left: rect.Left + 18, Top: rect.Top + 66, Right: rect.Left + 520, Bottom: rect.Top + 84}, historyColorMuted, 12, 400, historyDTLeft|historyDTSingleLine|historyDTVCenter|historyDTEndEllipsis)

	historyDrawText(hdc, "数据范围", historyRect{Left: rect.Right - 330, Top: rect.Top + 15, Right: rect.Right - 18, Bottom: rect.Top + 34}, historyColorMuted, 12, 600, historyDTRight|historyDTSingleLine|historyDTVCenter)
	if len(data.Samples) == 0 {
		historyDrawText(hdc, "尚未形成有效记录", historyRect{Left: rect.Right - 330, Top: rect.Top + 39, Right: rect.Right - 18, Bottom: rect.Top + 62}, historyColorInk, 15, 600, historyDTRight|historyDTSingleLine|historyDTVCenter)
		historyDrawText(hdc, "成功读取设备后自动开始记录", historyRect{Left: rect.Right - 330, Top: rect.Top + 66, Right: rect.Right - 18, Bottom: rect.Top + 84}, historyColorMuted, 12, 400, historyDTRight|historyDTSingleLine|historyDTVCenter)
		return
	}
	historyDrawText(hdc, historyTime(data.Samples[0].At)+" — "+historyTime(data.Samples[len(data.Samples)-1].At), historyRect{Left: rect.Right - 520, Top: rect.Top + 39, Right: rect.Right - 18, Bottom: rect.Top + 62}, historyColorInk, 15, 600, historyDTRight|historyDTSingleLine|historyDTVCenter)
	historyDrawText(hdc, fmt.Sprintf("%d 次有效采样 · %d 条异常记录", len(data.Samples), len(data.Incidents)), historyRect{Left: rect.Right - 330, Top: rect.Top + 66, Right: rect.Right - 18, Bottom: rect.Top + 84}, historyColorMuted, 12, 400, historyDTRight|historyDTSingleLine|historyDTVCenter)
}

func historyWindowStatus(data historyData, metrics historyMetrics, now time.Time) (string, string, uint32) {
	if metrics.hasActiveIncident {
		detail := "当前读取异常"
		if metrics.hasLastSample {
			detail = "最后有效状态：" + historySampleText(metrics.lastSample) + " · " + historyRelativeTime(metrics.lastSample.At, now)
		}
		return errorKindText(metrics.activeIncident.Kind), detail, historyColorRed
	}
	if !metrics.hasLastSample {
		return "暂无成功读取记录", "成功读取设备后，历史记录会自动开始", historyColorMuted
	}
	last := metrics.lastSample
	if now.Unix()-last.At > int64(historyMaxSampleGap/time.Second) {
		return "数据暂未更新", "最后有效状态：" + historySampleText(last) + " · " + historyTime(last.At), historyColorOrange
	}
	return historySampleText(last), "最近采样：" + historyRelativeTime(last.At, now), historyPercentColor(last.Percent)
}

func historyEstimateHint(data historyData, metrics historyMetrics, now time.Time) string {
	if metrics.hasActiveIncident {
		return "当前存在读取异常，暂不估算"
	}
	if len(data.Samples) < 2 {
		return "需要至少两次连续有效采样"
	}
	last := data.Samples[len(data.Samples)-1]
	if last.Charge != ChargeNormal {
		return "充电或状态变化时不估算"
	}
	if now.Unix()-last.At > int64(historyMaxSampleGap/time.Second) {
		return "最近采样已超过 10 分钟"
	}
	return "需要连续 30 分钟且下降至少 2%"
}

func historyRelativeTime(unix int64, now time.Time) string {
	seconds := now.Unix() - unix
	if seconds < 60 {
		return "刚刚"
	}
	if seconds < int64(time.Hour/time.Second) {
		return fmt.Sprintf("%d 分钟前", seconds/int64(time.Minute/time.Second))
	}
	if seconds < int64(24*time.Hour/time.Second) {
		return fmt.Sprintf("%d 小时前", seconds/int64(time.Hour/time.Second))
	}
	return historyTime(unix)
}

func historyPercentColor(percent int) uint32 {
	if percent < 20 {
		return historyColorRed
	}
	if percent < 50 {
		return historyColorOrange
	}
	return historyColorGreen
}

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

func historyDrawEvents(hdc syscall.Handle, rect historyRect, data historyData, offset int) {
	historyDrawText(hdc, "事件记录", historyRect{Left: rect.Left + 18, Top: rect.Top + 14, Right: rect.Left + 180, Bottom: rect.Top + 38}, historyColorInk, 13, 600, historyDTLeft|historyDTSingleLine|historyDTVCenter)
	historyDrawText(hdc, fmt.Sprintf("%d 条 · 滚轮查看更多", len(data.Incidents)), historyRect{Left: rect.Right - 220, Top: rect.Top + 15, Right: rect.Right - 18, Bottom: rect.Top + 37}, historyColorMuted, 11, 400, historyDTRight|historyDTSingleLine|historyDTVCenter)

	if len(data.Incidents) == 0 {
		historyDrawText(hdc, "最近 8 天没有设备断连或读取失败记录", historyRect{Left: rect.Left + 18, Top: rect.Top + 58, Right: rect.Right - 18, Bottom: rect.Top + 88}, historyColorMuted, 13, 400, historyDTLeft|historyDTSingleLine|historyDTVCenter)
		return
	}

	rowTop := rect.Top + 46
	visibleRows := int((rect.Bottom - rowTop - 10) / 30)
	if visibleRows < 1 {
		visibleRows = 1
	}
	for row := 0; row < visibleRows; row++ {
		index := len(data.Incidents) - 1 - offset - row
		if index < 0 {
			break
		}
		incident := data.Incidents[index]
		top := rowTop + int32(row)*30
		if row%2 == 1 {
			historyFill(hdc, historyRect{Left: rect.Left + 12, Top: top, Right: rect.Right - 12, Bottom: top + 29}, historyColorSurfaceAlt)
		}
		duration := "进行中"
		end := "未结束"
		if incident.EndedAt != 0 {
			end = historyTime(incident.EndedAt)
			duration = formatHistoryDuration(time.Duration(incident.EndedAt-incident.StartedAt) * time.Second)
		}
		historyDrawText(hdc, errorKindText(incident.Kind), historyRect{Left: rect.Left + 22, Top: top + 5, Right: rect.Left + 130, Bottom: top + 25}, historyColorRed, 12, 600, historyDTLeft|historyDTSingleLine|historyDTVCenter)
		historyDrawText(hdc, historyTime(incident.StartedAt), historyRect{Left: rect.Left + 142, Top: top + 5, Right: rect.Left + 290, Bottom: top + 25}, historyColorInk, 11, 400, historyDTLeft|historyDTSingleLine|historyDTVCenter)
		historyDrawText(hdc, end, historyRect{Left: rect.Left + 300, Top: top + 5, Right: rect.Left + 448, Bottom: top + 25}, historyColorMuted, 11, 400, historyDTLeft|historyDTSingleLine|historyDTVCenter)
		historyDrawText(hdc, duration, historyRect{Left: rect.Left + 458, Top: top + 5, Right: rect.Left + 580, Bottom: top + 25}, historyColorInk, 11, 600, historyDTLeft|historyDTSingleLine|historyDTVCenter)
		historyDrawText(hdc, incident.Message, historyRect{Left: rect.Left + 590, Top: top + 5, Right: rect.Right - 22, Bottom: top + 25}, historyColorMuted, 11, 400, historyDTLeft|historyDTSingleLine|historyDTEndEllipsis|historyDTVCenter)
	}
}

func historyDrawMetric(hdc syscall.Handle, rect historyRect, label, value, hint string) {
	historyDrawText(hdc, label, historyRect{Left: rect.Left + 18, Top: rect.Top + 14, Right: rect.Right - 14, Bottom: rect.Top + 33}, historyColorMuted, 11, 600, historyDTLeft|historyDTSingleLine|historyDTVCenter)
	historyDrawText(hdc, value, historyRect{Left: rect.Left + 18, Top: rect.Top + 36, Right: rect.Right - 14, Bottom: rect.Top + 62}, historyColorInk, 15, 600, historyDTLeft|historyDTSingleLine|historyDTVCenter|historyDTEndEllipsis)
	historyDrawText(hdc, hint, historyRect{Left: rect.Left + 18, Top: rect.Top + 68, Right: rect.Right - 14, Bottom: rect.Bottom - 12}, historyColorMuted, 10, 400, historyDTLeft|historyDTSingleLine|historyDTVCenter|historyDTEndEllipsis)
}

func historyDrawButton(hdc syscall.Handle, rect historyRect, text string, selected bool) {
	fill := uint32(historyColorButtonFace)
	textColor := uint32(historyColorInk)
	border := uint32(historyColorBorder)
	if selected {
		fill = historyColorBlue
		textColor = historyColorWhite
		border = historyColorBlue
	}
	historyDrawCard(hdc, rect, fill, border)
	historyDrawText(hdc, text, rect, textColor, 11, 400, historyDTCenter|historyDTSingleLine|historyDTVCenter)
}

func historyDrawCard(hdc syscall.Handle, rect historyRect, fill, border uint32) {
	historyFill(hdc, rect, fill)
	brush, _, _ := historyCreateSolidBrush.Call(uintptr(border))
	nativeRect := rect
	historyFrameRect.Call(uintptr(hdc), uintptr(unsafe.Pointer(&nativeRect)), brush)
	deleteGDIObject.Call(brush)
}

func historyDrawPoint(hdc syscall.Handle, x, y int32, color uint32) {
	brush, _, _ := historyCreateSolidBrush.Call(uintptr(color))
	oldBrush, _, _ := historySelectObject.Call(uintptr(hdc), brush)
	historyEllipse.Call(uintptr(hdc), uintptr(x-3), uintptr(y-3), uintptr(x+4), uintptr(y+4))
	historySelectObject.Call(uintptr(hdc), oldBrush)
	deleteGDIObject.Call(brush)
}

func historySelectPenAndDraw(hdc syscall.Handle, color uint32, width, style int, draw func()) {
	pen, _, _ := historyCreatePen.Call(uintptr(style), uintptr(width), uintptr(color))
	oldPen, _, _ := historySelectObject.Call(uintptr(hdc), pen)
	draw()
	historySelectObject.Call(uintptr(hdc), oldPen)
	deleteGDIObject.Call(pen)
}

func historyDrawLine(hdc syscall.Handle, x1, y1, x2, y2 int32, color uint32, width, style int) {
	historySelectPenAndDraw(hdc, color, width, style, func() {
		historyMoveToEx.Call(uintptr(hdc), uintptr(x1), uintptr(y1), 0)
		historyLineTo.Call(uintptr(hdc), uintptr(x2), uintptr(y2))
	})
}

func historyDrawText(hdc syscall.Handle, text string, rect historyRect, color uint32, size, weight int32, format uint32) {
	if text == "" {
		return
	}
	textPtr, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	dpi, _, _ := historyGetDeviceCaps.Call(uintptr(hdc), historyLogPixelsY)
	if dpi == 0 {
		dpi = 96
	}
	size = size * int32(dpi) / 96
	font, _, _ := historyCreateFontW.Call(
		uintptr(int64(-size)),
		0,
		0,
		0,
		uintptr(weight),
		0,
		0,
		0,
		uintptr(historyDefaultCharset),
		0,
		0,
		uintptr(historyClearTypeNaturalQuality),
		0,
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("Segoe UI"))),
	)
	oldFont, _, _ := historySelectObject.Call(uintptr(hdc), font)
	historySetTextColor.Call(uintptr(hdc), uintptr(color))
	historySetBkMode.Call(uintptr(hdc), historyTransparent)
	nativeRect := rect
	historyDrawTextW.Call(uintptr(hdc), uintptr(unsafe.Pointer(textPtr)), uintptr(^uint(0)), uintptr(unsafe.Pointer(&nativeRect)), uintptr(format|historyDTNoPrefix))
	historySelectObject.Call(uintptr(hdc), oldFont)
	deleteGDIObject.Call(font)
}

func historyFill(hdc syscall.Handle, rect historyRect, color uint32) {
	brush, _, _ := historyCreateSolidBrush.Call(uintptr(color))
	nativeRect := rect
	historyFillRect.Call(uintptr(hdc), uintptr(unsafe.Pointer(&nativeRect)), brush)
	deleteGDIObject.Call(brush)
}

func historyRectContains(rect historyRect, x, y int32) bool {
	return x >= rect.Left && x < rect.Right && y >= rect.Top && y < rect.Bottom
}

func historyMessageX(lParam uintptr) int32 {
	return int32(int16(lParam & 0xffff))
}

func historyMessageY(lParam uintptr) int32 {
	return int32(int16((lParam >> 16) & 0xffff))
}

const (
	historyColorGreen          = 0x00618A16
	historyColorOrange         = 0x001675C8
	historyColorRed            = 0x004C3DC5
	historyColorIncidentFill   = 0x00EAE8FC
	historyColorChargingFill   = 0x00D9F2FF
	historyColorUnobservedFill = 0x00F6F3F0
)

const (
	historySysColorButtonFace    = 15
	historySysColorWindow        = 5
	historySysColorWindowText    = 8
	historySysColorGrayText      = 17
	historySysColorHighlight     = 13
	historySysColorHighlightText = 14
	historySysColor3DLight       = 22
	historySysColor3DShadow      = 16
)

var (
	historyColorBackground = historySystemColor(historySysColorButtonFace)
	historyColorSurface    = historySystemColor(historySysColorWindow)
	historyColorSurfaceAlt = historySystemColor(historySysColorButtonFace)
	historyColorBorder     = historySystemColor(historySysColor3DShadow)
	historyColorGrid       = historySystemColor(historySysColor3DLight)
	historyColorInk        = historySystemColor(historySysColorWindowText)
	historyColorMuted      = historySystemColor(historySysColorGrayText)
	historyColorBlue       = historySystemColor(historySysColorHighlight)
	historyColorBlueMuted  = historySystemColor(historySysColor3DShadow)
	historyColorWhite      = historySystemColor(historySysColorHighlightText)
	historyColorButtonFace = historySystemColor(historySysColorButtonFace)
	historyColorUnobserved = historySystemColor(historySysColorGrayText)
)

func historySystemColor(index uint32) uint32 {
	color, _, _ := historyGetSysColor.Call(uintptr(index))
	return uint32(color)
}
