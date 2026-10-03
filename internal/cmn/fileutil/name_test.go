// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSafeName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "AlreadySafeString",
			input:    "simple_file-name123",
			expected: "simple_file-name123",
		},
		{
			name:     "StringWithSpaces",
			input:    "file name with spaces",
			expected: "file_name_with_spaces",
		},
		{
			name:     "StringWithSpecialCharacters",
			input:    "file!@#$%^&*()name.txt",
			expected: "file__________name_txt",
		},
		{
			name:     "StringWithWindowsReservedFilename",
			input:    "CON.txt",
			expected: "CON_txt",
		},
		{
			name:     "StringWithPathLikeCharacters",
			input:    "path/to\\file:name",
			expected: "path_to_file_name",
		},
		{
			name:     "EmptyString",
			input:    "",
			expected: "",
		},
		{
			name:     "VeryLongString",
			input:    strings.Repeat("a", 200),
			expected: strings.Repeat("a", MaxSafeNameLength),
		},
		{
			name:     "StringWithMixedCharacters",
			input:    "File Name 123!@#.txt",
			expected: "File_Name_123____txt",
		},
		{
			name:     "StringWithLeadingAndTrailingSpaces",
			input:    " filename ",
			expected: "_filename_",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SafeName(tt.input)
			if result != tt.expected {
				t.Errorf("SafeName(%q) = %q, expected %q", tt.input, result, tt.expected)
			}

			// Verify that the result only contains allowed characters
			if !isAllowedCharsOnly(result) {
				t.Errorf("SafeName(%q) = %q, contains disallowed characters", tt.input, result)
			}
		})
	}
}

// Helper function to verify that a string only contains allowed characters
func isAllowedCharsOnly(s string) bool {
	for _, r := range s {
		// nolint:staticcheck
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// TestEdgeCases tests some additional edge cases
func TestEdgeCases(t *testing.T) {
	// Test that strings with only disallowed characters work correctly
	result := SafeName("!@#$%^&*()")
	if result != "__________" {
		t.Errorf("SafeName('!@#$%%^&*()') = %q, expected '__________'", result)
	}

	// Test truncation at exactly the maximum length
	exactLengthInput := strings.Repeat("a", MaxSafeNameLength)
	result = SafeName(exactLengthInput)
	if len(result) != MaxSafeNameLength {
		t.Errorf("SafeName with exact max length returned incorrect length: got %d, want %d",
			len(result), MaxSafeNameLength)
	}

	// Test truncation with one character over the limit
	overLengthInput := strings.Repeat("a", MaxSafeNameLength+1)
	result = SafeName(overLengthInput)
	if len(result) != MaxSafeNameLength {
		t.Errorf("SafeName with over max length returned incorrect length: got %d, want %d",
			len(result), MaxSafeNameLength)
	}
}

func TestNormalizeFilename(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		replacement string
		expected    string
	}{
		{"EmptyString", "", "-", ""},
		{"NormalName", "normal-name", "-", "normal-name"},
		{"Spaces", "hello world", "-", "hello-world"},
		{"ReservedChars", "<>:\"|?*", "-", "-------"},
		{"BackslashForwardSlash", "a/b\\c", "-", "a-b-c"},
		{"ControlChars", "a\x00b\x1Fc", "-", "a-b-c"},
		{"WindowsReservedCON", "CON", "-", "-"},
		{"WindowsReservedPRN", "PRN", "_", "_"},
		{"WindowsReservedAUX", "AUX", "-", "-"},
		{"WindowsReservedNUL", "NUL", "-", "-"},
		{"WindowsReservedCOM1", "COM1", "-", "-"},
		{"WindowsReservedLPT1", "LPT1", "-", "-"},
		{"WindowsReservedCaseInsensitive", "con", "-", "-"},
		{"WindowsReservedWithExtension", "CON.txt", "-", "-.txt"},
		{"WindowsReservedCOM1WithExt", "COM1.txt", "-", "-.txt"},
		{"COM0NotReserved", "COM0", "-", "COM0"},
		{"LPT0NotReserved", "LPT0", "-", "LPT0"},
		{"MixedReservedAndNormal", "mix<ed>name", "-", "mix-ed-name"},
		{"MultipleSpaces", "a  b  c", "-", "a--b--c"},
		{"UnderscoreReplacement", "hello world", "_", "hello_world"},
		{"TrailingDot", "foo.", "-", "foo-"},
		{"MultipleTrailingDots", "name...", "-", "name-"},
		{"SingleDot", ".", "-", "-"},
		{"OnlyDots", "...", "-", "-"},
		{"OnlyDotsUnderscoreReplacement", "..", "_", "_"},
		{"InteriorDotsKept", "a..b", "-", "a..b"},
		{"ReservedNameTrailingDot", "CON.", "-", "CON-"},
		{"ExtensionTrailingDot", "foo.txt.", "-", "foo.txt-"},
		{"TrailingSpace", "foo ", "-", "foo-"},
		{"MultipleTrailingSpaces", "foo  ", "-", "foo-"},
		{"TrailingDotAndSpace", "foo. ", "-", "foo-"},
		{"TrailingSpaceAndDot", "foo .", "-", "foo-"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeFilename(tt.input, tt.replacement)
			require.Equal(t, tt.expected, result, "NormalizeFilename(%q, %q)", tt.input, tt.replacement)
		})
	}
}
