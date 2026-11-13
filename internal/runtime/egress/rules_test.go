// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package egress_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/runtime/egress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A rule is a host name with an optional port. A wildcard covers the subdomains of
// a name and not the name itself. A rule with no port allows each port.
func TestRulesAllow(t *testing.T) {
	t.Parallel()

	rules, err := egress.ParseRules([]string{"api.example.com:443", "*.cdn.example.net", "Registry.Example.org.", "10.1.2.3:8080"})
	require.NoError(t, err)

	for _, tc := range []struct {
		host string
		port int
		want bool
	}{
		{"api.example.com", 443, true},
		{"API.example.com", 443, true},
		{"api.example.com.", 443, true},
		{"api.example.com", 80, false},
		{"example.com", 443, false},
		{"evil-api.example.com", 443, false},
		{"api.example.com.evil.test", 443, false},
		{"a.cdn.example.net", 1, true},
		{"a.b.cdn.example.net", 65535, true},
		{"cdn.example.net", 443, false},
		{"xcdn.example.net", 443, false},
		{"registry.example.org", 5000, true},
		{"10.1.2.3", 8080, true},
		{"10.1.2.3", 80, false},
		{"10.1.2.4", 8080, false},
		{"", 443, false},
	} {
		assert.Equal(t, tc.want, rules.Allows(tc.host, tc.port), "%s:%d", tc.host, tc.port)
	}
}

// The check never resolves a name, so a name that resolves to a listed address is
// not listed.
func TestRulesDoNotResolveNames(t *testing.T) {
	t.Parallel()

	rules, err := egress.ParseRules([]string{"10.1.2.3"})
	require.NoError(t, err)
	assert.True(t, rules.Allows("10.1.2.3", 1234))
	assert.False(t, rules.Allows("other.example.com", 1234))
}

func TestParseRuleRejectsWhatIsNotADestination(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"", ":443", "api.example.com:", "api.example.com:0", "api.example.com:65536", "api.example.com:-1",
		"api.example.com:0443", "api.example.com:https", "https://api.example.com", "api.example.com/path",
		"api example.com", "*.", "*.*.example.com", "..example.com", "a..b", "*.10.0.0.1", "user@example.com",
	} {
		_, err := egress.ParseRule(text)
		assert.Error(t, err, "%q", text)
	}
	rule, err := egress.ParseRule("*.Example.COM:8443")
	require.NoError(t, err)
	assert.Equal(t, egress.Rule{Text: "*.Example.COM:8443", Host: "example.com", Wildcard: true, Port: 8443}, rule)
}

func TestRulesListKeepsTheText(t *testing.T) {
	t.Parallel()

	rules, err := egress.ParseRules([]string{"b.example.com:1", "a.example.com"})
	require.NoError(t, err)
	assert.Equal(t, []string{"b.example.com:1", "a.example.com"}, rules.List())
	assert.Equal(t, 2, rules.Len())
	_, err = egress.ParseRules([]string{"ok.example.com", "bad host"})
	require.Error(t, err)
}
