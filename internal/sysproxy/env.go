// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sysproxy

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// envSchemes are the proxy schemes a proxy environment variable may name.
var envSchemes = map[string]proxyKind{
	"http":    httpProxy,
	"https":   httpsProxy,
	"socks":   socks5Proxy,
	"socks5":  socks5Proxy,
	"socks5h": socks5Proxy,
	"socks4":  socks4Proxy,
	"socks4a": socks4Proxy,
}

// envProxyConfig holds the proxy environment variables.
type envProxyConfig struct {
	httpProxy  string
	httpsProxy string
	allProxy   string
	noProxy    string
}

// readEnvProxyConfig reads the proxy environment variables. A lower-case
// name takes precedence over its upper-case form, as it does for Go's HTTP
// client.
func readEnvProxyConfig(getenv func(string) string) envProxyConfig {
	first := func(names ...string) string {
		for _, name := range names {
			if value := strings.TrimSpace(getenv(name)); value != "" {
				return value
			}
		}
		return ""
	}
	return envProxyConfig{
		httpProxy:  first("http_proxy", "HTTP_PROXY"),
		httpsProxy: first("https_proxy", "HTTPS_PROXY"),
		allProxy:   first("all_proxy", "ALL_PROXY"),
		noProxy:    first("no_proxy", "NO_PROXY"),
	}
}

// hasProxy reports whether any variable names a proxy.
func (c envProxyConfig) hasProxy() bool {
	return c.httpProxy != "" || c.httpsProxy != "" || c.allProxy != ""
}

// staticConfig converts the variables: HTTP_PROXY and HTTPS_PROXY serve their
// schemes, ALL_PROXY serves the other requests, and NO_PROXY lists hosts
// that connect directly.
func (c envProxyConfig) staticConfig() (staticConfig, []string) {
	var cfg staticConfig
	var warnings []string
	for _, v := range []struct {
		name    string
		value   string
		servers *[]proxyServer
	}{
		{name: "HTTP_PROXY", value: c.httpProxy, servers: &cfg.http},
		{name: "HTTPS_PROXY", value: c.httpsProxy, servers: &cfg.https},
		{name: "ALL_PROXY", value: c.allProxy, servers: &cfg.fallback},
	} {
		if v.value == "" {
			continue
		}
		server, withCredentials, err := parseProxyAddress(v.value, envSchemes, httpProxy)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("ignored %s: %v", v.name, err))
			continue
		}
		if withCredentials {
			warnings = append(warnings, fmt.Sprintf("%s includes a user name or password, which the browser cannot send to the proxy", v.name))
		}
		*v.servers = append(*v.servers, server)
	}
	conditions, bypassAll, noProxyWarnings := noProxyConditions(c.noProxy)
	warnings = append(warnings, noProxyWarnings...)
	if bypassAll {
		return staticConfig{}, warnings
	}
	cfg.bypass = conditions
	return cfg, warnings
}

// noProxyConditions converts comma-separated NO_PROXY entries as Go's HTTP
// client reads them: * for every host, an IP address or range, a domain
// that matches itself and its subdomains, or, with a leading . or *., a
// domain that matches only its subdomains.
func noProxyConditions(noProxy string) (conditions []string, all bool, warnings []string) {
	for entry := range strings.SplitSeq(noProxy, ",") {
		entry = strings.TrimSpace(entry)
		host := strings.ToLower(entry)
		switch host {
		case "":
			continue
		case "*":
			return nil, true, warnings
		}
		if prefix, err := netip.ParsePrefix(host); err == nil {
			if !prefix.Addr().Is4() {
				warnings = append(warnings, fmt.Sprintf("ignored the NO_PROXY entry %q: IPv6 ranges are not supported", entry))
				continue
			}
			conditions = append(conditions, networkCondition(prefix.Masked()))
			continue
		}
		if h, _, err := net.SplitHostPort(host); err == nil {
			warnings = append(warnings, fmt.Sprintf("the NO_PROXY entry %q names a port, which is ignored: %s connects directly on every port", entry, h))
			host = h
		}
		if addr, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
			conditions = append(conditions, hostCondition(addr.String()))
			continue
		}
		domain, subdomainsOnly := strings.CutPrefix(host, ".")
		if wildcard, ok := strings.CutPrefix(host, "*."); ok {
			domain, subdomainsOnly = wildcard, true
		}
		if !hostNamePattern.MatchString(domain) {
			warnings = append(warnings, fmt.Sprintf("ignored the NO_PROXY entry %q: it is not a host name, IP address or range", entry))
			continue
		}
		if !subdomainsOnly {
			conditions = append(conditions, hostCondition(domain))
		}
		conditions = append(conditions, domainCondition("."+domain))
	}
	return conditions, false, warnings
}
