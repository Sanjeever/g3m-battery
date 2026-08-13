//go:build windows

package main

import (
	"fmt"
	"time"
)

type Transport int

const (
	TransportUnknown Transport = iota
	TransportWired
	TransportReceiver
)

type ChargeState int

const (
	ChargeUnknown ChargeState = iota
	ChargeCharging
	ChargeFull
	ChargeNormal
)

type BatteryState struct {
	Percent   int
	RawFlag   byte
	Transport Transport
	Charge    ChargeState
	UpdatedAt time.Time
	Error     string
}

func transportForPID(pid uint16) Transport {
	switch pid {
	case targetPIDWired:
		return TransportWired
	case targetPIDReceiver:
		return TransportReceiver
	default:
		return TransportUnknown
	}
}

func decodeChargeState(percent int, flag byte, transport Transport) ChargeState {
	switch flag {
	case 0x02:
		return ChargeCharging
	case 0x01:
		if transport == TransportWired && percent == 100 {
			return ChargeFull
		}
	case 0x00:
		return ChargeNormal
	}
	return ChargeUnknown
}

func (s BatteryState) transportText() string {
	switch s.Transport {
	case TransportWired:
		return "有线"
	case TransportReceiver:
		return "2.4G 接收器"
	default:
		return "未知连接"
	}
}

func (s BatteryState) chargeText() string {
	switch s.Charge {
	case ChargeCharging:
		return "正在充电"
	case ChargeFull:
		return "已充满"
	case ChargeNormal:
		return "普通状态"
	default:
		return "状态未知"
	}
}

func (s BatteryState) tooltip() string {
	if s.Error != "" {
		if s.Percent >= 0 {
			return fmt.Sprintf("G3M Pro · %d%% · %s", s.Percent, s.Error)
		}
		return "G3M Pro · " + s.Error
	}
	return fmt.Sprintf("G3M Pro · %d%% · %s · %s", s.Percent, s.transportText(), s.chargeText())
}

func (s BatteryState) menuLines() []string {
	if s.Error != "" {
		if s.Percent >= 0 {
			return []string{
				fmt.Sprintf("电量：%d%%", s.Percent),
				"连接：" + s.transportText(),
				"状态：" + s.Error,
			}
		}
		return []string{"G3M Pro：" + s.Error}
	}
	return []string{
		fmt.Sprintf("电量：%d%%", s.Percent),
		"连接：" + s.transportText(),
		"状态：" + s.chargeText(),
	}
}
