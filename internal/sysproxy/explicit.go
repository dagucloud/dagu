// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sysproxy

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// explicitSchemes are the proxy schemes an explicit proxy may use.
var explicitSchemes = map[string]proxyKind{
	"http":   httpProxy,
	"https":  httpsProxy,
	"socks":  socks5Proxy,
	"socks5": socks5Proxy,
}

// explicitPAC returns a script that sends every request through the proxy
// [scheme://]host[:port], except requests to loopback hosts. Errors do not
// repeat the address, which may hold a password.
func explicitPAC(explicit string) (string, error) {
	switch {
	case strings.ContainsAny(explicit, "=;"):
		return "", errors.New("invalid proxy: proxy rules such as http=a;https=b are not supported; give one proxy as [scheme://]host[:port]")
	case strings.ContainsFunc(explicit, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }):
		return "", errors.New("invalid proxy: proxy lists are not supported; give one proxy as [scheme://]host[:port]")
	case strings.HasPrefix(strings.ToLower(explicit), "direct://"):
		return "", errors.New("invalid proxy: direct:// is not supported; leave the proxy unset to follow the device's proxy settings")
	}
	server, withCredentials, err := parseProxyAddress(explicit, explicitSchemes, httpProxy)
	if err != nil {
		return "", fmt.Errorf("invalid proxy: %w", err)
	}
	if withCredentials {
		return "", errors.New("invalid proxy: proxies that require a user name or password are not supported")
	}
	return staticConfig{fallback: []proxyServer{server}}.pac(), nil
}
