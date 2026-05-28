// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import "encoding/json"

// Encode marshals v to JSON. Store adapters use this before [Collection.Put].
func Encode(v any) ([]byte, error) {
	return json.Marshal(v)
}

// Decode unmarshals rec.Data into v as JSON. Store adapters use this after
// receiving a [Record] from [Collection.Get] or [Collection.List].
func Decode[T any](rec *Record, v *T) error {
	return json.Unmarshal(rec.Data, v)
}
