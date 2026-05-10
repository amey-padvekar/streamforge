package encoder

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
)

// EncodeJPEG encodes an RGBA image to JPEG format at the specified quality level.
//
// The quality parameter should be between 1 and 100, where higher values produce
// better quality but larger output. Recommended default: 75 for a good balance
// between quality and bandwidth.
//
// Note: Measure encode time per frame and log it for profiling and optimization.
// In later phases, consider parallel encoding or hardware acceleration if encode
// time becomes a bottleneck.
func EncodeJPEG(img *image.RGBA, quality int) ([]byte, error) {
	if img == nil {
		return nil, fmt.Errorf("image is nil")
	}

	if quality < 1 || quality > 100 {
		return nil, fmt.Errorf("quality must be between 1 and 100, got %d", quality)
	}

	buf := &bytes.Buffer{}
	err := jpeg.Encode(buf, img, &jpeg.Options{Quality: quality})
	if err != nil {
		return nil, fmt.Errorf("JPEG encode failed: %w", err)
	}

	return buf.Bytes(), nil
}
