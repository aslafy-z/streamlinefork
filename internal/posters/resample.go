package posters

import (
	"image"
	"image/draw"
	"math"
)

// tap is the run of source pixels one destination pixel is made of, along one
// axis, and the weight of each.
type tap struct {
	start   int
	weights []float32
}

// catmullRom is the Catmull-Rom cubic, zero past 2.
func catmullRom(t float64) float64 {
	t = math.Abs(t)
	switch {
	case t < 1:
		return (1.5*t-2.5)*t*t + 1
	case t < 2:
		return ((-0.5*t+2.5)*t-4)*t + 2
	}
	return 0
}

// taps spreads a Catmull-Rom filter over srcLen pixels for each of dstLen,
// its support widened by the reduction so every source pixel contributes:
// without that a reduction samples the source and aliases.
func taps(srcLen, dstLen int) []tap {
	scale := float64(srcLen) / float64(dstLen)
	stretch := max(scale, 1)
	support := 2 * stretch
	out := make([]tap, dstLen)
	for i := range out {
		center := (float64(i) + 0.5) * scale
		lo := max(0, int(math.Floor(center-support)))
		hi := min(srcLen, int(math.Ceil(center+support)))
		w := make([]float64, hi-lo)
		var sum float64
		for j := range w {
			w[j] = catmullRom((float64(lo+j) + 0.5 - center) / stretch)
			sum += w[j]
		}
		t := tap{start: lo, weights: make([]float32, len(w))}
		for j := range w {
			t.weights[j] = float32(w[j] / sum)
		}
		out[i] = t
	}
	return out
}

// resample scales src to w by h through a Catmull-Rom filter, separably: each
// destination row is first blended from the source rows under it, then along
// the row. Source rows are converted to RGBA into a band holding only the
// rows of the current destination row's window, which slides down the image,
// so a resize never holds a converted copy of the whole source.
func resample(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	xs, ys := taps(sw, w), taps(sh, h)
	rows := 0
	for _, t := range ys {
		rows = max(rows, len(t.weights))
	}
	band := image.NewRGBA(image.Rect(0, 0, sw, rows))
	bandLo, bandHi := 0, 0 // the source rows band holds, [bandLo, bandHi)
	acc := make([]float32, sw*4)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))

	for dy, ty := range ys {
		lo, hi := ty.start, ty.start+len(ty.weights)
		keep := 0
		if lo >= bandLo && lo < bandHi {
			keep = bandHi - lo
			copy(
				band.Pix,
				band.Pix[(lo-bandLo)*band.Stride:bandHi*band.Stride-bandLo*band.Stride],
			)
		}
		if lo+keep < hi {
			fill := band.SubImage(image.Rect(0, keep, sw, hi-lo)).(*image.RGBA)
			draw.Draw(
				fill,
				fill.Bounds(),
				src,
				image.Pt(b.Min.X, b.Min.Y+lo+keep),
				draw.Src,
			)
		}
		bandLo, bandHi = lo, hi

		clear(acc)
		for j, wy := range ty.weights {
			row := band.Pix[j*band.Stride : j*band.Stride+sw*4]
			for x, v := range row {
				acc[x] += wy * float32(v)
			}
		}

		out := dst.Pix[dy*dst.Stride:]
		for dx, tx := range xs {
			var r, g, bl, a float32
			p := acc[tx.start*4:]
			for k, wx := range tx.weights {
				r += wx * p[k*4]
				g += wx * p[k*4+1]
				bl += wx * p[k*4+2]
				a += wx * p[k*4+3]
			}
			// The cubic's negative lobes overshoot at hard edges; RGBA is
			// premultiplied, so no channel may exceed alpha.
			av := clampByte(a)
			o := out[dx*4 : dx*4+4]
			o[0] = min(clampByte(r), av)
			o[1] = min(clampByte(g), av)
			o[2] = min(clampByte(bl), av)
			o[3] = av
		}
	}
	return dst
}

func clampByte(v float32) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	}
	return uint8(v + 0.5)
}
