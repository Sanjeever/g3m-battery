//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
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
	mu   sync.Mutex
	path string
	data historyData
}

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
		path: filepath.Join(dataDir, "history.json"),
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

func (h *historyStore) openHistory() error {
	return showHistoryWindow(h)
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
