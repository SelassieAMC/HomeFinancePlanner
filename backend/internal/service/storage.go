package service

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"home-finance-planner/backend/internal/imageproc"
)

// Shared storage helpers for uploaded files (bill receipts, store logos).

// allowedMime maps accepted receipt types to the file extension those bytes
// are stored under. Converted parts are always written as ".jpg" (their mime
// becomes image/jpeg); the other extensions only ever apply when an
// un-convertible legacy upload passes through unchanged.
var allowedMime = map[string]string{
	"image/jpeg":      ".jpg",
	"image/png":       ".png",
	"image/webp":      ".webp",
	"image/heic":      ".heic",
	"image/heif":      ".heif",
	"image/gif":       ".gif",
	"image/tiff":      ".tif",
	"image/bmp":       ".bmp",
	"application/pdf": ".pdf",
}

// logoMime accepts the same raster formats as receipts (extension values are
// only used for the rare pass-through saves) but no PDFs — logos and product
// photos are UI images, never receipts.
var logoMime = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/heic": ".heic",
	"image/heif": ".heif",
	"image/gif":  ".gif",
	"image/tiff": ".tif",
	"image/bmp":  ".bmp",
}

// writeFileRandom writes file under dir with a random, unguessable name
// ("<yyyymmdd>-<8 random bytes hex><ext>") and mode 0o600.
func writeFileRandom(dir, ext string, file []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s dir: %w", filepath.Base(dir), err)
	}
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate file name: %w", err)
	}
	name := fmt.Sprintf("%s-%s%s", time.Now().Format("20060102"), hex.EncodeToString(buf), ext)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, file, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// detectMime infers the file mime from the stored extension.
func detectMime(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".heic":
		return "image/heic"
	case ".heif":
		return "image/heif"
	case ".pdf":
		return "application/pdf"
	default:
		return "image/jpeg"
	}
}

// sniffMime detects an upload's type from its magic bytes, for files that
// arrive with a generic or untrustworthy content type. The magic-byte table
// (HEIC/HEIF, PDF, BMP, TIFF, WebP) lives in imageproc next to the decoders
// that consume it — this wrapper keeps the service-side call sites stable.
func sniffMime(file []byte) string {
	return imageproc.Sniff(file)
}
