// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/unicode"
)

func TestReadLogLines(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	// Create a test log file with known content
	testLogPath := filepath.Join(tempDir, "test.log")
	testContent := []string{
		"Line 1",
		"Line 2",
		"Line 3",
		"Line 4",
		"Line 5",
		"Line 6",
		"Line 7",
		"Line 8",
		"Line 9",
		"Line 10",
	}
	err := os.WriteFile(testLogPath, []byte(strings.Join(testContent, "\n")), 0600)
	if err != nil {
		t.Fatalf("Failed to create test log file: %v", err)
	}

	// Create an empty log file for testing
	emptyLogPath := filepath.Join(tempDir, "empty.log")
	err = os.WriteFile(emptyLogPath, []byte(""), 0600)
	if err != nil {
		t.Fatalf("Failed to create empty log file: %v", err)
	}

	tests := []struct {
		name          string
		filePath      string
		options       LogReadOptions
		expectedLines []string
		expectedCount int
		expectedTotal int
		expectedMore  bool
		expectError   bool
	}{
		{
			name:          "ReadEntireFile",
			filePath:      testLogPath,
			options:       LogReadOptions{},
			expectedLines: testContent,
			expectedCount: 10,
			expectedTotal: 10,
			expectedMore:  false,
			expectError:   false,
		},
		{
			name:          "ReadWithHeadOption",
			filePath:      testLogPath,
			options:       LogReadOptions{Head: 3},
			expectedLines: testContent[:3],
			expectedCount: 3,
			expectedTotal: 10,
			expectedMore:  true,
			expectError:   false,
		},
		{
			name:          "ReadWithTailOption",
			filePath:      testLogPath,
			options:       LogReadOptions{Tail: 3},
			expectedLines: testContent[7:],
			expectedCount: 3,
			expectedTotal: 10,
			expectedMore:  true,
			expectError:   false,
		},
		{
			name:          "ReadWithOffsetAndLimit",
			filePath:      testLogPath,
			options:       LogReadOptions{Offset: 4, Limit: 3},
			expectedLines: testContent[3:6],
			expectedCount: 3,
			expectedTotal: 10,
			expectedMore:  true,
			expectError:   false,
		},
		{
			name:          "ReadWithOffsetBeyondFileSize",
			filePath:      testLogPath,
			options:       LogReadOptions{Offset: 20},
			expectedLines: []string{},
			expectedCount: 0,
			expectedTotal: 10,
			expectedMore:  false,
			expectError:   false,
		},
		{
			name:          "ReadWithHeadLargerThanFile",
			filePath:      testLogPath,
			options:       LogReadOptions{Head: 20},
			expectedLines: testContent,
			expectedCount: 10,
			expectedTotal: 10,
			expectedMore:  false,
			expectError:   false,
		},
		{
			name:          "ReadWithTailLargerThanFile",
			filePath:      testLogPath,
			options:       LogReadOptions{Tail: 20},
			expectedLines: testContent,
			expectedCount: 10,
			expectedTotal: 10,
			expectedMore:  false,
			expectError:   false,
		},
		{
			name:          "ReadEmptyFile",
			filePath:      emptyLogPath,
			options:       LogReadOptions{},
			expectedLines: []string{},
			expectedCount: 0,
			expectedTotal: 0,
			expectedMore:  false,
			expectError:   false,
		},
		{
			name:          "ReadNonExistentFile",
			filePath:      filepath.Join(tempDir, "nonexistent.log"),
			options:       LogReadOptions{},
			expectedLines: nil,
			expectedCount: 0,
			expectedTotal: 0,
			expectedMore:  false,
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ReadLogLines(tt.filePath, tt.options)

			// Check error expectation
			if (err != nil) != tt.expectError {
				t.Errorf("ReadLogLines() error = %v, expectError %v", err, tt.expectError)
				return
			}

			if tt.expectError {
				return // No need to check the result if we expected an error
			}

			// Check result
			if result.LineCount != tt.expectedCount {
				t.Errorf("ReadLogLines() LineCount = %v, want %v", result.LineCount, tt.expectedCount)
			}

			if result.TotalLines != tt.expectedTotal {
				t.Errorf("ReadLogLines() TotalLines = %v, want %v", result.TotalLines, tt.expectedTotal)
			}

			if result.HasMore != tt.expectedMore {
				t.Errorf("ReadLogLines() HasMore = %v, want %v", result.HasMore, tt.expectedMore)
			}

			if len(result.Lines) != len(tt.expectedLines) {
				t.Errorf("ReadLogLines() returned %v lines, want %v", len(result.Lines), len(tt.expectedLines))
				return
			}

			for i, line := range result.Lines {
				if line != tt.expectedLines[i] {
					t.Errorf("ReadLogLines() line %d = %q, want %q", i, line, tt.expectedLines[i])
				}
			}
		})
	}
}

func TestReadLogContent(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	// Create a test log file with known content
	testLogPath := filepath.Join(tempDir, "test.log")
	testContent := []string{
		"Line 1",
		"Line 2",
		"Line 3",
		"Line 4",
		"Line 5",
	}
	err := os.WriteFile(testLogPath, []byte(strings.Join(testContent, "\n")), 0600)
	if err != nil {
		t.Fatalf("Failed to create test log file: %v", err)
	}

	tests := []struct {
		name             string
		filePath         string
		options          LogReadOptions
		expectedString   string
		expectedCount    int
		expectedTotal    int
		expectedMore     bool
		expectedEstimate bool
		expectError      bool
	}{
		{
			name:             "ReadEntireFile",
			filePath:         testLogPath,
			options:          LogReadOptions{},
			expectedString:   strings.Join(testContent, "\n"),
			expectedCount:    5,
			expectedTotal:    5,
			expectedMore:     false,
			expectedEstimate: false,
			expectError:      false,
		},
		{
			name:             "ReadWithHeadOption",
			filePath:         testLogPath,
			options:          LogReadOptions{Head: 2},
			expectedString:   strings.Join(testContent[:2], "\n"),
			expectedCount:    2,
			expectedTotal:    5,
			expectedMore:     true,
			expectedEstimate: false,
			expectError:      false,
		},
		{
			name:             "ReadWithTailOption",
			filePath:         testLogPath,
			options:          LogReadOptions{Tail: 2},
			expectedString:   strings.Join(testContent[3:], "\n"),
			expectedCount:    2,
			expectedTotal:    5,
			expectedMore:     true,
			expectedEstimate: false,
			expectError:      false,
		},
		{
			name:             "ReadNonExistentFile",
			filePath:         filepath.Join(tempDir, "nonexistent.log"),
			options:          LogReadOptions{},
			expectedString:   "",
			expectedCount:    0,
			expectedTotal:    0,
			expectedMore:     false,
			expectedEstimate: false,
			expectError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, lineCount, totalLines, hasMore, isEstimate, err := ReadLogContent(tt.filePath, tt.options)

			// Check error expectation
			if (err != nil) != tt.expectError {
				t.Errorf("ReadLogContent() error = %v, expectError %v", err, tt.expectError)
				return
			}

			if tt.expectError {
				return // No need to check the result if we expected an error
			}

			// Check result
			if content != tt.expectedString {
				t.Errorf("ReadLogContent() content = %q, want %q", content, tt.expectedString)
			}

			if lineCount != tt.expectedCount {
				t.Errorf("ReadLogContent() lineCount = %v, want %v", lineCount, tt.expectedCount)
			}

			if totalLines != tt.expectedTotal {
				t.Errorf("ReadLogContent() totalLines = %v, want %v", totalLines, tt.expectedTotal)
			}

			if hasMore != tt.expectedMore {
				t.Errorf("ReadLogContent() hasMore = %v, want %v", hasMore, tt.expectedMore)
			}

			if isEstimate != tt.expectedEstimate {
				t.Errorf("ReadLogContent() isEstimate = %v, want %v", isEstimate, tt.expectedEstimate)
			}
		})
	}
}

func TestCountLinesExact(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	// Create test files with different line counts
	testCases := []struct {
		name      string
		content   string
		lineCount int
	}{
		{
			name:      "EmptyFile",
			content:   "",
			lineCount: 0,
		},
		{
			name:      "SingleLine",
			content:   "Line 1",
			lineCount: 1,
		},
		{
			name:      "MultipleLines",
			content:   "Line 1\nLine 2\nLine 3",
			lineCount: 3,
		},
		{
			name:      "LinesWithEmptyLines",
			content:   "Line 1\n\nLine 3",
			lineCount: 3,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create test file
			testFilePath := filepath.Join(tempDir, tc.name+".log")
			err := os.WriteFile(testFilePath, []byte(tc.content), 0600)
			if err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			// Count lines
			count, err := countLinesExact(testFilePath)
			if err != nil {
				t.Fatalf("countLines() error = %v", err)
			}

			// Check result
			if count != tc.lineCount {
				t.Errorf("countLines() = %v, want %v", count, tc.lineCount)
			}
		})
	}

	// Test non-existent file
	t.Run("NonExistentFile", func(t *testing.T) {
		_, err := countLinesExact(filepath.Join(tempDir, "nonexistent.log"))
		if err == nil {
			t.Errorf("countLines() expected error for non-existent file, got nil")
		}
	})
}

func TestReadFirstLines(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	// Create a test log file with known content
	testLogPath := filepath.Join(tempDir, "test.log")
	testContent := []string{
		"Line 1",
		"Line 2",
		"Line 3",
		"Line 4",
		"Line 5",
	}
	err := os.WriteFile(testLogPath, []byte(strings.Join(testContent, "\n")), 0600)
	if err != nil {
		t.Fatalf("Failed to create test log file: %v", err)
	}

	tests := []struct {
		name          string
		filePath      string
		n             int
		totalLines    int
		expectedLines []string
		expectedCount int
		expectedMore  bool
		expectError   bool
	}{
		{
			name:          "ReadFirst3Lines",
			filePath:      testLogPath,
			n:             3,
			totalLines:    5,
			expectedLines: testContent[:3],
			expectedCount: 3,
			expectedMore:  true,
			expectError:   false,
		},
		{
			name:          "ReadMoreLinesThanFileHas",
			filePath:      testLogPath,
			n:             10,
			totalLines:    5,
			expectedLines: testContent,
			expectedCount: 5,
			expectedMore:  false,
			expectError:   false,
		},
		{
			name:          "Read0Lines",
			filePath:      testLogPath,
			n:             0,
			totalLines:    5,
			expectedLines: []string{},
			expectedCount: 0,
			expectedMore:  true,
			expectError:   false,
		},
		{
			name:          "NonExistentFile",
			filePath:      filepath.Join(tempDir, "nonexistent.log"),
			n:             3,
			totalLines:    0,
			expectedLines: nil,
			expectedCount: 0,
			expectedMore:  false,
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := readFirstLines(tt.filePath, tt.n, tt.totalLines, lineDecoder{})

			// Check error expectation
			if (err != nil) != tt.expectError {
				t.Errorf("readFirstLines() error = %v, expectError %v", err, tt.expectError)
				return
			}

			if tt.expectError {
				return // No need to check the result if we expected an error
			}

			// Check result
			if result.LineCount != tt.expectedCount {
				t.Errorf("readFirstLines() LineCount = %v, want %v", result.LineCount, tt.expectedCount)
			}

			if result.TotalLines != tt.totalLines {
				t.Errorf("readFirstLines() TotalLines = %v, want %v", result.TotalLines, tt.totalLines)
			}

			if result.HasMore != tt.expectedMore {
				t.Errorf("readFirstLines() HasMore = %v, want %v", result.HasMore, tt.expectedMore)
			}

			if len(result.Lines) != len(tt.expectedLines) {
				t.Errorf("readFirstLines() returned %v lines, want %v", len(result.Lines), len(tt.expectedLines))
				return
			}

			for i, line := range result.Lines {
				if line != tt.expectedLines[i] {
					t.Errorf("readFirstLines() line %d = %q, want %q", i, line, tt.expectedLines[i])
				}
			}
		})
	}
}

func TestReadLastLines(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	// Create a test log file with known content
	testLogPath := filepath.Join(tempDir, "test.log")
	testContent := []string{
		"Line 1",
		"Line 2",
		"Line 3",
		"Line 4",
		"Line 5",
	}
	err := os.WriteFile(testLogPath, []byte(strings.Join(testContent, "\n")), 0600)
	if err != nil {
		t.Fatalf("Failed to create test log file: %v", err)
	}

	tests := []struct {
		name          string
		filePath      string
		n             int
		totalLines    int
		expectedLines []string
		expectedCount int
		expectedMore  bool
		expectError   bool
	}{
		{
			name:          "ReadLast3Lines",
			filePath:      testLogPath,
			n:             3,
			totalLines:    5,
			expectedLines: testContent[2:],
			expectedCount: 3,
			expectedMore:  true,
			expectError:   false,
		},
		{
			name:          "ReadMoreLinesThanFileHas",
			filePath:      testLogPath,
			n:             10,
			totalLines:    5,
			expectedLines: testContent,
			expectedCount: 5,
			expectedMore:  false,
			expectError:   false,
		},
		{
			name:          "Read0Lines",
			filePath:      testLogPath,
			n:             0,
			totalLines:    5,
			expectedLines: []string{},
			expectedCount: 0,
			expectedMore:  true,
			expectError:   false,
		},
		{
			name:          "NonExistentFile",
			filePath:      filepath.Join(tempDir, "nonexistent.log"),
			n:             3,
			totalLines:    0,
			expectedLines: nil,
			expectedCount: 0,
			expectedMore:  false,
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := readLastLines(tt.filePath, tt.n, tt.totalLines, lineDecoder{})

			// Check error expectation
			if (err != nil) != tt.expectError {
				t.Errorf("readLastLines() error = %v, expectError %v", err, tt.expectError)
				return
			}

			if tt.expectError {
				return // No need to check the result if we expected an error
			}

			// Check result
			if result.LineCount != tt.expectedCount {
				t.Errorf("readLastLines() LineCount = %v, want %v", result.LineCount, tt.expectedCount)
			}

			if result.TotalLines != tt.totalLines {
				t.Errorf("readLastLines() TotalLines = %v, want %v", result.TotalLines, tt.totalLines)
			}

			if result.HasMore != tt.expectedMore {
				t.Errorf("readLastLines() HasMore = %v, want %v", result.HasMore, tt.expectedMore)
			}

			if len(result.Lines) != len(tt.expectedLines) {
				t.Errorf("readLastLines() returned %v lines, want %v", len(result.Lines), len(tt.expectedLines))
				return
			}

			for i, line := range result.Lines {
				if line != tt.expectedLines[i] {
					t.Errorf("readLastLines() line %d = %q, want %q", i, line, tt.expectedLines[i])
				}
			}
		})
	}
}

func TestReadLinesRange(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	// Create a test log file with known content
	testLogPath := filepath.Join(tempDir, "test.log")
	testContent := []string{
		"Line 1",
		"Line 2",
		"Line 3",
		"Line 4",
		"Line 5",
	}
	err := os.WriteFile(testLogPath, []byte(strings.Join(testContent, "\n")), 0600)
	if err != nil {
		t.Fatalf("Failed to create test log file: %v", err)
	}

	tests := []struct {
		name          string
		filePath      string
		offset        int
		limit         int
		totalLines    int
		expectedLines []string
		expectedCount int
		expectedMore  bool
		expectError   bool
	}{
		{
			name:          "ReadLines24",
			filePath:      testLogPath,
			offset:        2,
			limit:         3,
			totalLines:    5,
			expectedLines: testContent[1:4],
			expectedCount: 3,
			expectedMore:  true,
			expectError:   false,
		},
		{
			name:          "ReadBeyondFileEnd",
			filePath:      testLogPath,
			offset:        4,
			limit:         10,
			totalLines:    5,
			expectedLines: testContent[3:],
			expectedCount: 2,
			expectedMore:  false,
			expectError:   false,
		},
		{
			name:          "OffsetBeyondFileSize",
			filePath:      testLogPath,
			offset:        10,
			limit:         5,
			totalLines:    5,
			expectedLines: []string{},
			expectedCount: 0,
			expectedMore:  false,
			expectError:   false,
		},
		{
			name:          "NegativeOffset",
			filePath:      testLogPath,
			offset:        -1,
			limit:         3,
			totalLines:    5,
			expectedLines: testContent[:3],
			expectedCount: 3,
			expectedMore:  true,
			expectError:   false,
		},
		{
			name:          "ZeroLimit",
			filePath:      testLogPath,
			offset:        2,
			limit:         0,
			totalLines:    5,
			expectedLines: []string{},
			expectedCount: 0,
			expectedMore:  true,
			expectError:   false,
		},
		{
			name:          "NonExistentFile",
			filePath:      filepath.Join(tempDir, "nonexistent.log"),
			offset:        1,
			limit:         5,
			totalLines:    0,
			expectedLines: nil,
			expectedCount: 0,
			expectedMore:  false,
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := readLinesRange(tt.filePath, tt.offset, tt.limit, tt.totalLines, lineDecoder{})

			// Check error expectation
			if (err != nil) != tt.expectError {
				t.Errorf("readLinesRange() error = %v, expectError %v", err, tt.expectError)
				return
			}

			if tt.expectError {
				return // No need to check the result if we expected an error
			}

			// Check result
			if result.LineCount != tt.expectedCount {
				t.Errorf("readLinesRange() LineCount = %v, want %v", result.LineCount, tt.expectedCount)
			}

			if result.TotalLines != tt.totalLines {
				t.Errorf("readLinesRange() TotalLines = %v, want %v", result.TotalLines, tt.totalLines)
			}

			if result.HasMore != tt.expectedMore {
				t.Errorf("readLinesRange() HasMore = %v, want %v", result.HasMore, tt.expectedMore)
			}

			if len(result.Lines) != len(tt.expectedLines) {
				t.Errorf("readLinesRange() returned %v lines, want %v", len(result.Lines), len(tt.expectedLines))
				return
			}

			for i, line := range result.Lines {
				if line != tt.expectedLines[i] {
					t.Errorf("readLinesRange() line %d = %q, want %q", i, line, tt.expectedLines[i])
				}
			}
		})
	}
}

func TestEstimateLineCount(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	// Test cases
	tests := []struct {
		name             string
		content          string
		expectedCount    int
		expectedEstimate bool
		fileSize         int // in KB
	}{
		{
			name:             "SmallFileExactCount",
			content:          strings.Repeat("Line content\n", 100),
			expectedCount:    100,
			expectedEstimate: false,
			fileSize:         1, // 1KB
		},
		{
			name:             "MediumFileEstimatedCount",
			content:          strings.Repeat("Medium line content for testing estimation\n", 5000),
			expectedCount:    5000,
			expectedEstimate: true,
			fileSize:         200, // 200KB
		},
		{
			name:             "FileWithVaryingLineLengths",
			content:          generateVaryingLineLengths(5000),
			expectedCount:    5000,
			expectedEstimate: true,
			fileSize:         150, // 150KB
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test file with specified content
			testFilePath := filepath.Join(tempDir, tt.name+".log")

			// For small files, write the exact content
			if tt.fileSize <= 10 {
				err := os.WriteFile(testFilePath, []byte(tt.content), 0600)
				if err != nil {
					t.Fatalf("Failed to create test file: %v", err)
				}
			} else {
				// For larger files, generate content to match the specified size
				// This is more efficient than generating a huge string in memory
				file, err := os.Create(testFilePath)
				if err != nil {
					t.Fatalf("Failed to create test file: %v", err)
				}
				defer func() {
					_ = file.Close()
				}()

				// Write content in chunks to reach desired file size
				contentBytes := []byte(tt.content)
				targetSize := tt.fileSize * 1024
				written := 0

				for written < targetSize {
					n, err := file.Write(contentBytes)
					if err != nil {
						t.Fatalf("Failed to write to test file: %v", err)
					}
					written += n
				}
			}

			// Get actual line count for verification
			actualLines, err := countLinesExact(testFilePath)
			if err != nil {
				t.Fatalf("Failed to count lines: %v", err)
			}

			// Test the estimateLineCount function
			count, isEstimate, err := estimateLineCount(testFilePath)
			if err != nil {
				t.Fatalf("estimateLineCount() error = %v", err)
			}

			// For small files, we expect exact counts
			if tt.fileSize < 10 {
				if isEstimate {
					t.Errorf("estimateLineCount() isEstimate = %v, want %v", isEstimate, false)
				}

				if count != actualLines {
					t.Errorf("estimateLineCount() count = %v, want %v", count, actualLines)
				}
			} else {
				// For large files, we expect estimates
				if !isEstimate {
					t.Errorf("estimateLineCount() isEstimate = %v, want %v", isEstimate, true)
				}

				// The estimate should be within 10% of the actual count
				errorMargin := float64(actualLines) * 0.1
				if float64(count) < float64(actualLines)-errorMargin || float64(count) > float64(actualLines)+errorMargin {
					t.Errorf("estimateLineCount() count = %v, actual = %v, outside 10%% error margin", count, actualLines)
				}
			}
		})
	}
}

// Helper function to generate content with varying line lengths
func generateVaryingLineLengths(lineCount int) string {
	var builder strings.Builder
	for i := range lineCount {
		// Vary line length between 10 and 100 characters
		lineLength := 10 + (i % 91)
		line := strings.Repeat("x", lineLength) + "\n"
		builder.WriteString(line)
	}
	return builder.String()
}

// A non-UTF-8 charset decodes only the lines that are not already UTF-8, so a
// log that mixes UTF-8 and code page output stays readable.
func TestReadLogLinesEncoding(t *testing.T) {
	encode := func(enc encoding.Encoding, s string) []byte {
		b, err := enc.NewEncoder().Bytes([]byte(s))
		if err != nil {
			t.Fatalf("Failed to encode %q: %v", s, err)
		}
		return b
	}
	mixed := slices.Concat(
		[]byte("開始\r\n"),
		encode(japanese.ShiftJIS, "こんにちは\r\n"),
		[]byte("done\r\n終了\r\n"),
	)

	tests := []struct {
		name    string
		content []byte
		options LogReadOptions
		want    []string
	}{
		{
			name:    "UTF8UnderShiftJIS",
			content: []byte("日本語の出力テスト\r\n"),
			options: LogReadOptions{Encoding: "shift_jis"},
			want:    []string{"日本語の出力テスト"},
		},
		{
			name:    "ShiftJIS",
			content: encode(japanese.ShiftJIS, "こんにちは\n"),
			options: LogReadOptions{Encoding: "shift_jis"},
			want:    []string{"こんにちは"},
		},
		{
			name:    "MixedHead",
			content: mixed,
			options: LogReadOptions{Encoding: "shift_jis", Head: 2},
			want:    []string{"開始", "こんにちは"},
		},
		{
			name:    "MixedTail",
			content: mixed,
			options: LogReadOptions{Encoding: "shift_jis", Tail: 3},
			want:    []string{"こんにちは", "done", "終了"},
		},
		{
			name:    "MixedRange",
			content: mixed,
			options: LogReadOptions{Encoding: "shift_jis", Offset: 2, Limit: 3},
			want:    []string{"こんにちは", "done", "終了"},
		},
		{
			// ISO-2022-JP text is 7-bit, which is also valid UTF-8.
			name:    "ISO2022JP",
			content: encode(japanese.ISO2022JP, "日本語\n"),
			options: LogReadOptions{Encoding: "iso-2022-jp"},
			want:    []string{"日本語"},
		},
		{
			// "~" before a newline continues the line in HZ-GB2312.
			name:    "HZGB2312Continuation",
			content: []byte("foo~\nbar\n"),
			options: LogReadOptions{Encoding: "hz-gb-2312"},
			want:    []string{"foobar"},
		},
		{
			name:    "UTF16LE",
			content: encode(unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM), "A\n日本\n"),
			options: LogReadOptions{Encoding: "utf-16le"},
			want:    []string{"A", "日本"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "step.out")
			if err := os.WriteFile(path, tt.content, 0600); err != nil {
				t.Fatalf("Failed to create test log file: %v", err)
			}

			result, err := ReadLogLines(path, tt.options)
			if err != nil {
				t.Fatalf("ReadLogLines() error = %v", err)
			}
			if !slices.Equal(result.Lines, tt.want) {
				t.Errorf("ReadLogLines() lines = %q, want %q", result.Lines, tt.want)
			}
		})
	}
}

func TestDecodeString(t *testing.T) {
	tests := []struct {
		name     string
		charset  string
		input    []byte
		expected string
	}{
		{
			name:     "EmptyInput",
			charset:  "utf-8",
			input:    []byte{},
			expected: "",
		},
		{
			name:     "UTF8NoDecoding",
			charset:  "utf-8",
			input:    []byte("Hello, World!"),
			expected: "Hello, World!",
		},
		{
			name:     "EmptyCharset",
			charset:  "",
			input:    []byte("Hello, World!"),
			expected: "Hello, World!",
		},
		{
			name:     "UnknownCharset",
			charset:  "unknown-charset-xyz",
			input:    []byte("Hello, World!"),
			expected: "Hello, World!",
		},
		{
			name:    "ShiftJIS",
			charset: "shift_jis",
			// "こんにちは" in Shift_JIS encoding
			input:    []byte{0x82, 0xb1, 0x82, 0xf1, 0x82, 0xc9, 0x82, 0xbf, 0x82, 0xcd},
			expected: "こんにちは",
		},
		{
			name:    "EUCJP",
			charset: "euc-jp",
			// "日本語" in EUC-JP encoding
			input:    []byte{0xc6, 0xfc, 0xcb, 0xdc, 0xb8, 0xec},
			expected: "日本語",
		},
		{
			name:    "ISO88591Latin1",
			charset: "iso-8859-1",
			// "café" with é as 0xe9 in ISO-8859-1
			input:    []byte{0x63, 0x61, 0x66, 0xe9},
			expected: "café",
		},
		{
			name:    "Windows1252",
			charset: "windows-1252",
			// "naïve" with ï as 0xef in Windows-1252
			input:    []byte{0x6e, 0x61, 0xef, 0x76, 0x65},
			expected: "naïve",
		},
		{
			name:    "GBK",
			charset: "gbk",
			// "中文" in GBK encoding
			input:    []byte{0xd6, 0xd0, 0xce, 0xc4},
			expected: "中文",
		},
		{
			name:    "Big5",
			charset: "big5",
			// "台" in Big5 encoding
			input:    []byte{0xa5, 0x78},
			expected: "台",
		},
		{
			name:    "EUCKR",
			charset: "euc-kr",
			// "한글" in EUC-KR encoding
			input:    []byte{0xc7, 0xd1, 0xb1, 0xdb},
			expected: "한글",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DecodeString(tt.charset, tt.input)
			if result != tt.expected {
				t.Errorf("DecodeString(%q, %v) = %q, want %q", tt.charset, tt.input, result, tt.expected)
			}
		})
	}
}

func TestDecodeStringCharsetNormalization(t *testing.T) {
	// Test that charset names are properly normalized
	// "こんにちは" in Shift_JIS encoding
	shiftJISInput := []byte{0x82, 0xb1, 0x82, 0xf1, 0x82, 0xc9, 0x82, 0xbf, 0x82, 0xcd}
	expected := "こんにちは"

	charsetVariations := []string{
		"shift_jis",
		"shift-jis",
		"shiftjis",
		"sjis",
		"s-jis",
	}

	for _, charset := range charsetVariations {
		t.Run(charset, func(t *testing.T) {
			result := DecodeString(charset, shiftJISInput)
			if result != expected {
				t.Errorf("DecodeString(%q, input) = %q, want %q", charset, result, expected)
			}
		})
	}
}
