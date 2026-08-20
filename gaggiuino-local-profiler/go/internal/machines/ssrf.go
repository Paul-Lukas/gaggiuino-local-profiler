package machines

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// This file ports lib/ssrf-guard.js's assertMachineHost — the narrower of
// its two guards (see internal/library/ssrf.go for assertPublicHost, the
// wider one that guards untrusted external content). A machine host is the
// app owner's own trusted LAN configuration, not untrusted external
// content — the opposite threat model, so only loopback/link-local/cloud-
// metadata addresses are blocked here, NOT the RFC1918 ranges
// assertPublicHost blocks (a real Gaggiuino/GaggiMate machine lives in
// exactly those ranges, #336).

// ErrSSRFBlocked mirrors internal/library's own error type (kept separate,
// not shared, so this package doesn't need to import library — see
// internal/library/ssrf.go's own header comment for why assertMachineHost
// wasn't ported there instead).
type ErrSSRFBlocked struct{ msg string }

func (e *ErrSSRFBlocked) Error() string { return e.msg }

func isSSRFBlocked(err error) bool {
	var e *ErrSSRFBlocked
	return errors.As(err, &e)
}

// lookupIPAddr is a package-level var so tests can substitute a fake
// resolver instead of depending on real DNS or real address literals.
var lookupIPAddr = net.DefaultResolver.LookupIPAddr

// isLoopbackOrMetadataIPv4 ports ssrf-guard.js's isLoopbackOrMetadataIPv4.
func isLoopbackOrMetadataIPv4(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return true // fail closed on garbage
	}
	a, b := v4[0], v4[1]
	if a == 0 || a == 127 {
		return true
	}
	if a == 169 && b == 254 {
		return true // link-local, incl. cloud metadata (169.254.169.254)
	}
	return false
}

// isLoopbackOrMetadataIPv6 ports ssrf-guard.js's isLoopbackOrMetadataIPv6.
func isLoopbackOrMetadataIPv6(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsUnspecified() {
		return true
	}
	b16 := ip.To16()
	if b16 == nil {
		return true
	}
	if b16[0] == 0xfe && (b16[1]&0xc0) == 0x80 {
		return true // fe80::/10 link-local
	}
	return false
}

func isLoopbackOrMetadataAddress(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		return isLoopbackOrMetadataIPv4(v4)
	}
	return isLoopbackOrMetadataIPv6(ip)
}

// assertMachineHost ports ssrf-guard.js's assertMachineHost(hostname), via
// its shared _assertHostPasses helper.
func assertMachineHost(ctx context.Context, hostname string) error {
	bare := strings.TrimPrefix(strings.TrimSuffix(hostname, "]"), "[")
	if ip := net.ParseIP(bare); ip != nil {
		if isLoopbackOrMetadataAddress(ip) {
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
		if isLoopbackOrMetadataAddress(a.IP) {
			return &ErrSSRFBlocked{fmt.Sprintf("blocked address: %s (%s)", a.IP, bare)}
		}
	}
	return nil
}
