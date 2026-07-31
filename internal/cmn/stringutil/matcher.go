// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package stringutil

import (
	"bufio"
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
)

const rePrefix = "re:"

// MatchOption represents an option for pattern matching
type MatchOption func(*matchOptions)

type matchOptions struct {
	exactMatch    bool
	maxBufferSize int
}

// WithExactMatch configures the matcher to use exact string matching for literal patterns
func WithExactMatch() MatchOption {
	return func(o *matchOptions) {
		o.exactMatch = true
	}
}

// WithMaxBufferSize configures the maximum buffer size for handling long lines
func WithMaxBufferSize(size int) MatchOption {
	return func(o *matchOptions) {
		o.maxBufferSize = size
	}
}

// MatchPattern matches content against patterns using either literal or regex matching.
// For files or large content, use MatchPatternScanner instead.
func MatchPattern(ctx context.Context, content string, patterns []string, opts ...MatchOption) bool {
	// Apply options to get configuration
	options := &matchOptions{
		maxBufferSize: 1024 * 1024, // Default 1MB
	}
	for _, opt := range opts {
		opt(options)
	}

	scanner := bufio.NewScanner(strings.NewReader(content))

	// First try with default buffer
	matched, err := matchPatternWithScanner(ctx, scanner, patterns, opts...)
	if err == nil {
		return matched
	}

	// If we got a "token too long" error, retry with larger buffer
	if errors.Is(err, bufio.ErrTooLong) {
		logger.Debug(ctx, "Token too long, retrying with larger buffer",
			tag.Size(len(content)),
			tag.MaxSize(options.maxBufferSize))
		scanner = bufio.NewScanner(strings.NewReader(content))
		// Use configured buffer size
		buf := make([]byte, 0, 64*1024) // Start with 64KB buffer
		scanner.Buffer(buf, options.maxBufferSize)
		matched, _ = matchPatternWithScanner(ctx, scanner, patterns, opts...)
		return matched
	}

	return matched
}

func MatchPatternScanner(ctx context.Context, scanner *bufio.Scanner, patterns []string, opts ...MatchOption) bool {
	matched, _ := matchPatternWithScanner(ctx, scanner, patterns, opts...)
	return matched
}

// matchPatternWithScanner is the internal implementation that returns both result and error
func matchPatternWithScanner(ctx context.Context, scanner *bufio.Scanner, patterns []string, opts ...MatchOption) (bool, error) {
	if len(patterns) == 0 {
		return false, nil
	}

	// Apply options
	options := &matchOptions{}
	for _, opt := range opts {
		opt(options)
	}

	var regexps []*regexp.Regexp
	var literalPatterns []string

	// Compile regex patterns first
	for _, pattern := range patterns {
		switch {
		case strings.HasPrefix(pattern, rePrefix):
			re, err := regexp.Compile(strings.TrimPrefix(pattern, rePrefix))
			if err != nil {
				logger.Error(ctx, "Invalid regexp pattern",
					tag.Pattern(pattern),
					tag.Error(err))
				continue
			}
			regexps = append(regexps, re)
		default:
			literalPatterns = append(literalPatterns, pattern)
		}
	}

	// Special case: if scanner is empty and we're looking for empty string
	if !scanner.Scan() {
		// Check if scan failed due to an error
		if err := scanner.Err(); err != nil {
			return false, err
		}

		// Check for empty string patterns
		if slices.Contains(literalPatterns, "") {
			return true, nil
		}
		// Check regex patterns against empty string
		for _, re := range regexps {
			if re.MatchString("") {
				return true, nil
			}
		}
		return false, nil
	}

	// Process first line (already read by scanner.Scan() above)
	line := scanner.Text()
	if matchLine(line, literalPatterns, regexps, options) {
		return true, nil
	}

	// Process remaining lines
	for scanner.Scan() {
		if matchLine(scanner.Text(), literalPatterns, regexps, options) {
			return true, nil
		}
	}

	if err := scanner.Err(); err != nil {
		if !errors.Is(err, bufio.ErrTooLong) {
			logger.Error(ctx, "Scanner error", tag.Error(err))
		}
		return false, err
	}

	return false, nil
}

// matchLine checks if a single line matches any of the patterns
func matchLine(line string, literalPatterns []string, regexps []*regexp.Regexp, opts *matchOptions) bool {
	// Check literal patterns
	for _, p := range literalPatterns {
		if opts.exactMatch {
			if line == p {
				return true
			}
		} else {
			if strings.Contains(line, p) {
				return true
			}
		}
	}

	// Check regex patterns
	for _, re := range regexps {
		if re.MatchString(line) {
			return true
		}
	}

	return false
}
