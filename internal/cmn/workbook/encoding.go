// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
)

// Encoding is the text encoding of a CSV file.
type Encoding string

// Encodings.
const (
	// EncodingUTF8 is UTF-8 without a byte order mark; reading accepts one.
	EncodingUTF8 Encoding = "utf-8"
	// EncodingUTF8BOM is UTF-8 with a byte order mark, which Excel needs to
	// open a CSV as UTF-8 by double click.
	EncodingUTF8BOM Encoding = "utf-8-bom"
	// EncodingShiftJIS is Shift_JIS as Windows uses it (code page 932,
	// Windows-31J), the encoding of CSV files from Japanese Excel.
	EncodingShiftJIS Encoding = "shift_jis"
)

// ParseEncoding reads an encoding option. Empty is no encoding, the zero
// Encoding, which LoadOptions detects. Shift_JIS accepts the names it goes
// by: shift_jis, shift-jis, sjis, cp932, windows-31j, and ms932.
func ParseEncoding(s string) (Encoding, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return "", nil
	case "utf-8", "utf8":
		return EncodingUTF8, nil
	case "utf-8-bom", "utf8-bom", "utf-8-sig":
		return EncodingUTF8BOM, nil
	case "shift_jis", "shift-jis", "shiftjis", "sjis", "cp932", "windows-31j", "ms932":
		return EncodingShiftJIS, nil
	default:
		return "", fmt.Errorf("unknown encoding %q: use utf-8, utf-8-bom, or shift_jis", s)
	}
}

// charset returns the character set behind an encoding, or nil for UTF-8.
func (e Encoding) charset() encoding.Encoding {
	if e == EncodingShiftJIS {
		return japanese.ShiftJIS
	}
	return nil
}

// utf8BOM is the UTF-8 byte order mark.
var utf8BOM = []byte("\xEF\xBB\xBF")

// Decoding errors.
var (
	// ErrNotUTF8 is a file read as UTF-8 that is not valid UTF-8.
	ErrNotUTF8 = errors.New("not valid UTF-8")
	// ErrUnknownEncoding is a file, read without an encoding, that is
	// neither UTF-8 nor Shift_JIS.
	ErrUnknownEncoding = errors.New("not valid UTF-8 or Shift_JIS")
)

// DetectText decodes a text file whose encoding is not given, as Japanese
// Windows writes them: a UTF-8 byte order mark means UTF-8, valid UTF-8 is
// UTF-8, and otherwise the bytes are Shift_JIS when every one decodes as
// it. The mark is dropped, and UTF-8 input is returned as it is, resliced
// past the mark, so a large file is not copied. Bytes that are neither are
// ErrUnknownEncoding, so a file in some other encoding fails instead of
// turning into replacement characters.
func DetectText(data []byte) ([]byte, Encoding, error) {
	if rest, marked := bytes.CutPrefix(data, utf8BOM); marked {
		if !utf8.Valid(rest) {
			return nil, "", ErrNotUTF8
		}
		return rest, EncodingUTF8BOM, nil
	}
	if utf8.Valid(data) {
		return data, EncodingUTF8, nil
	}
	decoded, err := japanese.ShiftJIS.NewDecoder().Bytes(data)
	if err != nil || bytes.ContainsRune(decoded, utf8.RuneError) {
		return nil, "", ErrUnknownEncoding
	}
	return decoded, EncodingShiftJIS, nil
}

// decode turns file bytes in the encoding into UTF-8 and drops a leading
// byte order mark; an empty encoding is detected by DetectText. UTF-8
// input must be valid UTF-8, or the result is ErrNotUTF8 rather than
// replacement characters; it is returned as it is, resliced past the
// mark, so a large file is not copied.
func (e Encoding) decode(data []byte) ([]byte, error) {
	if e == "" {
		decoded, _, err := DetectText(data)
		return decoded, err
	}
	if cs := e.charset(); cs != nil {
		decoded, err := cs.NewDecoder().Bytes(data)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", e, err)
		}
		return bytes.TrimPrefix(decoded, utf8BOM), nil
	}
	data = bytes.TrimPrefix(data, utf8BOM)
	if !utf8.Valid(data) {
		return nil, ErrNotUTF8
	}
	return data, nil
}
