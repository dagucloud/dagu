// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package hostpattern matches host names against patterns: an exact host,
// *.example.com for every subdomain of example.com, or * for every host.
package hostpattern

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

const (
	// Any matches every host.
	Any = "*"
	// WildcardPrefix starts a pattern that matches subdomains.
	WildcardPrefix = "*."
)

// labelPattern matches one DNS label, letters and digits with inner hyphens;
// an internationalized label is written in its xn-- form.
var labelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// Canonical returns host lower-cased, without surrounding spaces, trailing
// dots, or the brackets of an IPv6 literal.
func Canonical(host string) string {
	host = strings.TrimRight(strings.ToLower(strings.TrimSpace(host)), ".")
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	return host
}

// Validate rejects a pattern that is not *, *. followed by a host name, a
// host name, or an IP address.
func Validate(pattern string) error {
	p := Canonical(pattern)
	switch {
	case p == "":
		return fmt.Errorf("host pattern %q is empty", pattern)
	case p == Any:
		return nil
	case strings.HasPrefix(p, WildcardPrefix):
		domain := strings.TrimPrefix(p, WildcardPrefix)
		if _, err := netip.ParseAddr(domain); err == nil {
			return fmt.Errorf("host pattern %q may not use * with an IP address", pattern)
		}
		if !validName(domain) {
			return fmt.Errorf("host pattern %q must be *. followed by a host name, such as *.example.com", pattern)
		}
		return nil
	}
	if _, err := netip.ParseAddr(p); err == nil {
		return nil
	}
	if strings.ContainsAny(p, "/:?#@") {
		return fmt.Errorf("host pattern %q must be a host name without a scheme, port, or path", pattern)
	}
	if strings.Contains(p, "*") {
		return fmt.Errorf("host pattern %q may use * only alone or as its first label, as in *.example.com", pattern)
	}
	if !validName(p) {
		return fmt.Errorf("host pattern %q is not a valid host name", pattern)
	}
	return nil
}

// Match reports whether host matches pattern. A *. pattern matches the
// subdomains of its domain but not the domain itself.
func Match(host, pattern string) bool {
	host, pattern = Canonical(host), Canonical(pattern)
	if host == "" {
		return false
	}
	if pattern == Any {
		return true
	}
	if domain, wildcard := strings.CutPrefix(pattern, WildcardPrefix); wildcard {
		return strings.HasSuffix(host, "."+domain)
	}
	if hostAddr, err := netip.ParseAddr(host); err == nil {
		patternAddr, err := netip.ParseAddr(pattern)
		return err == nil && hostAddr.Unmap() == patternAddr.Unmap()
	}
	return host == pattern
}

// MatchAny reports whether host matches any of patterns.
func MatchAny(host string, patterns []string) bool {
	for _, pattern := range patterns {
		if Match(host, pattern) {
			return true
		}
	}
	return false
}

func validName(name string) bool {
	for label := range strings.SplitSeq(name, ".") {
		if !labelPattern.MatchString(label) {
			return false
		}
	}
	return true
}
