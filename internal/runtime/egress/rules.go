// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package egress holds the egress proxy that the command dagu egress-proxy
// runs. The proxy forwards a connection to a listed destination only. It
// resolves the names itself, so a client that has no route and no DNS can
// still reach the destinations of its policy.
package egress

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/idna"
)

// Rule is one destination of the allow list: a host name or an address,
// with an optional port. A host name can start with "*.". A rule with no port
// allows each port of the host.
type Rule struct {
	// Text is the rule as the operator wrote it.
	Text string
	// Host is the host name in lower case and in its IDNA ASCII form, without a
	// leading "*." and without a trailing dot, or the address in its shortest
	// form.
	Host string
	// Wildcard is true for a rule that starts with "*.". It matches each
	// subdomain of Host, and not Host itself.
	Wildcard bool
	// Port is zero when the rule has no port.
	Port int
}

// ParseRule reads one destination. An IPv6 address with a port is in brackets,
// such as [2001:db8::1]:443. A host name can be in Unicode or in its IDNA ASCII
// form. It accepts no scheme, no path, no whitespace and no control character.
// The port is a whole number from 1 to 65535.
func ParseRule(text string) (Rule, error) {
	rule := Rule{Text: text}
	invalid := fmt.Errorf("egress destination %q: expected a host name with an optional port, such as api.example.com:443", text)
	host, rest, ok := splitRule(text)
	if !ok {
		return Rule{}, invalid
	}
	if p, hasPort := strings.CutPrefix(rest, ":"); hasPort {
		port, err := strconv.Atoi(p)
		if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != p {
			return Rule{}, fmt.Errorf("egress destination %q: the port must be a whole number from 1 to 65535, got %q", text, p)
		}
		rule.Port = port
	}
	if strings.HasPrefix(host, "*.") {
		rule.Wildcard = true
		host = strings.TrimPrefix(host, "*.")
	}
	if strings.ContainsAny(host, "/\\@*[]") || strings.ContainsFunc(host, notHostRune) {
		return Rule{}, invalid
	}
	// IDNA maps the name before it is lowercased, as in normalizeHost.
	host, err := asciiHost(host)
	if err != nil {
		return Rule{}, fmt.Errorf("egress destination %q: the host name is not a valid internationalized name", text)
	}
	host = strings.ToLower(host)
	// IDNA maps dots such as U+3002 to ".", so the one optional root dot is
	// removed after the mapping.
	host = strings.TrimSuffix(host, ".")
	if host == "" || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return Rule{}, invalid
	}
	if rule.Wildcard && net.ParseIP(host) != nil {
		return Rule{}, fmt.Errorf("egress destination %q: a wildcard needs a host name, not an address", text)
	}
	rule.Host = canonicalAddr(host)
	return rule, nil
}

// splitRule splits a destination into its host and the rest, which is empty or
// a colon and the port. An IPv6 address has colons: with a port it is in
// brackets, and without one it stands alone. ok is false for a form that is
// neither.
func splitRule(text string) (host, rest string, ok bool) {
	if inner, bracketed := strings.CutPrefix(text, "["); bracketed {
		addr, after, closed := strings.Cut(inner, "]")
		if !closed || !isIPv6(addr) || (after != "" && !strings.HasPrefix(after, ":")) {
			return "", "", false
		}
		return addr, after, true
	}
	if strings.Count(text, ":") > 1 {
		return text, "", isIPv6(text)
	}
	if i := strings.Index(text, ":"); i >= 0 {
		return text[:i], text[i:], true
	}
	return text, "", true
}

func isIPv6(text string) bool {
	return strings.Contains(text, ":") && net.ParseIP(text) != nil
}

// notHostRune reports a rune that no host name or address has.
func notHostRune(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r)
}

// asciiHost gives a host name with letters outside ASCII in its IDNA ASCII form,
// which the HTTP transport dials and clients send. An ASCII name stays as it
// is.
func asciiHost(host string) (string, error) {
	if !strings.ContainsFunc(host, func(r rune) bool { return r > unicode.MaxASCII }) {
		return host, nil
	}
	return idna.Lookup.ToASCII(host)
}

// canonicalAddr gives an address in its shortest form, so that two spellings of
// one IPv6 address match. A host name stays as it is.
func canonicalAddr(host string) string {
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return host
}

// normalizeHost puts a host of a request in the form of Rule.Host. A name that
// has no IDNA ASCII form stays in Unicode and matches no rule. IDNA maps the
// name before it is lowercased, so the name that matches is the name that the
// proxy resolves: Go lowercases U+0130 to "i", where IDNA keeps another label.
func normalizeHost(host string) string {
	if ascii, err := asciiHost(host); err == nil {
		host = strings.ToLower(ascii)
	}
	return canonicalAddr(strings.TrimSuffix(host, "."))
}

// matches reports whether the rule allows the host and the port. The host is in
// lower case, with no trailing dot.
func (r Rule) matches(host string, port int) bool {
	if r.Port != 0 && r.Port != port {
		return false
	}
	if r.Wildcard {
		return len(host) > len(r.Host)+1 && strings.HasSuffix(host, "."+r.Host)
	}
	return host == r.Host
}

// overlaps reports whether one host and port can match both rules. A rule with
// no port overlaps each port. A wildcard overlaps each name below its host and
// each wildcard below or above it.
func (r Rule) overlaps(other Rule) bool {
	if r.Port != 0 && other.Port != 0 && r.Port != other.Port {
		return false
	}
	below := func(name, host string) bool { return strings.HasSuffix(name, "."+host) }
	switch {
	case !r.Wildcard && !other.Wildcard:
		return r.Host == other.Host
	case r.Wildcard && !other.Wildcard:
		return below(other.Host, r.Host)
	case !r.Wildcard && other.Wildcard:
		return below(r.Host, other.Host)
	default:
		return r.Host == other.Host || below(r.Host, other.Host) || below(other.Host, r.Host)
	}
}

// Rules is the allow list of a policy. The zero value allows nothing.
type Rules struct {
	rules []Rule
}

// ParseRules reads a list of destinations. The first invalid entry stops it.
func ParseRules(list []string) (*Rules, error) {
	rules := &Rules{rules: make([]Rule, 0, len(list))}
	for _, text := range list {
		rule, err := ParseRule(text)
		if err != nil {
			return nil, err
		}
		rules.rules = append(rules.rules, rule)
	}
	return rules, nil
}

// Len returns the number of rules.
func (r *Rules) Len() int { return len(r.rules) }

// List returns the rules as they were written.
func (r *Rules) List() []string {
	list := make([]string, 0, len(r.rules))
	for _, rule := range r.rules {
		list = append(list, rule.Text)
	}
	return list
}

// Allows reports whether the policy lets a connection go to the host and the
// port. The host is a name or an address, as the client wrote it. An address
// matches a rule that has the same address, and a name matches a rule that has
// the same name or a wildcard that covers it. The check never resolves a name:
// a name that resolves to a listed address is still not listed.
func (r *Rules) Allows(host string, port int) bool {
	host = normalizeHost(host)
	for _, rule := range r.rules {
		if rule.matches(host, port) {
			return true
		}
	}
	return false
}

// AllowsExactly reports whether a rule that is not a wildcard lets a connection
// go to the host and the port.
func (r *Rules) AllowsExactly(host string, port int) bool {
	if r == nil {
		return false
	}
	host = normalizeHost(host)
	for _, rule := range r.rules {
		if !rule.Wildcard && rule.matches(host, port) {
			return true
		}
	}
	return false
}
