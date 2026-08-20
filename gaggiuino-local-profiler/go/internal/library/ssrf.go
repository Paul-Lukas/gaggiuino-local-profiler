package library

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// This file ports lib/ssrf-guard.js's assertPublicHost — used by the
// barcode-scan endpoint (scan.go) as a DNS-rebinding guard on
// world.openfoodfacts.org: that hostname is a fixed literal, not user
// input, but a compromised/rebound DNS answer for it could still point at
// an internal address, so every resolved address is checked before the
// request is allowed to proceed. assertMachineHost (the looser
// loopback/link-local-only variant used by routes/machines.js) is NOT
// ported here — it belongs to the not-yet-ported machines domain
// (internal/machines).

// ErrSSRFBlocked ports ssrf-guard.js's SsrfBlockedError — distinguished
// from an ordinary resolution failure so callers can map it to its own
// response (scan.go logs+502s on both, but distinctly, matching
// routes/library/scan.js's own `e instanceof SsrfBlockedError` branch).
type ErrSSRFBlocked struct{ msg string }

func (e *ErrSSRFBlocked) Error() string { return e.msg }

// lookupIPAddr is a package-level var (not a bare net.DefaultResolver call)
// so tests can substitute a fake resolver instead of depending on real DNS
// or real private/public IP literals being reachable in CI.
var lookupIPAddr = net.DefaultResolver.LookupIPAddr

// isPrivateIPv4 ports ssrf-guard.js's isPrivateIPv4.
func isPrivateIPv4(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return true // fail closed on garbage
	}
	a, b := v4[0], v4[1]
	if a == 0 || a == 10 || a == 127 {
		return true
	}
	if a == 169 && b == 254 {
		return true // link-local
	}
	if a == 172 && b >= 16 && b <= 31 {
		return true // RFC1918
	}
	if a == 192 && b == 168 {
		return true // RFC1918
	}
	if a == 100 && b >= 64 && b <= 127 {
		return true // CGNAT (RFC6598)
	}
	return false
}

// isPrivateIPv6 ports ssrf-guard.js's isPrivateIPv6 — only called for a
// genuine (non-v4-mapped) IPv6 address; isPrivateAddress below routes
// v4-mapped addresses (::ffff:a.b.c.d) through isPrivateIPv4 instead, same
// branch ssrf-guard.js's own isPrivateIPv6 takes internally.
func isPrivateIPv6(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsUnspecified() {
		return true
	}
	b16 := ip.To16()
	if b16 == nil {
		return true // fail closed on garbage
	}
	if b16[0] == 0xfe && (b16[1]&0xc0) == 0x80 {
		return true // fe80::/10 link-local
	}
	if (b16[0] & 0xfe) == 0xfc {
		return true // fc00::/7 unique local
	}
	return false
}

// isPrivateAddress ports ssrf-guard.js's isPrivateAddress: net.isIPv4(ip) ?
// isPrivateIPv4(ip) : isPrivateIPv6(ip) — Go's ip.To4() != nil is the
// equivalent test (true for both plain dotted-decimal addresses and
// IPv4-mapped IPv6 addresses, matching ssrf-guard.js's own explicit
// `::ffff:` unwrap inside isPrivateIPv6 for the latter).
func isPrivateAddress(ip net.IP) bool {
	if ip == nil {
		return true // unrecognised format — fail closed
	}
	if v4 := ip.To4(); v4 != nil {
		return isPrivateIPv4(v4)
	}
	return isPrivateIPv6(ip)
}

// assertPublicHost ports ssrf-guard.js's assertPublicHost(hostname):
// resolves hostname (or accepts a literal IP) and returns ErrSSRFBlocked if
// any resolved address is private/loopback/link-local. A plain error is
// returned when the hostname can't be resolved at all — an ordinary fetch
// failure, not a security rejection, matching the Node original's
// distinction (scan.go's caller only special-cases ErrSSRFBlocked).
func assertPublicHost(ctx context.Context, hostname string) error {
	bare := strings.TrimPrefix(strings.TrimSuffix(hostname, "]"), "[")
	if ip := net.ParseIP(bare); ip != nil {
		if isPrivateAddress(ip) {
			return &ErrSSRFBlocked{fmt.Sprintf("blocked address: %s", bare)}
		}
		return nil
	}
	addrs, err := lookupIPAddr(ctx, bare)
	if err != nil {
		return fmt.Errorf("could not resolve host: %s: %w", bare, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("could not resolve host: %s", bare)
	}
	for _, a := range addrs {
		if isPrivateAddress(a.IP) {
			return &ErrSSRFBlocked{fmt.Sprintf("blocked address: %s (%s)", a.IP, bare)}
		}
	}
	return nil
}

// isSSRFBlocked reports whether err is (or wraps) an ErrSSRFBlocked.
func isSSRFBlocked(err error) bool {
	var e *ErrSSRFBlocked
	return errors.As(err, &e)
}
