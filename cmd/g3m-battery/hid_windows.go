//go:build windows

package main

import (
	"fmt"
	"sort"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	targetVID          = 0x320F
	targetPIDWired     = 0x706B
	targetPIDReceiver  = 0x706E
	targetUsagePage    = 0xFF1C
	targetUsage        = 0x0092
	queryReportLength  = 64
	inputReportMinimum = 10
)

const (
	digcfPresent         = 0x00000002
	digcfDeviceInterface = 0x00000010

	genericRead         = 0x80000000
	genericWrite        = 0x40000000
	fileShareRead       = 0x00000001
	fileShareWrite      = 0x00000002
	openExisting        = 3
	fileAttributeNormal = 0x00000080

	hidpStatusSuccess = 0x00110000
	errorNoMoreItems  = 259
)

var (
	hidDLL      = syscall.NewLazyDLL("hid.dll")
	setupAPIDLL = syscall.NewLazyDLL("setupapi.dll")
	kernel32DLL = syscall.NewLazyDLL("kernel32.dll")

	hidDGetHidGuid        = hidDLL.NewProc("HidD_GetHidGuid")
	hidDGetAttributes     = hidDLL.NewProc("HidD_GetAttributes")
	hidDGetPreparsedData  = hidDLL.NewProc("HidD_GetPreparsedData")
	hidDFreePreparsedData = hidDLL.NewProc("HidD_FreePreparsedData")
	hidPGetCaps           = hidDLL.NewProc("HidP_GetCaps")

	setupDiGetClassDevsW             = setupAPIDLL.NewProc("SetupDiGetClassDevsW")
	setupDiEnumDeviceInterfaces      = setupAPIDLL.NewProc("SetupDiEnumDeviceInterfaces")
	setupDiGetDeviceInterfaceDetailW = setupAPIDLL.NewProc("SetupDiGetDeviceInterfaceDetailW")
	setupDiDestroyDeviceInfoList     = setupAPIDLL.NewProc("SetupDiDestroyDeviceInfoList")

	createFileW = kernel32DLL.NewProc("CreateFileW")
	closeHandle = kernel32DLL.NewProc("CloseHandle")
)

type spDeviceInterfaceData struct {
	CbSize             uint32
	InterfaceClassGuid syscall.GUID
	Flags              uint32
	Reserved           uintptr
}

type hiddAttributes struct {
	Size          uint32
	VendorID      uint16
	ProductID     uint16
	VersionNumber uint16
}

type hidpCaps struct {
	Usage                     uint16
	UsagePage                 uint16
	InputReportByteLength     uint16
	OutputReportByteLength    uint16
	FeatureReportByteLength   uint16
	Reserved                  [17]uint16
	NumberLinkCollectionNodes uint16
	NumberInputButtonCaps     uint16
	NumberInputValueCaps      uint16
	NumberInputDataIndices    uint16
	NumberOutputButtonCaps    uint16
	NumberOutputValueCaps     uint16
	NumberOutputDataIndices   uint16
	NumberFeatureButtonCaps   uint16
	NumberFeatureValueCaps    uint16
	NumberFeatureDataIndices  uint16
}

type hidCandidate struct {
	Path      string
	ProductID uint16
}

func enumerateCandidates() ([]hidCandidate, error) {
	var hidGUID syscall.GUID
	hidDGetHidGuid.Call(uintptr(unsafe.Pointer(&hidGUID)))

	devs, _, err := setupDiGetClassDevsW.Call(
		uintptr(unsafe.Pointer(&hidGUID)),
		0,
		0,
		digcfPresent|digcfDeviceInterface,
	)
	if devs == ^uintptr(0) {
		return nil, fmt.Errorf("SetupDiGetClassDevsW: %w", err)
	}
	defer setupDiDestroyDeviceInfoList.Call(devs)

	candidates := make([]hidCandidate, 0, 2)
	for index := uint32(0); ; index++ {
		var interfaceData spDeviceInterfaceData
		interfaceData.CbSize = uint32(unsafe.Sizeof(interfaceData))
		ok, _, callErr := setupDiEnumDeviceInterfaces.Call(
			devs,
			0,
			uintptr(unsafe.Pointer(&hidGUID)),
			uintptr(index),
			uintptr(unsafe.Pointer(&interfaceData)),
		)
		if ok == 0 {
			if callErr == syscall.Errno(errorNoMoreItems) {
				break
			}
			return nil, fmt.Errorf("SetupDiEnumDeviceInterfaces: %w", callErr)
		}

		var required uint32
		setupDiGetDeviceInterfaceDetailW.Call(
			devs,
			uintptr(unsafe.Pointer(&interfaceData)),
			0,
			0,
			uintptr(unsafe.Pointer(&required)),
			0,
		)
		if required < 5 {
			continue
		}

		detail := make([]byte, required)
		// On Windows x64 the API expects cbSize == 8, while DevicePath starts at byte 4.
		*(*uint32)(unsafe.Pointer(&detail[0])) = uint32(unsafe.Sizeof(uintptr(0)))
		ok, _, _ = setupDiGetDeviceInterfaceDetailW.Call(
			devs,
			uintptr(unsafe.Pointer(&interfaceData)),
			uintptr(unsafe.Pointer(&detail[0])),
			uintptr(required),
			uintptr(unsafe.Pointer(&required)),
			0,
		)
		if ok == 0 {
			continue
		}

		path := utf16PtrToString((*uint16)(unsafe.Pointer(&detail[4])))
		candidate, matched := inspectCandidate(path)
		if matched {
			candidates = append(candidates, candidate)
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidatePriority(candidates[i].ProductID) < candidatePriority(candidates[j].ProductID)
	})
	return candidates, nil
}

func utf16PtrToString(value *uint16) string {
	values := make([]uint16, 0, 128)
	for index := uintptr(0); ; index++ {
		current := *(*uint16)(unsafe.Add(unsafe.Pointer(value), index*unsafe.Sizeof(uint16(0))))
		if current == 0 {
			break
		}
		values = append(values, current)
	}
	return syscall.UTF16ToString(values)
}

func inspectCandidate(path string) (hidCandidate, bool) {
	var empty hidCandidate
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return empty, false
	}

	handle, _, _ := createFileW.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		genericRead|genericWrite,
		fileShareRead|fileShareWrite,
		0,
		openExisting,
		fileAttributeNormal,
		0,
	)
	if handle == ^uintptr(0) {
		return empty, false
	}
	defer closeHandle.Call(handle)

	attributes := hiddAttributes{Size: uint32(unsafe.Sizeof(hiddAttributes{}))}
	ok, _, _ := hidDGetAttributes.Call(handle, uintptr(unsafe.Pointer(&attributes)))
	if ok == 0 || attributes.VendorID != targetVID || !isTargetPID(attributes.ProductID) {
		return empty, false
	}

	var preparsed uintptr
	ok, _, _ = hidDGetPreparsedData.Call(handle, uintptr(unsafe.Pointer(&preparsed)))
	if ok == 0 || preparsed == 0 {
		return empty, false
	}
	defer hidDFreePreparsedData.Call(preparsed)

	var caps hidpCaps
	status, _, _ := hidPGetCaps.Call(preparsed, uintptr(unsafe.Pointer(&caps)))
	if uint32(status) != hidpStatusSuccess || caps.UsagePage != targetUsagePage || caps.Usage != targetUsage {
		return empty, false
	}
	if caps.InputReportByteLength < queryReportLength || caps.OutputReportByteLength < queryReportLength {
		return empty, false
	}

	return hidCandidate{Path: path, ProductID: attributes.ProductID}, true
}

func isTargetPID(pid uint16) bool {
	return pid == targetPIDWired || pid == targetPIDReceiver
}

func candidatePriority(pid uint16) int {
	if pid == targetPIDWired {
		return 0
	}
	return 1
}

func readBattery(path string) (int, byte, error) {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, fmt.Errorf("设备路径: %w", err)
	}

	handle, err := windows.CreateFile(
		pathPtr,
		genericRead|genericWrite,
		fileShareRead|fileShareWrite,
		nil,
		openExisting,
		fileAttributeNormal|windows.FILE_FLAG_OVERLAPPED,
		0,
	)
	if err != nil {
		return 0, 0, fmt.Errorf("CreateFileW: %w", err)
	}
	defer windows.CloseHandle(handle)

	query := [queryReportLength]byte{
		0x04, 0x20, 0x00, 0x1A, 0x06,
	}
	written, err := writeHIDReport(handle, query[:])
	if err != nil {
		return 0, 0, fmt.Errorf("WriteFile: %w", err)
	}
	if written != queryReportLength {
		return 0, 0, fmt.Errorf("WriteFile 写入长度异常: %d", written)
	}

	var response [queryReportLength]byte
	read, err := readHIDReport(handle, response[:])
	if err != nil {
		return 0, 0, fmt.Errorf("ReadFile: %w", err)
	}
	return parseBatteryResponse(response[:read])
}

const hidIOTimeout = 2 * time.Second

func writeHIDReport(handle windows.Handle, report []byte) (uint32, error) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, fmt.Errorf("创建异步 I/O 事件: %w", err)
	}
	defer windows.CloseHandle(event)

	overlapped := windows.Overlapped{HEvent: event}
	var written uint32
	err = windows.WriteFile(handle, report, &written, &overlapped)
	if err == nil {
		return written, nil
	}
	if err != windows.ERROR_IO_PENDING {
		return 0, err
	}
	return completeHIDOperation(handle, &overlapped, "写入 HID 报文")
}

func readHIDReport(handle windows.Handle, response []byte) (uint32, error) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, fmt.Errorf("创建异步 I/O 事件: %w", err)
	}
	defer windows.CloseHandle(event)

	overlapped := windows.Overlapped{HEvent: event}
	var read uint32
	err = windows.ReadFile(handle, response, &read, &overlapped)
	if err == nil {
		return read, nil
	}
	if err != windows.ERROR_IO_PENDING {
		return 0, err
	}
	return completeHIDOperation(handle, &overlapped, "读取 HID 响应")
}

func completeHIDOperation(handle windows.Handle, overlapped *windows.Overlapped, operation string) (uint32, error) {
	result, err := windows.WaitForSingleObject(overlapped.HEvent, uint32(hidIOTimeout/time.Millisecond))
	if err != nil {
		return 0, fmt.Errorf("等待%s: %w", operation, err)
	}
	if result == uint32(windows.WAIT_TIMEOUT) {
		cancelErr := windows.CancelIoEx(handle, overlapped)
		if cancelErr != nil && cancelErr != windows.ERROR_NOT_FOUND {
			return 0, fmt.Errorf("取消%s: %w", operation, cancelErr)
		}
		var ignored uint32
		_ = windows.GetOverlappedResult(handle, overlapped, &ignored, true)
		return 0, fmt.Errorf("%s超时", operation)
	}
	if result != windows.WAIT_OBJECT_0 {
		return 0, fmt.Errorf("等待%s返回未知结果: %d", operation, result)
	}

	var completed uint32
	if err := windows.GetOverlappedResult(handle, overlapped, &completed, true); err != nil {
		return 0, err
	}
	return completed, nil
}

func parseBatteryResponse(response []byte) (int, byte, error) {
	if len(response) < inputReportMinimum {
		return 0, 0, fmt.Errorf("响应长度不足: %d", len(response))
	}
	if response[0] != 0x04 || response[1] != 0x20 || response[3] != 0x1A {
		return 0, 0, fmt.Errorf("响应头无效: % X", response[:inputReportMinimum])
	}
	if response[7] == 0xFF {
		return 0, 0, fmt.Errorf("设备返回无效数据块")
	}
	if response[8] > 100 {
		return 0, 0, fmt.Errorf("电量字节无效: %d", response[8])
	}

	return int(response[8]), response[9], nil
}
