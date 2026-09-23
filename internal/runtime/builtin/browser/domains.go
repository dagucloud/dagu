// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const wildcardPrefix = "*."

// domainLabelPattern matches one DNS label, as the browser runtime requires.
var domainLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// validateDomainPattern rejects an allowed_domains entry the browser runtime
// would reject: a host with at least two labels, optionally prefixed by *.
func validateDomainPattern(pattern string) error {
	host := canonicalHost(pattern)
	if strings.ContainsAny(host, "/:?#") {
		return fmt.Errorf("allowed domain %q must be a host name without a scheme, port, or path", pattern)
	}
	host = strings.TrimPrefix(host, wildcardPrefix)
	if strings.Contains(host, "*") {
		return fmt.Errorf("allowed domain %q may use * only as a leading *.", pattern)
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return fmt.Errorf("allowed domain %q must have at least two labels, such as example.com", pattern)
	}
	for _, label := range labels {
		if !domainLabelPattern.MatchString(label) {
			return fmt.Errorf("allowed domain %q is not a valid host name", pattern)
		}
	}
	return nil
}

// checkAllowedDomain rejects a URL outside allowed, using the browser
// runtime's rules: example.com matches only that host, and *.example.com
// matches its subdomains. URLs without a network host, such as about:blank
// or data:, are not checked.
func checkAllowedDomain(target string, allowed []string) error {
	if len(allowed) == 0 {
		return nil
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", target, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil
	}
	host := canonicalHost(parsed.Hostname())
	for _, pattern := range allowed {
		pattern = canonicalHost(pattern)
		if domain, wildcard := strings.CutPrefix(pattern, wildcardPrefix); wildcard {
			if strings.HasSuffix(host, "."+domain) {
				return nil
			}
			continue
		}
		if host == pattern {
			return nil
		}
	}
	return fmt.Errorf("%s is outside browser.allowed_domains", parsed.Host)
}

func canonicalHost(host string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(host)), ".")
}
