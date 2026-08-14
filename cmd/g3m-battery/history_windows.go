//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	historyDataVersion       = 1
	historyRetention         = 8 * 24 * time.Hour
	historySampleInterval    = 5 * time.Minute
	historyEstimateWindow    = 6 * time.Hour
	historyMinEstimateWindow = 30 * time.Minute
	historyMaxSampleGap      = 2 * historySampleInterval
)

type historySample struct {
	At        int64       `json:"at"`
	Percent   int         `json:"percent"`
	Transport Transport   `json:"transport"`
	Charge    ChargeState `json:"charge"`
}

type historyIncident struct {
	Kind      ErrorKind `json:"kind"`
	StartedAt int64     `json:"started_at"`
	EndedAt   int64     `json:"ended_at,omitempty"`
	Message   string    `json:"message"`
}

type historyData struct {
	Version   int               `json:"version"`
	Samples   []historySample   `json:"samples"`
	Incidents []historyIncident `json:"incidents"`
}

type historyStore struct {
	mu         sync.Mutex
	path       string
	reportPath string
	data       historyData
}

var shellExecuteW = shell32TrayDLL.NewProc("ShellExecuteW")

func newHistoryStore() (*historyStore, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("获取用户缓存目录: %w", err)
	}

	dataDir := filepath.Join(cacheDir, "G3M Battery")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("创建历史记录目录: %w", err)
	}

	store := &historyStore{
		path:       filepath.Join(dataDir, "history.json"),
		reportPath: filepath.Join(dataDir, "history.html"),
		data: historyData{
			Version: historyDataVersion,
		},
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (h *historyStore) load() error {
	payload, err := os.ReadFile(h.path)
	if err == os.ErrNotExist || os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取历史记录: %w", err)
	}

	var data historyData
	if err := json.Unmarshal(payload, &data); err != nil {
		return fmt.Errorf("解析历史记录: %w", err)
	}
	if data.Version == 0 {
		data.Version = historyDataVersion
	}
	if data.Version != historyDataVersion {
		return fmt.Errorf("历史记录版本不支持: %d", data.Version)
	}
	h.data = data
	return nil
}

func (h *historyStore) record(state BatteryState) error {
	at := state.UpdatedAt
	if at.IsZero() {
		at = time.Now()
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	changed := trimHistory(&h.data, at)
	if state.Error != "" || state.Percent < 0 {
		changed = h.recordIncidentLocked(state, at) || changed
	} else {
		changed = h.closeIncidentLocked(at) || changed
		changed = h.recordSampleLocked(state, at) || changed
	}
	if !changed {
		return nil
	}
	return h.saveLocked()
}

func (h *historyStore) recordSampleLocked(state BatteryState, at time.Time) bool {
	sample := historySample{
		At:        at.Unix(),
		Percent:   state.Percent,
		Transport: state.Transport,
		Charge:    state.Charge,
	}
	if len(h.data.Samples) == 0 {
		h.data.Samples = append(h.data.Samples, sample)
		return true
	}

	last := h.data.Samples[len(h.data.Samples)-1]
	if last.Percent != sample.Percent ||
		last.Transport != sample.Transport ||
		last.Charge != sample.Charge ||
		sample.At-last.At >= int64(historySampleInterval/time.Second) {
		h.data.Samples = append(h.data.Samples, sample)
		return true
	}
	return false
}

func (h *historyStore) recordIncidentLocked(state BatteryState, at time.Time) bool {
	kind := state.ErrorKind
	if kind == ErrorNone {
		kind = ErrorUnknown
	}

	if len(h.data.Incidents) > 0 {
		last := &h.data.Incidents[len(h.data.Incidents)-1]
		if last.EndedAt == 0 {
			if last.Kind == kind {
				return false
			}
			last.EndedAt = at.Unix()
		}
	}

	h.data.Incidents = append(h.data.Incidents, historyIncident{
		Kind:      kind,
		StartedAt: at.Unix(),
		Message:   state.Error,
	})
	return true
}

func (h *historyStore) closeIncidentLocked(at time.Time) bool {
	for index := len(h.data.Incidents) - 1; index >= 0; index-- {
		incident := &h.data.Incidents[index]
		if incident.EndedAt == 0 {
			incident.EndedAt = at.Unix()
			return true
		}
	}
	return false
}

func (h *historyStore) saveLocked() error {
	payload, err := json.MarshalIndent(h.data, "", "  ")
	if err != nil {
		return fmt.Errorf("编码历史记录: %w", err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(h.path, payload, 0600); err != nil {
		return fmt.Errorf("写入历史记录: %w", err)
	}
	return nil
}

func (h *historyStore) snapshot() historyData {
	h.mu.Lock()
	defer h.mu.Unlock()

	data := historyData{
		Version:   h.data.Version,
		Samples:   append([]historySample(nil), h.data.Samples...),
		Incidents: append([]historyIncident(nil), h.data.Incidents...),
	}
	trimHistory(&data, time.Now())
	return data
}

func (h *historyStore) remainingText(state BatteryState) string {
	if state.Error != "" || state.Percent < 0 || state.Charge != ChargeNormal {
		return "暂无估算"
	}

	data := h.snapshot()
	if len(data.Samples) == 0 {
		return "暂无估算"
	}
	last := data.Samples[len(data.Samples)-1]
	if last.Percent != state.Percent ||
		last.Transport != state.Transport ||
		last.Charge != state.Charge {
		return "暂无估算"
	}

	remaining, ok := estimateRemaining(data, time.Now())
	if !ok {
		return "暂无估算"
	}
	return "约 " + formatHistoryDuration(remaining)
}

func (h *historyStore) openReport() error {
	data := h.snapshot()
	report := renderHistoryReport(data, time.Now())
	if err := os.WriteFile(h.reportPath, []byte(report), 0600); err != nil {
		return fmt.Errorf("写入历史报告: %w", err)
	}

	verb, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return fmt.Errorf("打开历史报告动作: %w", err)
	}
	path, err := syscall.UTF16PtrFromString(h.reportPath)
	if err != nil {
		return fmt.Errorf("打开历史报告路径: %w", err)
	}
	result, _, callErr := shellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(path)),
		0,
		0,
		1,
	)
	if result <= 32 {
		if callErr != nil {
			return fmt.Errorf("ShellExecuteW: %w", callErr)
		}
		return fmt.Errorf("ShellExecuteW 返回值异常: %d", result)
	}
	return nil
}

func trimHistory(data *historyData, now time.Time) bool {
	cutoff := now.Add(-historyRetention).Unix()
	changed := false

	samples := make([]historySample, 0, len(data.Samples))
	for _, sample := range data.Samples {
		if sample.At < cutoff {
			changed = true
			continue
		}
		samples = append(samples, sample)
	}
	data.Samples = samples

	incidents := make([]historyIncident, 0, len(data.Incidents))
	for _, incident := range data.Incidents {
		if incident.EndedAt != 0 && incident.EndedAt < cutoff {
			changed = true
			continue
		}
		if incident.StartedAt < cutoff {
			incident.StartedAt = cutoff
			changed = true
		}
		incidents = append(incidents, incident)
	}
	data.Incidents = incidents
	return changed
}

type historyMetrics struct {
	lastSample         historySample
	hasLastSample      bool
	activeIncident     historyIncident
	hasActiveIncident  bool
	currentCharging    time.Duration
	hasCurrentCharging bool
	lastCharging       time.Duration
	hasLastCharging    bool
	lastDischarge      time.Duration
	hasLastDischarge   bool
	remaining          time.Duration
	hasRemaining       bool
}

func buildHistoryMetrics(data historyData, now time.Time) historyMetrics {
	var metrics historyMetrics
	if len(data.Samples) > 0 {
		metrics.lastSample = data.Samples[len(data.Samples)-1]
		metrics.hasLastSample = true
	}
	if len(data.Incidents) > 0 {
		last := data.Incidents[len(data.Incidents)-1]
		if last.EndedAt == 0 {
			metrics.activeIncident = last
			metrics.hasActiveIncident = true
		}
	}

	metrics.lastCharging, metrics.hasLastCharging,
		metrics.currentCharging, metrics.hasCurrentCharging = calculateChargingDurations(data, now)
	metrics.lastDischarge, metrics.hasLastDischarge = calculateDischargeDuration(data)
	if !metrics.hasActiveIncident {
		metrics.remaining, metrics.hasRemaining = estimateRemaining(data, now)
	}
	if metrics.hasActiveIncident {
		metrics.hasCurrentCharging = false
		metrics.hasRemaining = false
	}
	return metrics
}

func calculateChargingDurations(data historyData, now time.Time) (time.Duration, bool, time.Duration, bool) {
	var activeStart int64
	var lastDuration time.Duration
	var hasLast bool

	for index, sample := range data.Samples {
		if index > 0 && !historySamplesContinuous(data, data.Samples[index-1], sample) {
			activeStart = 0
		}
		if sample.Charge == ChargeCharging {
			if activeStart == 0 {
				activeStart = sample.At
			}
			continue
		}
		if activeStart == 0 {
			continue
		}
		if sample.Charge == ChargeFull || sample.Charge == ChargeNormal {
			duration := sample.At - activeStart
			if duration > 0 {
				lastDuration = time.Duration(duration) * time.Second
				hasLast = true
			}
		}
		activeStart = 0
	}

	if activeStart == 0 || len(data.Samples) == 0 {
		return lastDuration, hasLast, 0, false
	}
	lastSample := data.Samples[len(data.Samples)-1]
	if lastSample.Charge != ChargeCharging ||
		now.Unix()-lastSample.At > int64(historyMaxSampleGap/time.Second) {
		return lastDuration, hasLast, 0, false
	}
	duration := now.Unix() - activeStart
	if duration <= 0 {
		return lastDuration, hasLast, 0, false
	}
	return lastDuration, hasLast, time.Duration(duration) * time.Second, true
}

func calculateDischargeDuration(data historyData) (time.Duration, bool) {
	var start int64
	var lastDuration time.Duration
	var hasLast bool

	for index, sample := range data.Samples {
		if index > 0 && !historySamplesContinuous(data, data.Samples[index-1], sample) {
			start = 0
		}
		if sample.Charge != ChargeNormal {
			start = 0
			continue
		}
		if start == 0 {
			if sample.Percent == 100 {
				start = sample.At
			}
			continue
		}
		if sample.Percent <= 20 {
			duration := sample.At - start
			if duration > 0 {
				lastDuration = time.Duration(duration) * time.Second
				hasLast = true
			}
			start = 0
		}
	}
	return lastDuration, hasLast
}

func estimateRemaining(data historyData, now time.Time) (time.Duration, bool) {
	if len(data.Samples) < 2 {
		return 0, false
	}

	last := data.Samples[len(data.Samples)-1]
	if last.Charge != ChargeNormal ||
		now.Unix()-last.At > int64(historyMaxSampleGap/time.Second) {
		return 0, false
	}

	cutoff := now.Add(-historyEstimateWindow).Unix()
	firstIndex := len(data.Samples) - 1
	for index := len(data.Samples) - 2; index >= 0; index-- {
		current := data.Samples[index]
		next := data.Samples[index+1]
		if current.At < cutoff ||
			current.Charge != ChargeNormal ||
			current.Transport != last.Transport ||
			current.Percent < next.Percent ||
			!historySamplesContinuous(data, current, next) {
			break
		}
		firstIndex = index
	}

	first := data.Samples[firstIndex]
	elapsed := time.Duration(last.At-first.At) * time.Second
	drop := first.Percent - last.Percent
	if elapsed < historyMinEstimateWindow || drop < 2 {
		return 0, false
	}

	ratePerHour := float64(drop) / elapsed.Hours()
	if ratePerHour <= 0 {
		return 0, false
	}
	remainingHours := float64(last.Percent) / ratePerHour
	return time.Duration(remainingHours * float64(time.Hour)), true
}

func historySamplesContinuous(data historyData, before, after historySample) bool {
	if after.At-before.At > int64(historyMaxSampleGap/time.Second) {
		return false
	}
	if after.At < before.At {
		return false
	}
	if before.Transport != after.Transport {
		return false
	}
	return !historyIncidentBetween(data.Incidents, before.At, after.At)
}

func historyIncidentBetween(incidents []historyIncident, start, end int64) bool {
	for _, incident := range incidents {
		if incident.StartedAt >= end {
			continue
		}
		if incident.EndedAt == 0 || incident.EndedAt > start {
			return true
		}
	}
	return false
}

func errorKindText(kind ErrorKind) string {
	switch kind {
	case ErrorNoDevice:
		return "设备断连"
	case ErrorEnumerate:
		return "设备枚举失败"
	case ErrorRead:
		return "读取失败"
	default:
		return "未知错误"
	}
}

func historySampleText(sample historySample) string {
	state := BatteryState{
		Transport: sample.Transport,
		Charge:    sample.Charge,
	}
	return fmt.Sprintf("%d%% · %s · %s", sample.Percent, state.transportText(), state.chargeText())
}

func historyTime(unix int64) string {
	if unix == 0 {
		return "-"
	}
	return time.Unix(unix, 0).In(time.Local).Format("2006-01-02 15:04")
}

func formatHistoryDuration(duration time.Duration) string {
	if duration < time.Minute {
		return "不到 1 分钟"
	}
	hours := int(duration / time.Hour)
	minutes := int(duration % time.Hour / time.Minute)
	if hours == 0 {
		return fmt.Sprintf("%d 分钟", minutes)
	}
	if minutes == 0 {
		return fmt.Sprintf("%d 小时", hours)
	}
	return fmt.Sprintf("%d 小时 %d 分钟", hours, minutes)
}

func renderHistoryReport(data historyData, now time.Time) string {
	metrics := buildHistoryMetrics(data, now)
	status := "暂无成功读取记录"
	if metrics.hasActiveIncident {
		status = "异常：" + errorKindText(metrics.activeIncident.Kind)
	} else if metrics.hasLastSample {
		status = historySampleText(metrics.lastSample)
	}

	charging := "暂无完整充电记录"
	if metrics.hasCurrentCharging {
		charging = "当前充电，已观测 " + formatHistoryDuration(metrics.currentCharging)
	} else if metrics.hasLastCharging {
		charging = formatHistoryDuration(metrics.lastCharging)
	}

	discharge := "暂无完整记录"
	if metrics.hasLastDischarge {
		discharge = formatHistoryDuration(metrics.lastDischarge)
	}

	remaining := "暂无估算"
	if metrics.hasRemaining {
		remaining = "约 " + formatHistoryDuration(metrics.remaining)
	}

	var builder strings.Builder
	builder.WriteString(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>G3M Pro 电量历史</title>
<style>
body { margin: 0; padding: 24px; color: #1f2937; background: #f3f6fa; font-family: "Segoe UI", "Microsoft YaHei", sans-serif; }
main { max-width: 960px; margin: 0 auto; }
h1 { margin: 0 0 6px; }
h2 { margin: 0 0 12px; font-size: 18px; }
.muted { color: #6b7280; }
.cards { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 12px; margin: 20px 0; }
.card, section { background: white; border: 1px solid #e5e7eb; border-radius: 10px; box-shadow: 0 1px 2px #0000000d; }
.card { padding: 14px; min-height: 64px; }
.card span { display: block; color: #6b7280; font-size: 13px; margin-bottom: 8px; }
.card strong { display: block; font-size: 16px; line-height: 1.4; }
section { padding: 18px; margin: 16px 0; overflow-x: auto; }
svg { display: block; width: 100%; min-width: 680px; height: auto; }
table { width: 100%; border-collapse: collapse; font-size: 14px; }
th, td { padding: 9px 8px; border-bottom: 1px solid #edf0f3; text-align: left; vertical-align: top; }
th { color: #6b7280; font-weight: 600; }
.empty { color: #6b7280; padding: 12px 0; }
@media (max-width: 760px) { .cards { grid-template-columns: repeat(2, minmax(0, 1fr)); } body { padding: 14px; } }
</style>
</head>
<body>
<main>
<h1>G3M Pro 电量历史</h1>
<div class="muted">报告生成时间：`)
	builder.WriteString(html.EscapeString(historyTime(now.Unix())))
	builder.WriteString(`</div>
<div class="cards">`)
	fmt.Fprintf(&builder, "<div class=\"card\"><span>最近成功状态</span><strong>%s</strong></div>", html.EscapeString(status))
	fmt.Fprintf(&builder, "<div class=\"card\"><span>预计剩余使用时间</span><strong>%s</strong></div>", html.EscapeString(remaining))
	fmt.Fprintf(&builder, "<div class=\"card\"><span>最近一次充电</span><strong>%s</strong></div>", html.EscapeString(charging))
	fmt.Fprintf(&builder, "<div class=\"card\"><span>100%% → 20%%</span><strong>%s</strong></div>", html.EscapeString(discharge))
	builder.WriteString(`</div>
<section>
<h2>最近 24 小时</h2>`)
	builder.WriteString(renderHistoryChart(data, now, 24*time.Hour))
	builder.WriteString(`</section>
<section>
<h2>最近 7 天</h2>`)
	builder.WriteString(renderHistoryChart(data, now, 7*24*time.Hour))
	builder.WriteString(`</section>
<section>
<h2>断连和读取失败记录</h2>
<table>
<thead><tr><th>类型</th><th>开始时间</th><th>结束时间</th><th>持续时间</th><th>信息</th></tr></thead>
<tbody>`)

	if len(data.Incidents) == 0 {
		builder.WriteString("<tr><td colspan=\"5\" class=\"empty\">没有记录</td></tr>")
	} else {
		count := 0
		for index := len(data.Incidents) - 1; index >= 0 && count < 20; index-- {
			incident := data.Incidents[index]
			end := "-"
			duration := "进行中"
			if incident.EndedAt != 0 {
				end = historyTime(incident.EndedAt)
				duration = formatHistoryDuration(time.Duration(incident.EndedAt-incident.StartedAt) * time.Second)
			}
			fmt.Fprintf(
				&builder,
				"<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>",
				html.EscapeString(errorKindText(incident.Kind)),
				html.EscapeString(historyTime(incident.StartedAt)),
				html.EscapeString(end),
				html.EscapeString(duration),
				html.EscapeString(incident.Message),
			)
			count++
		}
	}

	builder.WriteString(`</tbody>
</table>
</section>
</main>
</body>
</html>
`)
	return builder.String()
}

func renderHistoryChart(data historyData, now time.Time, window time.Duration) string {
	const (
		width  = 760.0
		height = 260.0
		left   = 48.0
		right  = 16.0
		top    = 20.0
		bottom = 32.0
	)
	plotWidth := width - left - right
	plotHeight := height - top - bottom
	start := now.Add(-window)
	startUnix := start.Unix()
	endUnix := now.Unix()

	var builder strings.Builder
	builder.WriteString("<svg viewBox=\"0 0 760 260\" role=\"img\" aria-label=\"电量曲线\">")
	for _, level := range []int{0, 20, 40, 60, 80, 100} {
		y := top + (100-float64(level))/100*plotHeight
		fmt.Fprintf(
			&builder,
			"<line x1=\"%.1f\" y1=\"%.1f\" x2=\"%.1f\" y2=\"%.1f\" stroke=\"#e5e7eb\"/><text x=\"6\" y=\"%.1f\" fill=\"#6b7280\" font-size=\"12\">%d%%</text>",
			left,
			y,
			width-right,
			y,
			y+4,
			level,
		)
	}

	hasPoint := false
	var path strings.Builder
	for _, sample := range data.Samples {
		if sample.At < startUnix || sample.At > endUnix {
			continue
		}
		ratio := float64(sample.At-startUnix) / float64(endUnix-startUnix)
		x := left + ratio*plotWidth
		y := top + (100-float64(sample.Percent))/100*plotHeight
		if !hasPoint {
			fmt.Fprintf(&path, "M %.1f %.1f", x, y)
			hasPoint = true
		} else {
			fmt.Fprintf(&path, " L %.1f %.1f", x, y)
		}

		fill := "#2563eb"
		if sample.Charge == ChargeCharging {
			fill = "#f59e0b"
		} else if sample.Charge == ChargeFull {
			fill = "#16a34a"
		}
		fmt.Fprintf(&builder, "<circle cx=\"%.1f\" cy=\"%.1f\" r=\"3\" fill=\"%s\"/>", x, y, fill)
	}

	if hasPoint {
		fmt.Fprintf(&builder, "<path d=\"%s\" fill=\"none\" stroke=\"#2563eb\" stroke-width=\"2.5\" stroke-linejoin=\"round\" stroke-linecap=\"round\"/>", path.String())
	} else {
		builder.WriteString("<text x=\"380\" y=\"135\" text-anchor=\"middle\" fill=\"#6b7280\" font-size=\"14\">暂无有效采样数据</text>")
	}

	for _, incident := range data.Incidents {
		if incident.StartedAt > endUnix || (incident.EndedAt != 0 && incident.EndedAt < startUnix) {
			continue
		}
		incidentAt := incident.StartedAt
		if incidentAt < startUnix {
			incidentAt = startUnix
		}
		ratio := float64(incidentAt-startUnix) / float64(endUnix-startUnix)
		x := left + ratio*plotWidth
		fmt.Fprintf(&builder, "<line x1=\"%.1f\" y1=\"%.1f\" x2=\"%.1f\" y2=\"%.1f\" stroke=\"#dc2626\" stroke-width=\"1\" stroke-dasharray=\"4 3\"/>", x, top, x, height-bottom)
	}

	fmt.Fprintf(&builder, "<text x=\"%.1f\" y=\"%.1f\" fill=\"#6b7280\" font-size=\"12\">%s</text>", left, height-8, start.Format("01-02 15:04"))
	fmt.Fprintf(&builder, "<text x=\"%.1f\" y=\"%.1f\" text-anchor=\"end\" fill=\"#6b7280\" font-size=\"12\">%s</text>", width-right, height-8, now.Format("01-02 15:04"))
	builder.WriteString("</svg>")
	return builder.String()
}
