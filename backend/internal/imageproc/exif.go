package imageproc

import (
	"encoding/binary"
	"image"
)

// Minimal EXIF support: JPEGs keep their camera orientation in an EXIF tag,
// but no decoder in the pipeline applies it — and the re-encoded JPEGs this
// package produces carry no EXIF at all, so the orientation must be baked
// into the pixels before downscaling. TIFF's own orientation tag and WebP
// EXIF chunks stay unhandled (documented limitation).

// jpegOrientation reads the EXIF orientation value (1..8, 0 when absent or
// unreadable) from a JPEG's APP1 segment.
func jpegOrientation(data []byte) int {
	// Walk the JPEG marker segments to the APP1 Exif block.
	pos := 2 // past SOI
	for pos+4 <= len(data) {
		if data[pos] != 0xFF {
			return 0
		}
		marker := data[pos+1]
		if marker == 0xE1 && pos+10 <= len(data) && string(data[pos+4:pos+10]) == "Exif\x00\x00" {
			return tiffOrientation(data[pos+10:])
		}
		switch {
		case marker == 0xD8, marker == 0x01,
			marker >= 0xD0 && marker <= 0xD7: // standalone / restart markers
			pos += 2
			continue
		case marker == 0xDA, marker == 0xD9: // scan data / EOI: no EXIF past here
			return 0
		default:
			if pos+4 > len(data) {
				return 0
			}
			pos += 2 + int(binary.BigEndian.Uint16(data[pos+2:]))
		}
	}
	return 0
}

// tiffOrientation reads tag 0x0112 (Orientation) from IFD0 of the Exif TIFF
// block (which starts right after the "Exif\0\0" header).
func tiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 0
	}
	var byteOrder binary.ByteOrder
	switch string(tiff[0:2]) {
	case "II":
		byteOrder = binary.LittleEndian
	case "MM":
		byteOrder = binary.BigEndian
	default:
		return 0
	}
	ifd0 := int(byteOrder.Uint32(tiff[4:8]))
	if ifd0 < 8 || ifd0+2 > len(tiff) {
		return 0
	}
	count := int(byteOrder.Uint16(tiff[ifd0:]))
	entry := ifd0 + 2
	if entry+count*12 > len(tiff) {
		return 0
	}
	for i := 0; i < count; i++ {
		e := entry + i*12
		if byteOrder.Uint16(tiff[e:]) == 0x0112 {
			// Orientation is a SHORT: the value sits inline as the field's
			// first two bytes, in TIFF byte order.
			return int(byteOrder.Uint16(tiff[e+8:]))
		}
	}
	return 0
}

// orient bakes the EXIF orientation into the pixels; anything outside 1..8 is
// treated as un-oriented.
func orient(img image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	sb := img.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	var dw, dh int
	var srcX, srcY func(x, y int) int
	switch o {
	case 2: // flip horizontal
		dw, dh = sw, sh
		srcX = func(x, y int) int { return sw - 1 - x }
		srcY = func(x, y int) int { return y }
	case 3: // rotate 180°
		dw, dh = sw, sh
		srcX = func(x, y int) int { return sw - 1 - x }
		srcY = func(x, y int) int { return sh - 1 - y }
	case 4: // flip vertical
		dw, dh = sw, sh
		srcX = func(x, y int) int { return x }
		srcY = func(x, y int) int { return sh - 1 - y }
	case 5: // transpose (flip over the main diagonal)
		dw, dh = sh, sw
		srcX = func(x, y int) int { return y }
		srcY = func(x, y int) int { return x }
	case 6: // rotate 90° clockwise
		dw, dh = sh, sw
		srcX = func(x, y int) int { return y }
		srcY = func(x, y int) int { return sh - 1 - x }
	case 7: // transverse (flip over the anti-diagonal)
		dw, dh = sh, sw
		srcX = func(x, y int) int { return sw - 1 - y }
		srcY = func(x, y int) int { return sh - 1 - x }
	case 8: // rotate 90° counter-clockwise
		dw, dh = sh, sw
		srcX = func(x, y int) int { return sw - 1 - y }
		srcY = func(x, y int) int { return x }
	default:
		return img
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			dst.Set(x, y, img.At(sb.Min.X+srcX(x, y), sb.Min.Y+srcY(x, y)))
		}
	}
	return dst
}
