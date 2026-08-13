//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

const pollInterval = 5 * time.Second

func main() {
	runtime.LockOSThread()

	logger := log.New(os.Stderr, "g3m-battery: ", log.LstdFlags)
	instance, first, err := acquireSingleInstance()
	if err != nil {
		logger.Fatal(err)
	}
	if !first {
		return
	}
	defer closeHandle.Call(uintptr(instance))

	refresh := make(chan struct{}, 1)
	stop := make(chan struct{})

	tray, err := newTray(logger, func() {
		select {
		case refresh <- struct{}{}:
		default:
		}
	})
	if err != nil {
		logger.Fatal(err)
	}

	go monitorLoop(stop, refresh, tray.setState, logger)

	tray.run()
	close(stop)
}

const errorAlreadyExists = 183

var createMutexW = kernel32DLL.NewProc("CreateMutexW")

func acquireSingleInstance() (syscall.Handle, bool, error) {
	name, err := syscall.UTF16PtrFromString("Local\\G3MBatteryTray")
	if err != nil {
		return 0, false, err
	}

	handle, _, callErr := createMutexW.Call(
		0,
		0,
		uintptr(unsafe.Pointer(name)),
	)
	if handle == 0 {
		return 0, false, fmt.Errorf("CreateMutexW: %w", callErr)
	}
	if callErr == syscall.Errno(errorAlreadyExists) {
		closeHandle.Call(handle)
		return 0, false, nil
	}
	return syscall.Handle(handle), true, nil
}

func monitorLoop(stop <-chan struct{}, refresh <-chan struct{}, publish func(BatteryState), logger *log.Logger) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	pollAndPublish(publish, logger)
	for {
		select {
		case <-ticker.C:
			pollAndPublish(publish, logger)
		case <-refresh:
			pollAndPublish(publish, logger)
		case <-stop:
			return
		}
	}
}

func pollAndPublish(publish func(BatteryState), logger *log.Logger) {
	state := BatteryState{Percent: -1, UpdatedAt: time.Now()}

	candidates, err := enumerateCandidates()
	if err != nil {
		state.Error = err.Error()
		publish(state)
		return
	}
	if len(candidates) == 0 {
		state.Error = "未找到 G3M PRO HID 厂商集合"
		publish(state)
		return
	}

	var lastErr error
	for _, candidate := range candidates {
		percent, flag, err := readBattery(candidate.Path)
		if err != nil {
			lastErr = err
			continue
		}

		transport := transportForPID(candidate.ProductID)
		state = BatteryState{
			Percent:   percent,
			RawFlag:   flag,
			Transport: transport,
			Charge:    decodeChargeState(percent, flag, transport),
			UpdatedAt: time.Now(),
		}
		publish(state)
		return
	}

	if lastErr != nil {
		state.Error = "读取电量失败: " + lastErr.Error()
	} else {
		state.Error = "读取电量失败"
	}
	publish(state)
}
