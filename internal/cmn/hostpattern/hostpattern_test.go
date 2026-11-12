// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package hostpattern

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidate(t *testing.T) {
	t.Parallel()
	for _, pattern := range []string{
		"*", "example.com", "Example.COM.", "intranet", "*.corp", "*.example.co.jp",
		"xn--wgv71a119e.jp", "10.0.0.1", "::1", "[fe80::1]",
	} {
		assert.NoError(t, Validate(pattern), pattern)
	}
	for _, pattern := range []string{
		"", " ", "https://example.com", "example.com:443", "example.com/path",
		"*.10.0.0.1", "a.*.com", "**", "*example.com", "exa mple.com", "-bad.com", "user@example.com",
	} {
		assert.Error(t, Validate(pattern), pattern)
	}
}

func TestMatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		host, pattern string
		want          bool
	}{
		{"portal.example.com", "*", true},
		{"10.0.0.1", "*", true},
		{"", "*", false},
		{"portal.example.com", "portal.example.com", true},
		{"PORTAL.example.com.", "portal.example.com", true},
		{"other.example.com", "portal.example.com", false},
		{"a.example.com", "*.example.com", true},
		{"a.b.example.com", "*.example.com", true},
		{"example.com", "*.example.com", false},
		{"badexample.com", "*.example.com", false},
		{"intranet", "intranet", true},
		{"10.0.0.1", "10.0.0.1", true},
		{"[::1]", "::1", true},
		{"::ffff:10.0.0.1", "10.0.0.1", true},
		{"10.0.0.2", "10.0.0.1", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, Match(tt.host, tt.pattern), "%s vs %s", tt.host, tt.pattern)
	}
}

func TestMatchAny(t *testing.T) {
	t.Parallel()
	patterns := []string{"portal.example.com", "*.corp.example"}
	assert.True(t, MatchAny("x.corp.example", patterns))
	assert.False(t, MatchAny("example.com", patterns))
	assert.False(t, MatchAny("example.com", nil))
}
