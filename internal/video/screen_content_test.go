package video

import (
	"bytes"
	"image"
	"runtime"
	"testing"
	"unsafe"
)

func TestConvertYUVScreenContent(t *testing.T) {
	const (
		i420 = 1
		nv12 = 2
		j420 = 3
	)
	l := &lib{pixYUV420P: i420, pixNV12: nv12, pixYUVJ420P: j420}
	for _, tt := range []struct {
		name       string
		format     int32
		matrix     int32
		wantChroma byte
		wantMatrix Matrix
	}{
		{"planar untagged", j420, 2, 187, BT709},
		{"interleaved untagged", nv12, 2, 187, BT709},
		{"tagged video", i420, 6, 200, BT601},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var raw [40]uint64
			frame := unsafe.Pointer(&raw[0])
			picture := flatYUV(tt.format == nv12, 6, 4, 100, 200, 128, BT601, true)
			picture.Planes[0][5] = 0      // the first column of right padding
			clear(picture.Planes[0][18:]) // the bottom padding row
			for i, plane := range picture.Planes {
				if len(plane) != 0 {
					putPtr(frame, frameData+uintptr(8*i), unsafe.Pointer(&plane[0]))
				}
				putInt32(frame, frameLinesize+uintptr(4*i), int32(picture.Strides[i]))
			}
			putInt32(frame, frameWidth, 6)
			putInt32(frame, frameHeight, 4)
			putInt32(frame, frameFormat, tt.format)
			putInt32(frame, frameColorRange, avColorRangeJPEG)
			putInt32(frame, frameColorspace, tt.matrix)
			putInt32(frame, frameColorPrim, 2)
			putInt32(frame, frameColorTRC, 2)
			decoder := &Decoder{l: l, frame: frame, hwPixFmt: -1, have: true}
			var got YUV
			if err := decoder.convertYUV(&got, image.Pt(5, 3), chromaTable(82), true); err != nil {
				t.Fatal(err)
			}
			if got.Width != 5 || got.Height != 3 || !bytes.Equal(got.Planes[0], bytes.Repeat([]byte{100}, 15)) {
				t.Fatalf("padded picture was not cropped: %dx%d, %v", got.Width, got.Height, got.Planes[0])
			}
			if got.Matrix != tt.wantMatrix {
				t.Errorf("matrix = %d, want %d", got.Matrix, tt.wantMatrix)
			}
			if got.Planes[1][0] != tt.wantChroma {
				t.Errorf("chroma = %d, want %d", got.Planes[1][0], tt.wantChroma)
			}
			if err := decoder.convertYUV(&got, image.Pt(8, 3), nil, false); err != nil {
				t.Fatal(err)
			}
			if got.Width != 6 || got.Height != 4 || got.Planes[1][0] != 200 {
				t.Fatalf("unexpected change to undersized video or default chroma: %dx%d, %d", got.Width, got.Height, got.Planes[1][0])
			}
			runtime.KeepAlive(picture)
		})
	}
}
