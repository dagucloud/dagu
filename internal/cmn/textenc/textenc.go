// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package textenc reads text files whose encoding is not declared, as
// Japanese Windows writes them: UTF-8, with or without a byte order mark,
// or Shift_JIS.
package textenc

import (
	"bytes"
	"errors"
	"unicode/utf8"

	"golang.org/x/text/encoding/japanese"
)

// UTF8BOM is the UTF-8 byte order mark.
const UTF8BOM = "\xEF\xBB\xBF"

// Decoding errors.
var (
	// ErrNotUTF8 is a file read as UTF-8 that is not valid UTF-8.
	ErrNotUTF8 = errors.New("not valid UTF-8")
	// ErrUnknownEncoding is a file, read without an encoding, that is
	// neither UTF-8 nor Shift_JIS.
	ErrUnknownEncoding = errors.New("not valid UTF-8 or Shift_JIS")
)

// Detect decodes a text file whose encoding is not given: a UTF-8 byte
// order mark means UTF-8, valid UTF-8 is UTF-8, and otherwise the bytes
// are Shift_JIS when every one decodes as it. The mark is dropped, and
// UTF-8 input is returned as it is, resliced past the mark, so a large
// file is not copied. Bytes that are neither are ErrUnknownEncoding, so a
// file in some other encoding fails instead of turning into replacement
// characters.
func Detect(data []byte) ([]byte, error) {
	if rest, marked := bytes.CutPrefix(data, []byte(UTF8BOM)); marked {
		if !utf8.Valid(rest) {
			return nil, ErrNotUTF8
		}
		return rest, nil
	}
	if utf8.Valid(data) {
		return data, nil
	}
	decoded, err := japanese.ShiftJIS.NewDecoder().Bytes(data)
	if err != nil || bytes.ContainsRune(decoded, utf8.RuneError) {
		return nil, ErrUnknownEncoding
	}
	return decoded, nil
}
