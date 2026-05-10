// Package capture provides screen capture functionality for the agent.
//
// # Phase 1: GDI Capture
//
// The current implementation uses Windows GDI (Graphics Device Interface) via
// golang.org/x/sys/windows to capture the desktop. This approach is simple and
// sufficient for Phase 1 but has limitations:
//
// - No dirty rectangle tracking (full screen captured every frame)
// - Single-threaded blocking captures
// - No GPU acceleration
// - No multi-monitor support
//
// # Future: DXGI Upgrade (Phase 5)
//
// For higher performance and lower latency, consider upgrading to DXGI (Direct3D):
//
// - Use IDXGIOutputDuplication for GPU-accelerated screen capture
// - Dirty rectangles tracked by DXGI automatically
// - Non-blocking async capture pipeline
// - Substantially lower CPU and latency footprint
// - Requires github.com/go-ole/go-ole and Direct3D bindings
//
// The Capture() and ScreenBounds() interfaces will remain stable across
// the upgrade, allowing a drop-in replacement.
package capture
