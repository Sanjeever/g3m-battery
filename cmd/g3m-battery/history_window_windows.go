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
