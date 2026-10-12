package imageproc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	pmodel "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// maxPDFPages bounds how many pages one scanned PDF may contribute (mirrors
// the bill service's MaxBillScanFiles: callers reject expansions past their
// receipt-part cap). A raster PDF past it is rejected rather than truncated —
// silently dropping receipt pages would fake a complete read.
const maxPDFPages = 8

// fromPDF converts a scanned PDF into one JPEG per page, in page order. The
// largest image on a page IS the page (stray logos, stamps and signature
// images are ignored); pages with no raster image contribute nothing. A PDF
// that yields no images at all — vector text, encrypted content, a broken
// XObject — passes through unchanged: it was a valid upload before conversion
// existed, and the extractors still read such PDFs through their document
// blocks (with their per-provider gate).
func fromPDF(ctx context.Context, data []byte, opts Options) ([]ProcessedImage, error) {
	images, err := api.ExtractImagesRaw(ctx, bytes.NewReader(data), nil, nil)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
	}

	// Count the pages that carry a raster image first (vector/text-only pages
	// never become parts, so long text receipts stay valid) and reject an
	// over-cap raster PDF up front — conversion is never truncated.
	var candidates []*pmodel.Image
	for _, page := range images {
		if img := pickLargest(page); img != nil {
			candidates = append(candidates, img)
		}
	}
	if len(candidates) > maxPDFPages {
		return nil, fmt.Errorf("a scanned PDF may have at most %d image pages — this one has %d", maxPDFPages, len(candidates))
	}

	outs := make([]ProcessedImage, 0, len(candidates))
	for _, img := range candidates {
		decoded, err := decodePDFImage(img)
		if err != nil {
			continue
		}
		out, err := encodeJPEG(decoded, opts)
		if err != nil {
			continue
		}
		outs = append(outs, ProcessedImage{Data: out, MimeType: "image/jpeg", Converted: true})
	}
	if len(outs) == 0 {
		return []ProcessedImage{{Data: data, MimeType: "application/pdf"}}, nil
	}
	return outs, nil
}

// pickLargest takes the biggest image (by declared pixel area) on one page.
func pickLargest(page map[int]pmodel.Image) *pmodel.Image {
	var best pmodel.Image
	found := false
	for _, img := range page {
		if !found || img.Width*img.Height > best.Width*best.Height {
			best = img
			found = true
		}
	}
	if !found {
		return nil
	}
	return &best
}

// decodePDFImage reads the raw stream pdfcpu extracted for one image resource
// (JPEG/PNG bytes for the common cases) and rasterizes it. JPX/JBIG2 pages
// stay unconverted — image.Decode cannot read them in pure Go.
func decodePDFImage(img *pmodel.Image) (image.Image, error) {
	raw, err := io.ReadAll(img)
	if err != nil || len(raw) == 0 {
		return nil, fmt.Errorf("extracted image is empty")
	}
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("extracted image is not a readable raster: %w", err)
	}
	return decoded, nil
}
