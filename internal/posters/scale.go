package posters

import (
	"image"
	"image/draw"
)

// Downscale averages each destination pixel over the source pixels it
// covers: a box filter, which for a reduction is as good as anything a
// player shows a cover at, and keeps x/image out of the binary.
//
// The source is converted to RGBA one band at a time, the rows a single
// destination row averages, so a resize holds one band beside the decoded
// picture and never a second full copy of it.
func Downscale(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	band := image.NewRGBA(image.Rect(0, 0, sw, (sh+h-1)/h+1))
	for dy := range h {
		y0, y1 := dy*sh/h, max((dy+1)*sh/h, dy*sh/h+1)
		rows := band.SubImage(image.Rect(0, 0, sw, y1-y0)).(*image.RGBA)
		draw.Draw(
			rows,
			rows.Bounds(),
			src,
			image.Pt(b.Min.X, b.Min.Y+y0),
			draw.Src,
		)
		for dx := range w {
			x0, x1 := dx*sw/w, max((dx+1)*sw/w, dx*sw/w+1)
			var r, g, bl, a, n uint64
			for y := range y1 - y0 {
				row := rows.Pix[y*rows.Stride:]
				for x := x0; x < x1; x++ {
					p := row[x*4 : x*4+4]
					r += uint64(p[0])
					g += uint64(p[1])
					bl += uint64(p[2])
					a += uint64(p[3])
					n++
				}
			}
			o := dst.Pix[dy*dst.Stride+dx*4:]
			// An average of bytes is a byte.
			o[0] = byteOf(r / n)
			o[1] = byteOf(g / n)
			o[2] = byteOf(bl / n)
			o[3] = byteOf(a / n)
		}
	}
	return dst
}

func byteOf(v uint64) uint8 {
	return uint8(min(v, 255))
}
