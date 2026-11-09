package screen

import (
	"image"
	"image/draw"
	"math"
)

// DisplayMask is the shape of the visible panel, in its current orientation.
// Its vertices are in reference pixels and must not be modified.
type DisplayMask struct {
	Width, Height int
	Vertices      []DisplayPoint
}

type DisplayPoint struct{ X, Y float64 }

const (
	proWidth  = 1170
	proHeight = 2532
)

// These points come from the crosshatched display active area in Apple's
// iPhone 13 Pro dimensional drawing (2021-09-28, sheet 1), normalized to
// 1170×2532. Both the corners and the notch are symmetric left to right.
var proCorner = []DisplayPoint{
	{0, 202.4}, {0.1, 177.7}, {0.6, 155.8}, {1.9, 136.3}, {4.1, 118.9},
	{7.5, 103.1}, {15.6, 79.6}, {27, 59.1}, {41.5, 41.5}, {59.2, 26.9},
	{79.6, 15.6}, {103.1, 7.4}, {118.9, 4.1}, {136.3, 1.9}, {155.8, 0.6},
	{177.7, 0.1}, {202.4, 0},
}

var proNotch = []DisplayPoint{
	{318.4, 0}, {324.8, 0.1}, {329.7, 0.8}, {333.7, 2.3}, {337.1, 4.7},
	{339.7, 8}, {341.3, 12.1}, {342, 16.9}, {342.3, 23.4}, {342.6, 31},
	{342.9, 36.5}, {343.4, 41.1}, {344, 45.4}, {344.8, 49.7}, {348.5, 60.8},
	{354.6, 71.6}, {362.8, 81.4}, {372.8, 89.5}, {383.8, 95.3}, {395, 98.9},
	{406, 100.5}, {417.2, 101}, {430, 101},
}

var proMasks = func() [4]DisplayMask {
	var outline []DisplayPoint
	add := func(points []DisplayPoint, flipX, flipY bool) {
		for i := range points {
			if flipX != flipY {
				i = len(points) - 1 - i
			}
			p := points[i]
			if flipX {
				p.X = proWidth - p.X
			}
			if flipY {
				p.Y = proHeight - p.Y
			}
			outline = append(outline, p)
		}
	}
	add(proCorner, false, false)
	add(proNotch, false, false)
	add(proNotch, true, false)
	add(proCorner, true, false)
	add(proCorner, true, true)
	add(proCorner, false, true)
	masks := [4]DisplayMask{
		{Width: proWidth, Height: proHeight, Vertices: outline},
		{Width: proHeight, Height: proWidth},
		{Width: proWidth, Height: proHeight},
		{Width: proHeight, Height: proWidth},
	}
	for _, p := range outline {
		masks[1].Vertices = append(masks[1].Vertices, DisplayPoint{proHeight - p.Y, p.X})
		masks[2].Vertices = append(masks[2].Vertices, DisplayPoint{proWidth - p.X, proHeight - p.Y})
		masks[3].Vertices = append(masks[3].Vertices, DisplayPoint{p.Y, proWidth - p.X})
	}
	return masks
}()

const (
	matePadWidth  = 1840
	matePadHeight = 2800
	// The BKY-family straight-on product image shows an active corner
	// radius of about three pixels across a 323-pixel-wide display.
	// This is an estimate from the product image, not a CAD tolerance.
	matePadRadius = 28
)

var matePadMasks = func() [2]DisplayMask {
	var masks [2]DisplayMask
	for i, size := range [2]image.Point{{X: matePadWidth, Y: matePadHeight}, {X: matePadHeight, Y: matePadWidth}} {
		w, h := float64(size.X), float64(size.Y)
		r := float64(matePadRadius)
		mask := DisplayMask{Width: size.X, Height: size.Y}
		centers := [4]DisplayPoint{{w - r, r}, {w - r, h - r}, {r, h - r}, {r, r}}
		for corner, c := range centers {
			for j := 0; j <= 12; j++ {
				angle := math.Pi / 2 * (float64(corner) - 1 + float64(j)/12)
				mask.Vertices = append(mask.Vertices, DisplayPoint{X: c.X + r*math.Cos(angle), Y: c.Y + r*math.Sin(angle)})
			}
		}
		masks[i] = mask
	}
	return masks
}()

// PhysicalDisplayMask returns a measured outline for a known model, turned
// clockwise in quarter turns from the display's native portrait frame.
func PhysicalDisplayMask(model string, turn int) *DisplayMask {
	switch model {
	case "iPhone 13 Pro":
		return &proMasks[turn&3]
	case "BKY-W20":
		return &matePadMasks[turn&1]
	default:
		return nil
	}
}

// UprightDisplayMask uses the actual picture dimensions on tablets
// whose stream is already rotated before it reaches Pinky.
func UprightDisplayMask(model string, turn, w, h int) *DisplayMask {
	if model == "BKY-W20" && w > 0 && h > 0 {
		turn = 0
		if w > h {
			turn = 1
		}
	}
	return PhysicalDisplayMask(model, turn)
}

// Fits checks that the outline and the upright screen have matching axes.
func (m *DisplayMask) Fits(w, h int) bool {
	return m != nil && w > 0 && h > 0 && math.Abs(float64(w)/float64(h)*float64(m.Height)/float64(m.Width)-1) < 0.02
}

// spans gives the visible pixel intervals on row y at the requested size.
// This outline crosses a scanline at most four times, including the notch.
func (m *DisplayMask) spans(y, w, h int) ([4]int, int) {
	row := (float64(y) + 0.5) * float64(m.Height) / float64(h)
	var crossings [4]float64
	n := 0
	previous := m.Vertices[len(m.Vertices)-1]
	for _, p := range m.Vertices {
		if (p.Y > row) != (previous.Y > row) {
			x := p.X + (row-p.Y)*(previous.X-p.X)/(previous.Y-p.Y)
			i := n
			for i > 0 && x < crossings[i-1] {
				crossings[i] = crossings[i-1]
				i--
			}
			crossings[i] = x
			n++
		}
		previous = p
	}
	var pixels [4]int
	for i := range n {
		pixels[i] = int(math.Ceil(crossings[i]*float64(w)/float64(m.Width) - 0.5))
	}
	return pixels, n
}

// Visible reports whether the entire positioned region lies on the display.
func (m *DisplayMask) Visible(r Rect, w, h int) bool {
	if !m.Fits(w, h) || r.Empty() {
		return true
	}
	if r.X < 0 || r.Y < 0 || r.W > w-r.X || r.H > h-r.Y {
		return false
	}
	for y := r.Y; y < r.Y+r.H; y++ {
		spans, n := m.spans(y, w, h)
		inside := false
		for i := 0; i < n; i += 2 {
			if r.X >= spans[i] && r.X+r.W <= spans[i+1] {
				inside = true
				break
			}
		}
		if !inside {
			return false
		}
	}
	return true
}

// Cover blacks out pixels the display cannot show, keeping its dimensions.
func (m *DisplayMask) Cover(img image.Image) image.Image {
	b := img.Bounds()
	if !m.Fits(b.Dx(), b.Dy()) {
		return img
	}
	out := image.NewRGBA(b)
	draw.Draw(out, b, image.Black, image.Point{}, draw.Src)
	for y := range b.Dy() {
		spans, n := m.spans(y, b.Dx(), b.Dy())
		for i := 0; i < n; i += 2 {
			r := image.Rect(b.Min.X+spans[i], b.Min.Y+y, b.Min.X+spans[i+1], b.Min.Y+y+1)
			draw.Draw(out, r, img, r.Min, draw.Src)
		}
	}
	return out
}
