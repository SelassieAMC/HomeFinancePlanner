package imageproc

import (
	"net/http"
	"strings"
)

// Sniff detects an upload's type from its magic bytes, for files that arrive
// with a generic or untrustworthy content type (browsers outside Safari often
// declare .heic/.pdf as octet-stream). HEIC/HEIF, PDF and BMP are detected by
// hand (the net/http sniff table is incomplete there); the rest falls through
// to the standard sniff table (jpeg, png, gif, webp, tiff).
func Sniff(file []byte) string {
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
	if len(file) >= 2 && file[0] == 'B' && file[1] == 'M' {
		return "image/bmp"
	}
	if len(file) >= 4 && (string(file[:4]) == "II*\x00" || string(file[:4]) == "MM\x00*") {
		return "image/tiff"
	}
	if len(file) >= 12 && string(file[:4]) == "RIFF" && string(file[8:12]) == "WEBP" {
		return "image/webp"
	}
	return http.DetectContentType(file)
}
