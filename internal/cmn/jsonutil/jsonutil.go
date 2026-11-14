// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package jsonutil

import (
	"bytes"
	"encoding/json"
)

// MarshalUnescaped serializes a value as JSON, leaving <, > and & as the
// characters the value holds rather than as escape sequences. The encoded
// trailing newline is removed.
func MarshalUnescaped(v any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
