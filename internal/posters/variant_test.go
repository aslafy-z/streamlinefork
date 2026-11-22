package posters

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("poster variants", Label("unit", "posters"), func() {
	var svc Manager

	jpegOf := func(w, h int) []byte {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := range h {
			for x := range w {
				img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 90, A: 255})
			}
		}
		var buf bytes.Buffer
		Expect(jpeg.Encode(&buf, img, nil)).To(Succeed())
		return buf.Bytes()
	}

	serve := func(kind string, id uint32, query string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(
			http.MethodGet,
			"/posters/x/1/poster.jpg"+query,
			nil,
		)
		svc.Serve(rec, req, kind, id)
		return rec
	}

	BeforeEach(func() {
		var err error
		svc, err = New(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
	})

	It("serves a poster scaled to the asked width, keeping its proportions", func() {
		Expect(
			svc.Put(
				context.Background(),
				"movies",
				1,
				bytes.NewReader(jpegOf(780, 1170)),
			),
		).
			To(Succeed())

		rec := serve("movies", 1, "?w=320")

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get("Content-Type")).To(Equal("image/jpeg"))
		Expect(rec.Header().Get("Cache-Control")).To(ContainSubstring("max-age="))
		Expect(rec.Header().Get("Last-Modified")).NotTo(BeEmpty())
		cfg, err := jpeg.DecodeConfig(rec.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Width).To(Equal(320))
		Expect(cfg.Height).To(Equal(480))
	})

	It("keeps the variant beside the poster and reuses it", func() {
		Expect(
			svc.Put(
				context.Background(),
				"movies",
				1,
				bytes.NewReader(jpegOf(780, 1170)),
			),
		).
			To(Succeed())
		serve("movies", 1, "?w=200")

		path := variantPath(svc.Path("movies", 1), 200)
		Expect(os.WriteFile(path, []byte("kept"), 0o600)).To(Succeed())
		orig, err := os.Stat(svc.Path("movies", 1))
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Chtimes(path, orig.ModTime(), orig.ModTime())).To(Succeed())

		Expect(serve("movies", 1, "?w=200").Body.String()).To(Equal("kept"))
	})

	It("rebuilds a variant once the poster is replaced", func() {
		ctx := context.Background()
		Expect(
			svc.Put(ctx, "albums", 2, bytes.NewReader(jpegOf(500, 500))),
		).To(Succeed())
		serve("albums", 2, "?w=120")

		Expect(
			svc.Put(ctx, "albums", 2, bytes.NewReader(jpegOf(600, 300))),
		).To(Succeed())
		later := time.Now().Add(time.Hour)
		Expect(os.Chtimes(svc.Path("albums", 2), later, later)).To(Succeed())

		cfg, err := jpeg.DecodeConfig(serve("albums", 2, "?w=120").Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Height).To(Equal(60))
	})

	It("serves the poster itself when it is no wider than the asked width", func() {
		body := jpegOf(300, 450)
		Expect(
			svc.Put(context.Background(), "books", 3, bytes.NewReader(body)),
		).To(Succeed())

		rec := serve("books", 3, "?w=320")

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.Bytes()).To(Equal(body))
	})

	It("serves the poster itself when it cannot be decoded", func() {
		Expect(
			svc.Put(
				context.Background(),
				"movies",
				4,
				strings.NewReader("not an image"),
			),
		).
			To(Succeed())

		rec := serve("movies", 4, "?w=320")

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(Equal("not an image"))
	})

	It("serves the poster itself when it has transparency", func() {
		img := image.NewNRGBA(image.Rect(0, 0, 400, 400))
		var buf bytes.Buffer
		Expect(png.Encode(&buf, img)).To(Succeed())
		Expect(
			svc.Put(
				context.Background(),
				"artists",
				5,
				bytes.NewReader(buf.Bytes()),
			),
		).
			To(Succeed())

		rec := serve("artists", 5, "?w=200")

		Expect(rec.Header().Get("Content-Type")).To(Equal("image/png"))
		Expect(rec.Body.Bytes()).To(Equal(buf.Bytes()))
	})

	It("rounds a width between rungs up to the next one", func() {
		Expect(
			svc.Put(
				context.Background(),
				"movies",
				6,
				bytes.NewReader(jpegOf(780, 1170)),
			),
		).
			To(Succeed())

		cfg, err := jpeg.DecodeConfig(serve("movies", 6, "?w=321").Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Width).To(Equal(480))
		_, err = os.Stat(variantPath(svc.Path("movies", 6), 321))
		Expect(os.IsNotExist(err)).To(BeTrue())
	})

	It("serves the poster itself for a width past the widest rung", func() {
		body := jpegOf(780, 1170)
		Expect(
			svc.Put(context.Background(), "movies", 8, bytes.NewReader(body)),
		).To(Succeed())

		rec := serve("movies", 8, "?w=99999")

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.Bytes()).To(Equal(body))
	})

	It("refuses a width that is not a positive integer", func() {
		Expect(
			svc.Put(
				context.Background(),
				"movies",
				6,
				bytes.NewReader(jpegOf(780, 1170)),
			),
		).
			To(Succeed())

		for _, q := range []string{"?w=0", "?w=-200", "?w=abc", "?w=1.5"} {
			Expect(serve("movies", 6, q).Code).To(Equal(http.StatusBadRequest), q)
		}
		entries, err := os.ReadDir(
			strings.TrimSuffix(svc.Path("movies", 6), "poster.jpg"),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1))
	})

	It("downscales band by band exactly as from a full copy", func() {
		src := image.NewYCbCr(
			image.Rect(3, 5, 3+517, 5+771),
			image.YCbCrSubsampleRatio420,
		)
		for i := range src.Y {
			src.Y[i] = uint8(i * 7)
		}
		for i := range src.Cb {
			src.Cb[i], src.Cr[i] = uint8(i*3), uint8(i*5)
		}
		full := image.NewRGBA(image.Rect(0, 0, 517, 771))
		draw.Draw(full, full.Bounds(), src, src.Bounds().Min, draw.Src)

		for _, size := range [][2]int{{200, 298}, {320, 477}, {1, 1}, {517, 771}} {
			Expect(Downscale(src, size[0], size[1]).Pix).
				To(Equal(Downscale(full, size[0], size[1]).Pix), "%v", size)
		}
	})

	It("serves the poster itself when its EXIF turns it", func() {
		body := withSegment(jpegOf(780, 1170), exifSegment(binary.BigEndian, 6))
		Expect(
			svc.Put(context.Background(), "artists", 9, bytes.NewReader(body)),
		).To(Succeed())

		Expect(serve("artists", 9, "?w=320").Body.Bytes()).To(Equal(body))
	})

	It("scales a poster whose EXIF orientation is upright", func() {
		body := withSegment(jpegOf(780, 1170), exifSegment(binary.LittleEndian, 1))
		Expect(
			svc.Put(context.Background(), "artists", 10, bytes.NewReader(body)),
		).To(Succeed())

		cfg, err := jpeg.DecodeConfig(serve("artists", 10, "?w=320").Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Width).To(Equal(320))
	})

	It("keeps the poster's ICC profile in the variant", func() {
		profile := append(
			[]byte("ICC_PROFILE\x00\x01\x01"),
			bytes.Repeat([]byte{7}, 300)...)
		seg := segment(0xE2, profile)
		Expect(
			svc.Put(
				context.Background(),
				"movies",
				11,
				bytes.NewReader(withSegment(jpegOf(780, 1170), seg)),
			),
		).To(Succeed())

		out := serve("movies", 11, "?w=320").Body.Bytes()
		Expect(out[:2+len(seg)]).To(Equal(append([]byte{0xFF, 0xD8}, seg...)))
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(out))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Width).To(Equal(320))
	})

	It("decodes a poster it serves as it is only once", func() {
		img := image.NewNRGBA(image.Rect(0, 0, 400, 400))
		var buf bytes.Buffer
		Expect(png.Encode(&buf, img)).To(Succeed())
		Expect(
			svc.Put(
				context.Background(),
				"artists",
				12,
				bytes.NewReader(buf.Bytes()),
			),
		).To(Succeed())
		path := svc.Path("artists", 12)
		st, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(servedAsIs.has(path, st)).To(BeFalse())

		serve("artists", 12, "?w=200")

		Expect(servedAsIs.has(path, st)).To(BeTrue())
		Expect(serve("artists", 12, "?w=120").Body.Bytes()).To(Equal(buf.Bytes()))
	})

	It("drops a poster's variants when it is replaced", func() {
		ctx := context.Background()
		Expect(
			svc.Put(ctx, "movies", 13, bytes.NewReader(jpegOf(780, 1170))),
		).To(Succeed())
		serve("movies", 13, "?w=200")
		path := variantPath(svc.Path("movies", 13), 200)
		Expect(path).To(BeAnExistingFile())

		Expect(
			svc.Put(ctx, "movies", 13, bytes.NewReader(jpegOf(600, 900))),
		).To(Succeed())

		Expect(path).NotTo(BeAnExistingFile())
	})

	It("serves the poster with a short cache life when its variant fails", func() {
		body := jpegOf(780, 1170)
		Expect(
			svc.Put(context.Background(), "movies", 14, bytes.NewReader(body)),
		).To(Succeed())
		// A directory where the variant goes makes the rename fail.
		Expect(
			os.Mkdir(variantPath(svc.Path("movies", 14), 320), 0o750),
		).To(Succeed())

		rec := serve("movies", 14, "?w=320")

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.Bytes()).To(Equal(body))
		Expect(rec.Header().Get("Cache-Control")).To(Equal("public, max-age=300"))
	})

	It("lists the same widths as the SPA", func() {
		ts, err := os.ReadFile("../../web/app/lib/posters.ts")
		Expect(err).NotTo(HaveOccurred())
		m := regexp.MustCompile(`POSTER_WIDTHS = \[([^\]]*)\]`).FindSubmatch(ts)
		Expect(m).NotTo(BeNil())
		spa := make([]int, 0, len(variantWidths))
		for f := range strings.SplitSeq(string(m[1]), ",") {
			n, err := strconv.Atoi(strings.TrimSpace(f))
			Expect(err).NotTo(HaveOccurred())
			spa = append(spa, n)
		}
		Expect(spa).To(Equal(variantWidths))
	})

	It("answers 404 for a width of a poster that is not cached", func() {
		Expect(serve("movies", 404, "?w=320").Code).To(Equal(http.StatusNotFound))
	})

	It("ignores the retry parameter the SPA adds", func() {
		body := jpegOf(100, 150)
		Expect(
			svc.Put(context.Background(), "movies", 7, bytes.NewReader(body)),
		).To(Succeed())

		Expect(serve("movies", 7, "?retry=2").Body.Bytes()).To(Equal(body))
	})
})

// withSegment inserts a marker segment right after a JPEG's SOI.
func withSegment(j, seg []byte) []byte {
	out := append([]byte{}, j[:2]...)
	out = append(out, seg...)
	return append(out, j[2:]...)
}

// exifSegment is an APP1 segment holding one IFD with only an orientation.
func exifSegment(bo binary.ByteOrder, orientation uint16) []byte {
	tiff := make([]byte, 8+2+12+4)
	if bo == binary.LittleEndian {
		copy(tiff, "II")
	} else {
		copy(tiff, "MM")
	}
	bo.PutUint16(tiff[2:], 42)
	bo.PutUint32(tiff[4:], 8)
	bo.PutUint16(tiff[8:], 1)
	bo.PutUint16(tiff[10:], 0x0112)
	bo.PutUint16(tiff[12:], 3)
	bo.PutUint32(tiff[14:], 1)
	bo.PutUint16(tiff[18:], orientation)
	body := append([]byte("Exif\x00\x00"), tiff...)
	return segment(0xE1, body)
}

// segment frames a JPEG marker segment around its body.
func segment(marker byte, body []byte) []byte {
	//nolint:gosec // a test segment is far below the 64 KiB a length can say
	n := uint16(len(body) + 2)
	out := binary.BigEndian.AppendUint16([]byte{0xFF, marker}, n)
	return append(out, body...)
}
