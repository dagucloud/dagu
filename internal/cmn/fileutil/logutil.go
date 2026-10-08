// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// LogReadOptions defines options for reading log files
type LogReadOptions struct {
	Head     int    // Number of lines from the beginning
	Tail     int    // Number of lines from the end
	Offset   int    // Line number to start from (1-based)
	Limit    int    // Maximum number of lines to return
	Encoding string // Charset for lines that are not valid UTF-8 (e.g., "shift_jis", "euc-jp")
}

// LogResult represents the result of reading a log file
type LogResult struct {
	Lines      []string // The lines read from the file
	LineCount  int      // Number of lines returned
	TotalLines int      // Total number of lines in the file
	HasMore    bool     // Whether there are more lines available
	IsEstimate bool     // Whether the TotalLines count is an estimate
}

// getEncodingDecoder returns an encoding.Decoder for the given charset name.
// Returns nil for UTF-8 or empty charset (no decoding needed).
// Supports a wide range of encodings including Japanese, Chinese, Korean,
// and various ISO-8859 and Windows code pages.
func getEncodingDecoder(charset string) *encoding.Decoder {
	if charset == "" {
		return nil
	}

	switch normalizeCharset(charset) {
	// UTF-8 (no decoder needed)
	case "utf-8", "utf8":
		return nil

	// Japanese encodings
	case "euc-jp", "eucjp":
		return japanese.EUCJP.NewDecoder()
	case "shift-jis", "shiftjis", "sjis", "s-jis", "x-sjis", "ms-kanji", "csshiftjis":
		return japanese.ShiftJIS.NewDecoder()
	case "iso-2022-jp", "iso2022jp", "csiso2022jp":
		return japanese.ISO2022JP.NewDecoder()

	// Simplified Chinese encodings
	case "gb2312", "gb-2312", "csgb2312":
		return simplifiedchinese.GBK.NewDecoder()
	case "gbk", "cp936", "ms936", "windows-936":
		return simplifiedchinese.GBK.NewDecoder()
	case "gb18030":
		return simplifiedchinese.GB18030.NewDecoder()
	case "hz-gb-2312", "hz":
		return simplifiedchinese.HZGB2312.NewDecoder()

	// Traditional Chinese encodings
	case "big5", "big-5", "csbig5", "x-x-big5", "cn-big5":
		return traditionalchinese.Big5.NewDecoder()

	// Korean encodings
	case "euc-kr", "euckr", "cseuckr", "ks-c-5601-1987", "ksc5601", "iso-ir-149", "korean":
		return korean.EUCKR.NewDecoder()

	// ISO-8859 encodings (Latin character sets)
	case "iso-8859-1", "iso88591", "latin1", "latin-1", "l1", "csisolatin1", "iso-ir-100", "ibm819", "cp819":
		return charmap.ISO8859_1.NewDecoder()
	case "iso-8859-2", "iso88592", "latin2", "latin-2", "l2", "csisolatin2", "iso-ir-101":
		return charmap.ISO8859_2.NewDecoder()
	case "iso-8859-3", "iso88593", "latin3", "latin-3", "l3", "csisolatin3", "iso-ir-109":
		return charmap.ISO8859_3.NewDecoder()
	case "iso-8859-4", "iso88594", "latin4", "latin-4", "l4", "csisolatin4", "iso-ir-110":
		return charmap.ISO8859_4.NewDecoder()
	case "iso-8859-5", "iso88595", "cyrillic", "iso-ir-144", "csisolatincyrillic":
		return charmap.ISO8859_5.NewDecoder()
	case "iso-8859-6", "iso88596", "arabic", "iso-ir-127", "csisolatinarabic", "ecma-114", "asmo-708":
		return charmap.ISO8859_6.NewDecoder()
	case "iso-8859-7", "iso88597", "greek", "greek8", "iso-ir-126", "csisolatingreek", "ecma-118", "elot-928":
		return charmap.ISO8859_7.NewDecoder()
	case "iso-8859-8", "iso88598", "hebrew", "iso-ir-138", "csisolatinhebrew":
		return charmap.ISO8859_8.NewDecoder()
	case "iso-8859-9", "iso88599", "latin5", "latin-5", "l5", "iso-ir-148", "csisolatin5", "turkish":
		return charmap.ISO8859_9.NewDecoder()
	case "iso-8859-10", "iso885910", "latin6", "latin-6", "l6", "iso-ir-157", "csisolatin6":
		return charmap.ISO8859_10.NewDecoder()
	case "iso-8859-13", "iso885913", "latin7", "latin-7", "l7":
		return charmap.ISO8859_13.NewDecoder()
	case "iso-8859-14", "iso885914", "latin8", "latin-8", "l8", "iso-ir-199", "iso-celtic":
		return charmap.ISO8859_14.NewDecoder()
	case "iso-8859-15", "iso885915", "latin9", "latin-9", "l9", "latin0":
		return charmap.ISO8859_15.NewDecoder()
	case "iso-8859-16", "iso885916", "latin10", "latin-10", "l10", "iso-ir-226":
		return charmap.ISO8859_16.NewDecoder()

	// Windows code pages
	case "windows-1250", "cp1250", "x-cp1250":
		return charmap.Windows1250.NewDecoder()
	case "windows-1251", "cp1251", "x-cp1251":
		return charmap.Windows1251.NewDecoder()
	case "windows-1252", "cp1252", "x-cp1252", "ansi":
		return charmap.Windows1252.NewDecoder()
	case "windows-1253", "cp1253", "x-cp1253":
		return charmap.Windows1253.NewDecoder()
	case "windows-1254", "cp1254", "x-cp1254":
		return charmap.Windows1254.NewDecoder()
	case "windows-1255", "cp1255", "x-cp1255":
		return charmap.Windows1255.NewDecoder()
	case "windows-1256", "cp1256", "x-cp1256":
		return charmap.Windows1256.NewDecoder()
	case "windows-1257", "cp1257", "x-cp1257":
		return charmap.Windows1257.NewDecoder()
	case "windows-1258", "cp1258", "x-cp1258":
		return charmap.Windows1258.NewDecoder()

	// Cyrillic encodings
	case "koi8-r", "koi8r", "cskoi8r":
		return charmap.KOI8R.NewDecoder()
	case "koi8-u", "koi8u":
		return charmap.KOI8U.NewDecoder()

	// IBM code pages
	case "ibm437", "cp437", "437", "cspc8codepage437":
		return charmap.CodePage437.NewDecoder()
	case "ibm850", "cp850", "850", "cspc850multilingual":
		return charmap.CodePage850.NewDecoder()
	case "ibm852", "cp852", "852":
		return charmap.CodePage852.NewDecoder()
	case "ibm855", "cp855", "855":
		return charmap.CodePage855.NewDecoder()
	case "ibm858", "cp858", "858":
		return charmap.CodePage858.NewDecoder()
	case "ibm860", "cp860", "860":
		return charmap.CodePage860.NewDecoder()
	case "ibm862", "cp862", "862":
		return charmap.CodePage862.NewDecoder()
	case "ibm863", "cp863", "863":
		return charmap.CodePage863.NewDecoder()
	case "ibm865", "cp865", "865":
		return charmap.CodePage865.NewDecoder()
	case "ibm866", "cp866", "866", "csibm866":
		return charmap.CodePage866.NewDecoder()

	// Mac encodings
	case "macintosh", "mac", "macroman", "csmacintosh", "x-mac-roman":
		return charmap.Macintosh.NewDecoder()

	// Unicode variants
	case "utf-16", "utf16":
		return unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder()
	case "utf-16le", "utf16le":
		return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()
	case "utf-16be", "utf16be":
		return unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM).NewDecoder()

	default:
		// Unknown encoding, return nil (treat as UTF-8)
		return nil
	}
}

// normalizeCharset lowercases a charset name and spells its separators as hyphens.
func normalizeCharset(charset string) string {
	normalized := strings.ToLower(strings.ReplaceAll(charset, "_", "-"))
	return strings.ReplaceAll(normalized, " ", "-")
}

// lineDecoder turns the raw lines of a log file into UTF-8 text.
type lineDecoder struct {
	// stream decodes the whole file before it is split into lines.
	stream *encoding.Decoder
	// fallback decodes a line that is not already UTF-8.
	fallback *encoding.Decoder
}

// newLineDecoder returns the lineDecoder for charset. An empty, UTF-8, or
// unknown charset keeps every line as is.
func newLineDecoder(charset string) lineDecoder {
	decoder := getEncodingDecoder(charset)
	switch normalizeCharset(charset) {
	// A line cannot be judged on its own in these charsets: the UTF-16
	// newline spans two bytes, and ISO-2022-JP and HZ-GB2312 are 7-bit,
	// so always valid UTF-8, with shift state that carries across lines.
	case "utf-16", "utf16", "utf-16le", "utf16le", "utf-16be", "utf16be",
		"iso-2022-jp", "iso2022jp", "csiso2022jp",
		"hz-gb-2312", "hz":
		return lineDecoder{stream: decoder}
	}
	return lineDecoder{fallback: decoder}
}

// reader returns r, decoded first when its lines cannot be split on raw bytes.
func (d lineDecoder) reader(r io.Reader) io.Reader {
	if d.stream == nil {
		return r
	}
	return transform.NewReader(r, d.stream)
}

// text returns a scanned line as UTF-8 text. A line that is already valid
// UTF-8 is kept as is, so UTF-8 output survives a code page charset.
func (d lineDecoder) text(line []byte) string {
	if d.fallback == nil || utf8.Valid(line) {
		return string(line)
	}
	decoded, err := d.fallback.Bytes(line)
	if err != nil {
		return string(line)
	}
	return string(decoded)
}

// ReadLogLines reads a specific portion of a log file without loading the entire file into memory
func ReadLogLines(filePath string, options LogReadOptions) (*LogResult, error) {
	// Check if file exists
	if !FileExists(filePath) {
		return nil, fmt.Errorf("file not found: %s: %w", filePath, os.ErrNotExist)
	}

	// Get file info for size
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("error getting file info: %w", err)
	}

	// If file is empty, return empty result
	if fileInfo.Size() == 0 {
		return &LogResult{
			Lines:      []string{},
			LineCount:  0,
			TotalLines: 0,
			HasMore:    false,
			IsEstimate: false,
		}, nil
	}

	decoder := newLineDecoder(options.Encoding)

	// Estimate or count total lines in the file
	totalLines, isEstimate, err := estimateLineCount(filePath)
	if err != nil {
		return nil, fmt.Errorf("error counting lines: %w", err)
	}

	// If tail is specified, read from the end
	if options.Tail > 0 {
		result, err := readLastLines(filePath, options.Tail, totalLines, decoder)
		if err != nil {
			return nil, err
		}
		result.IsEstimate = isEstimate
		return result, nil
	}

	// If head is specified, read from the beginning
	if options.Head > 0 {
		result, err := readFirstLines(filePath, options.Head, totalLines, decoder)
		if err != nil {
			return nil, err
		}
		result.IsEstimate = isEstimate
		return result, nil
	}

	// If offset and limit are specified, read a specific range
	if options.Offset > 0 {
		limit := options.Limit
		if limit <= 0 {
			limit = 1000 // Default limit
		}
		result, err := readLinesRange(filePath, options.Offset, limit, totalLines, decoder)
		if err != nil {
			return nil, err
		}
		result.IsEstimate = isEstimate
		return result, nil
	}

	// Default: read with a reasonable limit
	limit := options.Limit
	if limit <= 0 {
		limit = 1000 // Default limit
	}
	result, err := readLinesRange(filePath, 1, limit, totalLines, decoder)
	if err != nil {
		return nil, err
	}
	result.IsEstimate = isEstimate
	return result, nil
}

// estimateLineCount estimates the number of lines in a file based on file size and sampling
func estimateLineCount(filePath string) (int, bool, error) {
	// Get file info for size
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return 0, false, fmt.Errorf("error getting file info: %w", err)
	}

	fileSize := fileInfo.Size()

	// For small files (e.g., < 10KB), just count the lines exactly
	if fileSize < 10*1024 {
		exactCount, err := countLinesExact(filePath)
		return exactCount, false, err
	}

	// For large files, sample the file to estimate average line length
	sampleSize := min(
		// 1MB sample
		int64(1024*1024), fileSize)

	file, err := os.Open(filePath) //nolint:gosec
	if err != nil {
		return 0, false, err
	}
	defer func() {
		_ = file.Close()
	}()
	// Sample from the beginning of the file
	startSample := make([]byte, sampleSize)
	_, err = file.Read(startSample)
	if err != nil {
		return 0, false, err
	}

	// Count newlines in the sample
	startLineCount := bytes.Count(startSample, []byte{'\n'})

	// If the file is large enough, take another sample from the middle
	var middleLineCount int
	if fileSize > sampleSize*2 {
		_, err = file.Seek(fileSize/2, 0)
		if err != nil {
			return 0, false, err
		}

		middleSample := make([]byte, sampleSize)
		_, err = file.Read(middleSample)
		if err != nil && err != io.EOF {
			return 0, false, err
		}

		middleLineCount = bytes.Count(middleSample, []byte{'\n'})
	}

	// Calculate average line length from samples
	totalSampleLines := startLineCount + middleLineCount
	if totalSampleLines == 0 {
		// Avoid division by zero; assume 1 line
		return 1, true, nil
	}

	var avgLineLength float64
	if fileSize > sampleSize*2 {
		// Average of start and middle samples
		avgLineLength = float64(sampleSize*2) / float64(totalSampleLines)
	} else {
		// Just use start sample
		avgLineLength = float64(sampleSize) / float64(startLineCount)
	}

	// Estimate total lines based on file size and average line length
	estimatedLines := int(float64(fileSize) / avgLineLength)

	// Add a small buffer to the estimate to ensure we don't underestimate
	estimatedLines = int(float64(estimatedLines) * 1.05)

	return estimatedLines, true, nil
}

// countLinesExact counts the exact number of lines in a file
func countLinesExact(filePath string) (int, error) {
	file, err := os.Open(filePath) //nolint:gosec
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = file.Close()
	}()

	scanner := bufio.NewScanner(file)
	ConfigureScanner(scanner)
	lineCount := 0
	for scanner.Scan() {
		lineCount++
	}

	if err := scanner.Err(); err != nil {
		return 0, err
	}

	return lineCount, nil
}

// readFirstLines reads the first n lines from a file
func readFirstLines(filePath string, n int, totalLines int, decoder lineDecoder) (*LogResult, error) {
	file, err := os.Open(filePath) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	scanner := bufio.NewScanner(decoder.reader(file))
	ConfigureScanner(scanner)
	lines := make([]string, 0, n)
	lineCount := 0

	for scanner.Scan() && lineCount < n {
		lines = append(lines, decoder.text(scanner.Bytes()))
		lineCount++
	}

	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return &LogResult{
				Lines: []string{"Error: Line too long to read."},
			}, nil
		}
		return nil, err
	}

	return &LogResult{
		Lines:      lines,
		LineCount:  lineCount,
		TotalLines: totalLines,
		HasMore:    lineCount < totalLines,
		IsEstimate: false, // Will be set by the caller
	}, nil
}

// readLastLines reads the last n lines from a file
func readLastLines(filePath string, n int, totalLines int, decoder lineDecoder) (*LogResult, error) {
	// If n is 0, return empty result
	if n <= 0 {
		return &LogResult{
			Lines:      []string{},
			LineCount:  0,
			TotalLines: totalLines,
			HasMore:    totalLines > 0,
			IsEstimate: false, // Will be set by the caller
		}, nil
	}

	file, err := os.Open(filePath) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	// If n is greater than or equal to total lines, read all lines
	if n >= totalLines {
		return readFirstLines(filePath, totalLines, totalLines, decoder)
	}

	// Use a ring buffer to keep the last n lines
	ring := make([]string, n)
	scanner := bufio.NewScanner(decoder.reader(file))
	ConfigureScanner(scanner)
	lineCount := 0
	ringIndex := 0

	for scanner.Scan() {
		ring[ringIndex] = decoder.text(scanner.Bytes())
		ringIndex = (ringIndex + 1) % n
		lineCount++
	}

	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return &LogResult{
				Lines: []string{"Error: Line too long to read."},
			}, nil
		}
		return nil, err
	}

	// Rearrange the ring buffer to get the lines in the correct order
	result := make([]string, 0, n)
	if lineCount < n {
		// If we have fewer lines than requested, just return all lines
		result = ring[:lineCount]
	} else {
		// Otherwise, rearrange the ring buffer
		result = append(result, ring[ringIndex:]...)
		result = append(result, ring[:ringIndex]...)
	}

	return &LogResult{
		Lines:      result,
		LineCount:  len(result),
		TotalLines: totalLines,
		HasMore:    len(result) < totalLines,
		IsEstimate: false, // Will be set by the caller
	}, nil
}

// readLinesRange reads a range of lines from a file
func readLinesRange(filePath string, offset, limit int, totalLines int, decoder lineDecoder) (*LogResult, error) {
	file, err := os.Open(filePath) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	// Adjust offset if it's out of range
	if offset < 1 {
		offset = 1
	}
	if offset > totalLines {
		return &LogResult{
			Lines:      []string{},
			LineCount:  0,
			TotalLines: totalLines,
			HasMore:    false,
			IsEstimate: false, // Will be set by the caller
		}, nil
	}

	scanner := bufio.NewScanner(decoder.reader(file))
	ConfigureScanner(scanner)
	lineNum := 1
	lines := make([]string, 0, limit)

	// Skip lines before the offset
	for lineNum < offset && scanner.Scan() {
		lineNum++
	}

	// Read lines from offset to offset+limit
	for lineNum <= totalLines && len(lines) < limit && scanner.Scan() {
		lines = append(lines, decoder.text(scanner.Bytes()))
		lineNum++
	}

	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return &LogResult{Lines: []string{"Error: Line too long to read."}}, nil
		}
		return nil, err
	}

	return &LogResult{
		Lines:      lines,
		LineCount:  len(lines),
		TotalLines: totalLines,
		HasMore:    offset+len(lines)-1 < totalLines,
		IsEstimate: false, // Will be set by the caller
	}, nil
}

// ReadLogContent reads a specific portion of a log file and returns it as a string
// nolint:revive
func ReadLogContent(filePath string, options LogReadOptions) (string, int, int, bool, bool, error) {
	result, err := ReadLogLines(filePath, options)
	if err != nil {
		return "", 0, 0, false, false, err
	}

	content := strings.Join(result.Lines, "\n")
	return content, result.LineCount, result.TotalLines, result.HasMore, result.IsEstimate, nil
}

// DecodeString decodes a byte slice using the specified character encoding.
// If the encoding is empty, UTF-8, or unknown, the bytes are returned as-is.
// This function is useful for converting log output from non-UTF-8 encodings
// (such as Shift_JIS, EUC-JP, etc.) to UTF-8 strings.
func DecodeString(charset string, data []byte) string {
	if len(data) == 0 {
		return ""
	}

	decoder := getEncodingDecoder(charset)
	if decoder == nil {
		// No decoding needed (UTF-8 or unknown encoding)
		return string(data)
	}

	// Decode the bytes using the specified encoding
	decoded, err := decoder.Bytes(data)
	if err != nil {
		// If decoding fails, return the original bytes as string
		return string(data)
	}

	return string(decoded)
}
