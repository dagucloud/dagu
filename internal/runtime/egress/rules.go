// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package egress holds the egress proxy of Spec 092, "The egress proxy". The
// command dagu egress-proxy runs it. The proxy forwards a connection to a listed
// destination only. It resolves the names itself, so a client that has no route
// and no DNS can still reach the destinations of its policy.
package egress

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Rule is one destination of the key network.egress: a host name with an
// optional port. The host can start with "*.". A rule with no port allows each
// port of the host.
type Rule struct {
	// Text is the rule as the operator wrote it.
	Text string
	// Host is the host name in lower case, without a leading "*." and without a
	// trailing dot.
	Host string
	// Wildcard is true for a rule that starts with "*.". It matches each
	// subdomain of Host, and not Host itself.
	Wildcard bool
	// Port is zero when the rule has no port.
	Port int
}

// ParseRule reads one destination. It accepts no scheme, no path and no
// whitespace. The port is a whole number from 1 to 65535.
func ParseRule(text string) (Rule, error) {
	rule := Rule{Text: text}
	host := text
	if h, p, ok := strings.Cut(text, ":"); ok {
		host = h
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
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" || strings.ContainsAny(host, " \t/\\@*[]") || strings.HasPrefix(host, ".") || strings.Contains(host, "..") {
		return Rule{}, fmt.Errorf("egress destination %q: expected a host name with an optional port, such as api.example.com:443", text)
	}
	if rule.Wildcard && net.ParseIP(host) != nil {
		return Rule{}, fmt.Errorf("egress destination %q: a wildcard needs a host name, not an address", text)
	}
	rule.Host = host
	return rule, nil
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
	host = strings.ToLower(strings.TrimSuffix(host, "."))
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
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, rule := range r.rules {
		if !rule.Wildcard && rule.matches(host, port) {
			return true
		}
	}
	return false
}
