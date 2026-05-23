// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"encoding/json"
	"fmt"
)

// Encode marshals v to JSON and returns the bytes and encoding type.
// Store adapters use this before calling [Collection.Put].
func Encode(v any) ([]byte, Encoding, error) {
	data, err := json.Marshal(v)
	return data, EncodingJSON, err
}

// Decode unmarshals rec.Data into v according to rec.Encoding.
// Store adapters use this after receiving a [Record] from [Collection.Get] or [Collection.List].
func Decode[T any](rec *Record, v *T) error {
	switch rec.Encoding {
	case EncodingJSON:
		return json.Unmarshal(rec.Data, v)
	case EncodingProto:
		return fmt.Errorf("persis: proto encoding is not supported by this decoder")
	default:
		return fmt.Errorf("persis: unsupported encoding %q", rec.Encoding)
	}
}
