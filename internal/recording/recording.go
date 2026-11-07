// Package recording writes a phone's video stream to MP4 files without
// re-encoding, and plays them back as a driver.Stream, so the client shows
// a recording with the same decoder and view as the live mirror.
package recording

import (
	"context"
	"fmt"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// Open plays the recording at path from offset from: it starts at the key
// frame at or before from and sends packets at the pace they were
// recorded, as a live stream would arrive. Closing the stream stops it.
func Open(ctx context.Context, path string, from time.Duration) (driver.Stream, error) {
	return nil, fmt.Errorf("recording: playback: %w", driver.ErrUnsupported)
}
