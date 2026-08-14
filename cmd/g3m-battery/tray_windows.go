//go:build windows

package main

import (
	"fmt"
	"log"
	"sync"
	"syscall"
	"unsafe"
)

var (
	shell32TrayDLL               = syscall.NewLazyDLL("shell32.dll")
	registerClassExW             = user32IconDLL.NewProc("RegisterClassExW")
	getModuleHandleW             = syscall.NewLazyDLL("kernel32.dll").NewProc("GetModuleHandleW")
	createWindowExW              = user32IconDLL.NewProc("CreateWindowExW")
	destroyWindow                = user32IconDLL.NewProc("DestroyWindow")
	unregisterClassW             = user32IconDLL.NewProc("UnregisterClassW")
	getMessageW                  = user32IconDLL.NewProc("GetMessageW")
	translateMessage             = user32IconDLL.NewProc("TranslateMessage")
	dispatchMessageW             = user32IconDLL.NewProc("DispatchMessageW")
	defWindowProcW               = user32IconDLL.NewProc("DefWindowProcW")
	postMessageW                 = user32IconDLL.NewProc("PostMessageW")
	postQuitMessage              = user32IconDLL.NewProc("PostQuitMessage")
	getCursorPos                 = user32IconDLL.NewProc("GetCursorPos")
	setForegroundWindow          = user32IconDLL.NewProc("SetForegroundWindow")
	registerDeviceNotificationW  = user32IconDLL.NewProc("RegisterDeviceNotificationW")
	unregisterDeviceNotification = user32IconDLL.NewProc("UnregisterDeviceNotification")
	createPopupMenu              = user32IconDLL.NewProc("CreatePopupMenu")
	appendMenuW                  = user32IconDLL.NewProc("AppendMenuW")
	trackPopupMenu               = user32IconDLL.NewProc("TrackPopupMenu")
	destroyMenu                  = user32IconDLL.NewProc("DestroyMenu")
	shellNotifyIconW             = shell32TrayDLL.NewProc("Shell_NotifyIconW")
)

const (
	wmDestroy      = 0x0002
	wmNull         = 0x0000
	wmTrayIcon     = 0x8001
	wmTrayUpdate   = 0x8002
	wmContextMenu  = 0x007B
	wmDeviceChange = 0x0219
	wmLButtonUp    = 0x0202
	wmRButtonUp    = 0x0205

	dbtDeviceArrival          = 0x8000
	dbtDeviceRemoveComplete   = 0x8004
	dbtDevTypeDeviceInterface = 0x00000005
	deviceNotifyWindowHandle  = 0x00000000

	nimAdd             = 0x00000000
	nimModify          = 0x00000001
	nimDelete          = 0x00000002
	nimSetVersion      = 0x00000004
	nifMessage         = 0x00000001
	nifIcon            = 0x00000002
	nifTip             = 0x00000004
	nifInfo            = 0x00000010
	nifShowTip         = 0x00000080
	notifyIconVersion4 = 4
	niifWarning        = 0x00000002

	mfString       = 0x00000000
	mfGrayed       = 0x00000001
	mfDisabled     = 0x00000002
	mfChecked      = 0x00000008
	mfSeparator    = 0x00000800
	tpmRetCommand  = 0x00000100
	tpmRightButton = 0x00000002

	menuRefresh = 1001
	menuExit    = 1002
	menuStartup = 1003
	menuHistory = 1004
)

type point struct {
	X int32
	Y int32
}

type windowsMessage struct {
	HWnd    syscall.Handle
	Message uint32
	Padding uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	WndProc       uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     syscall.Handle
	HIcon         syscall.Handle
	HCursor       syscall.Handle
	HbrBackground syscall.Handle
	MenuName      *uint16
	ClassName     *uint16
	HIconSm       syscall.Handle
}

type notifyIconData struct {
	CbSize           uint32
	HWnd             syscall.Handle
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            syscall.Handle
	Tip              [128]uint16
	State            uint32
	StateMask        uint32
	Info             [256]uint16
	TimeoutOrVersion uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GuidItem         syscall.GUID
	BalloonIcon      syscall.Handle
}

type devBroadcastDeviceInterface struct {
	Size       uint32
	DeviceType uint32
	Reserved   uint32
	ClassGUID  syscall.GUID
	Name       [1]uint16
}

type trayApp struct {
	logger       *log.Logger
	hwnd         syscall.Handle
	hInstance    syscall.Handle
	className    *uint16
	icon         syscall.Handle
	deviceNotify syscall.Handle
	onRefresh    func()
	onHistory    func()
	onRemaining  func(BatteryState) string

	mu                 sync.Mutex
	current            BatteryState
	pending            BatteryState
	lowBatteryNotified bool
	startupEnabled     bool
}

var activeTray *trayApp
var trayWindowProc = syscall.NewCallback(windowProc)

func newTray(logger *log.Logger, onRefresh, onHistory func(), onRemaining func(BatteryState) string) (*trayApp, error) {
	className, err := syscall.UTF16PtrFromString("G3MBatteryTrayWindow")
	if err != nil {
		return nil, err
	}
	title, err := syscall.UTF16PtrFromString("G3M Battery")
	if err != nil {
		return nil, err
	}

	instance, _, callErr := getModuleHandleW.Call(0)
	if instance == 0 {
		return nil, fmt.Errorf("GetModuleHandleW: %w", callErr)
	}

	class := wndClassEx{
		CbSize:    uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc:   trayWindowProc,
		HInstance: syscall.Handle(instance),
		ClassName: className,
	}
	atom, _, callErr := registerClassExW.Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 && callErr != syscall.Errno(1410) { // ERROR_CLASS_ALREADY_EXISTS
		return nil, fmt.Errorf("RegisterClassExW: %w", callErr)
	}
	startupEnabled, startupErr := readStartupEnabled()
	if startupErr != nil {
		logger.Printf("读取开机启动状态失败: %v", startupErr)
	}

	tray := &trayApp{
		logger:         logger,
		hInstance:      syscall.Handle(instance),
		className:      className,
		onRefresh:      onRefresh,
		onHistory:      onHistory,
		onRemaining:    onRemaining,
		current:        BatteryState{Percent: -1, Error: "未连接"},
		pending:        BatteryState{Percent: -1, Error: "未连接"},
		startupEnabled: startupEnabled,
	}
	activeTray = tray

	hwnd, _, callErr := createWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		0,
		0, 0, 0, 0,
		0,
		0,
		instance,
		0,
	)
	if hwnd == 0 {
		activeTray = nil
		return nil, fmt.Errorf("CreateWindowExW: %w", callErr)
	}
	tray.hwnd = syscall.Handle(hwnd)

	if err := tray.registerDeviceNotifications(); err != nil {
		destroyWindow.Call(hwnd)
		activeTray = nil
		return nil, err
	}
	if err := tray.addIcon(tray.current); err != nil {
		tray.unregisterDeviceNotifications()
		destroyWindow.Call(hwnd)
		activeTray = nil
		return nil, err
	}
	return tray, nil
}

func (t *trayApp) run() {
	var message windowsMessage
	for {
		ret, _, callErr := getMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(ret) == -1 {
			t.logger.Printf("GetMessageW: %v", callErr)
			break
		}
		if ret == 0 {
			break
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&message)))
		dispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}

	t.unregisterDeviceNotifications()
	t.removeIcon()
	destroyWindow.Call(uintptr(t.hwnd))
	unregisterClassW.Call(uintptr(unsafe.Pointer(t.className)), uintptr(t.hInstance))
	activeTray = nil
}

func (t *trayApp) setState(state BatteryState) {
	t.mu.Lock()
	t.pending = state
	t.mu.Unlock()
	if t.hwnd != 0 {
		postMessageW.Call(uintptr(t.hwnd), wmTrayUpdate, 0, 0)
	}
}

func (t *trayApp) applyPendingState() {
	t.mu.Lock()
	state := t.pending
	t.current = state
	t.mu.Unlock()
	t.updateIcon(state)
	t.updateLowBatteryNotification(state)
}

func (t *trayApp) registerDeviceNotifications() error {
	var hidGUID syscall.GUID
	hidDGetHidGuid.Call(uintptr(unsafe.Pointer(&hidGUID)))

	filter := devBroadcastDeviceInterface{
		Size:       uint32(unsafe.Sizeof(devBroadcastDeviceInterface{})),
		DeviceType: dbtDevTypeDeviceInterface,
		ClassGUID:  hidGUID,
	}
	handle, _, callErr := registerDeviceNotificationW.Call(
		uintptr(t.hwnd),
		uintptr(unsafe.Pointer(&filter)),
		deviceNotifyWindowHandle,
	)
	if handle == 0 {
		return fmt.Errorf("RegisterDeviceNotificationW: %w", callErr)
	}
	t.deviceNotify = syscall.Handle(handle)
	return nil
}

func (t *trayApp) unregisterDeviceNotifications() {
	if t.deviceNotify == 0 {
		return
	}
	unregisterDeviceNotification.Call(uintptr(t.deviceNotify))
	t.deviceNotify = 0
}

func (t *trayApp) addIcon(state BatteryState) error {
	icon, err := buildBatteryIcon(state)
	if err != nil {
		return err
	}
	data := t.makeNotifyData(icon, state.tooltip())
	ret, _, callErr := shellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&data)))
	if ret == 0 {
		destroyIcon.Call(uintptr(icon))
		return fmt.Errorf("Shell_NotifyIconW(NIM_ADD): %w", callErr)
	}
	t.icon = icon
	versionData := notifyIconData{
		CbSize: uint32(unsafe.Sizeof(notifyIconData{})),
		HWnd:   t.hwnd,
		UID:    1,
	}
	versionData.TimeoutOrVersion = notifyIconVersion4
	shellNotifyIconW.Call(nimSetVersion, uintptr(unsafe.Pointer(&versionData)))
	return nil
}

func (t *trayApp) updateIcon(state BatteryState) {
	icon, err := buildBatteryIcon(state)
	if err != nil {
		t.logger.Printf("生成托盘图标失败: %v", err)
		return
	}
	data := t.makeNotifyData(icon, state.tooltip())
	ret, _, callErr := shellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&data)))
	if ret == 0 {
		destroyIcon.Call(uintptr(icon))
		t.logger.Printf("Shell_NotifyIconW(NIM_MODIFY): %v", callErr)
		return
	}
	oldIcon := t.icon
	t.icon = icon
	if oldIcon != 0 {
		destroyIcon.Call(uintptr(oldIcon))
	}
}

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

func (t *trayApp) showMenu() {
	t.mu.Lock()
	state := t.current
	t.mu.Unlock()

	menu, _, callErr := createPopupMenu.Call()
	if menu == 0 {
		t.logger.Printf("CreatePopupMenu: %v", callErr)
		return
	}
	defer destroyMenu.Call(menu)

	remainingText := "暂无估算"
	if t.onRemaining != nil {
		remainingText = t.onRemaining(state)
	}
	for _, line := range state.menuLines(remainingText) {
		text, _ := syscall.UTF16FromString(line)
		appendMenuW.Call(menu, mfString|mfGrayed|mfDisabled, uintptr(menuRefresh+10), uintptr(unsafe.Pointer(&text[0])))
	}
	appendMenuW.Call(menu, mfSeparator, 0, 0)
	startupFlags := uint32(mfString)
	if t.startupEnabled {
		startupFlags |= mfChecked
	}
	startupText, _ := syscall.UTF16FromString("开机启动")
	appendMenuW.Call(menu, uintptr(startupFlags), menuStartup, uintptr(unsafe.Pointer(&startupText[0])))
	refreshText, _ := syscall.UTF16FromString("立即刷新")
	appendMenuW.Call(menu, mfString, menuRefresh, uintptr(unsafe.Pointer(&refreshText[0])))
	historyText, _ := syscall.UTF16FromString("电量历史")
	appendMenuW.Call(menu, mfString, menuHistory, uintptr(unsafe.Pointer(&historyText[0])))
	exitText, _ := syscall.UTF16FromString("退出")
	appendMenuW.Call(menu, mfString, menuExit, uintptr(unsafe.Pointer(&exitText[0])))

	var cursor point
	getCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	setForegroundWindow.Call(uintptr(t.hwnd))
	selected, _, _ := trackPopupMenu.Call(
		menu,
		tpmRetCommand|tpmRightButton,
		uintptr(cursor.X),
		uintptr(cursor.Y),
		0,
		uintptr(t.hwnd),
		0,
	)
	postMessageW.Call(uintptr(t.hwnd), wmNull, 0, 0)

	switch uint32(selected) {
	case menuStartup:
		if t.startupEnabled {
			if err := disableStartup(); err != nil {
				t.logger.Printf("关闭开机启动失败: %v", err)
				return
			}
			t.startupEnabled = false
		} else {
			if err := enableStartup(); err != nil {
				t.logger.Printf("开启开机启动失败: %v", err)
				return
			}
			t.startupEnabled = true
		}
	case menuRefresh:
		if t.onRefresh != nil {
			t.onRefresh()
		}
	case menuHistory:
		if t.onHistory != nil {
			t.onHistory()
		}
	case menuExit:
		postQuitMessage.Call(0)
	}
}

func windowProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	if activeTray != nil {
		switch message {
		case wmTrayUpdate:
			activeTray.applyPendingState()
			return 0
		case wmDeviceChange:
			switch uint32(wParam) {
			case dbtDeviceArrival, dbtDeviceRemoveComplete:
				if activeTray.onRefresh != nil {
					activeTray.onRefresh()
				}
			}
			return 0
		case wmTrayIcon:
			notifyEvent := uint32(lParam & 0xFFFF)
			if notifyEvent == wmContextMenu || notifyEvent == wmLButtonUp || notifyEvent == wmRButtonUp {
				activeTray.showMenu()
				return 0
			}
		case wmDestroy:
			postQuitMessage.Call(0)
			return 0
		}
	}
	ret, _, _ := defWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return ret
}
