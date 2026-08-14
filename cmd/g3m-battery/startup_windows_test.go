//go:build windows

package main

import (
	"path/filepath"
	"testing"
)

func TestNormalizeStartupPath(t *testing.T) {
	want := filepath.Clean(`C:\Program Files\G3M Battery\g3m-battery.exe`)
	tests := []string{
		`C:\Program Files\G3M Battery\g3m-battery.exe`,
		`"C:\Program Files\G3M Battery\g3m-battery.exe"`,
		`  "C:\Program Files\G3M Battery\folder\..\g3m-battery.exe"  `,
	}
	for _, value := range tests {
		if got := normalizeStartupPath(value); got != want {
			t.Errorf("normalizeStartupPath(%q) = %q, want %q", value, got, want)
		}
	}
}
