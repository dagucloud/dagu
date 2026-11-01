// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sysproxy

import (
	"fmt"
	"strings"
	"unicode"
)

// ieSchemes are the proxy schemes a Windows proxy entry may name. socks
// means SOCKS4, as it does for WinINET and for Chrome.
var ieSchemes = map[string]proxyKind{
	"http":   httpProxy,
	"https":  httpsProxy,
	"socks":  socks4Proxy,
	"socks4": socks4Proxy,
	"socks5": socks5Proxy,
}

// ieProxyConfig is the current user's Windows Internet Options proxy
// configuration, which Chrome on Windows follows as well.
type ieProxyConfig struct {
	autoDetect    bool
	autoConfigURL string
	// proxy is one proxy for every scheme, or per-scheme entries such as
	// http=proxy:80;https=proxy:443;socks=proxy:1080.
	proxy string
	// proxyBypass lists hosts that connect directly; <local> stands for
	// host names without dots.
	proxyBypass string
}

// systemConfig converts the Internet Options configuration.
func (c ieProxyConfig) systemConfig() (systemConfig, []string) {
	cfg := systemConfig{autoConfigURL: strings.TrimSpace(c.autoConfigURL), autoDetect: c.autoDetect}
	var warnings []string
	for _, entry := range splitIEList(c.proxy) {
		servers, kind, address := &cfg.static.fallback, httpProxy, entry
		if scheme, rest, perScheme := strings.Cut(entry, "="); perScheme {
			address = rest
			switch strings.ToLower(scheme) {
			case "http":
				servers = &cfg.static.http
			case "https":
				// The secure proxy is an HTTP proxy that https requests tunnel through.
				servers = &cfg.static.https
			case "socks":
				kind = socks4Proxy
			default:
				continue // such as ftp, which browsers do not use
			}
		}
		server, _, err := parseProxyAddress(address, ieSchemes, kind)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("ignored the Windows proxy entry %q: %v", entry, err))
			continue
		}
		*servers = append(*servers, server)
	}
	for _, entry := range splitIEList(c.proxyBypass) {
		condition, err := bypassCondition(entry)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("ignored the Windows proxy exception %q: %v; requests to it use the proxy", entry, err))
			continue
		}
		cfg.static.bypass = append(cfg.static.bypass, condition)
	}
	return cfg, warnings
}

// splitIEList splits a Windows proxy or bypass list, which separates entries
// with semicolons or white space.
func splitIEList(list string) []string {
	return strings.FieldsFunc(list, func(r rune) bool { return r == ';' || unicode.IsSpace(r) })
}
