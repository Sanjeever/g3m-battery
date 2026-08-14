//go:build windows

package main

import "testing"

func TestTransportForPID(t *testing.T) {
	tests := []struct {
		name string
		pid  uint16
		want Transport
	}{
		{name: "wired", pid: targetPIDWired, want: TransportWired},
		{name: "receiver", pid: targetPIDReceiver, want: TransportReceiver},
		{name: "unknown", pid: 0xffff, want: TransportUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := transportForPID(test.pid); got != test.want {
				t.Fatalf("transportForPID(%#x) = %v, want %v", test.pid, got, test.want)
			}
		})
	}
}

func TestDecodeChargeState(t *testing.T) {
	tests := []struct {
		name      string
		percent   int
		flag      byte
		transport Transport
		want      ChargeState
	}{
		{name: "normal", percent: 80, flag: 0x00, transport: TransportReceiver, want: ChargeNormal},
		{name: "flag two charging", percent: 80, flag: 0x02, transport: TransportReceiver, want: ChargeCharging},
		{name: "wired charging", percent: 80, flag: 0x01, transport: TransportWired, want: ChargeCharging},
		{name: "wired full", percent: 100, flag: 0x01, transport: TransportWired, want: ChargeFull},
		{name: "receiver flag one unknown", percent: 80, flag: 0x01, transport: TransportReceiver, want: ChargeUnknown},
		{name: "unknown flag", percent: 80, flag: 0xff, transport: TransportWired, want: ChargeUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := decodeChargeState(test.percent, test.flag, test.transport); got != test.want {
				t.Fatalf("decodeChargeState(%d, %#x, %v) = %v, want %v", test.percent, test.flag, test.transport, got, test.want)
			}
		})
	}
}

func TestParseBatteryResponse(t *testing.T) {
	valid := make([]byte, queryReportLength)
	valid[0] = 0x04
	valid[1] = 0x20
	valid[3] = 0x1A
	valid[8] = 73
	valid[9] = 0x02

	percent, flag, err := parseBatteryResponse(valid)
	if err != nil {
		t.Fatalf("parseBatteryResponse(valid) returned error: %v", err)
	}
	if percent != 73 || flag != 0x02 {
		t.Fatalf("parseBatteryResponse(valid) = %d, %#x, want 73, %#x", percent, flag, byte(0x02))
	}

	tests := []struct {
		name   string
		length int
		mutate func([]byte)
	}{
		{name: "short response", length: inputReportMinimum - 1},
		{name: "invalid header", mutate: func(response []byte) { response[1] = 0x21 }},
		{name: "invalid block", mutate: func(response []byte) { response[7] = 0xff }},
		{name: "invalid percent", mutate: func(response []byte) { response[8] = 101 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := append([]byte(nil), valid...)
			if test.length > 0 {
				response = response[:test.length]
			}
			if test.mutate != nil {
				test.mutate(response)
			}
			if _, _, err := parseBatteryResponse(response); err == nil {
				t.Fatalf("parseBatteryResponse(%s) returned nil error", test.name)
			}
		})
	}
}
