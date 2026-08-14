//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

func (t *trayApp) showMenu() {
	t.mu.Lock()
	state := t.current
	historyError := t.historyError
	startupError := t.startupError
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
	startupTextValue := "开机启动"
	if startupError != "" {
		startupTextValue += "（状态不可用）"
		errorText, _ := syscall.UTF16FromString("开机启动不可用：" + startupError)
		appendMenuW.Call(menu, mfString|mfGrayed|mfDisabled, uintptr(menuRefresh+12), uintptr(unsafe.Pointer(&errorText[0])))
	}
	startupText, _ := syscall.UTF16FromString(startupTextValue)
	appendMenuW.Call(menu, uintptr(startupFlags), menuStartup, uintptr(unsafe.Pointer(&startupText[0])))
	refreshText, _ := syscall.UTF16FromString("立即刷新")
	appendMenuW.Call(menu, mfString, menuRefresh, uintptr(unsafe.Pointer(&refreshText[0])))
	historyTextValue := "查看电量历史"
	historyFlags := uint32(mfString)
	if historyError != "" {
		historyTextValue = "查看电量历史（不可用）"
		historyFlags |= mfGrayed | mfDisabled
		text, _ := syscall.UTF16FromString("历史记录不可用：" + historyError)
		appendMenuW.Call(menu, mfString|mfGrayed|mfDisabled, uintptr(menuRefresh+11), uintptr(unsafe.Pointer(&text[0])))
	}
	historyText, _ := syscall.UTF16FromString(historyTextValue)
	appendMenuW.Call(menu, uintptr(historyFlags), menuHistory, uintptr(unsafe.Pointer(&historyText[0])))
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
				t.setStartupError(err)
				return
			}
			t.startupEnabled = false
			t.setStartupError(nil)
		} else {
			if err := enableStartup(); err != nil {
				t.logger.Printf("开启开机启动失败: %v", err)
				t.setStartupError(err)
				return
			}
			t.startupEnabled = true
			t.setStartupError(nil)
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
