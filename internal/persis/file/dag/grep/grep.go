// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package grep

import (
	"bufio"
	"bytes"
	"errors"
	"math"
	"regexp"
	"strings"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/samber/lo"
)

var (
	// ErrNoMatch is returned when no match is found.
	ErrNoMatch = errors.New("no match found")
	// ErrEmptyPattern is returned when pattern is empty.
	ErrEmptyPattern = errors.New("empty pattern")
)

// Matcher is the interface for matching lines.
type Matcher interface {
	// Match returns true if the given line matches.
	Match(line string) bool
}

// GrepOptions represents grep options.
// If IsRegexp is true, the pattern is treated as a regular expression.
// Before and After are the number of lines before and after the matched line.
type GrepOptions struct {
	IsRegexp bool
	Before   int
	After    int
	Offset   int
	Limit    int
	Matcher  Matcher
}

var DefaultGrepOptions = GrepOptions{
	IsRegexp: true,
	Before:   2,
	After:    2,
}

// WindowResult contains a bounded snippet window and its continuation state.
type WindowResult struct {
	Matches    []*exec.Match
	HasMore    bool
	NextOffset int
}

// Grep reads data and returns lines that match the given pattern.
// If opts is nil, default options will be used.
func Grep(dat []byte, pattern string, opts GrepOptions) ([]*exec.Match, error) {
	matches, _, err := GrepWithCount(dat, pattern, opts)
	return matches, err
}

// GrepWithCount reads data and returns matching snippets and the total match count.
func GrepWithCount(dat []byte, pattern string, opts GrepOptions) ([]*exec.Match, int, error) {
	if pattern == "" {
		return nil, 0, ErrEmptyPattern
	}

	matcher, err := getMatcher(pattern, opts)
	if err != nil {
		return nil, 0, err
	}

	lines, matches, err := scanLines(dat, matcher)
	if err != nil {
		return nil, 0, err
	}

	return buildMatches(lines, matches, opts), len(matches), nil
}

// GrepWindow returns at most opts.Limit snippets after opts.Offset matches.
// It stops once it can determine whether a continuation page exists.
func GrepWindow(dat []byte, pattern string, opts GrepOptions) (*WindowResult, error) {
	if pattern == "" {
		return nil, ErrEmptyPattern
	}

	matcher, err := getMatcher(pattern, opts)
	if err != nil {
		return nil, err
	}

	offset := max(opts.Offset, 0)
	limit := opts.Limit
	if limit <= 0 {
		limit = math.MaxInt
	}

	type pendingMatch struct {
		match          *exec.Match
		lines          []string
		afterRemaining int
	}

	finalizePending := func(pending []*pendingMatch, force bool, out []*exec.Match) ([]*pendingMatch, []*exec.Match) {
		next := pending[:0]
		for _, item := range pending {
			if force || item.afterRemaining == 0 {
				item.match.Line = strings.Join(item.lines, "\n")
				out = append(out, item.match)
				continue
			}
			next = append(next, item)
		}
		return next, out
	}

	scanner := bufio.NewScanner(bytes.NewReader(dat))
	beforeBuf := make([]string, 0, opts.Before)
	results := make([]*exec.Match, 0, min(limit, 8))
	pending := make([]*pendingMatch, 0, min(limit, 4))
	lineNumber := 0
	matchedCount := 0
	anyMatch := false
	stopCollecting := false
	hasMore := false

	for scanner.Scan() {
		line := scanner.Text()
		lineNumber++

		for _, item := range pending {
			if item.afterRemaining > 0 && lineNumber > item.match.LineNumber {
				item.lines = append(item.lines, line)
				item.afterRemaining--
			}
		}
		pending, results = finalizePending(pending, false, results)

		if !stopCollecting && matcher.Match(line) {
			anyMatch = true

			switch {
			case matchedCount < offset:
				matchedCount++
			case len(results)+len(pending) < limit:
				contextLines := append([]string(nil), beforeBuf...)
				contextLines = append(contextLines, line)
				item := &pendingMatch{
					match: &exec.Match{
						StartLine:  lineNumber - len(beforeBuf),
						LineNumber: lineNumber,
					},
					lines:          contextLines,
					afterRemaining: opts.After,
				}
				pending = append(pending, item)
				pending, results = finalizePending(pending, false, results)
				matchedCount++
			default:
				hasMore = true
				stopCollecting = true
			}
		}

		if opts.Before > 0 {
			if len(beforeBuf) == opts.Before {
				beforeBuf = append(beforeBuf[1:], line)
			} else {
				beforeBuf = append(beforeBuf, line)
			}
		}

		if stopCollecting && len(pending) == 0 {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	_, results = finalizePending(pending, true, results)
	if !anyMatch {
		return nil, ErrNoMatch
	}

	nextOffset := 0
	if hasMore {
		nextOffset = offset + len(results)
	}

	return &WindowResult{
		Matches:    results,
		HasMore:    hasMore,
		NextOffset: nextOffset,
	}, nil
}

// getMatcher returns a matcher based on the pattern and options.
func getMatcher(pattern string, opts GrepOptions) (Matcher, error) {
	if opts.Matcher != nil {
		return opts.Matcher, nil
	}
	return defaultMatcher(pattern, opts)
}

// scanLines scans through data and returns lines and their matched indices.
func scanLines(dat []byte, matcher Matcher) ([]string, []int, error) {
	scanner := bufio.NewScanner(bytes.NewReader(dat))
	var lines []string
	var matches []int
	var idx int

	for scanner.Scan() {
		line := scanner.Text()
		lines = append(lines, line)
		if matcher.Match(line) {
			matches = append(matches, idx)
		}
		idx++
	}

	if len(matches) == 0 {
		return nil, nil, ErrNoMatch
	}
	return lines, matches, scanner.Err()
}

// buildMatches constructs Match objects from matched line indices.
func buildMatches(lines []string, matches []int, opts GrepOptions) []*exec.Match {
	var ret []*exec.Match
	start := max(opts.Offset, 0)
	end := len(matches)
	if opts.Limit > 0 && start+opts.Limit < end {
		end = start + opts.Limit
	}
	if start >= len(matches) {
		return ret
	}

	for _, m := range matches[start:end] {
		low := lo.Max([]int{0, m - opts.Before})
		high := lo.Min([]int{len(lines), m + opts.After + 1})
		matchText := strings.Join(lines[low:high], "\n")

		ret = append(ret, &exec.Match{
			StartLine:  low + 1,
			LineNumber: m + 1,
			Line:       matchText,
		})
	}
	return ret
}

func defaultMatcher(pattern string, opts GrepOptions) (Matcher, error) {
	if opts.IsRegexp {
		reg, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		return &regexpMatcher{
			Regexp: reg,
		}, nil
	}
	return &simpleMatcher{
		Pattern: pattern,
	}, nil
}

var _ Matcher = (*regexpMatcher)(nil)

type regexpMatcher struct {
	Regexp *regexp.Regexp
}

func (rm *regexpMatcher) Match(line string) bool {
	return rm.Regexp.MatchString(line)
}

var _ Matcher = (*simpleMatcher)(nil)

type simpleMatcher struct {
	Pattern string
}

func (rm *simpleMatcher) Match(line string) bool {
	return strings.Contains(line, rm.Pattern)
}
