package ui

import (
	"math"
	"testing"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

func TestPhysicalBezelScalesWithWindow(t *testing.T) {
	for _, model := range []string{"iPhone 13 Pro", "BKY-W20"} {
		d := gateway.DeviceView{Info: driver.Info{Model: model}}
		_, _, small, corner, _ := deviceBezel(d, .66, 340, 530, 9)
		_, _, large, largeCorner, _ := deviceBezel(d, .66, 680, 1060, 9)
		if small <= 0 || math.Abs(float64(large-2*small)) > .001 || math.Abs(float64(largeCorner-2*corner)) > .001 {
			t.Fatalf("%s: bezel %v to %v, corner %v to %v", model, small, large, corner, largeCorner)
		}
	}
}
