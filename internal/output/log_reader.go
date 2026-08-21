// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package output

import (
	"bufio"
	"os"
	"slices"
	"strings"
	"unicode"
)

// maxScannerBuffer is the maximum buffer size for reading log lines.
// This allows reading lines up to 1MB in length.
const maxScannerBuffer = 1024 * 1024

// maxLogFileSize is the maximum log file size we'll read (10MB).
// Larger files are skipped to prevent memory exhaustion.
const maxLogFileSize = 10 * 1024 * 1024

// ReadLogFileTail reads the last N lines from a log file.
// It returns:
//   - lines: the actual log lines (tail portion if truncated)
//   - truncated: number of lines that were truncated from the beginning
//   - err: any error encountered while reading
//
// If maxLines is 0 or negative, all lines are returned.
// If the file doesn't exist or is empty, returns nil, 0, nil.
// Binary files are detected and return ["(binary data)"], 0, nil.
// Files larger than 10MB are skipped to prevent memory exhaustion.
func ReadLogFileTail(path string, maxLines int) ([]string, int, error) {
	if path == "" {
		return nil, 0, nil
	}

	// #nosec G304 - file path is from trusted DAG execution status data
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	defer func() {
		_ = file.Close()
	}()

	// Check file size to prevent memory exhaustion
	stat, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	if stat.Size() > maxLogFileSize {
		return []string{"(file too large, >10MB)"}, 0, nil
	}

	scanner := bufio.NewScanner(file)

	// Increase buffer size for long lines
	buf := make([]byte, maxScannerBuffer)
	scanner.Buffer(buf, maxScannerBuffer)

	// Use ring buffer for tail when maxLines > 0 to limit memory usage
	if maxLines > 0 {
		return readTailWithRingBuffer(scanner, maxLines)
	}

	// Read all lines (no limit)
	return readAllLines(scanner)
}

// readTailWithRingBuffer reads lines using a ring buffer to keep only the last N lines in memory.
func readTailWithRingBuffer(scanner *bufio.Scanner, maxLines int) ([]string, int, error) {
	ring := make([]string, maxLines)
	ringPos := 0
	totalLines := 0
	isFirstLine := true

	for scanner.Scan() {
		line := scanner.Text()

		// Check for binary content in first line
		if isFirstLine {
			if isBinaryContent([]byte(line)) {
				return []string{"(binary data)"}, 0, nil
			}
			isFirstLine = false
		}

		// Clean and add lines to ring buffer
		cleanedLines := cleanLogLine(line)
		for _, cleaned := range cleanedLines {
			ring[ringPos%maxLines] = cleaned
			ringPos++
			totalLines++
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}

	if totalLines == 0 {
		return nil, 0, nil
	}

	// Extract lines from ring buffer in correct order
	var result []string
	if totalLines <= maxLines {
		result = ring[:totalLines]
	} else {
		result = make([]string, maxLines)
		start := ringPos % maxLines
		for i := range maxLines {
			result[i] = ring[(start+i)%maxLines]
		}
	}

	result = trimTrailingEmptyLines(result)
	truncated := 0
	if totalLines > maxLines {
		truncated = totalLines - maxLines
	}
	return result, truncated, nil
}

// readAllLines reads all lines from a scanner (used when no limit is set).
func readAllLines(scanner *bufio.Scanner) ([]string, int, error) {
	var allLines []string
	isFirstLine := true

	for scanner.Scan() {
		line := scanner.Text()

		// Check for binary content in first line
		if isFirstLine {
			if isBinaryContent([]byte(line)) {
				return []string{"(binary data)"}, 0, nil
			}
			isFirstLine = false
		}

		cleanedLines := cleanLogLine(line)
		allLines = append(allLines, cleanedLines...)
	}

	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}

	allLines = trimTrailingEmptyLines(allLines)
	return allLines, 0, nil
}

// trimTrailingEmptyLines removes empty lines from the end of a slice.
func trimTrailingEmptyLines(lines []string) []string {
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// cleanLogLine processes a log line to handle control characters.
// It handles:
//   - Carriage returns (progress bars): splits into multiple lines, keeping last segment
//   - Other control characters: removed
//   - Trailing whitespace: trimmed
func cleanLogLine(line string) []string {
	// Handle carriage returns (common in progress bars like curl)
	// Only keep the final segment after the last \r
	if strings.Contains(line, "\r") {
		segments := strings.Split(line, "\r")
		// Get the last non-empty segment (the final state of the line)
		for _, raw := range slices.Backward(segments) {
			segment := strings.TrimRightFunc(raw, unicode.IsSpace)
			if segment != "" {
				return []string{cleanControlChars(segment)}
			}
		}
		return nil
	}

	// Clean other control characters and trim
	cleaned := cleanControlChars(line)
	cleaned = strings.TrimRightFunc(cleaned, unicode.IsSpace)
	if cleaned == "" {
		return nil
	}
	return []string{cleaned}
}

// cleanControlChars removes non-printable control characters from a string,
// preserving tabs and spaces.
func cleanControlChars(s string) string {
	var result strings.Builder
	result.Grow(len(s))

	for _, r := range s {
		// Keep printable characters, tabs, and spaces
		if r >= 32 || r == '\t' {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// isBinaryContent checks if the content appears to be binary data.
// It uses two heuristics:
//  1. Presence of null bytes (strong indicator)
//  2. High percentage (>30%) of non-printable characters
func isBinaryContent(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	// Check for null bytes (strong indicator of binary)
	if slices.Contains(data, 0) {
		return true
	}

	// Count non-printable characters (excluding common whitespace)
	nonPrintable := 0
	for _, b := range data {
		if b < 32 && b != '\t' && b != '\n' && b != '\r' {
			nonPrintable++
		}
	}

	// If more than 30% non-printable, consider it binary
	return float64(nonPrintable)/float64(len(data)) > 0.3
}
