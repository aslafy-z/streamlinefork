package posters

import (
	"bytes"
	"encoding/binary"
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
