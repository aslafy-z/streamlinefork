package posters

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"io"
)

// jpegMeta walks the segments ahead of a JPEG's image data and returns its
// EXIF orientation (0 without one) and every ICC profile segment, whole,
// marker included. A truncated or malformed header ends the walk with what
// was read so far.
func jpegMeta(b []byte) (orientation int, icc [][]byte) {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 0, nil
	}
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF {
			return orientation, icc
		}
		m := b[i+1]
		switch {
		case m == 0xFF: // fill byte
			i++
			continue
		case m == 0x01 || (m >= 0xD0 && m <= 0xD8): // no length
			i += 2
			continue
		case m == 0xDA || m == 0xD9: // image data, or the end
			return orientation, icc
		}
		n := int(binary.BigEndian.Uint16(b[i+2:]))
		if n < 2 || i+2+n > len(b) {
			return orientation, icc
		}
		seg := b[i+4 : i+2+n]
		switch {
		case m == 0xE1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")):
			orientation = exifOrientation(seg[6:])
		case m == 0xE2 && bytes.HasPrefix(seg, []byte("ICC_PROFILE\x00")):
			icc = append(icc, b[i:i+2+n])
		}
		i += 2 + n
	}
	return orientation, icc
}

// exifOrientation reads tag 0x0112 from a TIFF block's first IFD, 0 when it
// is not there.
func exifOrientation(t []byte) int {
	if len(t) < 8 {
		return 0
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	if bo.Uint16(t[2:]) != 42 {
		return 0
	}
	off := int(bo.Uint32(t[4:]))
	if off < 8 || off+2 > len(t) {
		return 0
	}
	for k := range int(bo.Uint16(t[off:])) {
		e := off + 2 + 12*k
		if e+12 > len(t) {
			return 0
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			// A SHORT sits at the start of the value field in either order.
			return int(bo.Uint16(t[e+8:]))
		}
	}
	return 0
}

// maxICCBytes bounds the profile read out of a PNG's iCCP chunk; real
// profiles are a few kilobytes, rarely more than a few hundred.
const maxICCBytes = 1 << 20

// iccSegmentData is how much of a profile one APP2 segment carries: a
// segment's 65535 bytes less its length, the "ICC_PROFILE\0" tag and the
// sequence bytes.
const iccSegmentData = 65535 - 2 - 12 - 2

// pngICC reads a PNG's iCCP chunk and returns its profile as the APP2
// segments a JPEG carries it in, or nil when there is none or it cannot be
// read. The chunk must come before the image data, which is all head holds.
func pngICC(head []byte) [][]byte {
	const sig = "\x89PNG\r\n\x1a\n"
	if !bytes.HasPrefix(head, []byte(sig)) {
		return nil
	}
	for i := len(sig); i+8 <= len(head); {
		n := int(binary.BigEndian.Uint32(head[i:]))
		typ := string(head[i+4 : i+8])
		if n < 0 || i+12+n > len(head) || typ == "IDAT" {
			return nil
		}
		if typ == "iCCP" {
			return iccSegments(inflateICC(head[i+8 : i+8+n]))
		}
		i += 12 + n
	}
	return nil
}

// inflateICC decompresses an iCCP chunk's body: a name, a NUL, a compression
// method (0, zlib) and the compressed profile.
func inflateICC(chunk []byte) []byte {
	nul := bytes.IndexByte(chunk, 0)
	if nul < 0 || nul+2 > len(chunk) || chunk[nul+1] != 0 {
		return nil
	}
	r, err := zlib.NewReader(bytes.NewReader(chunk[nul+2:]))
	if err != nil {
		return nil
	}
	defer r.Close()
	profile, err := io.ReadAll(io.LimitReader(r, maxICCBytes+1))
	if err != nil || len(profile) > maxICCBytes {
		return nil
	}
	return profile
}

// iccSegments splits a profile into numbered APP2 segments.
func iccSegments(profile []byte) [][]byte {
	if len(profile) == 0 {
		return nil
	}
	count := (len(profile) + iccSegmentData - 1) / iccSegmentData
	if count > 255 {
		return nil
	}
	segs := make([][]byte, 0, count)
	for k := range count {
		part := profile[k*iccSegmentData : min(len(profile), (k+1)*iccSegmentData)]
		seg := make([]byte, 0, 4+12+2+len(part))
		seg = append(seg, 0xFF, 0xE2)
		//nolint:gosec // at most 65535, by iccSegmentData
		n := uint16(2 + 12 + 2 + len(part))
		seg = binary.BigEndian.AppendUint16(seg, n)
		seg = append(seg, "ICC_PROFILE\x00"...)
		seg = append(seg, byte(k+1), byte(count))
		segs = append(segs, append(seg, part...))
	}
	return segs
}
