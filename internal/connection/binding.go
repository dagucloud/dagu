// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connection

import (
	"net"
	"net/netip"
	"net/url"
	"strings"
)

// httpsPort is the port that a bound host without a port matches.
const httpsPort = "443"

// BindsURL reports whether c's access tokens may be sent to u over HTTP: u
// is HTTPS and its host matches one of c's bound hosts. A hostname matches
// without regard to case or one trailing dot, never a subdomain; an IP
// literal matches only the same address. A bound host without a port
// matches only port 443.
func (c *Connection) BindsURL(u *url.URL) bool {
	if u == nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" {
		return false
	}
	host, port := normalizeHost(u.Hostname()), u.Port()
	if port == "" {
		port = httpsPort
	}
	for _, entry := range c.AllBoundHosts() {
		entryHost, entryPort := splitBoundHost(entry)
		if entryPort == "" {
			entryPort = httpsPort
		}
		if entryPort == port && sameHost(normalizeHost(entryHost), host) {
			return true
		}
	}
	return false
}

// splitBoundHost splits a bound host entry, a hostname or host:port, into
// its host and port. The port is empty when the entry names none.
func splitBoundHost(entry string) (host, port string) {
	if h, p, err := net.SplitHostPort(entry); err == nil {
		return h, p
	}
	return strings.TrimSuffix(strings.TrimPrefix(entry, "["), "]"), ""
}

func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

// sameHost compares IP literals as addresses, so two spellings of one IPv6
// address match, and hostnames as strings.
func sameHost(a, b string) bool {
	ipA, errA := netip.ParseAddr(a)
	ipB, errB := netip.ParseAddr(b)
	if errA == nil || errB == nil {
		return errA == nil && errB == nil && ipA == ipB
	}
	return a == b
}
