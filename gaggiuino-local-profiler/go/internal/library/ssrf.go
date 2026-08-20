package library

import (
	"context"
	"net"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/netguard"
)

// This file ports lib/ssrf-guard.js's assertPublicHost — used by the
// barcode-scan endpoint (scan.go) as a DNS-rebinding guard on
// world.openfoodfacts.org: that hostname is a fixed literal, not user
// input, but a compromised/rebound DNS answer for it could still point at
// an internal address, so every resolved address is checked before the
// request is allowed to proceed. assertMachineHost (the looser
// loopback/link-local-only variant used by routes/machines.js) is NOT
// ported here — it belongs to the not-yet-ported machines domain
// (internal/machines). The actual resolve-then-check plumbing both
// variants share now lives in internal/netguard (#901 code review) — this
// file keeps only what's genuinely specific to this package's threat model:
// the private/public IP predicate and the lookupIPAddr test seam.

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

// assertPublicHost ports ssrf-guard.js's assertPublicHost(hostname), via
// internal/netguard's shared AssertHost.
func assertPublicHost(ctx context.Context, hostname string) error {
	return netguard.AssertHost(ctx, hostname, isPrivateAddress, lookupIPAddr)
}

// isSSRFBlocked reports whether err is (or wraps) an ErrBlocked from a
// failed assertPublicHost call.
func isSSRFBlocked(err error) bool {
	return netguard.IsBlocked(err)
}
