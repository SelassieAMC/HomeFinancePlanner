// Package imageproc normalizes uploaded files into the one image form the
// bill-scanning pipeline can always read: JPEG. Any common raster format
// (HEIC, PNG, GIF, WebP, TIFF, BMP, …) is decoded, EXIF-oriented, downscaled
// and re-encoded as JPEG; PDFs with scanned pages become one JPEG per page.
// The converted bytes are what gets stored on disk and handed to the AI
// extractors, so a normalized upload removes every per-provider format gate —
// only legacy persisted rows can still carry HEIC/PDF mime types. Decoding is
// pure Go throughout (HEIC via a WASM decoder, PDF image extraction via
// pdfcpu); no cgo, no external tools.
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
	"strings"

	"github.com/gen2brain/heic"
	xbmp "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	xtiff "golang.org/x/image/tiff"
	xwebp "golang.org/x/image/webp"
)

// Options tune one conversion run.
type Options struct {
	// MaxEdge caps the longest edge of every produced image: larger sources
	// are downscaled, smaller ones are never upscaled.
	MaxEdge int
	// AllowPDF lets PDF uploads through (scanned pages become JPEGs,
	// vector-only PDFs fall back to the original bytes); logos and product
	// photos never accept PDFs.
	AllowPDF bool
}

// ReceiptOpts match what the vision extractors can read off a printed
// receipt: text stays legible at ~2048 px and every supported model downscales
// internally long before, so anything larger is wasted storage.
var ReceiptOpts = Options{MaxEdge: 2048, AllowPDF: true}

// PhotoOpts suit product photos, which are shown at UI sizes.
var PhotoOpts = Options{MaxEdge: 1024}

// LogoOpts suit store logos shown in small chips.
var LogoOpts = Options{MaxEdge: 512}

// maxJPEGPassthroughBytes already-acceptable JPEGs below this size pass
// through byte-identical; larger ones are re-encoded (quality 85 routinely
// halves them with no reading loss).
const maxJPEGPassthroughBytes = 2 << 20

// maxDecodePixels caps decoded pixel allocation (~4 bytes per pixel): a
// receipt camera never needs more and a decompression bomb must not OOM the
// server. The guard runs on image.DecodeConfig values before any decode.
const maxDecodePixels = 40 << 20

// ProcessedImage is one conversion result ready for storage. Converted is
// false when the original bytes passed through untouched — either a JPEG that
// needed no work, or a legacy-format file that could not be converted (the
// pipeline accepted those formats before conversion existed; the extractors
// keep their per-provider support gates for such rows).
type ProcessedImage struct {
	Data      []byte
	MimeType  string
	Converted bool
}

// Process converts one uploaded file into 1..n normalized images — never
// zero. A multi-page scanned PDF becomes one JPEG per page, in page order.
// declaredMime is only consulted when the magic bytes decide nothing (or name
// an unsupported format); an actual sniffed format always wins, mirroring the
// upload validation, which sniffs for exactly that reason.
func Process(ctx context.Context, data []byte, declaredMime string, opts Options) ([]ProcessedImage, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("no file data to convert")
	}
	return convert(ctx, data, strings.ToLower(strings.TrimSpace(declaredMime)), opts)
}

func isSupportedFormat(mime string) bool {
	switch mime {
	case "application/pdf", "image/jpeg", "image/png", "image/gif",
		"image/webp", "image/bmp", "image/tiff", "image/heic", "image/heif":
		return true
	}
	return false
}

func convert(ctx context.Context, data []byte, declaredMime string, opts Options) ([]ProcessedImage, error) {
	mime := Sniff(data)
	if !isSupportedFormat(mime) {
		mime = declaredMime
	}
	switch mime {
	case "application/pdf":
		if !opts.AllowPDF {
			return nil, fmt.Errorf("unsupported file type %q", mime)
		}
		return fromPDF(ctx, data, opts)
	case "image/jpeg":
		return fromJPEG(data, opts)
	case "image/png", "image/gif", "image/webp", "image/bmp", "image/tiff",
		"image/heic", "image/heif":
		return fromRaster(data, mime, opts, isLegacyFormat(mime))
	default:
		return nil, fmt.Errorf("unsupported file type %q", mime)
	}
}

// legacyFormat reports the upload types the pipeline accepted before
// conversion existed. They are granted a pass-through fallback when they
// cannot be converted, so yesterday's working upload never starts failing
// today.
func isLegacyFormat(mime string) bool {
	switch mime {
	case "image/png", "image/webp", "image/heic", "image/heif":
		return true
	}
	return false
}

func fromJPEG(data []byte, opts Options) ([]ProcessedImage, error) {
	img, err := decodeRaster("image/jpeg", data)
	if err != nil {
		return passthrough(data, "image/jpeg", true, err)
	}
	o := jpegOrientation(data)
	if o <= 1 {
		b := img.Bounds()
		if _, isCMYK := img.(*image.CMYK); !isCMYK &&
			max(b.Dx(), b.Dy()) <= opts.MaxEdge && len(data) <= maxJPEGPassthroughBytes {
			return []ProcessedImage{{Data: data, MimeType: "image/jpeg"}}, nil
		}
	}
	// A re-encoded JPEG carries no EXIF, so the orientation must be baked
	// into the pixels first. CMYK JPEGs are always re-encoded (the extractors
	// cannot read the CMYK encoding).
	out, err := encodeJPEG(orient(img, o), opts)
	if err != nil {
		return passthrough(data, "image/jpeg", true, err)
	}
	return []ProcessedImage{{Data: out, MimeType: "image/jpeg", Converted: true}}, nil
}

func fromRaster(data []byte, mime string, opts Options, legacy bool) ([]ProcessedImage, error) {
	img, err := decodeRaster(mime, data)
	if err != nil {
		return passthrough(data, mime, legacy, err)
	}
	out, err := encodeJPEG(img, opts)
	if err != nil {
		return passthrough(data, mime, legacy, err)
	}
	return []ProcessedImage{{Data: out, MimeType: "image/jpeg", Converted: true}}, nil
}

// passthrough keeps the original bytes when they cannot be converted but the
// pipeline has been accepting their format all along; formats only newly
// accepted (GIF, TIFF, BMP) surface the error instead — a GIF is unreadable
// by most extractors, so keeping it would fail later anyway.
func passthrough(data []byte, mime string, legacy bool, err error) ([]ProcessedImage, error) {
	if legacy {
		return []ProcessedImage{{Data: data, MimeType: mime}}, nil
	}
	return nil, fmt.Errorf("could not convert %s file: %w", mime, err)
}

// decodeRaster decodes any supported raster format after the pixel-budget
// guard. An animated GIF yields its first frame.
func decodeRaster(mime string, data []byte) (image.Image, error) {
	if err := checkPixelBudget(mime, data); err != nil {
		return nil, err
	}
	r := bytes.NewReader(data)
	switch mime {
	case "image/jpeg":
		return jpeg.Decode(r)
	case "image/gif":
		return gif.Decode(r)
	case "image/png":
		return png.Decode(r)
	case "image/webp":
		return xwebp.Decode(r)
	case "image/bmp":
		return xbmp.Decode(r)
	case "image/tiff":
		return xtiff.Decode(r)
	case "image/heic", "image/heif":
		return heic.Decode(r)
	default:
		return nil, fmt.Errorf("unsupported image type %q", mime)
	}
}

func checkPixelBudget(mime string, data []byte) error {
	var cfg image.Config
	var err error
	r := bytes.NewReader(data)
	switch mime {
	case "image/jpeg":
		cfg, _, err = image.DecodeConfig(r)
	case "image/gif":
		cfg, err = gif.DecodeConfig(r)
	case "image/png":
		cfg, err = png.DecodeConfig(r)
	case "image/webp":
		cfg, err = xwebp.DecodeConfig(r)
	case "image/bmp":
		cfg, err = xbmp.DecodeConfig(r)
	case "image/tiff":
		cfg, err = xtiff.DecodeConfig(r)
	case "image/heic", "image/heif":
		cfg, err = heic.DecodeConfig(r)
	default:
		return fmt.Errorf("unsupported image type %q", mime)
	}
	if err != nil {
		// Leave the specifics to the decode error path.
		return err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return fmt.Errorf("image has invalid dimensions %dx%d", cfg.Width, cfg.Height)
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxDecodePixels {
		return fmt.Errorf("image is %dx%d pixels — over the %d megapixel limit", cfg.Width, cfg.Height, maxDecodePixels>>20)
	}
	return nil
}

// encodeJPEG downscales to MaxEdge, composites over white (JPEG has no alpha
// channel) and encodes at a quality that keeps receipt text readable without
// wasting storage.
func encodeJPEG(img image.Image, opts Options) ([]byte, error) {
	quality := 85
	img = fit(img, opts.MaxEdge)
	b := img.Bounds()
	canvas := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	xdraw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, xdraw.Src)
	xdraw.Draw(canvas, canvas.Bounds(), img, b.Min, xdraw.Over)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	return buf.Bytes(), nil
}

// fit downscales so the longest edge is MaxEdge; smaller images pass through
// untouched (never upscaled).
func fit(img image.Image, maxEdge int) image.Image {
	b := img.Bounds()
	long := max(b.Dx(), b.Dy())
	if long == 0 || long <= maxEdge {
		return img
	}
	ratio := float64(maxEdge) / float64(long)
	w := max(1, int(float64(b.Dx())*ratio+0.5))
	h := max(1, int(float64(b.Dy())*ratio+0.5))
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
	return dst
}
