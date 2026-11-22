package posters

import (
	"image"
	"image/color"
	"math"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("resample", Label("unit", "posters"), func() {
	It("keeps a flat colour flat and opaque", func() {
		src := image.NewYCbCr(
			image.Rect(5, 9, 5+781, 9+1171),
			image.YCbCrSubsampleRatio420,
		)
		for i := range src.Y {
			src.Y[i] = 140
		}
		for i := range src.Cb {
			src.Cb[i], src.Cr[i] = 100, 160
		}
		want := color.RGBAModel.Convert(src.At(5, 9)).(color.RGBA)

		out := resample(src, 320, 480)

		Expect(out.Bounds()).To(Equal(image.Rect(0, 0, 320, 480)))
		Expect(out.Opaque()).To(BeTrue())
		for y := range 480 {
			for x := range 320 {
				Expect(out.RGBAAt(x, y)).To(Equal(want))
			}
		}
	})

	It("aliases far less than the box filter", func() {
		// A zone plate whose rings reach the source's Nyquist frequency: past
		// the destination's, a resize should leave flat grey.
		const sw, sh, w, h = 780, 1170, 320, 480
		src := image.NewGray(image.Rect(0, 0, sw, sh))
		k := math.Pi * 0.5 / math.Hypot(sw, sh)
		for y := range sh {
			for x := range sw {
				src.Pix[y*src.Stride+x] = uint8(
					127.5 + 127.5*math.Cos(k*float64(x*x+y*y)),
				)
			}
		}
		alias := func(out *image.RGBA) float64 {
			scale := float64(sw) / w
			var sum, n float64
			for y := range h {
				for x := range w {
					r := math.Hypot((float64(x)+0.5)*scale, (float64(y)+0.5)*scale)
					if k*r/math.Pi > 1.3*0.5/scale {
						d := float64(out.Pix[y*out.Stride+x*4]) - 127.5
						sum += d * d
						n++
					}
				}
			}
			return math.Sqrt(sum / n)
		}

		Expect(
			alias(resample(src, w, h)),
		).To(BeNumerically("<", alias(Downscale(src, w, h))/2))
	})
})

var _ = Describe("jpegMeta", Label("unit", "posters"), func() {
	It("stops at a truncated segment without reading past it", func() {
		j := withSegment(
			[]byte{0xFF, 0xD8, 0xFF, 0xD9},
			[]byte{0xFF, 0xE1, 0x40, 0x00, 'E'},
		)
		orientation, icc := jpegMeta(j)
		Expect(orientation).To(BeZero())
		Expect(icc).To(BeEmpty())
	})
})
