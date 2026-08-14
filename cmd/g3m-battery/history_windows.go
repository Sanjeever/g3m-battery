//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/windows"
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
	Version          int               `json:"version"`
	Samples          []historySample   `json:"samples"`
	Incidents        []historyIncident `json:"incidents"`
	sessionStartedAt int64
}

type historyStore struct {
	mu    sync.Mutex
	path  string
	data  historyData
	dirty bool
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
	store.data.sessionStartedAt = time.Now().Unix()
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

	changed := h.dirty || trimHistory(&h.data, at)
	changed = h.closePreviousSessionIncidentsLocked(at) || changed
	if state.Error != "" || state.Percent < 0 {
		changed = h.recordIncidentLocked(state, at) || changed
	} else {
		changed = h.closeIncidentLocked(at) || changed
		changed = h.recordSampleLocked(state, at) || changed
	}
	if !changed {
		return nil
	}
	h.dirty = true
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
	if h.data.sessionStartedAt != 0 && last.At < h.data.sessionStartedAt && sample.At >= h.data.sessionStartedAt {
		h.data.Samples = append(h.data.Samples, sample)
		return true
	}
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

func (h *historyStore) closePreviousSessionIncidentsLocked(at time.Time) bool {
	if h.data.sessionStartedAt == 0 || at.Unix() < h.data.sessionStartedAt {
		return false
	}

	changed := false
	for index := range h.data.Incidents {
		incident := &h.data.Incidents[index]
		if incident.EndedAt == 0 && incident.StartedAt < h.data.sessionStartedAt {
			incident.EndedAt = h.data.sessionStartedAt
			changed = true
		}
	}
	return changed
}

func (h *historyStore) saveLocked() error {
	payload, err := json.MarshalIndent(h.data, "", "  ")
	if err != nil {
		return fmt.Errorf("编码历史记录: %w", err)
	}
	payload = append(payload, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(h.path), ".history-*.tmp")
	if err != nil {
		return fmt.Errorf("创建历史记录临时文件: %w", err)
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		if temporaryPath != "" {
			_ = os.Remove(temporaryPath)
		}
	}()

	if _, err := temporary.Write(payload); err != nil {
		return fmt.Errorf("写入历史记录临时文件: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("同步历史记录临时文件: %w", err)
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return fmt.Errorf("关闭历史记录临时文件: %w", err)
	}
	closed = true

	if err := windows.Rename(temporaryPath, h.path); err != nil {
		return fmt.Errorf("替换历史记录文件: %w", err)
	}
	temporaryPath = ""
	h.dirty = false
	return nil
}

func (h *historyStore) snapshot() historyData {
	h.mu.Lock()
	defer h.mu.Unlock()

	data := historyData{
		Version:          h.data.Version,
		Samples:          append([]historySample(nil), h.data.Samples...),
		Incidents:        append([]historyIncident(nil), h.data.Incidents...),
		sessionStartedAt: h.data.sessionStartedAt,
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
	if data.sessionStartedAt != 0 && last.At < data.sessionStartedAt {
		return "暂无估算"
	}
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
