// Package encoder provides frame encoding functionality for the agent.
//
// # Phase 1: Standard Library JPEG
//
// The current implementation uses the standard library image/jpeg encoder,
// which is adequate for Phase 1 but has performance limitations:
//
// - Single-threaded encoding
// - No hardware acceleration
// - CPU-intensive for high-resolution screens
//
// # Benchmarking Note
//
// Measure JPEG encode time per frame during Phase 1 validation:
//
//   - Log encode duration for each frame (milliseconds)
//   - Calculate rolling 5-second average
//   - Identify if encode time exceeds target tick interval
//
// If encode time becomes a bottleneck (>5ms per frame at target FPS),
// consider these optimizations in later phases:
//
// - Parallel encoding pipeline (prefetch next frame while encoding current)
// - Hardware-accelerated encoding (NVIDIA NVENC, Intel QuickSync)
// - Downsampling or region-of-interest encoding
// - MJPEG or H.264 codec switch
//
// # Future: Codec Upgrades (Phase 5+)
//
// For sustained high FPS or ultra-low latency, upgrade to:
// - H.264 or H.265 with hardware acceleration
// - Requires ffmpeg or libvpx bindings
// - Maintains compatible EncodeJPEG interface through wrapper
package encoder
