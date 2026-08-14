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

var (
	setProcessDpiAwarenessContext = user32IconDLL.NewProc("SetProcessDpiAwarenessContext")
	setProcessDPIAware            = user32IconDLL.NewProc("SetProcessDPIAware")
)

func enableProcessDPIAwareness() {
	if err := setProcessDpiAwarenessContext.Find(); err == nil {
		const perMonitorAwareV2 = ^uintptr(3)
		if result, _, _ := setProcessDpiAwarenessContext.Call(perMonitorAwareV2); result != 0 {
			return
		}
	}
	setProcessDPIAware.Call()
}

func main() {
	runtime.LockOSThread()
	enableProcessDPIAwareness()

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

	history, historyErr := newHistoryStore()
	if historyErr != nil {
		logger.Printf("初始化历史记录失败: %v", historyErr)
	}

	tray, err := newTray(logger, func() {
		select {
		case refresh <- struct{}{}:
		default:
		}
	}, func() {
		if history == nil {
			logger.Printf("历史记录不可用")
			return
		}
		if err := history.openHistory(); err != nil {
			logger.Printf("打开电量历史失败: %v", err)
		}
	}, func(state BatteryState) string {
		if history == nil {
			return "暂无估算"
		}
		return history.remainingText(state)
	})
	if err != nil {
		logger.Fatal(err)
	}
	if historyErr != nil {
		tray.setHistoryError(historyErr)
	}

	publish := func(state BatteryState) {
		if history != nil {
			if err := history.record(state); err != nil {
				logger.Printf("记录电量历史失败: %v", err)
				tray.setHistoryError(err)
			} else {
				tray.setHistoryError(nil)
			}
		}
		tray.setState(state)
	}
	monitorDone := make(chan struct{})
	go monitorLoop(stop, refresh, publish, logger, monitorDone)

	tray.run(func() {
		close(stop)
		select {
		case <-monitorDone:
		case <-time.After(2 * time.Second):
			logger.Printf("监控循环未在退出超时内结束")
		}
	})
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

func monitorLoop(stop <-chan struct{}, refresh <-chan struct{}, publish func(BatteryState), logger *log.Logger, done chan<- struct{}) {
	defer close(done)
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
		state.ErrorKind = ErrorEnumerate
		state.Error = err.Error()
		publish(state)
		return
	}
	if len(candidates) == 0 {
		state.ErrorKind = ErrorNoDevice
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
			ErrorKind: ErrorNone,
			UpdatedAt: time.Now(),
		}
		publish(state)
		return
	}

	if lastErr != nil {
		state.ErrorKind = ErrorRead
		state.Error = "读取电量失败: " + lastErr.Error()
	} else {
		state.ErrorKind = ErrorRead
		state.Error = "读取电量失败"
	}
	publish(state)
}
