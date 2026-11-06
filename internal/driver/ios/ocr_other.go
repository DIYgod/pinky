//go:build !darwin

package ios

import (
	"fmt"

	"github.com/DIYgod/pinky/internal/driver"
)

// recognizeText is only implemented with macOS Vision so far. Windows
// (Windows.Media.Ocr) and Linux (PaddleOCR on ONNX Runtime) are planned in
// the design.
func recognizeText([]byte, ocrOptions) ([]ocrLine, error) {
	return nil, fmt.Errorf("ocr on this platform: %w", driver.ErrUnsupported)
}
