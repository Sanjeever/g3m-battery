//go:build windows

package main

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

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
	if data.sessionStartedAt != 0 && last.At < data.sessionStartedAt {
		return "等待本次会话采样", "上次有效状态：" + historySampleText(last) + " · 程序刚刚启动", historyColorOrange
	}
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
	if data.sessionStartedAt != 0 && last.At < data.sessionStartedAt {
		return "等待本次启动后的有效采样"
	}
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
