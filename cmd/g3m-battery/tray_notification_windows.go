//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

const lowBatteryThreshold = 20

func (t *trayApp) updateLowBatteryNotification(state BatteryState) {
	if state.Error != "" || state.Percent < 0 {
		return
	}
	if state.Percent >= lowBatteryThreshold || state.Charge == ChargeCharging || state.Charge == ChargeFull {
		t.lowBatteryNotified = false
		return
	}
	if t.lowBatteryNotified {
		return
	}

	data := t.makeNotifyData(t.icon, state.tooltip())
	data.UFlags |= nifInfo
	title, _ := syscall.UTF16FromString("G3M Pro 电量低")
	info, _ := syscall.UTF16FromString(fmt.Sprintf(
		"当前电量：%d%%，连接：%s。请及时充电。",
		state.Percent,
		state.transportText(),
	))
	copy(data.InfoTitle[:], title)
	copy(data.Info[:], info)
	data.InfoFlags = niifWarning

	ret, _, callErr := shellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&data)))
	if ret == 0 {
		t.logger.Printf("Shell_NotifyIconW(NIF_INFO): %v", callErr)
		return
	}
	t.lowBatteryNotified = true
}

func (t *trayApp) removeIcon() {
	if t.icon == 0 {
		return
	}
	data := t.makeNotifyData(t.icon, "")
	shellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
	destroyIcon.Call(uintptr(t.icon))
	t.icon = 0
}

func (t *trayApp) makeNotifyData(icon syscall.Handle, tip string) notifyIconData {
	data := notifyIconData{
		CbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
		HWnd:             t.hwnd,
		UID:              1,
		UFlags:           nifMessage | nifIcon | nifTip | nifShowTip,
		UCallbackMessage: wmTrayIcon,
		HIcon:            icon,
	}
	text, _ := syscall.UTF16FromString(tip)
	if len(text) > len(data.Tip) {
		text = text[:len(data.Tip)]
	}
	copy(data.Tip[:], text)
	return data
}
