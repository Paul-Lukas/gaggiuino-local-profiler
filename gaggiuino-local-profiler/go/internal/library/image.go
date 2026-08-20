package library

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file ports lib/services/ImageService.js in full — internal/shots'
// image.go only ported the upload/serve half (the shots domain never fetches
// an image by URL); this package additionally needs fetchBeanImage's
// URL-download path (bean.imageUrl on create), which is why
// ALLOWED_IMAGE_HOSTS is in scope here and wasn't for shots. Both packages
// currently duplicate this file's non-URL half rather than sharing a
// package — see shots/image.go's doc comment, which already flags this as
// the thing to fix once both exist.

// DefaultImageDir mirrors lib/constants.js's BEAN_IMAGE_DIR — the one
// directory every entity photo (bean/grinder/basket/puckScreen/shot) lives
// in, distinguished by filename prefix (see imageFilename). Injectable on
// Handlers so tests can point it at a t.TempDir() instead of the real /data
// mount.
const DefaultImageDir = "/data/bean-images"

// maxImageBytes mirrors lib/constants.js's BEAN_IMAGE_MAX_BYTES.
const maxImageBytes = 4 * 1024 * 1024

// allowedImageHosts mirrors lib/constants.js's ALLOWED_IMAGE_HOSTS
// (ALLOWED_IMPORT_HOSTS plus cdn.shopify.com): bean images are only ever
// downloaded from an import source's own host or its CDN, never an
// arbitrary URL a client sends — this exact allowlist, not a generic SSRF
// DNS-resolution guard (unlike assertPublicHost in ssrf.go, used by the
// barcode-scan endpoint instead), is what fetchBeanImage below checks.
var allowedImageHosts = map[string]bool{
	"kaffeebraun.com":          true,
	"www.kaffeebraun.com":      true,
	"hoppenworth-ploch.de":     true,
	"www.hoppenworth-ploch.de": true,
	"elbgold.com":              true,
	"www.elbgold.com":          true,
	"cdn.shopify.com":          true,
}

var contentTypeExt = map[string]string{
	"image/jpeg": "jpg",
	"image/png":  "png",
	"image/webp": "webp",
	"image/gif":  "gif",
}

var extContentType = map[string]string{
	"jpg":  "image/jpeg",
	"png":  "image/png",
	"webp": "image/webp",
	"gif":  "image/gif",
}

// imageFilename ports ImageService.js's imageFilename.
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
// "; charset=..." suffix) names one of the four whitelisted image types.
func contentTypeKnown(contentType string) (ext string, ok bool) {
	base := strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
	ext, ok = contentTypeExt[base]
	return ext, ok
}

// saveUploadedImage ports ImageService.js's saveUploadedImage.
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

// normalizeImageURL ports ImageService.js's normalizeImageUrl: a
// protocol-relative shop CDN URL ("//cdn.shopify.com/...") becomes https.
func normalizeImageURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "//") {
		return "https:" + trimmed
	}
	return trimmed
}

// isAllowedImageURL ports ImageService.js's isAllowedImageUrl: http(s) only,
// exact hostname match against allowedImageHosts.
func isAllowedImageURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && allowedImageHosts[u.Hostname()]
}

// fetchImageClient never follows redirects (maxRedirects: 0 in the Node
// original) — a redirect target isn't re-checked against the allowlist, so
// following it would reopen the exact SSRF surface the allowlist exists to
// close.
var fetchImageClient = &http.Client{
	Timeout: 8 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// fetchBeanImage ports ImageService.js's fetchBeanImage: downloads a bean
// image once, validating against the exact-hostname allowlist above (not
// assertPublicHost's DNS-resolution guard — see allowedImageHosts' doc
// comment), no redirect following, a size cap, and a content-type
// whitelist. The filename is derived from the (already-numeric) bean id,
// never from the URL. Returns the extension on success, "" on any failure
// (never an error — every caller treats this as best-effort, matching the
// Node original's `.catch(() => {})` fire-and-forget callers).
func fetchBeanImage(dir string, beanID int64, imageURL string) string {
	u := normalizeImageURL(imageURL)
	if u == "" || !isAllowedImageURL(u) {
		return ""
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "GLP/1.0 (Gaggiuino Local Profiler; private use)")
	resp, err := fetchImageClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	contentType := strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0])
	ext, known := contentTypeExt[contentType]
	if !known {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxImageBytes {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	if err := os.WriteFile(imagePath(dir, beanID, ext, ""), data, 0o644); err != nil {
		return ""
	}
	return ext
}
