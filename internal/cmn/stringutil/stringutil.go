// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package stringutil

import (
	cryptorand "crypto/rand"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	legacyTimeFormat = "2006-01-02 15:04:05"
)

// FormatTime returns formatted time.
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.Format(time.RFC3339)
}

// ParseTime parses time string.
func ParseTime(val string) (time.Time, error) {
	if val == "" || val == "-" {
		return time.Time{}, nil
	}
	if t, err := time.ParseInLocation(time.RFC3339, val, time.Local); err == nil {
		return t, nil
	}
	return time.ParseInLocation(legacyTimeFormat, val, time.Local)
}

// TruncString returns truncated string.
func TruncString(val string, max int) string {
	if len(val) > max {
		return val[:max]
	}
	return val
}

// TruncUTF8Bytes returns at most max bytes without splitting a UTF-8 codepoint.
func TruncUTF8Bytes(val string, max int) string {
	if max <= 0 {
		return ""
	}
	val = strings.ToValidUTF8(val, "\uFFFD")
	if len(val) <= max {
		return val
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(val[cut]) {
		cut--
	}
	return val[:cut]
}

// ParseBool parses a boolean value from the given input.
func ParseBool(value any) (bool, error) {
	switch v := value.(type) {
	case string:
		return strconv.ParseBool(v)
	case bool:
		return v, nil
	default:
		return false, fmt.Errorf("unsupported type %T for bool (value: %+v)", value, value)
	}
}

// RemoveQuotes removes leading and trailing double quotes from a string if present,
// and unescapes the content using strconv.Unquote.
func RemoveQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		unquoted, err := strconv.Unquote(s)
		if err == nil {
			return unquoted
		}
		// If unquoting fails (e.g., malformed string literal),
		// fall back to returning the original string.
	}
	return s
}

// ScreamingSnakeToCamel converts a SCREAMING_SNAKE_CASE string to camelCase.
// Example: TOTAL_COUNT -> totalCount, MY_VAR -> myVar, FOO -> foo
func ScreamingSnakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	if len(parts) == 0 {
		return ""
	}

	var result strings.Builder
	isFirst := true
	for _, part := range parts {
		if part == "" {
			continue
		}
		lower := strings.ToLower(part)
		if isFirst {
			result.WriteString(lower)
			isFirst = false
		} else {
			// Capitalize first letter of subsequent parts
			if len(lower) > 0 {
				result.WriteString(strings.ToUpper(lower[:1]))
				if len(lower) > 1 {
					result.WriteString(lower[1:])
				}
			}
		}
	}

	return result.String()
}

// KebabToCamel converts a kebab-case string to camelCase.
func KebabToCamel(s string) string {
	parts := strings.Split(s, "-")
	if len(parts) == 0 {
		return ""
	}

	// Find the first non-empty part for the initial word
	result := ""
	startIdx := 0
	for i := range parts {
		if len(parts[i]) > 0 {
			result = parts[i]
			startIdx = i + 1
			break
		}
	}

	// Capitalize remaining parts
	for i := startIdx; i < len(parts); i++ {
		if len(parts[i]) > 0 {
			result += strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}

	return result
}

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
const (
	letterIdxBits = 6                    // 6 bits to represent a letter index
	letterIdxMask = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
)

// IsMultiLine checks if the given string contains multiple lines.
// Returns true if the string contains line separators (\n, \r\n, or \r).
func IsMultiLine(s string) bool {
	return strings.ContainsAny(s, "\n\r")
}

// IsJSON checks if the given string is valid JSON.
// Returns true if the string can be parsed as JSON, false otherwise.
func IsJSON(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	return json.Valid([]byte(s))
}

// SeparatorType represents different types of separators for parsing text
type SeparatorType int

const (
	SeparatorTypeJSON SeparatorType = iota
	SeparatorTypeNewline
	SeparatorTypeComma
	SeparatorTypeSemicolon
	SeparatorTypePipe
	SeparatorTypeTab
	SeparatorTypeQuoted
	SeparatorTypeSpace
)

// DetectSeparatorType analyzes a string to determine the most likely separator type
func DetectSeparatorType(s string) SeparatorType {
	s = strings.TrimSpace(s)
	if s == "" {
		return SeparatorTypeSpace
	}

	isValidJSON := IsJSON(s)

	// Check for JSON first (both arrays and objects)
	if isValidJSON && (strings.HasPrefix(s, "[") || strings.HasPrefix(s, "{")) {
		return SeparatorTypeJSON
	}

	// Check for multiline (newline-separated)
	if IsMultiLine(s) {
		return SeparatorTypeNewline
	}

	// Count different separators to determine the most likely one
	commaCount := strings.Count(s, ",")
	semicolonCount := strings.Count(s, ";")
	pipeCount := strings.Count(s, "|")
	tabCount := strings.Count(s, "\t")
	quoteCount := strings.Count(s, `"`)

	// Check for quoted strings pattern
	// If we have quotes and the string starts or ends with a quote, or has quote-space patterns
	if quoteCount >= 2 && (strings.HasPrefix(s, `"`) || strings.HasSuffix(s, `"`) ||
		strings.Contains(s, `" `) || strings.Contains(s, ` "`)) {
		return SeparatorTypeQuoted
	}

	// Determine the most frequent separator (excluding spaces)
	maxCount := 0
	separatorType := SeparatorTypeSpace

	if tabCount > maxCount && tabCount > 0 {
		maxCount = tabCount
		separatorType = SeparatorTypeTab
	}
	if pipeCount > maxCount && pipeCount > 0 {
		maxCount = pipeCount
		separatorType = SeparatorTypePipe
	}
	if semicolonCount > maxCount && semicolonCount > 0 {
		maxCount = semicolonCount
		separatorType = SeparatorTypeSemicolon
	}
	if commaCount > maxCount && commaCount > 0 {
		separatorType = SeparatorTypeComma
	}

	return separatorType
}

// ParseSeparatedValues intelligently parses a string into separate values
// based on detected separator patterns. Handles JSON arrays, various delimiters,
// quoted strings, and space-separated values.
func ParseSeparatedValues(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}

	separatorType := DetectSeparatorType(s)

	switch separatorType {
	case SeparatorTypeJSON:
		// Array or object
		if strings.HasPrefix(s, "[") {
			var items []any
			err := json.Unmarshal([]byte(s), &items)
			if err != nil {
				return nil, fmt.Errorf("failed to parse JSON array: %w", err)
			}

			// Successfully parsed as array
			var result []string
			for _, item := range items {
				switch v := item.(type) {
				case string:
					result = append(result, v)
				case nil:
					result = append(result, "null")
				default:
					// Convert other types to string representation
					jsonBytes, err := json.Marshal(v)
					if err != nil {
						result = append(result, fmt.Sprintf("%v", v))
					} else {
						result = append(result, string(jsonBytes))
					}
				}
			}
			return result, nil
		}

		// Single JSON object - return as is
		return []string{s}, nil

	case SeparatorTypeNewline:
		return parseNewlineSeparated(s), nil

	case SeparatorTypeComma:
		return parseDelimited(s, ","), nil

	case SeparatorTypeSemicolon:
		return parseDelimited(s, ";"), nil

	case SeparatorTypePipe:
		return parseDelimited(s, "|"), nil

	case SeparatorTypeTab:
		return parseDelimited(s, "\t"), nil

	case SeparatorTypeQuoted:
		return parseQuotedStrings(s), nil

	case SeparatorTypeSpace:
		// Check if the entire string is a valid JSON object before space-splitting
		// This handles the case where a single JSON object (not array) is output
		// e.g., {"file": "params.txt", "config": "env"}
		if IsJSON(s) {
			return []string{s}, nil
		}
		return parseSpaceSeparated(s), nil

	default:
		// Fallback to space separation, but check for JSON object first
		if IsJSON(s) {
			return []string{s}, nil
		}
		return parseSpaceSeparated(s), nil
	}
}

// parseNewlineSeparated splits a string by newlines and cleans up each line
func parseNewlineSeparated(s string) []string {
	lines := strings.FieldsFunc(s, func(r rune) bool {
		return r == '\n' || r == '\r'
	})

	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

// parseDelimited splits a string by the given delimiter and trims whitespace
func parseDelimited(s, delimiter string) []string {
	parts := strings.Split(s, delimiter)
	var result []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

// parseQuotedStrings handles strings with quoted values like "item one" "item two"
func parseQuotedStrings(s string) []string {
	var result []string
	var current strings.Builder
	inQuotes := false
	i := 0

	for i < len(s) {
		r := rune(s[i])

		if r == '"' {
			if inQuotes {
				// End of quoted string
				result = append(result, current.String())
				current.Reset()
				inQuotes = false
			} else {
				// Start of quoted string
				inQuotes = true
			}
		} else if inQuotes {
			// Inside quotes, collect everything
			current.WriteRune(r)
		} else if !unicode.IsSpace(r) {
			// Outside quotes, collect non-space characters as unquoted value
			for i < len(s) && !unicode.IsSpace(rune(s[i])) && rune(s[i]) != '"' {
				current.WriteRune(rune(s[i]))
				i++
			}
			if current.Len() > 0 {
				result = append(result, current.String())
				current.Reset()
			}
			continue // Skip the increment at the end since we already advanced i
		}

		i++
	}

	// Handle case where string ends while in quotes
	if current.Len() > 0 {
		result = append(result, current.String())
	}

	return result
}

// parseSpaceSeparated uses strings.Fields as fallback for space-separated values
func parseSpaceSeparated(s string) []string {
	return strings.Fields(s)
}

// ExtractEmailDomain extracts the domain part from an email address.
// Returns an empty string if the email format is invalid.
func ExtractEmailDomain(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}

// RandomString generates a random string of length n using letters from letterBytes.
func RandomString(n int) string {
	b := make([]byte, n)
	randomByte := make([]byte, 1)
	for i := 0; i < n; {
		if _, err := cryptorand.Read(randomByte); err != nil {
			panic(fmt.Sprintf("stringutil.RandomString: %v", err))
		}
		idx := int(randomByte[0] & letterIdxMask)
		if idx >= len(letterBytes) {
			continue
		}
		b[i] = letterBytes[idx]
		i++
	}

	return string(b)
}
