// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package stringutil

import (
	"encoding/json"
	"strings"
)

type KeyValue string

// NewKeyValue constructs a KeyValue by concatenating key and value with an '=' separator.
// The key must not contain '=' characters to ensure round-trip correctness with Key() and Value().
// An empty value produces a trailing '=' (e.g., "KEY=").
func NewKeyValue(key, value string) KeyValue {
	return KeyValue(key + "=" + value)
}

func (kv KeyValue) Key() string {
	key, _, _ := strings.Cut(string(kv), "=")
	return key
}

func (kv KeyValue) Value() string {
	_, value, found := strings.Cut(string(kv), "=")
	if !found {
		return ""
	}
	return value
}

func (kv KeyValue) String() string {
	return string(kv)
}

func (kv KeyValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(kv.String())
}

func (kv *KeyValue) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*kv = KeyValue(s)
	return nil
}

// KeyValuesToMap converts a slice of "KEY=VALUE" strings into a map of keys to values.
// It splits each entry at the first '='; entries without '=' are skipped.
// Values may be empty (for example, "KEY=" results in map["KEY"] = "").
// Empty keys are allowed (for example, "=value" results in map[""] = "value").
func KeyValuesToMap(kvSlice []string) map[string]string {
	result := make(map[string]string, len(kvSlice))
	for _, kv := range kvSlice {
		key, value, found := strings.Cut(kv, "=")
		if found {
			result[key] = value
		}
	}
	return result
}
