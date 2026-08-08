// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Label represents a key-value label with optional value.
// For backward compatibility, key-only labels have an empty Value.
type Label struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

// String returns the canonical string representation of the label.
// Format: "key=value" or "key" if value is empty.
func (t Label) String() string {
	if t.Value == "" {
		return t.Key
	}
	return t.Key + "=" + t.Value
}

// IsZero returns true if the label is empty.
func (t Label) IsZero() bool {
	return t.Key == ""
}

// Label validation constants.
const (
	// MaxLabelKeyLength is the maximum allowed length for label keys (63 chars).
	MaxLabelKeyLength = 63
	// MaxLabelValueLength is the maximum allowed length for label values (255 chars).
	MaxLabelValueLength = 255
)

// Label validation pattern strings (exported for use by other packages).
const (
	// LabelKeyPatternStr is the regex pattern for valid label keys.
	// Allows alphanumeric, dash, underscore, dot. Must start with letter/number.
	LabelKeyPatternStr = `^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`
	// LabelValuePatternStr is the regex pattern for valid label values.
	// Allows alphanumeric, dash, underscore, dot, slash. Must start with letter/number.
	LabelValuePatternStr = `^[a-zA-Z0-9][a-zA-Z0-9_./-]*$`
)

// Label validation patterns (compiled regex).
var (
	// ValidLabelKeyPattern validates label keys.
	ValidLabelKeyPattern = regexp.MustCompile(LabelKeyPatternStr)
	// ValidLabelValuePattern validates label values.
	ValidLabelValuePattern = regexp.MustCompile(LabelValuePatternStr)
)

// ValidateLabel validates a label's key and value format.
func ValidateLabel(t Label) error {
	if t.Key == "" {
		return errors.New("label key cannot be empty")
	}
	if len(t.Key) > MaxLabelKeyLength {
		return fmt.Errorf("label key exceeds max length %d", MaxLabelKeyLength)
	}
	if !ValidLabelKeyPattern.MatchString(t.Key) {
		return fmt.Errorf("label key %q contains invalid characters (allowed: a-z, A-Z, 0-9, -, _, .)", t.Key)
	}
	if len(t.Value) > MaxLabelValueLength {
		return fmt.Errorf("label value exceeds max length %d", MaxLabelValueLength)
	}
	if t.Value != "" && !ValidLabelValuePattern.MatchString(t.Value) {
		return fmt.Errorf("label value %q contains invalid characters (allowed: a-z, A-Z, 0-9, -, _, ., /)", t.Value)
	}
	return nil
}

// ValidateLabels validates all labels in the collection.
func ValidateLabels(labels Labels) error {
	var errs []error
	for _, t := range labels {
		if err := ValidateLabel(t); err != nil {
			errs = append(errs, fmt.Errorf("label %q: %w", t.String(), err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// ParseLabel parses a string into a Label.
// Supports "key=value" and "key" (key-only) formats.
// Both key and value are normalized to lowercase.
func ParseLabel(s string) Label {
	s = strings.TrimSpace(s)
	if s == "" {
		return Label{}
	}

	key, value, found := strings.Cut(s, "=")
	key = strings.ToLower(strings.TrimSpace(key))

	if !found {
		return Label{Key: key}
	}

	return Label{
		Key:   key,
		Value: strings.ToLower(strings.TrimSpace(value)),
	}
}

// Labels represents a collection of labels.
type Labels []Label

// NewLabels creates a Labels collection from a slice of strings.
func NewLabels(strs []string) Labels {
	labels := make(Labels, 0, len(strs))
	for _, s := range strs {
		if t := ParseLabel(s); !t.IsZero() {
			labels = append(labels, t)
		}
	}
	return labels
}

// Strings returns the labels as a slice of strings for API compatibility.
func (t Labels) Strings() []string {
	if t == nil {
		return nil
	}
	strs := make([]string, len(t))
	for i, label := range t {
		strs[i] = label.String()
	}
	return strs
}

// Keys returns all unique keys in the collection.
func (t Labels) Keys() []string {
	seen := make(map[string]struct{})
	keys := make([]string, 0, len(t))
	for _, label := range t {
		if _, exists := seen[label.Key]; !exists {
			seen[label.Key] = struct{}{}
			keys = append(keys, label.Key)
		}
	}
	return keys
}

// HasKey checks if any label has the given key.
func (t Labels) HasKey(key string) bool {
	key = strings.ToLower(key)
	for _, label := range t {
		if label.Key == key {
			return true
		}
	}
	return false
}

// Get returns all values for a given key.
func (t Labels) Get(key string) []string {
	key = strings.ToLower(key)
	var values []string
	for _, label := range t {
		if label.Key == key {
			values = append(values, label.Value)
		}
	}
	return values
}

// MarshalJSON serializes Labels as an array of strings for backward compatibility.
func (t Labels) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.Strings())
}

// UnmarshalJSON deserializes Labels from an array of strings.
func (t *Labels) UnmarshalJSON(data []byte) error {
	var strs []string
	if err := json.Unmarshal(data, &strs); err != nil {
		return err
	}
	*t = NewLabels(strs)
	return nil
}

// LabelFilterType represents the type of label filter.
type LabelFilterType int

const (
	// LabelFilterTypeKeyOnly matches any label with the specified key (regardless of value).
	LabelFilterTypeKeyOnly LabelFilterType = iota
	// LabelFilterTypeExact matches labels with exact key=value.
	LabelFilterTypeExact
	// LabelFilterTypeNegation matches if the key does NOT exist.
	LabelFilterTypeNegation
	// LabelFilterTypeWildcard matches labels using glob patterns (* and ?).
	LabelFilterTypeWildcard
)

// LabelFilter represents a parsed filter condition.
type LabelFilter struct {
	Type  LabelFilterType
	Key   string
	Value string
}

// ParseLabelFilter parses a filter string into LabelFilter.
// Formats:
//   - "key" - matches any label with that key (KeyOnly)
//   - "key=value" - matches exact key=value (Exact)
//   - "!key" - matches if key does NOT exist (Negation)
//   - "key*" or "key=value*" - matches using glob patterns (Wildcard)
func ParseLabelFilter(s string) LabelFilter {
	s = strings.TrimSpace(s)
	if s == "" {
		return LabelFilter{}
	}

	// Negation filter
	if after, ok := strings.CutPrefix(s, "!"); ok {
		return LabelFilter{
			Type: LabelFilterTypeNegation,
			Key:  strings.ToLower(strings.TrimSpace(after)),
		}
	}

	// Key=value filter
	if key, value, found := strings.Cut(s, "="); found {
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.ToLower(strings.TrimSpace(value))

		// Check for wildcard pattern in key or value
		if containsWildcard(key) || containsWildcard(value) {
			return LabelFilter{
				Type:  LabelFilterTypeWildcard,
				Key:   key,
				Value: value,
			}
		}

		return LabelFilter{
			Type:  LabelFilterTypeExact,
			Key:   key,
			Value: value,
		}
	}

	// Key-only filter - check for wildcard
	key := strings.ToLower(s)
	if containsWildcard(key) {
		return LabelFilter{
			Type: LabelFilterTypeWildcard,
			Key:  key,
		}
	}

	return LabelFilter{
		Type: LabelFilterTypeKeyOnly,
		Key:  key,
	}
}

// containsWildcard checks if a string contains glob wildcard characters.
func containsWildcard(s string) bool {
	return strings.ContainsAny(s, "*?")
}

// MatchesLabels checks if a label collection matches this filter.
func (f LabelFilter) MatchesLabels(labels Labels) bool {
	switch f.Type {
	case LabelFilterTypeKeyOnly:
		return labels.HasKey(f.Key)

	case LabelFilterTypeExact:
		for _, t := range labels {
			if t.Key == f.Key && t.Value == f.Value {
				return true
			}
		}
		return false

	case LabelFilterTypeNegation:
		return !labels.HasKey(f.Key)

	case LabelFilterTypeWildcard:
		for _, t := range labels {
			if !matchGlob(f.Key, t.Key) {
				continue
			}
			// Key matches; check value if specified
			if f.Value == "" || matchGlob(f.Value, t.Value) {
				return true
			}
		}
		return false

	default:
		return false
	}
}

// matchGlob performs simple glob matching with * and ? wildcards.
// * matches zero or more characters (including slashes), ? matches exactly one character.
// Uses regex internally to properly handle slashes in label values.
// Returns false if the pattern is invalid.
func matchGlob(pattern, s string) bool {
	regexPattern := globToRegex(pattern)
	re, err := regexp.Compile(regexPattern)
	if err != nil {
		return false
	}
	return re.MatchString(s)
}

// globToRegex converts a glob pattern to a regex pattern.
// Escapes regex metacharacters and translates * to .* and ? to .
func globToRegex(glob string) string {
	var result strings.Builder
	result.WriteString("^")
	for _, ch := range glob {
		switch ch {
		case '*':
			result.WriteString(".*")
		case '?':
			result.WriteString(".")
		case '.', '+', '^', '$', '(', ')', '[', ']', '{', '}', '|', '\\':
			result.WriteRune('\\')
			result.WriteRune(ch)
		default:
			result.WriteRune(ch)
		}
	}
	result.WriteString("$")
	return result.String()
}

// MatchesFilters checks if labels match all filters (AND logic).
func (t Labels) MatchesFilters(filters []LabelFilter) bool {
	for _, f := range filters {
		if !f.MatchesLabels(t) {
			return false
		}
	}
	return true
}
