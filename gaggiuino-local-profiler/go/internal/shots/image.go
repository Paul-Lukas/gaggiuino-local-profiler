package shots

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// This file ports the subset of lib/services/ImageService.js
// routes/shots.js's own image endpoints use: imagePath/CONTENT_TYPE_EXT/
// deleteImage/saveUploadedImage. It deliberately does NOT port the
// URL-fetch/SSRF-guard half (fetchBeanImage/isAllowedImageUrl/
// normalizeImageUrl) — the shots routes never fetch an image by URL, only
// accept a direct upload — or matchesImageMagicBytes, which only
// routes/backup.js's restore path uses. ImageService.js is shared with the
// (not yet ported) library domain; once that lands, this should become a
// shared package instead of two copies — see doc.go.

// DefaultImageDir mirrors lib/constants.js's BEAN_IMAGE_DIR. Despite the
// name it's the one directory every entity photo (bean/grinder/shot) lives
// in, distinguished by filename prefix — see imageFilename. Handlers takes
// this as an injectable field (imagePath/deleteImage/saveUploadedImage all
// take a dir parameter rather than reading a package constant) so tests
// can point it at a t.TempDir() instead of the real /data mount, which a
// non-root test process can't write to.
const DefaultImageDir = "/data/bean-images"

// maxImageBytes mirrors lib/constants.js's BEAN_IMAGE_MAX_BYTES.
const maxImageBytes = 4 * 1024 * 1024

// contentTypeExt mirrors ImageService.js's CONTENT_TYPE_EXT.
var contentTypeExt = map[string]string{
	"image/jpeg": "jpg",
	"image/png":  "png",
	"image/webp": "webp",
	"image/gif":  "gif",
}

// extContentType is contentTypeExt inverted — ports the effect of Node's
// `res.type(ext)` (mime-type lookup by extension) for GET .../image.
var extContentType = map[string]string{
	"jpg":  "image/jpeg",
	"png":  "image/png",
	"webp": "image/webp",
	"gif":  "image/gif",
}

// imageFilename ports ImageService.js's imageFilename: prefix distinguishes
// entity types sharing imageDir ('shot-' here) so a shot id can never
// collide with a bean (no prefix) or grinder ('grinder-') image.
func imageFilename(id int64, ext, prefix string) string {
	return fmt.Sprintf("%s%d.%s", prefix, id, ext)
}

// imagePath ports ImageService.js's imagePath.
func imagePath(dir string, id int64, ext, prefix string) string {
	return filepath.Join(dir, imageFilename(id, ext, prefix))
}

// deleteImage ports ImageService.js's deleteImage: best-effort, silently
// ignores an already-missing file.
func deleteImage(dir string, id int64, ext, prefix string) {
	if ext == "" {
		return
	}
	_ = os.Remove(imagePath(dir, id, ext, prefix))
}

// contentTypeKnown reports whether contentType (optionally with a
// "; charset=..." suffix, as raw Content-Type headers carry) names one of
// the four whitelisted image types.
func contentTypeKnown(contentType string) (ext string, ok bool) {
	base := strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
	ext, ok = contentTypeExt[base]
	return ext, ok
}

// saveUploadedImage ports ImageService.js's saveUploadedImage: writes data
// to imageDir under (id, ext, prefix), returning the extension on success.
// The empty/oversized/unknown-content-type checks routes/shots.js's POST
// .../image performs itself before ever calling this (see handlers.go) are
// deliberately re-checked here too, matching the Node original's own
// belt-and-suspenders shape (saveUploadedImage() re-validates rather than
// trusting its caller).
func saveUploadedImage(dir, prefix string, id int64, data []byte, contentType string) (ext string, ok bool) {
	ext, known := contentTypeKnown(contentType)
	if !known || len(data) == 0 || len(data) > maxImageBytes {
		return "", false
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false
	}
	if err := os.WriteFile(imagePath(dir, id, ext, prefix), data, 0o644); err != nil {
		return "", false
	}
	return ext, true
}
