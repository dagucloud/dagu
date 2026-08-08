// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package pagination

import (
	"encoding/base64"
	"encoding/json"
	"errors"
)

var ErrInvalidCursor = errors.New("invalid cursor")

// EncodeSearchCursor serializes an opaque search cursor as base64url JSON.
func EncodeSearchCursor(payload any) string {
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

// DecodeSearchCursor deserializes an opaque search cursor from base64url JSON.
func DecodeSearchCursor(raw string, dest any) error {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return ErrInvalidCursor
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return ErrInvalidCursor
	}
	return nil
}

// CursorResult contains a bounded result window and continuation state.
type CursorResult[T any] struct {
	Items      []T
	HasMore    bool
	NextCursor string
}
