// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
)

type ipAccessPolicy struct {
	allowedIPs     []netip.Prefix
	trustedProxies []netip.Prefix
}

func newIPAccessPolicy(cfg config.IPAccessConfig) (*ipAccessPolicy, error) {
	allowedIPs, err := parseIPAccessPrefixes("allowed IP", cfg.AllowedIPs)
	if err != nil {
		return nil, err
	}
	trustedProxies, err := parseIPAccessPrefixes("trusted proxy", cfg.TrustedProxies)
	if err != nil {
		return nil, err
	}
	return &ipAccessPolicy{allowedIPs: allowedIPs, trustedProxies: trustedProxies}, nil
}

func parseIPAccessPrefixes(kind string, values []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := parseIPAccessPrefix(value)
		if err != nil {
			return nil, fmt.Errorf("invalid %s %q: %w", kind, value, err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func parseIPAccessPrefix(value string) (netip.Prefix, error) {
	if strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return netip.Prefix{}, err
		}
		if prefix.Addr().Is4In6() {
			if prefix.Bits() < 96 {
				return netip.Prefix{}, fmt.Errorf("mapped IPv4 prefix length must be at least 96")
			}
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}
		return prefix.Masked(), nil
	}

	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Prefix{}, err
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func (p *ipAccessPolicy) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(p.allowedIPs) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		clientIP, err := p.clientIP(r)
		if err != nil || !prefixesContain(p.allowedIPs, clientIP) {
			http.Error(w, "access denied", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (p *ipAccessPolicy) clientIP(r *http.Request) (netip.Addr, error) {
	peer, err := parseRequestIP(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}, err
	}
	if !prefixesContain(p.trustedProxies, peer) {
		return peer, nil
	}

	if values := r.Header.Values("X-Forwarded-For"); len(values) > 0 {
		chain := make([]netip.Addr, 0, len(values)+1)
		for _, value := range values {
			for part := range strings.SplitSeq(value, ",") {
				addr, err := parseRequestIP(part)
				if err != nil {
					return netip.Addr{}, err
				}
				chain = append(chain, addr)
			}
		}
		if len(chain) == 0 {
			return netip.Addr{}, fmt.Errorf("empty X-Forwarded-For header")
		}
		chain = append(chain, peer)
		index := len(chain) - 1
		for index > 0 && prefixesContain(p.trustedProxies, chain[index]) {
			index--
		}
		return chain[index], nil
	}

	if values := r.Header.Values("X-Real-IP"); len(values) > 0 {
		if len(values) != 1 {
			return netip.Addr{}, fmt.Errorf("multiple X-Real-IP headers")
		}
		return parseRequestIP(values[0])
	}
	return peer, nil
}

func parseRequestIP(value string) (netip.Addr, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return netip.Addr{}, fmt.Errorf("empty IP address")
	}
	if addr, err := netip.ParseAddr(value); err == nil {
		return addr.Unmap(), nil
	}
	addrPort, err := netip.ParseAddrPort(value)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid IP address %q: %w", value, err)
	}
	return addrPort.Addr().Unmap(), nil
}

func prefixesContain(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
