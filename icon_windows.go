//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	gdi32DLL           = syscall.NewLazyDLL("gdi32.dll")
	user32IconDLL      = syscall.NewLazyDLL("user32.dll")
	createDIBSection   = gdi32DLL.NewProc("CreateDIBSection")
	createBitmap       = gdi32DLL.NewProc("CreateBitmap")
	deleteGDIObject    = gdi32DLL.NewProc("DeleteObject")
	createIconIndirect = user32IconDLL.NewProc("CreateIconIndirect")
	destroyIcon        = user32IconDLL.NewProc("DestroyIcon")
)

const (
	dibRGBColors = 0
	dibSection   = 0
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

type iconInfo struct {
	FIcon    uint32
	XHotspot uint32
	YHotspot uint32
	HbmMask  syscall.Handle
	HbmColor syscall.Handle
}

func buildBatteryIcon(state BatteryState) (syscall.Handle, error) {
	const size = 32

	info := bitmapInfo{
		Header: bitmapInfoHeader{
			Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
			Width:       size,
			Height:      -size,
			Planes:      1,
			BitCount:    32,
			Compression: dibSection,
		},
	}

	var bits uintptr
	colorBitmap, _, callErr := createDIBSection.Call(
		0,
		uintptr(unsafe.Pointer(&info)),
		dibRGBColors,
		uintptr(unsafe.Pointer(&bits)),
		0,
		0,
	)
	if colorBitmap == 0 || bits == 0 {
		return 0, fmt.Errorf("CreateDIBSection: %w", callErr)
	}
	colorHandle := syscall.Handle(colorBitmap)

	maskBitmap, _, callErr := createBitmap.Call(size, size, 1, 1, 0)
	if maskBitmap == 0 {
		deleteGDIObject.Call(colorBitmap)
		return 0, fmt.Errorf("CreateBitmap: %w", callErr)
	}
	maskHandle := syscall.Handle(maskBitmap)

	pixels := unsafe.Slice((*uint32)(unsafe.Pointer(bits)), size*size)
	drawBatteryIcon(pixels, size, state)

	icon := iconInfo{
		FIcon:    1,
		HbmMask:  maskHandle,
		HbmColor: colorHandle,
	}
	hicon, _, callErr := createIconIndirect.Call(uintptr(unsafe.Pointer(&icon)))
	deleteGDIObject.Call(colorBitmap)
	deleteGDIObject.Call(maskBitmap)
	if hicon == 0 {
		return 0, fmt.Errorf("CreateIconIndirect: %w", callErr)
	}
	return syscall.Handle(hicon), nil
}

func drawBatteryIcon(pixels []uint32, size int, state BatteryState) {
	clearPixels(pixels, bgra(0, 0, 0, 0))

	outline := bgra(235, 240, 245, 255)
	if state.Error != "" || state.Percent < 0 {
		outline = bgra(145, 153, 163, 255)
	}

	drawRectBorder(pixels, size, 4, 6, 24, 20, 2, outline)
	drawRect(pixels, size, 28, 12, 3, 8, outline)

	percent := state.Percent
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	fill := bgra(46, 204, 113, 255)
	if percent < 20 {
		fill = bgra(231, 76, 60, 255)
	} else if percent < 50 {
		fill = bgra(241, 196, 15, 255)
	}
	fillWidth := percent * 19 / 100
	if fillWidth > 0 {
		drawRect(pixels, size, 7, 9, fillWidth, 14, fill)
	}

	if state.Charge == ChargeCharging {
		lightning := bgra(255, 255, 255, 255)
		drawThickLine(pixels, size, 19, 9, 14, 17, 3, lightning)
		drawThickLine(pixels, size, 14, 17, 19, 17, 3, lightning)
		drawThickLine(pixels, size, 19, 17, 16, 24, 3, lightning)
	} else if state.Error != "" || state.Percent < 0 {
		drawThickLine(pixels, size, 10, 11, 23, 23, 2, outline)
		drawThickLine(pixels, size, 23, 11, 10, 23, 2, outline)
	}
}

func clearPixels(pixels []uint32, value uint32) {
	for i := range pixels {
		pixels[i] = value
	}
}

func drawRect(pixels []uint32, size, x, y, width, height int, value uint32) {
	for yy := y; yy < y+height; yy++ {
		for xx := x; xx < x+width; xx++ {
			setPixel(pixels, size, xx, yy, value)
		}
	}
}

func drawRectBorder(pixels []uint32, size, x, y, width, height, thickness int, value uint32) {
	drawRect(pixels, size, x, y, width, thickness, value)
	drawRect(pixels, size, x, y+height-thickness, width, thickness, value)
	drawRect(pixels, size, x, y, thickness, height, value)
	drawRect(pixels, size, x+width-thickness, y, thickness, height, value)
}

func drawThickLine(pixels []uint32, size, x0, y0, x1, y1, thickness int, value uint32) {
	dx := abs(x1 - x0)
	sx := -1
	if x0 < x1 {
		sx = 1
	}
	dy := -abs(y1 - y0)
	sy := -1
	if y0 < y1 {
		sy = 1
	}
	err := dx + dy
	for {
		drawRect(pixels, size, x0-thickness/2, y0-thickness/2, thickness, thickness, value)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func setPixel(pixels []uint32, size, x, y int, value uint32) {
	if x < 0 || x >= size || y < 0 || y >= size {
		return
	}
	pixels[y*size+x] = value
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func bgra(r, g, b, a byte) uint32 {
	return uint32(a)<<24 | uint32(r)<<16 | uint32(g)<<8 | uint32(b)
}
