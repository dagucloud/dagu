// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package netutil

import (
	"net/netip"
	"slices"
)

var (
	ipv4CompatiblePrefix = netip.MustParsePrefix("::/96")
	nat64Prefix          = netip.MustParsePrefix("64:ff9b::/96")
	nat64LocalPrefix     = netip.MustParsePrefix("64:ff9b:1::/48")
	sixToFourPrefix      = netip.MustParsePrefix("2002::/16")
	teredoPrefix         = netip.MustParsePrefix("2001::/32")
)

// IsPrivateAddr reports whether addr is a loopback, private, link-local,
// multicast, or unspecified address, or an IPv6 transition address that
// carries such an IPv4 address and so can reach it through a translator. An
// IPv6 zone does not change the result.
func IsPrivateAddr(addr netip.Addr) bool {
	// Prefix.Contains never matches a zoned address.
	addr = addr.WithZone("").Unmap()
	if isLocalAddr(addr) || nat64LocalPrefix.Contains(addr) {
		return true
	}
	return slices.ContainsFunc(embeddedIPv4(addr), isLocalAddr)
}

func isLocalAddr(addr netip.Addr) bool {
	return addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified()
}

// embeddedIPv4 returns the IPv4 addresses an IPv6 transition address carries:
// IPv4-compatible, NAT64 well-known prefix, 6to4, and Teredo server and client.
func embeddedIPv4(addr netip.Addr) []netip.Addr {
	if !addr.Is6() {
		return nil
	}
	b := addr.As16()
	at := func(i int) netip.Addr {
		return netip.AddrFrom4([4]byte{b[i], b[i+1], b[i+2], b[i+3]})
	}
	switch {
	case ipv4CompatiblePrefix.Contains(addr), nat64Prefix.Contains(addr):
		return []netip.Addr{at(12)}
	case sixToFourPrefix.Contains(addr):
		return []netip.Addr{at(2)}
	case teredoPrefix.Contains(addr):
		// Teredo stores the client address with every bit inverted.
		client := netip.AddrFrom4([4]byte{^b[12], ^b[13], ^b[14], ^b[15]})
		return []netip.Addr{at(4), client}
	}
	return nil
}
