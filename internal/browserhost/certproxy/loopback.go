// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"runtime"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/hostpattern"
)

// localhostAddrs are the addresses Chrome gives a localhost name, in its
// order, without asking the system resolver.
var localhostAddrs = []string{"::1", "127.0.0.1"}

// manualProxy reports whether hosts name a destination Chrome will not send
// through a PAC script. The browser then needs a manual proxy. A * pattern
// does not count: it leaves those destinations to the browser, as Chrome
// does.
func manualProxy(hosts []string) bool {
	return slices.ContainsFunc(hosts, implicitBypass)
}

// implicitBypass reports whether pattern is, or can match, a destination
// Chrome skips in every PAC script: localhost names, *.localhost, the name
// loopback on Windows, a loopback address, or a link-local address.
func implicitBypass(pattern string) bool {
	pattern = hostpattern.Canonical(pattern)
	if isLocalhostName(pattern) || (runtime.GOOS == "windows" && pattern == "loopback") {
		return true
	}
	address, err := netip.ParseAddr(pattern)
	return err == nil && (address.Unmap().IsLoopback() || address.IsLinkLocalUnicast())
}

// isLocalhostName reports whether the canonical host is localhost or a
// name under it.
func isLocalhostName(host string) bool {
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}

// dialDirect connects to addr without a proxy. A localhost name reaches
// the loopback addresses, as in Chrome, which the system resolver need not
// know it by.
func dialDirect(ctx context.Context, dialer *net.Dialer, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || !isLocalhostName(hostpattern.Canonical(host)) {
		return dialer.DialContext(ctx, "tcp", addr)
	}
	var errs []error
	for _, loopback := range localhostAddrs {
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(loopback, port))
		if err == nil {
			return conn, nil
		}
		errs = append(errs, err)
		if ctx.Err() != nil {
			break
		}
	}
	return nil, errors.Join(errs...)
}
