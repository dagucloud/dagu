// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sysproxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// directPAC sends every request straight to its destination.
const directPAC = `function FindProxyForURL(url, host) {
  return "DIRECT";
}
`

const routeDirect = "DIRECT"

// Host conditions shared by every generated script.
const (
	// loopbackCondition matches the loopback hosts Chrome never proxies.
	loopbackCondition = `host == "localhost" || shExpMatch(host, "127.*") || host == "::1" || host == "[::1]"`
	// plainHostCondition matches host names without dots, such as intranet.
	plainHostCondition = "isPlainHostName(host)"
	// ipv4LiteralCondition keeps isInNet from resolving host names: bypass
	// ranges in proxy settings match only URLs that name an address.
	ipv4LiteralCondition = `/^\d+\.\d+\.\d+\.\d+$/.test(host)`
)

// hostNamePattern matches a DNS host name. Underscores are allowed because
// some intranet names use them.
var hostNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*\.?$`)

// shellPatternPattern matches a host shell pattern such as *.example.com.
var shellPatternPattern = regexp.MustCompile(`^[a-z0-9_.*?-]+$`)

// proxyKind is a kind of proxy server: the PAC keyword that names it and the
// port it listens on unless told otherwise.
type proxyKind struct {
	keyword     string
	defaultPort string
}

var (
	httpProxy   = proxyKind{keyword: "PROXY", defaultPort: "80"}
	httpsProxy  = proxyKind{keyword: "HTTPS", defaultPort: "443"}
	socks4Proxy = proxyKind{keyword: "SOCKS", defaultPort: "1080"}
	socks5Proxy = proxyKind{keyword: "SOCKS5", defaultPort: "1080"}
)

// proxyServer is one proxy server in a PAC result.
type proxyServer struct {
	kind proxyKind
	host string
	port string
}

// newProxyServer checks host and port; an empty port is kind's default.
func newProxyServer(kind proxyKind, host, port string) (proxyServer, error) {
	host = strings.Trim(host, "[]")
	if _, err := netip.ParseAddr(host); err != nil && !hostNamePattern.MatchString(host) {
		return proxyServer{}, fmt.Errorf("%q is not a host name or IP address", host)
	}
	if port == "" {
		port = kind.defaultPort
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return proxyServer{}, fmt.Errorf("%q is not a port number", port)
	}
	return proxyServer{kind: kind, host: strings.TrimSuffix(host, "."), port: port}, nil
}

func (p proxyServer) String() string {
	return p.kind.keyword + " " + net.JoinHostPort(p.host, p.port)
}

// parseProxyAddress parses [scheme://]host[:port]. schemes gives the kind of
// proxy each accepted scheme names; an address without a scheme is of kind
// fallback. withCredentials reports a user name or password in the address,
// which errors never repeat.
func parseProxyAddress(value string, schemes map[string]proxyKind, fallback proxyKind) (server proxyServer, withCredentials bool, err error) {
	kind, authority := fallback, value
	if scheme, rest, ok := strings.Cut(value, "://"); ok {
		known, supported := schemes[strings.ToLower(scheme)]
		if !supported {
			return proxyServer{}, false, fmt.Errorf("proxy scheme %q is not supported", scheme)
		}
		kind, authority = known, rest
	}
	u, err := url.Parse("//" + authority)
	if err != nil {
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}
		return proxyServer{}, false, err
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return proxyServer{}, false, errors.New("a proxy address is [scheme://]host[:port], without a path")
	}
	if u.Hostname() == "" {
		return proxyServer{}, false, errors.New("the proxy address has no host")
	}
	server, err = newProxyServer(kind, u.Hostname(), u.Port())
	return server, u.User != nil, err
}

// staticConfig routes requests by fixed rules instead of a script.
type staticConfig struct {
	bypass   []string      // PAC conditions on host under which requests go DIRECT
	http     []proxyServer // proxies for http URLs
	https    []proxyServer // proxies for https URLs
	fallback []proxyServer // proxies for URLs without a scheme-specific proxy
}

// pac returns a script that sends bypassed and loopback hosts DIRECT, then
// uses the scheme-specific proxies, then the fallback proxies, then DIRECT.
// ws: and wss: URLs use the fallback proxies, else the https: ones, else the
// http: ones, as Chrome does with fixed proxy rules, following RFC 6455.
func (c staticConfig) pac() string {
	if len(c.http) == 0 && len(c.https) == 0 && len(c.fallback) == 0 {
		return directPAC
	}
	var b strings.Builder
	b.WriteString("function FindProxyForURL(url, host) {\n")
	for _, condition := range append([]string{loopbackCondition}, c.bypass...) {
		fmt.Fprintf(&b, "  if (%s) return %s;\n", condition, jsString(routeDirect))
	}
	if len(c.https) > 0 {
		fmt.Fprintf(&b, "  if (shExpMatch(url, \"https:*\")) return %s;\n", jsString(route(c.https)))
	}
	if len(c.http) > 0 {
		fmt.Fprintf(&b, "  if (shExpMatch(url, \"http:*\")) return %s;\n", jsString(route(c.http)))
	}
	if len(c.fallback) == 0 {
		websocket := c.https
		if len(websocket) == 0 {
			websocket = c.http
		}
		fmt.Fprintf(&b, "  if (shExpMatch(url, \"ws:*\") || shExpMatch(url, \"wss:*\")) return %s;\n", jsString(route(websocket)))
	}
	fmt.Fprintf(&b, "  return %s;\n}\n", jsString(route(c.fallback)))
	return b.String()
}

// route is the PAC result that tries servers in order, or DIRECT for none.
func route(servers []proxyServer) string {
	if len(servers) == 0 {
		return routeDirect
	}
	parts := make([]string, len(servers))
	for i, server := range servers {
		parts[i] = server.String()
	}
	return strings.Join(parts, "; ")
}

// bypassCondition converts a macOS or Windows bypass entry: a host, an IP
// address, a shell pattern such as *.local, a domain with a leading dot, an
// IPv4 range such as 169.254/16, or <local> for host names without dots.
func bypassCondition(entry string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(entry))
	switch {
	case e == "<local>":
		return plainHostCondition, nil
	case strings.Contains(e, "/"):
		prefix, err := parseIPv4Range(e)
		if err != nil {
			return "", err
		}
		return networkCondition(prefix), nil
	case strings.HasPrefix(e, "."):
		e = "*" + e
	}
	if strings.ContainsAny(e, "*?") {
		if !shellPatternPattern.MatchString(e) {
			return "", fmt.Errorf("%q is not a host pattern", entry)
		}
		return shellPatternCondition(e), nil
	}
	if addr, err := netip.ParseAddr(strings.Trim(e, "[]")); err == nil {
		return hostCondition(addr.String()), nil
	}
	if !hostNamePattern.MatchString(e) {
		return "", fmt.Errorf("%q is not a host, pattern or address range", entry)
	}
	return hostCondition(strings.TrimSuffix(e, ".")), nil
}

// parseIPv4Range parses an IPv4 range, accepting the short form macOS uses
// where omitted trailing octets are zero, as in 169.254/16.
func parseIPv4Range(s string) (netip.Prefix, error) {
	addr, bits, _ := strings.Cut(s, "/")
	if !strings.Contains(addr, ":") {
		octets := strings.Split(addr, ".")
		for len(octets) < net.IPv4len {
			octets = append(octets, "0")
		}
		addr = strings.Join(octets, ".")
	}
	prefix, err := netip.ParsePrefix(addr + "/" + bits)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is not an address range", s)
	}
	if !prefix.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("%q: IPv6 ranges are not supported", s)
	}
	return prefix.Masked(), nil
}

func hostCondition(host string) string {
	return "host == " + jsString(host)
}

// domainCondition matches the subdomains of suffix, which starts with a dot.
func domainCondition(suffix string) string {
	return "dnsDomainIs(host, " + jsString(suffix) + ")"
}

func shellPatternCondition(pattern string) string {
	return "shExpMatch(host, " + jsString(pattern) + ")"
}

// networkCondition matches IPv4 literal hosts inside prefix.
func networkCondition(prefix netip.Prefix) string {
	mask := net.IP(net.CIDRMask(prefix.Bits(), net.IPv4len*8)).String()
	return fmt.Sprintf("%s && isInNet(host, %s, %s)", ipv4LiteralCondition,
		jsString(prefix.Addr().String()), jsString(mask))
}

// jsString quotes s as a JavaScript string literal.
func jsString(s string) string {
	quoted, _ := json.Marshal(s) // a string always marshals
	return string(quoted)
}
