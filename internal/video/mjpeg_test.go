package video

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

type jpegTestStream struct{ packets chan driver.Packet }

func (s jpegTestStream) Codec() driver.Codec           { return driver.MJPEG }
func (s jpegTestStream) Size() (int, int)              { return 18, 20 }
func (s jpegTestStream) Packets() <-chan driver.Packet { return s.packets }
func (s jpegTestStream) Err() error                    { return nil }
func (s jpegTestStream) Close() error                  { return nil }

func TestPlayerShowsJPEGWithoutFFmpegDecoder(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 18, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 18; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, img, nil); err != nil {
		t.Fatal(err)
	}
	packets := make(chan driver.Packet, 1)
	packets <- driver.Packet{KeyFrame: true, Data: jpg.Bytes()}
	close(packets)
	p, err := NewPlayer(jpegTestStream{packets: packets}, PlayerOptions{YUV: true})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	select {
	case <-p.Done():
	case <-time.After(time.Second):
		t.Fatal("JPEG player did not finish")
	}
	frame := p.Take()
	if frame == nil || frame.YUV == nil || frame.Width != 18 || frame.Height != 20 || !frame.YUV.FullRange {
		t.Fatalf("unexpected JPEG frame: %#v", frame)
	}
}
