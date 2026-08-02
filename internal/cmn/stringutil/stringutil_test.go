// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package stringutil_test

import (
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/stretchr/testify/require"
)

func Test_FormatTime(t *testing.T) {
	t.Run("Valid", func(t *testing.T) {
		tm := time.Date(2022, 2, 1, 2, 2, 2, 0, time.UTC)
		formatted := stringutil.FormatTime(tm)
		require.Equal(t, "2022-02-01T02:02:02Z", formatted)

		parsed, err := stringutil.ParseTime(formatted)
		require.NoError(t, err)
		require.Equal(t, tm, parsed)

		// Test empty time
		require.Equal(t, "", stringutil.FormatTime(time.Time{}))
		parsed, err = stringutil.ParseTime("-")
		require.NoError(t, err)
		require.Equal(t, time.Time{}, parsed)
	})
	t.Run("Empty", func(t *testing.T) {
		// Test empty time
		require.Equal(t, "", stringutil.FormatTime(time.Time{}))
	})
}

func Test_ParseTime(t *testing.T) {
	t.Run("Valid", func(t *testing.T) {
		parsed, err := stringutil.ParseTime("2022-02-01T02:02:02Z")
		require.NoError(t, err)
		require.Equal(t, time.Date(2022, 2, 1, 2, 2, 2, 0, time.UTC), parsed)
	})
	t.Run("ValidLegacy", func(t *testing.T) {
		parsed, err := stringutil.ParseTime("2022-02-01 02:02:02")
		require.NoError(t, err)
		require.Equal(t, time.Date(2022, 2, 1, 2, 2, 2, 0, time.Now().Location()), parsed)
	})

	t.Run("Empty", func(t *testing.T) {
		parsed, err := stringutil.ParseTime("-")
		require.NoError(t, err)
		require.Equal(t, time.Time{}, parsed)
	})
}

func TestTruncString(t *testing.T) {
	t.Run("Valid", func(t *testing.T) {
		// Test empty string
		require.Equal(t, "", stringutil.TruncString("", 8))
		// Test string with length less than limit
		require.Equal(t, "1234567", stringutil.TruncString("1234567", 8))
		// Test string with length equal to limit
		require.Equal(t, "12345678", stringutil.TruncString("123456789", 8))
	})
}

func TestTruncUTF8Bytes(t *testing.T) {
	t.Parallel()

	got := stringutil.TruncUTF8Bytes("界界界", 7)
	require.Equal(t, "界界", got)
	require.LessOrEqual(t, len(got), 7)
	require.True(t, utf8.ValidString(got))
}

func TestRemoveQuotes(t *testing.T) {
	t.Run("Valid", func(t *testing.T) {
		require.Equal(t, "hello", stringutil.RemoveQuotes(`"hello"`))
		require.Equal(t, "hello world", stringutil.RemoveQuotes(`"hello world"`))
		require.Equal(t, "hello", stringutil.RemoveQuotes(`hello`))
		require.Equal(t, "", stringutil.RemoveQuotes(``))
	})
}

func TestKebabToCamel(t *testing.T) {
	t.Run("SimpleKebabCase", func(t *testing.T) {
		result := stringutil.KebabToCamel("hello-world")
		require.Equal(t, "helloWorld", result)
	})

	t.Run("MultipleWords", func(t *testing.T) {
		result := stringutil.KebabToCamel("hello-world-foo-bar")
		require.Equal(t, "helloWorldFooBar", result)
	})

	t.Run("SingleWord", func(t *testing.T) {
		result := stringutil.KebabToCamel("hello")
		require.Equal(t, "hello", result)
	})

	t.Run("EmptyString", func(t *testing.T) {
		result := stringutil.KebabToCamel("")
		require.Equal(t, "", result)
	})

	t.Run("LeadingHyphen", func(t *testing.T) {
		result := stringutil.KebabToCamel("-hello-world")
		require.Equal(t, "helloWorld", result)
	})

	t.Run("TrailingHyphen", func(t *testing.T) {
		result := stringutil.KebabToCamel("hello-world-")
		require.Equal(t, "helloWorld", result)
	})

	t.Run("MultipleConsecutiveHyphens", func(t *testing.T) {
		result := stringutil.KebabToCamel("hello--world")
		require.Equal(t, "helloWorld", result)
	})

	t.Run("AllUpperCase", func(t *testing.T) {
		result := stringutil.KebabToCamel("HELLO-WORLD")
		require.Equal(t, "HELLOWORLD", result)
	})

	t.Run("MixedCase", func(t *testing.T) {
		result := stringutil.KebabToCamel("Hello-World")
		require.Equal(t, "HelloWorld", result)
	})

	t.Run("NumbersInString", func(t *testing.T) {
		result := stringutil.KebabToCamel("test-123-case")
		require.Equal(t, "test123Case", result)
	})

	t.Run("SingleLetter", func(t *testing.T) {
		result := stringutil.KebabToCamel("a-b-c")
		require.Equal(t, "aBC", result)
	})

	t.Run("WithDots", func(t *testing.T) {
		result := stringutil.KebabToCamel("main-func.foo-bar")
		require.Equal(t, "mainFunc.fooBar", result)
	})

	t.Run("MultipleDots", func(t *testing.T) {
		result := stringutil.KebabToCamel("com.example.my-package.my-class")
		require.Equal(t, "com.example.myPackage.myClass", result)
	})

	t.Run("DotsOnly", func(t *testing.T) {
		result := stringutil.KebabToCamel("com.example.package")
		require.Equal(t, "com.example.package", result)
	})
}

func TestScreamingSnakeToCamel(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"TOTAL_COUNT", "totalCount"},
		{"MY_VAR", "myVar"},
		{"FOO", "foo"},
		{"FOO_BAR_BAZ", "fooBarBaz"},
		{"", ""},
		{"_LEADING", "leading"},
		{"TRAILING_", "trailing"},
		{"DOUBLE__UNDERSCORE", "doubleUnderscore"},
		{"already_lower", "alreadyLower"},
		{"MiXeD_CaSe", "mixedCase"},
		{"A", "a"},
		{"A_B_C", "aBC"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := stringutil.ScreamingSnakeToCamel(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestIsMultiLine(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		// Single line cases
		{
			name:     "EmptyString",
			input:    "",
			expected: false,
		},
		{
			name:     "SingleLineText",
			input:    "hello world",
			expected: false,
		},
		{
			name:     "SingleWord",
			input:    "hello",
			expected: false,
		},
		{
			name:     "SingleLineWithSpaces",
			input:    "   hello world   ",
			expected: false,
		},
		{
			name:     "SingleLineWithSpecialCharacters",
			input:    "hello@world.com",
			expected: false,
		},

		// Multi-line cases with Unix line endings (\n)
		{
			name:     "TwoLinesWithUnixEnding",
			input:    "line1\nline2",
			expected: true,
		},
		{
			name:     "MultipleLinesWithUnixEndings",
			input:    "line1\nline2\nline3",
			expected: true,
		},
		{
			name:     "LineEndingAtStart",
			input:    "\nhello",
			expected: true,
		},
		{
			name:     "LineEndingAtEnd",
			input:    "hello\n",
			expected: true,
		},
		{
			name:     "OnlyNewlineCharacter",
			input:    "\n",
			expected: true,
		},

		// Multi-line cases with Windows line endings (\r\n)
		{
			name:     "TwoLinesWithWindowsEnding",
			input:    "line1\r\nline2",
			expected: true,
		},
		{
			name:     "MultipleLinesWithWindowsEndings",
			input:    "line1\r\nline2\r\nline3",
			expected: true,
		},
		{
			name:     "WindowsLineEndingAtStart",
			input:    "\r\nhello",
			expected: true,
		},
		{
			name:     "WindowsLineEndingAtEnd",
			input:    "hello\r\n",
			expected: true,
		},

		// Multi-line cases with old Mac line endings (\r)
		{
			name:     "TwoLinesWithMacEnding",
			input:    "line1\rline2",
			expected: true,
		},
		{
			name:     "MultipleLinesWithMacEndings",
			input:    "line1\rline2\rline3",
			expected: true,
		},
		{
			name:     "MacLineEndingAtStart",
			input:    "\rhello",
			expected: true,
		},
		{
			name:     "MacLineEndingAtEnd",
			input:    "hello\r",
			expected: true,
		},
		{
			name:     "OnlyCarriageReturnCharacter",
			input:    "\r",
			expected: true,
		},

		// Mixed line endings
		{
			name:     "MixedLineEndings",
			input:    "line1\nline2\r\nline3\rline4",
			expected: true,
		},
		{
			name:     "ConsecutiveNewlines",
			input:    "line1\n\nline3",
			expected: true,
		},
		{
			name:     "ConsecutiveCarriageReturns",
			input:    "line1\r\rline3",
			expected: true,
		},

		// Edge cases
		{
			name:     "OnlyWhitespaceWithNewline",
			input:    "   \n   ",
			expected: true,
		},
		{
			name:     "TabsAndNewlines",
			input:    "hello\tworld\nfoo",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stringutil.IsMultiLine(tt.input)
			require.Equal(t, tt.expected, result, "IsMultiLine(%q) = %v, want %v", tt.input, result, tt.expected)
		})
	}
}

func TestDetectSeparatorType(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected stringutil.SeparatorType
	}{
		// JSON tests
		{
			name:     "JsonArray",
			input:    `["item1", "item2", "item3"]`,
			expected: stringutil.SeparatorTypeJSON,
		},
		{
			name:     "JsonArrayWithMixedTypes",
			input:    `["string", 123, true, null]`,
			expected: stringutil.SeparatorTypeJSON,
		},

		// Newline tests
		{
			name:     "UnixNewlines",
			input:    "line1\nline2\nline3",
			expected: stringutil.SeparatorTypeNewline,
		},
		{
			name:     "WindowsNewlines",
			input:    "line1\r\nline2\r\nline3",
			expected: stringutil.SeparatorTypeNewline,
		},
		{
			name:     "MixedNewlines",
			input:    "line1\nline2\r\nline3\rline4",
			expected: stringutil.SeparatorTypeNewline,
		},

		// Delimiter tests
		{
			name:     "CommaSeparated",
			input:    "item1,item2,item3",
			expected: stringutil.SeparatorTypeComma,
		},
		{
			name:     "SemicolonSeparated",
			input:    "item1;item2;item3",
			expected: stringutil.SeparatorTypeSemicolon,
		},
		{
			name:     "PipeSeparated",
			input:    "item1|item2|item3",
			expected: stringutil.SeparatorTypePipe,
		},
		{
			name:     "TabSeparated",
			input:    "item1\titem2\titem3",
			expected: stringutil.SeparatorTypeTab,
		},

		// Quoted strings tests
		{
			name:     "QuotedStringsWithSpaces",
			input:    `"item one" "item two" "item three"`,
			expected: stringutil.SeparatorTypeQuoted,
		},
		{
			name:     "MixedQuotedAndUnquoted",
			input:    `"quoted item" unquoted`,
			expected: stringutil.SeparatorTypeQuoted,
		},

		// Space separated tests
		{
			name:     "SimpleSpaceSeparated",
			input:    "item1 item2 item3",
			expected: stringutil.SeparatorTypeSpace,
		},
		{
			name:     "MultipleSpaces",
			input:    "item1   item2     item3",
			expected: stringutil.SeparatorTypeSpace,
		},

		// Edge cases
		{
			name:     "EmptyString",
			input:    "",
			expected: stringutil.SeparatorTypeSpace,
		},
		{
			name:     "SingleItem",
			input:    "single",
			expected: stringutil.SeparatorTypeSpace,
		},
		{
			name:     "WhitespaceOnly",
			input:    "   \t\n   ",
			expected: stringutil.SeparatorTypeSpace,
		},

		// Priority tests (comma should win over other separators)
		{
			name:     "CommaWinsOverSemicolon",
			input:    "a,b,c;d",
			expected: stringutil.SeparatorTypeComma,
		},
		{
			name:     "PipeWinsOverCommaWhenMoreFrequent",
			input:    "a|b|c|d,e",
			expected: stringutil.SeparatorTypePipe,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stringutil.DetectSeparatorType(tt.input)
			require.Equal(t, tt.expected, result, "DetectSeparatorType(%q) = %v, want %v", tt.input, result, tt.expected)
		})
	}
}

func TestParseSeparatedValues(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
		wantErr  bool
	}{
		// JSON array tests
		{
			name:     "JsonArrayStrings",
			input:    `["item1", "item2", "item3"]`,
			expected: []string{"item1", "item2", "item3"},
			wantErr:  false,
		},
		{
			name:     "JsonArrayWithNumbers",
			input:    `["string", 123, true, false, null]`,
			expected: []string{"string", "123", "true", "false", "null"},
			wantErr:  false,
		},
		{
			name:     "JsonArrayWithObjects",
			input:    `[{"key": "value"}, "string", 42]`,
			expected: []string{`{"key":"value"}`, "string", "42"},
			wantErr:  false,
		},
		{
			name:     "InvalidJsonArrayDetectedAsQuoted",
			input:    `["unclosed array"`,
			expected: []string{`[`, `unclosed array`},
			wantErr:  false,
		},

		// Newline-separated tests
		{
			name:     "UnixNewlines",
			input:    "line1\nline2\nline3",
			expected: []string{"line1", "line2", "line3"},
			wantErr:  false,
		},
		{
			name:     "WindowsNewlines",
			input:    "line1\r\nline2\r\nline3",
			expected: []string{"line1", "line2", "line3"},
			wantErr:  false,
		},
		{
			name:     "MixedNewlinesWithEmptyLines",
			input:    "line1\n\nline3\r\n\r\nline5",
			expected: []string{"line1", "line3", "line5"},
			wantErr:  false,
		},
		{
			name:     "NewlinesWithWhitespace",
			input:    "  line1  \n\t\tline2\t\t\n   line3   ",
			expected: []string{"line1", "line2", "line3"},
			wantErr:  false,
		},

		// Comma-separated tests
		{
			name:     "SimpleCommaSeparated",
			input:    "item1,item2,item3",
			expected: []string{"item1", "item2", "item3"},
			wantErr:  false,
		},
		{
			name:     "CommaSeparatedWithSpaces",
			input:    "item1 , item2 , item3",
			expected: []string{"item1", "item2", "item3"},
			wantErr:  false,
		},
		{
			name:     "CommaSeparatedWithEmptyValues",
			input:    "item1,,item3",
			expected: []string{"item1", "item3"},
			wantErr:  false,
		},

		// Other delimiter tests
		{
			name:     "SemicolonSeparated",
			input:    "item1;item2;item3",
			expected: []string{"item1", "item2", "item3"},
			wantErr:  false,
		},
		{
			name:     "PipeSeparated",
			input:    "item1|item2|item3",
			expected: []string{"item1", "item2", "item3"},
			wantErr:  false,
		},
		{
			name:     "TabSeparated",
			input:    "item1\titem2\titem3",
			expected: []string{"item1", "item2", "item3"},
			wantErr:  false,
		},

		// Quoted strings tests
		{
			name:     "QuotedStringsWithSpaces",
			input:    `"item one" "item two" "item three"`,
			expected: []string{"item one", "item two", "item three"},
			wantErr:  false,
		},
		{
			name:     "MixedQuotedAndUnquoted",
			input:    `"quoted item" unquoted "another quoted"`,
			expected: []string{"quoted item", "unquoted", "another quoted"},
			wantErr:  false,
		},
		{
			name:     "QuotedStringsWithEscapedQuotesComplexParsing",
			input:    `"item with \"quotes\"" "normal item"`,
			expected: []string{`item with \`, `quotes\`, ``, `normal item`},
			wantErr:  false,
		},
		{
			name:     "UnclosedQuoteFallsBackToSpace",
			input:    `"unclosed quote item`,
			expected: []string{`"unclosed`, `quote`, `item`},
			wantErr:  false,
		},

		// Space-separated tests
		{
			name:     "SimpleSpaceSeparated",
			input:    "item1 item2 item3",
			expected: []string{"item1", "item2", "item3"},
			wantErr:  false,
		},
		{
			name:     "MultipleSpaces",
			input:    "item1   item2     item3",
			expected: []string{"item1", "item2", "item3"},
			wantErr:  false,
		},

		// File path tests (real-world examples)
		{
			name:     "FilePathsWithSpacesNewlineSeparated",
			input:    "/path/to/my file.txt\n/another/path with spaces.csv\n/simple/path.json",
			expected: []string{"/path/to/my file.txt", "/another/path with spaces.csv", "/simple/path.json"},
			wantErr:  false,
		},
		{
			name:     "FilePathsWithSpacesQuoted",
			input:    `"/path/to/my file.txt" "/another/path with spaces.csv" "/simple/path.json"`,
			expected: []string{"/path/to/my file.txt", "/another/path with spaces.csv", "/simple/path.json"},
			wantErr:  false,
		},
		{
			name:     "CommaSeparatedFilePaths",
			input:    "file1.txt,file2.csv,file3.json",
			expected: []string{"file1.txt", "file2.csv", "file3.json"},
			wantErr:  false,
		},

		// Edge cases
		{
			name:     "EmptyString",
			input:    "",
			expected: nil,
			wantErr:  false,
		},
		{
			name:     "WhitespaceOnly",
			input:    "   \t\n   ",
			expected: nil,
			wantErr:  false,
		},
		{
			name:     "SingleItem",
			input:    "single",
			expected: []string{"single"},
			wantErr:  false,
		},
		{
			name:     "SingleQuotedItem",
			input:    `"single item"`,
			expected: []string{"single item"},
			wantErr:  false,
		},

		// Issue #1274: Single JSON object tests
		{
			name:     "SingleJSONObject",
			input:    `{"file": "params.txt", "config": "env"}`,
			expected: []string{`{"file": "params.txt", "config": "env"}`},
			wantErr:  false,
		},
		{
			name:     "SingleJSONObjectCompact",
			input:    `{"key":"value"}`,
			expected: []string{`{"key":"value"}`},
			wantErr:  false,
		},
		{
			name:     "MultipleJSONObjectsNewlineSeparated",
			input:    "{\"file\": \"file1.txt\", \"config\": \"prod\"}\n{\"file\": \"file2.txt\", \"config\": \"test\"}\n{\"file\": \"file3.txt\", \"config\": \"dev\"}",
			expected: []string{`{"file": "file1.txt", "config": "prod"}`, `{"file": "file2.txt", "config": "test"}`, `{"file": "file3.txt", "config": "dev"}`},
			wantErr:  false,
		},

		// Real-world command output examples
		{
			name:     "FindCommandOutput",
			input:    "/usr/bin/find\n/usr/bin/grep\n/usr/bin/awk",
			expected: []string{"/usr/bin/find", "/usr/bin/grep", "/usr/bin/awk"},
			wantErr:  false,
		},
		{
			name:     "LsCommandOutputWithSpaces",
			input:    "My Document.pdf\nAnother File.txt\nno-spaces-file.json",
			expected: []string{"My Document.pdf", "Another File.txt", "no-spaces-file.json"},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := stringutil.ParseSeparatedValues(tt.input)
			if tt.wantErr {
				require.Error(t, err, "ParseSeparatedValues(%q) should return error", tt.input)
			} else {
				require.NoError(t, err, "ParseSeparatedValues(%q) should not return error", tt.input)
				require.Equal(t, tt.expected, result, "ParseSeparatedValues(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseSeparatedValues_BackwardCompatibility(t *testing.T) {
	// These tests ensure backward compatibility with the existing strings.Fields behavior
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "SpaceSeparatedServersExistingBehavior",
			input:    "server1 server2 server3",
			expected: []string{"server1", "server2", "server3"},
		},
		{
			name:     "MultipleWhitespaceWithTabs",
			input:    "item1\t\titem2\t\titem3",
			expected: []string{"item1", "item2", "item3"},
		},
		{
			name:     "JsonArrayCompatibility",
			input:    `["alpha", "beta", "gamma", "delta"]`,
			expected: []string{"alpha", "beta", "gamma", "delta"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := stringutil.ParseSeparatedValues(tt.input)
			require.NoError(t, err)
			require.Equal(t, tt.expected, result, "ParseSeparatedValues(%q) = %v, want %v", tt.input, result, tt.expected)
		})
	}
}

func TestRandomString(t *testing.T) {
	t.Run("GeneratesCorrectLength", func(t *testing.T) {
		// Test various lengths
		for _, length := range []int{0, 1, 5, 10, 32, 100} {
			result := stringutil.RandomString(length)
			require.Len(t, result, length, "expected string of length %d, got %d", length, len(result))
		}
	})

	t.Run("ContainsOnlyAlphabetic", func(t *testing.T) {
		// Generate a string and check all characters are alphabetic
		result := stringutil.RandomString(50)
		for i, ch := range result {
			require.True(t, (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z'),
				"character at position %d (%c) is not alphabetic", i, ch)
		}
	})

	t.Run("GeneratesDifferentStrings", func(t *testing.T) {
		// Generate multiple strings and check they're different
		// (technically could fail with extremely low probability)
		const numStrings = 10
		const stringLength = 10
		strings := make(map[string]bool)

		for range numStrings {
			s := stringutil.RandomString(stringLength)
			strings[s] = true
		}

		require.Greater(t, len(strings), 1, "expected different strings, but all were identical")
	})
}

func TestExtractEmailDomain(t *testing.T) {
	tests := []struct {
		email    string
		expected string
	}{
		{"user@example.com", "example.com"},
		{"john.doe@company.org", "company.org"},
		{"invalid-email", ""},
		{"multiple@at@signs.com", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			result := stringutil.ExtractEmailDomain(tt.email)
			require.Equal(t, tt.expected, result)
		})
	}
}
