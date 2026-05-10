//go:build windows

package capture

import (
	"fmt"
	"image"
	"syscall"
	"unsafe"
)

// GDI function declarations
var (
	user32                     = syscall.NewLazyDLL("user32.dll")
	gdi32                      = syscall.NewLazyDLL("gdi32.dll")
	procGetDesktopWindow       = user32.NewProc("GetDesktopWindow")
	procGetDC                  = user32.NewProc("GetDC")
	procReleaseDC              = user32.NewProc("ReleaseDC")
	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
)

const (
	SRCCOPY        = 0xCC0020
	BI_RGB         = 0
	DIB_RGB_COLORS = 0
	SM_CXSCREEN    = 0
	SM_CYSCREEN    = 1
)

// BITMAPINFOHEADER structure for DIB operations
type BITMAPINFOHEADER struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

// BITMAPINFO structure
type BITMAPINFO struct {
	BmiHeader BITMAPINFOHEADER
	BmiColors [1]uint32 // Placeholder for color table
}

// Capture retrieves the current desktop screen as an *image.RGBA.
//
// Implementation approach:
// 1. Call GetDesktopWindow() to get the desktop handle.
// 2. Call GetDC(hwnd) to get the desktop device context.
// 3. Create a compatible in-memory DC with CreateCompatibleDC.
// 4. Create a compatible bitmap with CreateCompatibleBitmap at the display resolution.
// 5. Select the bitmap into the memory DC with SelectObject.
// 6. Call BitBlt to copy the screen into the bitmap.
// 7. Call GetDIBits to extract raw pixel bytes in BGRA format.
// 8. Release DCs and delete objects after each capture.
// 9. Return a *image.RGBA for encoding.
func Capture() (*image.RGBA, error) {
	// Step 1: Get desktop window handle
	ret, _, err := procGetDesktopWindow.Call()
	hwnd := ret
	if hwnd == 0 {
		return nil, fmt.Errorf("failed to get desktop window: %w", err)
	}

	// Step 2: Get device context for the desktop
	ret, _, err = procGetDC.Call(hwnd)
	hdc := ret
	if hdc == 0 {
		return nil, fmt.Errorf("failed to get device context: %w", err)
	}
	defer procReleaseDC.Call(hwnd, hdc)

	// Get screen dimensions
	width, height, err := ScreenBounds()
	if err != nil {
		return nil, err
	}

	// Step 3: Create compatible in-memory device context
	ret, _, err = procCreateCompatibleDC.Call(hdc)
	hdcMem := ret
	if hdcMem == 0 {
		return nil, fmt.Errorf("failed to create compatible DC: %w", err)
	}
	defer procDeleteDC.Call(hdcMem)

	// Step 4: Create compatible bitmap at display resolution
	ret, _, err = procCreateCompatibleBitmap.Call(hdc, uintptr(width), uintptr(height))
	hbmp := ret
	if hbmp == 0 {
		return nil, fmt.Errorf("failed to create compatible bitmap: %w", err)
	}
	defer procDeleteObject.Call(hbmp)

	// Step 5: Select bitmap into memory DC
	ret, _, err = procSelectObject.Call(hdcMem, hbmp)
	oldObj := ret
	if oldObj == 0 {
		return nil, fmt.Errorf("failed to select bitmap into DC: %w", err)
	}
	defer procSelectObject.Call(hdcMem, oldObj)

	// Step 6: Copy screen pixels to bitmap using BitBlt
	ret, _, err = procBitBlt.Call(hdcMem, 0, 0, uintptr(width), uintptr(height), hdc, 0, 0, SRCCOPY)
	if ret == 0 {
		return nil, fmt.Errorf("BitBlt failed: %w", err)
	}

	// Step 7: Extract raw pixel bytes in BGRA format using GetDIBits
	// Create BITMAPINFOHEADER for DIB operations
	bmi := BITMAPINFO{
		BmiHeader: BITMAPINFOHEADER{
			BiSize:        uint32(unsafe.Sizeof(BITMAPINFOHEADER{})),
			BiWidth:       int32(width),
			BiHeight:      -int32(height), // Negative height means top-down
			BiPlanes:      1,
			BiBitCount:    32, // 32 bits per pixel (BGRA)
			BiCompression: BI_RGB,
		},
	}

	// Allocate buffer for pixel data
	pixelCount := width * height
	bgraData := make([]byte, pixelCount*4)

	// Extract pixel data
	ret, _, err = procGetDIBits.Call(hdc, hbmp, 0, uintptr(height), uintptr(unsafe.Pointer(&bgraData[0])), uintptr(unsafe.Pointer(&bmi)), DIB_RGB_COLORS)
	if ret == 0 {
		return nil, fmt.Errorf("GetDIBits failed: %w", err)
	}

	// Step 8: Convert BGRA to RGBA (image.RGBA format)
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < pixelCount; i++ {
		b := bgraData[i*4]
		g := bgraData[i*4+1]
		r := bgraData[i*4+2]
		a := bgraData[i*4+3]
		img.Pix[i*4] = r
		img.Pix[i*4+1] = g
		img.Pix[i*4+2] = b
		img.Pix[i*4+3] = a
	}

	// Step 9: Release DCs and delete objects (via defer above)
	return img, nil
}

// ScreenBounds returns the current display resolution (width, height) in pixels.
func ScreenBounds() (width, height int, err error) {
	// Query the primary display dimensions using GetSystemMetrics
	ret, _, _ := procGetSystemMetrics.Call(SM_CXSCREEN)
	width = int(ret)

	ret, _, _ = procGetSystemMetrics.Call(SM_CYSCREEN)
	height = int(ret)

	if width <= 0 || height <= 0 {
		return 0, 0, fmt.Errorf("failed to query screen dimensions: got %dx%d", width, height)
	}

	return width, height, nil
}
