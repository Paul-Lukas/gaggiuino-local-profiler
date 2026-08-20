package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"strings"
)

// DefaultTokenFile is the on-disk location the Node app reads/writes the
// generated X-GLP-Token from (lib/constants.js's TOKEN_FILE).
const DefaultTokenFile = "/data/api_token.txt"

// HAIngressPrefix is the fixed prefix of the X-Ingress-Path header HA Core
// sets when proxying a request through Ingress (lib/constants.js's
// HA_INGRESS_PREFIX). It is a PREFIX, not the add-on's own path: HA Core
// sets X-Ingress-Path to `/api/hassio_ingress/<per-session random token>`
// (homeassistant/components/hassio/ingress.py), never the add-on slug, and
// the token differs per install and even per dev-add-on install — there is
// no fixed suffix to pin. The Supervisor-IP check alongside every use of
// this prefix (see IsIngressRequest) is what makes the header trustworthy:
// any LAN client that can reach the app's port can otherwise send an
// arbitrary X-Ingress-Path.
const HAIngressPrefix = "/api/hassio_ingress/"

// IsSupervisorIP ports lib/helpers.js's isSupervisorIp(ip) verbatim,
// including its exact (non-obvious) string semantics: only literal
// "127.0.0.1" or "::1" — not the whole 127.0.0.0/8 loopback block — count as
// loopback, and "172.30." is a plain string prefix check on the (optionally
// IPv4-mapped) address, not a CIDR-aware containment check. This is
// deliberately a string comparison, not net.IP-based (no ParseIP,
// IsLoopback, or IPNet.Contains): the Node original never parses the IP
// either, it just strips a leading "::ffff:" and does ===/startsWith on the
// resulting string, so anything that isn't exactly one of those three forms
// (e.g. "127.0.0.5", an octal/non-canonical form, garbage) is untrusted —
// same as here.
//
// #801 (also called out in server.js's isFromSupervisor comment this
// mirrors): this deliberately trusts the *whole* 172.30.0.0/16 network, not
// only the Ingress proxy specifically — any other add-on running on that
// network could in principle send a crafted X-Ingress-Path and be treated
// as Ingress by IsIngressRequest. Not a regression versus the Node
// original and not exploitable beyond what the already-public GET
// /api/token grants, but load-bearing for anything later built on the
// assumption that Ingress implies trusted.
func IsSupervisorIP(ip string) bool {
	plain := strings.TrimPrefix(strings.TrimSpace(ip), "::ffff:")
	return plain == "127.0.0.1" || plain == "::1" || strings.HasPrefix(plain, "172.30.")
}

// RemoteIP extracts the connecting IP from an *http.Request's RemoteAddr
// (normally "host:port"; tests/fabricated requests may set a bare IP, which
// is passed through unchanged if it has no port to split off).
func RemoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// IsFromSupervisor ports server.js's isFromSupervisor(req): true only for
// requests whose connection originates from the HA Supervisor's internal
// network. External LAN clients arrive with their own IP and must not
// receive the same trust level.
func IsFromSupervisor(r *http.Request) bool {
	return IsSupervisorIP(RemoteIP(r))
}

// IsIngressRequest ports server.js's isIngressRequest(req): true only for
// requests that genuinely arrive through HA Ingress — an X-Ingress-Path
// header with the expected prefix AND a Supervisor-network source IP (the
// same trust check IsFromSupervisor uses). The Supervisor-IP check is what
// stops a LAN client on the app's exposed port from simply sending its own
// X-Ingress-Path header to pass this.
func IsIngressRequest(r *http.Request) bool {
	ingressPath := r.Header.Get("X-Ingress-Path")
	return strings.HasPrefix(ingressPath, HAIngressPrefix) && IsFromSupervisor(r)
}

// IsTokenValid ports server.js's isTokenValid(token): a constant-time
// comparison of a candidate X-GLP-Token against the app's real token, using
// crypto/subtle.ConstantTimeCompare exactly where Node uses
// crypto.timingSafeEqual. stored/candidate empty, or of different lengths,
// return false immediately without comparing — matching the Node original's
// own early-exit behavior 1:1 (Node itself declines to run
// timingSafeEqual on mismatched lengths, since that function panics on
// unequal-length buffers; ConstantTimeCompare instead returns 0 for
// unequal lengths, but the explicit length check is kept here to mirror the
// Node control flow exactly, not just its outcome).
func IsTokenValid(stored, candidate string) bool {
	if stored == "" || candidate == "" {
		return false
	}
	a := []byte(candidate)
	b := []byte(stored)
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}

// LoadOrCreateToken ports server.js's loadOrCreateApiToken(): reads the
// token at path if present (trimmed, matching Node's .trim() on read), or
// generates a new 32-byte random token (hex-encoded, matching Node's
// crypto.randomBytes(32).toString('hex')) and persists it via an atomic
// write (writeTokenFile below, the same tmp-file-then-rename pattern as
// lib/helpers.js's writeFileSafe) if none exists yet.
func LoadOrCreateToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return strings.TrimSpace(string(data)), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(tokenBytes)
	if err := writeTokenFile(path, token); err != nil {
		return "", err
	}
	return token, nil
}

// writeTokenFile ports lib/helpers.js's writeFileSafe (write-to-.tmp then
// rename, so a reader can never observe a partially-written token file).
func writeTokenFile(path, content string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SecurityHeaders wraps next with server.js's security-header middleware
// (the app.use((req, res, next) => {...}) block near the top of that file,
// lines ~83-98) — same header names and values verbatim, applied to every
// response before any route-specific handler runs. Chart.js, ECharts,
// topojson-client, QRCode and both fonts (Figtree, Fraunces) are bundled
// into the app, hence no third-party host needed in the CSP.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(self), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"font-src 'self' data:; "+
				"img-src 'self' data: blob:; "+
				"connect-src 'self';")
		next.ServeHTTP(w, r)
	})
}
