package video

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"time"
)

// Each JPEG is independently decodable. Use Go's decoder so installations
// whose FFmpeg only supports H.264 and HEVC can show the preview.
func (p *Player) runJPEG() {
	defer close(p.done)
	for pkt := range p.stream.Packets() {
		received := time.Now()
		conf, err := jpeg.DecodeConfig(bytes.NewReader(pkt.Data))
		if err != nil || conf.Width <= 0 || conf.Height <= 0 || conf.Width > 8192 || conf.Height > 8192 || conf.Width*conf.Height > 32_000_000 {
			p.mu.Lock()
			p.stats.Errors++
			p.mu.Unlock()
			continue
		}
		img, err := jpeg.Decode(bytes.NewReader(pkt.Data))
		if err != nil {
			p.mu.Lock()
			p.stats.Errors++
			p.mu.Unlock()
			continue
		}
		decoded := time.Now()
		b := img.Bounds()
		f := &Frame{Width: b.Dx(), Height: b.Dy(), PTS: pkt.PTS, Turn: pkt.Turn, Received: received}
		if p.opt.YUV {
			f.YUV = jpegPlanes(img)
		} else {
			f.Image = image.NewRGBA(b)
			draw.Draw(f.Image, b, img, b.Min, draw.Src)
		}
		f.Ready = time.Now()
		p.publish(f, "", f.Width, f.Height, received, decoded, f.Ready)
		if p.opt.OnFrame != nil {
			p.opt.OnFrame()
		}
	}
	err := p.stream.Err()
	if err == nil {
		err = io.EOF
	}
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
	if p.opt.OnFrame != nil {
		p.opt.OnFrame()
	}
}

func jpegPlanes(img image.Image) *YUV {
	b := img.Bounds()
	yuv := &YUV{FullRange: true, Matrix: BT601}
	yuv.layout(false, b.Dx(), b.Dy())
	switch src := img.(type) {
	case *image.YCbCr:
		for y := 0; y < b.Dy(); y++ {
			offset := src.YOffset(b.Min.X, b.Min.Y+y)
			copy(yuv.Planes[0][y*yuv.Strides[0]:], src.Y[offset:offset+b.Dx()])
		}
		for y := 0; y < b.Dy(); y += 2 {
			for x := 0; x < b.Dx(); x += 2 {
				offset := src.COffset(b.Min.X+x, b.Min.Y+y)
				i := y/2*yuv.Strides[1] + x/2
				yuv.Planes[1][i], yuv.Planes[2][i] = src.Cb[offset], src.Cr[offset]
			}
		}
	case *image.Gray:
		for y := 0; y < b.Dy(); y++ {
			offset := src.PixOffset(b.Min.X, b.Min.Y+y)
			copy(yuv.Planes[0][y*yuv.Strides[0]:], src.Pix[offset:offset+b.Dx()])
		}
		for i := range yuv.Planes[1] {
			yuv.Planes[1][i], yuv.Planes[2][i] = 128, 128
		}
	default:
		for y := 0; y < b.Dy(); y++ {
			for x := 0; x < b.Dx(); x++ {
				r, g, bv, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
				l, u, v := color.RGBToYCbCr(uint8(r>>8), uint8(g>>8), uint8(bv>>8))
				yuv.Planes[0][y*yuv.Strides[0]+x] = l
				if x%2 == 0 && y%2 == 0 {
					i := y/2*yuv.Strides[1] + x/2
					yuv.Planes[1][i], yuv.Planes[2][i] = u, v
				}
			}
		}
	}
	return yuv
}
