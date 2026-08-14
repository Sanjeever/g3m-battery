//go:build windows

package main

import (
	"fmt"
	"time"
)

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
	if data.sessionStartedAt != 0 && lastSample.At < data.sessionStartedAt {
		return lastDuration, hasLast, 0, false
	}
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
	if data.sessionStartedAt != 0 && last.At < data.sessionStartedAt {
		return 0, false
	}
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
	if data.sessionStartedAt != 0 && before.At < data.sessionStartedAt && after.At >= data.sessionStartedAt {
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
