// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package stringutil_test

import (
	"bufio"
	"context"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
)

func TestMatchPattern(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		val      string
		patterns []string
		want     bool
	}{
		{
			name:     "EmptyPatterns",
			val:      "test",
			patterns: []string{},
			want:     false,
		},
		{
			name:     "ExactLiteralMatch",
			val:      "test",
			patterns: []string{"test"},
			want:     true,
		},
		{
			name:     "NoLiteralMatch",
			val:      "test",
			patterns: []string{"foo", "bar"},
			want:     false,
		},
		{
			name:     "SimpleRegexMatch",
			val:      "test123",
			patterns: []string{"re:test\\d+"},
			want:     true,
		},
		{
			name:     "NoRegexMatch",
			val:      "test",
			patterns: []string{"re:\\d+"},
			want:     false,
		},
		{
			name:     "InvalidRegexPattern",
			val:      "test",
			patterns: []string{"re:["},
			want:     false,
		},
		{
			name:     "MixedPatternsWithLiteralMatch",
			val:      "test123",
			patterns: []string{"re:\\d+", "test123", "foo"},
			want:     true,
		},
		{
			name:     "MixedPatternsWithRegexMatch",
			val:      "test123",
			patterns: []string{"foo", "bar", "re:test\\d+"},
			want:     true,
		},
		{
			name:     "CaseSensitiveLiteralMatch",
			val:      "Test",
			patterns: []string{"test"},
			want:     false,
		},
		{
			name:     "CaseSensitiveRegexMatch",
			val:      "Test123",
			patterns: []string{"re:test\\d+"},
			want:     false,
		},
		{
			name:     "CaseInsensitiveRegexMatch",
			val:      "Test123",
			patterns: []string{"re:(?i)test\\d+"},
			want:     true,
		},
		{
			name:     "EmptyValue",
			val:      "",
			patterns: []string{"re:.*", ""},
			want:     true,
		},
		{
			name:     "SpecialCharactersInLiteralMatch",
			val:      "test.123",
			patterns: []string{"test.123"},
			want:     true,
		},
		{
			name:     "SpecialCharactersInRegexMatch",
			val:      "test.123",
			patterns: []string{"re:test\\.\\d+"},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringutil.MatchPattern(ctx, tt.val, tt.patterns)
			if got != tt.want {
				t.Errorf("MatchString() = %v, want %v", got, tt.want)
			}

			// Test scanner version with the same input
			scanner := bufio.NewScanner(strings.NewReader(tt.val))
			got = stringutil.MatchPatternScanner(ctx, scanner, tt.patterns)
			if got != tt.want {
				t.Errorf("MatchPatternScanner() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchPattern_LongLines(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		size     int
		pattern  string
		expected bool
	}{
		{
			name:     "UnderDefaultBufferLimit",
			size:     50_000,
			pattern:  "re:.+",
			expected: true,
		},
		{
			name:     "OverDefaultBufferLimitUserSCase",
			size:     78_000,
			pattern:  "re:.+",
			expected: true,
		},
		{
			name:     "EmptyStringDoesnTMatchRegex",
			size:     0,
			pattern:  "re:.+",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := strings.Repeat("x", tt.size)
			result := stringutil.MatchPattern(ctx, content, []string{tt.pattern})

			if result != tt.expected {
				t.Errorf("MatchPattern() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestMatchPattern_WithMaxBufferSize(t *testing.T) {
	ctx := context.Background()

	// Test with 2MB line and custom buffer size
	content := strings.Repeat("x", 2_000_000) // 2MB

	// Should fail with default 1MB buffer
	result := stringutil.MatchPattern(ctx, content, []string{"re:.+"})
	if result {
		t.Error("Expected match to fail with default 1MB buffer")
	}

	// Should succeed with 3MB buffer
	result = stringutil.MatchPattern(ctx, content, []string{"re:.+"}, stringutil.WithMaxBufferSize(3*1024*1024))
	if !result {
		t.Error("Expected match to succeed with 3MB buffer")
	}
}
