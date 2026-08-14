//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testHistorySample(at int64, percent int, charge ChargeState) historySample {
	return historySample{
		At:        at,
		Percent:   percent,
		Transport: TransportWired,
		Charge:    charge,
	}
}

func TestHistorySamplesContinuousBreaksAtSessionBoundary(t *testing.T) {
	data := historyData{sessionStartedAt: 2000}
	before := testHistorySample(1900, 80, ChargeNormal)
	after := testHistorySample(2000, 79, ChargeNormal)
	if historySamplesContinuous(data, before, after) {
		t.Fatal("samples across a process session boundary were considered continuous")
	}

	current := testHistorySample(2100, 78, ChargeNormal)
	if !historySamplesContinuous(data, after, current) {
		t.Fatal("samples within the same process session were not considered continuous")
	}
}

func TestEstimateRemainingDoesNotCrossSessionBoundary(t *testing.T) {
	now := time.Unix(3300, 0)
	data := historyData{
		sessionStartedAt: 3000,
		Samples: []historySample{
			testHistorySample(1000, 100, ChargeNormal),
			testHistorySample(1600, 96, ChargeNormal),
			testHistorySample(2200, 92, ChargeNormal),
			testHistorySample(2800, 90, ChargeNormal),
			testHistorySample(3000, 88, ChargeNormal),
			testHistorySample(3300, 86, ChargeNormal),
		},
	}
	withoutBoundary := data
	withoutBoundary.sessionStartedAt = 0
	if _, ok := estimateRemaining(withoutBoundary, now); !ok {
		t.Fatal("estimateRemaining rejected a continuous 30-minute discharge series")
	}
	if _, ok := estimateRemaining(data, now); ok {
		t.Fatal("estimateRemaining crossed the process session boundary")
	}
}

func TestHistoryStoreRetriesDirtyDataAfterSaveFailure(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "missing", "history.json")
	store := &historyStore{
		path: path,
		data: historyData{Version: historyDataVersion},
	}
	state := BatteryState{
		Percent:   80,
		Transport: TransportWired,
		Charge:    ChargeNormal,
		UpdatedAt: time.Unix(1000, 0),
	}
	if err := store.record(state); err == nil {
		t.Fatal("record unexpectedly succeeded with a missing parent directory")
	}
	if !store.dirty {
		t.Fatal("failed save did not keep the store dirty")
	}
	if err := os.Mkdir(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create parent directory: %v", err)
	}
	if err := store.record(state); err != nil {
		t.Fatalf("record retry failed: %v", err)
	}
	if store.dirty {
		t.Fatal("successful save left the store dirty")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("history file was not created: %v", err)
	}
}

func TestHistoryStoreRecordsFirstSampleOfSession(t *testing.T) {
	store := &historyStore{
		path: filepath.Join(t.TempDir(), "history.json"),
		data: historyData{
			Version:          historyDataVersion,
			sessionStartedAt: 2000,
			Samples: []historySample{
				testHistorySample(1900, 80, ChargeNormal),
			},
		},
	}
	state := BatteryState{
		Percent:   80,
		Transport: TransportWired,
		Charge:    ChargeNormal,
		UpdatedAt: time.Unix(2001, 0),
	}
	if err := store.record(state); err != nil {
		t.Fatalf("record first session sample: %v", err)
	}
	if len(store.data.Samples) != 2 || store.data.Samples[1].At != 2001 {
		t.Fatalf("first session sample was not recorded: %+v", store.data.Samples)
	}
}

func TestHistoryStoreDoesNotContinueIncidentAcrossSession(t *testing.T) {
	store := &historyStore{
		path: filepath.Join(t.TempDir(), "history.json"),
		data: historyData{
			Version:          historyDataVersion,
			sessionStartedAt: 2000,
			Incidents: []historyIncident{
				{Kind: ErrorRead, StartedAt: 1900, Message: "读取失败"},
			},
		},
	}
	state := BatteryState{
		Percent:   -1,
		ErrorKind: ErrorRead,
		Error:     "读取失败",
		UpdatedAt: time.Unix(2001, 0),
	}
	if err := store.record(state); err != nil {
		t.Fatalf("record new session incident: %v", err)
	}
	if len(store.data.Incidents) != 2 {
		t.Fatalf("new session incident was coalesced with old data: %+v", store.data.Incidents)
	}
	if store.data.Incidents[0].EndedAt != 2000 {
		t.Fatalf("old incident ended at %d, want 2000", store.data.Incidents[0].EndedAt)
	}
}

func TestHistoryStoreCoalescesAndClosesIncident(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	store := &historyStore{
		path: path,
		data: historyData{Version: historyDataVersion},
	}
	errorState := BatteryState{
		Percent:   -1,
		ErrorKind: ErrorRead,
		Error:     "读取失败",
		UpdatedAt: time.Unix(1000, 0),
	}
	if err := store.record(errorState); err != nil {
		t.Fatalf("record first incident: %v", err)
	}
	errorState.UpdatedAt = time.Unix(1005, 0)
	if err := store.record(errorState); err != nil {
		t.Fatalf("record repeated incident: %v", err)
	}
	if len(store.data.Incidents) != 1 {
		t.Fatalf("repeated incident created %d records, want 1", len(store.data.Incidents))
	}

	state := BatteryState{
		Percent:   80,
		Transport: TransportWired,
		Charge:    ChargeNormal,
		UpdatedAt: time.Unix(1010, 0),
	}
	if err := store.record(state); err != nil {
		t.Fatalf("close incident: %v", err)
	}
	if store.data.Incidents[0].EndedAt != 1010 {
		t.Fatalf("incident ended at %d, want 1010", store.data.Incidents[0].EndedAt)
	}
}

func TestFormatHistoryDuration(t *testing.T) {
	tests := []struct {
		input time.Duration
		want  string
	}{
		{input: 30 * time.Second, want: "不到 1 分钟"},
		{input: 15 * time.Minute, want: "15 分钟"},
		{input: 2 * time.Hour, want: "2 小时"},
		{input: 2*time.Hour + 15*time.Minute, want: "2 小时 15 分钟"},
	}
	for _, test := range tests {
		if got := formatHistoryDuration(test.input); got != test.want {
			t.Errorf("formatHistoryDuration(%s) = %q, want %q", test.input, got, test.want)
		}
	}
}
