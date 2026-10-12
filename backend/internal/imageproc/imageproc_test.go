package imageproc

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	xbmp "golang.org/x/image/bmp"
	xtiff "golang.org/x/image/tiff"
)

// All fixtures are generated in-test: real format bytes, no binary files.

func mustJPEG(w, h int, paint func(x, y int) color.Color) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, paint(x, y))
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func mustJPEGFill(w, h int, c color.Color) []byte {
	return mustJPEG(w, h, func(x, y int) color.Color { return c })
}

// process runs a conversion and requires exactly one result.
func process(t *testing.T, data []byte, declared string, opts Options) ProcessedImage {
	t.Helper()
	outs, err := Process(context.Background(), data, declared, opts)
	if err != nil {
		t.Fatalf("Process(%s): %v", declared, err)
	}
	if len(outs) != 1 {
		t.Fatalf("Process(%s): got %d results, want 1", declared, len(outs))
	}
	return outs[0]
}

func mustDecode(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("result is not a decodable image: %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("result format %q, want jpeg", format)
	}
	return img
}

func dims(img image.Image) (int, int) {
	b := img.Bounds()
	return b.Dx(), b.Dy()
}

// --- raster conversions -----------------------------------------------------

func TestTinyJPEGPassesThroughByteIdentical(t *testing.T) {
	src := mustJPEGFill(40, 30, color.RGBA{200, 50, 50, 255})
	out := process(t, src, "image/jpeg", ReceiptOpts)
	if out.Converted || !bytes.Equal(out.Data, src) || out.MimeType != "image/jpeg" {
		t.Fatalf("small JPEG must pass through byte-identical: converted=%v equal=%v mime=%s",
			out.Converted, bytes.Equal(out.Data, src), out.MimeType)
	}
}

func TestOversizedJPEGIsDownscaled(t *testing.T) {
	src := mustJPEGFill(3000, 2000, color.RGBA{10, 60, 10, 255})
	out := process(t, src, "image/jpeg", ReceiptOpts)
	if !out.Converted {
		t.Fatalf("oversized JPEG must be re-encoded")
	}
	img := mustDecode(t, out.Data)
	w, h := dims(img)
	if w != 2048 || h < 1360 || h > 1367 {
		t.Fatalf("downscaled dims %dx%d, want ~2048x1365", w, h)
	}
}

func TestTinyJPEGWithEXIFOrientationIsReencoded(t *testing.T) {
	src := jpegWithEXIF(t, mustJPEGFill(40, 30, color.RGBA{1, 2, 3, 255}), 6)
	out := process(t, src, "image/jpeg", ReceiptOpts)
	if !out.Converted {
		t.Fatalf("EXIF-oriented JPEG must be re-encoded (EXIF cannot survive)")
	}
	mustDecode(t, out.Data)
}

func TestPNGTransparentBecomesWhiteJPEG(t *testing.T) {
	// A PNG whose every pixel is fully transparent must not encode black.
	src := &image.NRGBA{Pix: make([]uint8, 40*30*4), Stride: 40 * 4, Rect: image.Rect(0, 0, 40, 30)}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	out := process(t, buf.Bytes(), "image/png", ReceiptOpts)
	if !out.Converted || out.MimeType != "image/jpeg" {
		t.Fatalf("PNG must convert to JPEG")
	}
	img := mustDecode(t, out.Data)
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r < 60000 || g < 60000 || bl < 60000 {
				t.Fatalf("transparent pixel (%d,%d) did not composite white: %v", x, y, img.At(x, y))
			}
		}
	}
}

func TestGIFConvertsFirstFrame(t *testing.T) {
	// Two frames: first red, second blue. Only the first survives.
	palette := []color.Color{color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}}
	f1 := image.NewPaletted(image.Rect(0, 0, 32, 20), color.Palette(palette))
	f2 := image.NewPaletted(image.Rect(0, 0, 32, 20), color.Palette(palette))
	for y := 0; y < 20; y++ {
		for x := 0; x < 32; x++ {
			f1.Set(x, y, palette[0])
			f2.Set(x, y, palette[1])
		}
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, &gif.GIF{Image: []*image.Paletted{f1, f2}, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	src := buf.Bytes()
	out := process(t, src, "image/gif", ReceiptOpts)
	if !out.Converted || out.MimeType != "image/jpeg" {
		t.Fatalf("GIF must convert to JPEG")
	}
	img := mustDecode(t, out.Data)
	w, h := dims(img)
	if w != 32 || h != 20 {
		t.Fatalf("dims %dx%d, want 32x20", w, h)
	}
	r, g, bl, _ := img.At(5, 5).RGBA()
	if r+15000 < g || r+15000 < bl {
		t.Fatalf("first frame lost: %v", img.At(5, 5))
	}
}

func TestTIFFAndBMPConvert(t *testing.T) {
	img32 := image.NewRGBA(image.Rect(0, 0, 30, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 30; x++ {
			img32.Set(x, y, color.RGBA{40, 90, 140, 255})
		}
	}
	for name, make := range map[string]func() []byte{
		"tiff": func() []byte {
			var buf bytes.Buffer
			if err := xtiff.Encode(&buf, img32, nil); err != nil {
				t.Fatal(err)
			}
			return buf.Bytes()
		},
		"bmp": func() []byte {
			var buf bytes.Buffer
			if err := xbmp.Encode(&buf, img32); err != nil {
				t.Fatal(err)
			}
			return buf.Bytes()
		},
	} {
		t.Run(name, func(t *testing.T) {
			src := make()
			out := process(t, src, "image/"+name, ReceiptOpts)
			if !out.Converted || out.MimeType != "image/jpeg" {
				t.Fatalf("%s must convert to JPEG (legacy=false)", name)
			}
			img := mustDecode(t, out.Data)
			if w, h := dims(img); w != 30 || h != 20 {
				t.Fatalf("dims %dx%d, want 30x20", w, h)
			}
		})
	}
}

// --- failure and passthrough semantics --------------------------------------

func TestUndecodableLegacyFormatsPassThrough(t *testing.T) {
	for declared := range map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true} {
		src := []byte("not really a " + declared)
		out := process(t, src, declared, ReceiptOpts)
		if out.Converted || !bytes.Equal(out.Data, src) || out.MimeType != declared {
			t.Fatalf("%s garbage must pass through untouched, got converted=%v mime=%s", declared, out.Converted, out.MimeType)
		}
	}
}

func TestUndecodableNewFormatsAreRejected(t *testing.T) {
	for _, declared := range []string{"image/gif", "image/bmp", "image/tiff"} {
		src := []byte("not really a " + declared)
		if _, err := Process(context.Background(), src, declared, ReceiptOpts); err == nil {
			t.Fatalf("corrupt %s must be rejected", declared)
		}
	}
}

func TestPixelBudgetGuardsAgainstBombs(t *testing.T) {
	// BMP header declaring 12000x12000 with barely any pixel data.
	hdr := make([]byte, 54)
	hdr[0], hdr[1] = 'B', 'M'
	writeLE := func(off int, v uint32) {
		hdr[off], hdr[off+1], hdr[off+2], hdr[off+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
	}
	writeLE(2, uint32(54+12000*12000*3)) // claimed file length, real data absent
	writeLE(10, 54)
	writeLE(14, 40)
	writeLE(18, 12000)
	writeLE(22, 12000)
	writeLE(26, 1)
	writeLE(28, 24)
	if _, err := Process(context.Background(), hdr, "image/bmp", ReceiptOpts); err == nil {
		t.Fatalf("decompression bomb must be rejected before decoding")
	}
}

func TestEmptyInputRejected(t *testing.T) {
	if _, err := Process(context.Background(), nil, "image/jpeg", ReceiptOpts); err == nil {
		t.Fatalf("empty input must be rejected")
	}
}

// --- EXIF orientation --------------------------------------------------------

// jpegWithEXIF inserts an APP1 Exif segment carrying the orientation into a
// plain JPEG.
func jpegWithEXIF(t *testing.T, jpegBytes []byte, orientation int) []byte {
	t.Helper()
	// TIFF block: "II", 42, IFD0 at offset 8, one entry, no next IFD.
	tif := []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0, // II, magic 42, IFD0@8, 1 entry
		0x12, 0x01, 3, 0, 1, 0, 0, 0, // tag 0x0112, type SHORT, count 1 (4 bytes)
		byte(orientation), byte(orientation >> 8), 0, 0, // value field: first two bytes = the SHORT
		0, 0, 0, 0} // next IFD = 0
	exif := append([]byte("Exif\x00\x00"), tif...)
	out := append([]byte{0xFF, 0xD8}, 0xFF, 0xE1)
	segLen := len(exif) + 2
	out = append(out, byte(segLen>>8), byte(segLen))
	out = append(out, exif...)
	return append(out, jpegBytes[2:]...)
}

func TestEXIFOrientationsBakeIntoPixels(t *testing.T) {
	// 16x8 source whose top-left quadrant (x<8, y<4 — big enough to survive
	// JPEG compression) is red; each orientation must move it to the position
	// derived from the EXIF meaning, and swap the dims for the 90° and
	// diagonal-flip cases.
	red := color.RGBA{255, 0, 0, 255}
	white := color.RGBA{255, 255, 255, 255}
	paint := func(x, y int) color.Color {
		if x < 8 && y < 4 {
			return red
		}
		return white
	}
	// expected: dims (dw, dh) and where the quadrant center src(2,2) lands.
	cases := map[int]struct{ dw, dh, rx, ry int }{
		1: {16, 8, 2, 2},
		2: {16, 8, 13, 2}, // flip horizontal
		3: {16, 8, 13, 5}, // rotate 180
		4: {16, 8, 2, 5},  // flip vertical
		5: {8, 16, 2, 2},  // transpose
		6: {8, 16, 5, 2},  // rotate 90° CW
		7: {8, 16, 5, 13}, // transverse
		8: {8, 16, 2, 13}, // rotate 90° CCW
	}
	for o, want := range cases {
		src := jpegWithEXIF(t, mustJPEG(16, 8, paint), o)
		out := process(t, src, "image/jpeg", ReceiptOpts)
		img := mustDecode(t, out.Data)
		w, h := dims(img)
		if w != want.dw || h != want.dh {
			t.Fatalf("orientation %d: dims %dx%d, want %dx%d", o, w, h, want.dw, want.dh)
		}
		r, g, b, _ := img.At(want.rx, want.ry).RGBA()
		if r+15000 < g || r+15000 < b {
			t.Fatalf("orientation %d: red quadrant center expected at (%d,%d), got %v", o, want.rx, want.ry, img.At(want.rx, want.ry))
		}
	}
}

// --- PDF ---------------------------------------------------------------------

type pdfImg struct {
	data []byte
	w, h int
}

// buildPDF assembles a minimal valid PDF around the given page images (a
// vector-only PDF when none are given).
func buildPDF(imgs ...pdfImg) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")
	var xref []int
	write := func(num int, text string) {
		xref = append(xref, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", num, text)
	}
	kids := make([]string, 0, len(imgs))
	for i := range imgs {
		kids = append(kids, fmt.Sprintf("%d 0 R", 4+3*i)) // page obj per image below
	}
	write(1, "<< /Type /Catalog /Pages 2 0 R >>")
	write(2, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", bytesJoin(kids, " "), len(imgs)))
	for i, im := range imgs {
		imgObjNum := 3 + 3*i
		pageObjNum := imgObjNum + 1
		contObjNum := imgObjNum + 2
		write(imgObjNum, fmt.Sprintf(
			"<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n%s\nendstream",
			im.w, im.h, len(im.data), im.data))
		write(pageObjNum, fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << /XObject << /Im0 %d 0 R >> >> /Contents %d 0 R >>",
			im.w, im.h, imgObjNum, contObjNum))
		content := fmt.Sprintf("q %d 0 0 %d 0 0 cm /Im0 Do Q", im.w, im.h)
		write(contObjNum, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	}
	if len(imgs) == 0 { // vector-only page: one page drawing a line
		write(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>")
		content := "q 1 G 10 10 m 190 190 l S Q"
		write(4, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	}
	xrefPos := buf.Len()
	buf.WriteString(fmt.Sprintf("xref\n0 %d\n", len(xref)+1))
	buf.WriteString("0000000000 65535 f \n")
	for _, off := range xref {
		buf.WriteString(fmt.Sprintf("%010d 00000 n \n", off))
	}
	buf.WriteString(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(xref)+1, xrefPos))
	return buf.Bytes()
}

func bytesJoin(parts []string, sep string) string {
	out := ""
	for i, s := range parts {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}

func TestScannedPDFConvertsEachPage(t *testing.T) {
	// Page 1 red-ish, page 2 blue-ish; conversion must yield two JPEGs in
	// page order.
	p1 := pdfImg{mustJPEGFill(120, 80, color.RGBA{200, 30, 30, 255}), 120, 80}
	p2 := pdfImg{mustJPEGFill(90, 60, color.RGBA{30, 30, 200, 255}), 90, 60}
	outs, err := Process(context.Background(), buildPDF(p1, p2), "application/pdf", ReceiptOpts)
	if err != nil {
		t.Fatalf("scanned PDF: %v", err)
	}
	if len(outs) != 2 {
		t.Fatalf("got %d page images, want 2", len(outs))
	}
	for _, o := range outs {
		if o.MimeType != "image/jpeg" || !o.Converted {
			t.Fatalf("page must convert to JPEG, got %s", o.MimeType)
		}
	}
	img1, img2 := mustDecode(t, outs[0].Data), mustDecode(t, outs[1].Data)
	if r, g, b, _ := img1.At(5, 5).RGBA(); r+15000 < g || r+15000 < b {
		t.Fatalf("page 1 lost: %v", img1.At(5, 5))
	}
	if r, g, b, _ := img2.At(5, 5).RGBA(); b+15000 < r || b+15000 < g {
		t.Fatalf("page 2 lost: %v", img2.At(5, 5))
	}
}

func TestVectorPDFPassesThrough(t *testing.T) {
	pdf := buildPDF() // no page images: only the (placeholder) catalog objects
	outs, err := Process(context.Background(), pdf, "application/pdf", ReceiptOpts)
	if err != nil {
		t.Fatalf("vector PDF: %v", err)
	}
	if len(outs) != 1 || outs[0].Converted || !bytes.Equal(outs[0].Data, pdf) || outs[0].MimeType != "application/pdf" {
		t.Fatalf("imageless PDF must pass through byte-identical: %+v", outs)
	}
}

func TestPhotoOptionsRejectPDF(t *testing.T) {
	pdf := buildPDF(pdfImg{mustJPEGFill(60, 40, color.Black), 60, 40})
	if _, err := Process(context.Background(), pdf, "application/pdf", PhotoOpts); err == nil {
		t.Fatalf("logos/photos must reject PDF uploads")
	}
}

// --- sniffing -----------------------------------------------------------------

func TestSniffMagicBytes(t *testing.T) {
	cases := map[string][]byte{
		"image/jpeg":      {0xFF, 0xD8, 0xFF, 0xE0},
		"image/png":       {0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A},
		"image/gif":       []byte("GIF89a..."),
		"image/webp":      append([]byte("RIFF\x00\x00\x00\x00"), []byte("WEBP")...),
		"image/tiff":      {0x49, 0x49, 0x2A, 0x00},
		"image/bmp":       {'B', 'M', 0, 0},
		"image/heic":      []byte("\x00\x00\x00 ftypheic\x00\x00\x00\x00"),
		"image/heif":      []byte("\x00\x00\x00 ftypmif1\x00\x00\x00\x00"),
		"application/pdf": []byte("%PDF-1.7 rest"),
	}
	for want, src := range cases {
		if got := Sniff(src); got != want {
			t.Fatalf("Sniff(% x) = %q, want %q", src[:min(8, len(src))], got, want)
		}
	}
	if got := Sniff([]byte("plain text")); got == "" {
		t.Fatalf("any input must sniff to something")
	}
}
