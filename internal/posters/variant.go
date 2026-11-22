package posters

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // registers the PNG decoder for image.Decode
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/datahearth/streamline/internal/otelx"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"
)

// variantWidths is every width a poster is served at besides its own. A
// browser reducing a poster by four or five times in one pass draws it
// aliased, so the SPA asks for the rung about twice the size it displays and
// leaves the browser a reduction it filters well. The ladder is closed: an
// asked width is rounded up to a rung, so a client cannot fill the data volume
// one pixel at a time, and a SPA holding another list still gets a poster.
// web/app/lib/posters.ts holds the same list.
var variantWidths = []int{120, 200, 320, 480, 640}

const (
	// maxVariantSourcePixels is the largest poster scaled; a larger one is
	// served as it is. Decoding holds the whole source: three to eight bytes a
	// pixel for most images, and about fourteen for a progressive JPEG, whose
	// decoder keeps every coefficient.
	maxVariantSourcePixels = 10_000_000

	// resizeBudget is how many source pixels are decoded at once, across every
	// resize in flight. Each resize weighs at least resizeBudget/resizeSlots,
	// which also bounds how many run together.
	resizeBudget = 12_000_000

	variantJPEGQuality = 85

	// headerBytes is how much of a poster is read to decide whether it can be
	// scaled: its size, format, EXIF orientation and ICC profile all come
	// before the image data.
	headerBytes = 1 << 20
)

var (
	resizeSlots = int64(min(4, max(1, runtime.GOMAXPROCS(0)/2)))
	resizing    = semaphore.NewWeighted(resizeBudget)

	// builds makes concurrent requests for one variant share one resize: a page
	// of cards asks for every poster at once, and a reload asks again before
	// the first build is done.
	builds singleflight.Group

	// servedAsIs remembers originals that were decoded and found not to scale
	// (transparency, a decode error), keyed on their modification time and
	// size, so each is decoded once rather than on every request.
	servedAsIs = asIsMemo{m: map[string]fileStamp{}}
)

// variantWidth reads the `w` query parameter as a rung of variantWidths: the
// smallest one at least as wide as asked, 0 (the poster itself) without one or
// past the widest, and an error for a value that is not a positive integer.
func variantWidth(q url.Values) (int, error) {
	raw := q.Get("w")
	if raw == "" {
		return 0, nil
	}
	asked, err := strconv.Atoi(raw)
	if err != nil || asked <= 0 {
		return 0, fmt.Errorf("posters: unsupported width %q", raw)
	}
	// The rung is returned, never the parsed value: nothing a request sends
	// reaches a file name.
	for _, w := range variantWidths {
		if w >= asked {
			return w, nil
		}
	}
	return 0, nil
}

func variantPath(orig string, w int) string {
	return filepath.Join(filepath.Dir(orig), "poster.w"+strconv.Itoa(w)+".jpg")
}

// removeVariants drops every scaled copy beside a poster, best effort: a copy
// left behind is still rebuilt, since its modification time no longer matches.
func removeVariants(orig string) {
	paths, _ := filepath.Glob(filepath.Join(filepath.Dir(orig), "poster.w*.jpg"))
	for _, p := range paths {
		_ = os.Remove(p)
	}
}

// variant returns the path of orig scaled down to w pixels wide, creating it
// on first use, or "" when the original is to be served as it is: it is
// already that narrow as shown, it is not a JPEG or PNG (a GIF keeps its
// animation), it is larger than maxVariantSourcePixels, or it has transparency
// a JPEG would lose. A JPEG's EXIF orientation is applied to the copy, which
// carries none. A variant carries its original's modification time, and one
// that does not is rebuilt.
func variant(ctx context.Context, orig string, w int) (string, error) {
	st, err := os.Stat(orig)
	if err != nil {
		return "", err
	}
	dst := variantPath(orig, w)
	if vst, err := os.Stat(dst); err == nil && vst.Mode().IsRegular() &&
		vst.Size() > 0 && vst.ModTime().Equal(st.ModTime()) {
		return dst, nil
	}
	if servedAsIs.has(orig, st) {
		return "", nil
	}

	hdr, err := readHeader(orig)
	if err != nil {
		if errors.Is(err, errNotScalable) {
			slog.DebugContext(ctx, "poster not resizable",
				"cache.path", orig, "error", err)
			return "", nil
		}
		return "", err
	}
	shownW, _ := hdr.shown()
	if shownW <= w || hdr.cfg.Width*hdr.cfg.Height > maxVariantSourcePixels ||
		hdr.orientation > 8 {
		return "", nil
	}

	// The build outlives a caller that hangs up, so the next request finds
	// the variant made; the caller still stops waiting for it.
	ch := builds.DoChan(dst, func() (any, error) {
		return build(context.WithoutCancel(ctx), orig, dst, st, hdr, w)
	})
	select {
	case r := <-ch:
		if r.Err != nil {
			return "", r.Err
		}
		return r.Val.(string), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func build(
	ctx context.Context,
	orig, dst string,
	st os.FileInfo,
	hdr header,
	w int,
) (string, error) {
	ctx, span := tracer.Start(ctx, "posters.variant")
	defer span.End()
	px := hdr.cfg.Width * hdr.cfg.Height
	span.SetAttributes(
		attribute.Int("poster.width", w),
		attribute.Int("poster.source.width", hdr.cfg.Width),
		attribute.Int("poster.source.height", hdr.cfg.Height),
	)

	weight := max(int64(px), resizeBudget/resizeSlots)
	if err := resizing.Acquire(ctx, weight); err != nil {
		return "", otelx.RecordSpanError(span, err)
	}
	defer resizing.Release(weight)

	f, err := os.Open(filepath.Clean(orig))
	if err != nil {
		return "", otelx.RecordSpanError(span, err)
	}
	src, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		servedAsIs.add(orig, st)
		return "", otelx.RecordSpanError(span, fmt.Errorf("decode poster: %w", err))
	}
	if o, ok := src.(interface{ Opaque() bool }); ok && !o.Opaque() {
		servedAsIs.add(orig, st)
		span.SetAttributes(attribute.String("outcome", "transparent"))
		return "", nil
	}

	shownW, shownH := hdr.shown()
	h := max(1, (shownH*w+shownW/2)/shownW)
	// Scaled as stored, then turned upright: the copy carries no EXIF, and
	// turning the small image costs nothing.
	sw, sh := w, h
	if hdr.orientation >= 5 {
		sw, sh = h, w
	}
	var enc bytes.Buffer
	if err := jpeg.Encode(
		&enc,
		orient(resample(src, sw, sh), hdr.orientation),
		&jpeg.Options{Quality: variantJPEGQuality},
	); err != nil {
		return "", otelx.RecordSpanError(
			span,
			fmt.Errorf("encode poster variant: %w", err),
		)
	}
	if err := writeVariant(dst, enc.Bytes(), hdr.icc, st.ModTime()); err != nil {
		return "", otelx.RecordSpanError(span, err)
	}
	// A Remove that ran during the build could not take the directory, which
	// held the copy being written; the copy and the directory go now.
	if _, err := os.Stat(orig); errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(dst)
		_ = os.Remove(filepath.Dir(dst))
		return "", otelx.RecordSpanError(span, err)
	}
	span.SetAttributes(attribute.String("outcome", "scaled"))
	return dst, nil
}

// writeVariant writes the encoded copy beside its original, with the
// original's ICC profile so its colours read the same, and stamps it with the
// original's modification time.
func writeVariant(dst string, enc []byte, icc [][]byte, mtime time.Time) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), "poster-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	// The encoder's output starts with SOI; the profile's APP2 segments go
	// right after it.
	if _, err := tmp.Write(enc[:2]); err != nil {
		return fail(err)
	}
	for _, seg := range icc {
		if _, err := tmp.Write(seg); err != nil {
			return fail(err)
		}
	}
	if _, err := tmp.Write(enc[2:]); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chtimes(tmpName, mtime, mtime); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("stamp poster variant: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

var errNotScalable = errors.New("posters: not a JPEG or PNG")

// header is what a poster's first bytes say about it.
type header struct {
	cfg image.Config
	// orientation is the JPEG's EXIF orientation, 0 without one.
	orientation int
	// icc holds the ICC profile as JPEG APP2 segments, each whole, marker
	// included: a JPEG's own, or a PNG's iCCP chunk split into them.
	icc [][]byte
}

// shown is the poster's size as a browser draws it, after its orientation.
func (h header) shown() (int, int) {
	if h.orientation >= 5 && h.orientation <= 8 {
		return h.cfg.Height, h.cfg.Width
	}
	return h.cfg.Width, h.cfg.Height
}

func readHeader(orig string) (header, error) {
	f, err := os.Open(filepath.Clean(orig))
	if err != nil {
		return header{}, err
	}
	defer f.Close()
	head, err := io.ReadAll(io.LimitReader(f, headerBytes))
	if err != nil {
		return header{}, err
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(head))
	if err != nil {
		return header{}, fmt.Errorf("%w: %w", errNotScalable, err)
	}
	hdr := header{cfg: cfg}
	switch format {
	case "jpeg":
		hdr.orientation, hdr.icc = jpegMeta(head)
	case "png":
		hdr.icc = pngICC(head)
	default:
		return header{}, fmt.Errorf("%w: %s", errNotScalable, format)
	}
	return hdr, nil
}

type fileStamp struct {
	mtime time.Time
	size  int64
}

// asIsMemo is a bounded set of originals, each valid while the file keeps its
// modification time and size.
type asIsMemo struct {
	mu sync.Mutex
	m  map[string]fileStamp
}

const asIsMemoMax = 4096

func (a *asIsMemo) has(path string, st os.FileInfo) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.m[path]
	return ok && s.size == st.Size() && s.mtime.Equal(st.ModTime())
}

func (a *asIsMemo) add(path string, st os.FileInfo) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.m) >= asIsMemoMax {
		clear(a.m)
	}
	a.m[path] = fileStamp{mtime: st.ModTime(), size: st.Size()}
}
