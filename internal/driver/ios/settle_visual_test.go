package ios

import (
	"image"
	"image/color"
	"testing"
)

func TestVisualMotionDistinguishesEncoderNoise(t *testing.T) {
	bounds := image.Rect(0, 0, 72, 144)
	before, after := image.NewRGBA(bounds), image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			before.SetRGBA(x, y, color.RGBA{R: 240, G: 240, B: 240, A: 255})
			after.SetRGBA(x, y, color.RGBA{R: 236, G: 244, B: 239, A: 255})
		}
	}
	if hasVisualMotion(before, after) {
		t.Fatal("small compression differences look like movement")
	}
	for x := 4; x < 68; x++ {
		after.SetRGBA(x, 90, color.RGBA{A: 255})
	}
	if !hasVisualMotion(before, after) {
		t.Fatal("a moving list divider was missed")
	}
}
