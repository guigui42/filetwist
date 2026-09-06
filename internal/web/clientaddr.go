package web

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// maxForwardedHops bounds the proxy chain this service is willing to walk. A
// longer chain is treated as unresolvable rather than parsed.
const maxForwardedHops = 32

// clientAddress resolves the address a request should be attributed to.
//
// The peer address is authoritative unless the peer is an explicitly trusted
// proxy, in which case the rightmost address of the forwarded chain that is
// not itself trusted becomes the client. The second return value reports
// whether the attribution is trustworthy: it is false when an untrusted peer
// supplied forwarding headers, when the chain cannot be parsed, or when the
// peer address itself is unusable. Callers that gate access must fail closed
// on false, because a same-host reverse proxy makes every remote client look
// like a loopback connection.
func clientAddress(request *http.Request, trusted []netip.Prefix) (netip.Addr, bool) {
	peer, ok := parseAddress(request.RemoteAddr)
	if !ok {
		return netip.Addr{}, false
	}
	forwarded := request.Header.Values("X-Forwarded-For")
	hasRFC7239 := len(request.Header.Values("Forwarded")) > 0
	if !isTrustedProxy(peer, trusted) {
		// Nothing about the forwarded chain can be verified, so a peer that
		// sends one is not attributed at all.
		return peer, len(forwarded) == 0 && !hasRFC7239
	}

	chain := forwardedChain(forwarded)
	if len(chain) == 0 {
		// RFC 7239 Forwarded is deliberately not parsed. A trusted proxy that
		// sends only that header is reported as unresolvable instead of being
		// silently attributed to the proxy itself.
		return peer, !hasRFC7239
	}
	if len(chain) > maxForwardedHops {
		return netip.Addr{}, false
	}
	for index := len(chain) - 1; index >= 0; index-- {
		address, ok := parseAddress(chain[index])
		if !ok {
			return netip.Addr{}, false
		}
		if isTrustedProxy(address, trusted) && index > 0 {
			continue
		}
		return address, true
	}
	return peer, true
}

// forwardedChain flattens every X-Forwarded-For header value into one ordered
// list of hops, left to right.
func forwardedChain(values []string) []string {
	var chain []string
	for _, value := range values {
		for _, entry := range strings.Split(value, ",") {
			trimmed := strings.TrimSpace(entry)
			if trimmed == "" {
				continue
			}
			chain = append(chain, trimmed)
			if len(chain) > maxForwardedHops {
				return chain
			}
		}
	}
	return chain
}

// parseAddress reads a bare address, a bracketed address, or a host:port pair.
func parseAddress(value string) (netip.Addr, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return netip.Addr{}, false
	}
	if host, _, err := net.SplitHostPort(trimmed); err == nil {
		trimmed = host
	}
	trimmed = strings.Trim(trimmed, "[]")
	if index := strings.Index(trimmed, "%"); index > 0 {
		trimmed = trimmed[:index]
	}
	address, err := netip.ParseAddr(trimmed)
	if err != nil {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}

func isTrustedProxy(address netip.Addr, trusted []netip.Prefix) bool {
	for _, prefix := range trusted {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
