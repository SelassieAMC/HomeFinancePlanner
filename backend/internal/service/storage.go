package service

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Shared storage helpers for uploaded files (bill receipts, store logos).

// allowedMime maps accepted receipt types to file extensions.
var allowedMime = map[string]string{
	"image/jpeg":      ".jpg",
	"image/png":       ".png",
	"image/webp":      ".webp",
	"image/heic":      ".heic",
	"image/heif":      ".heif",
	"application/pdf": ".pdf",
}

// logoMime maps accepted store-logo types to extensions (images only, no PDF).
var logoMime = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
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
// arrive with a generic or untrustworthy content type. HEIC/HEIF and PDF are
// detected by hand (the net/http sniff table is incomplete there); the rest
// falls through to the standard sniff table (jpeg, png, gif, webp, …).
func sniffMime(file []byte) string {
	if len(file) >= 12 && string(file[4:8]) == "ftyp" {
		brand := string(file[8:12])
		switch {
		case strings.HasPrefix(brand, "heic"), strings.HasPrefix(brand, "heix"),
			strings.HasPrefix(brand, "hevc"), strings.HasPrefix(brand, "hevx"):
			return "image/heic"
		case strings.HasPrefix(brand, "mif1"), strings.HasPrefix(brand, "msf1"):
			return "image/heif"
		}
	}
	if len(file) >= 5 && string(file[:4]) == "%PDF" {
		return "application/pdf"
	}
	return http.DetectContentType(file)
}
