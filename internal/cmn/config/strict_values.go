// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"math"
	"sort"
)

// The blocks agent_tools, ai, untrusted and coordinator.worker_pools are closed.
// These helpers read their values and make their error messages. The loader
// changes no value to make it fit.

// showConfigValue shows a raw value in an error message.
func showConfigValue(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return fmt.Sprintf("%q", v)
	case []any:
		return fmt.Sprintf("a list with %d items", len(v))
	case map[string]any:
		return "an object"
	default:
		return fmt.Sprintf("%v (%T)", v, v)
	}
}

// configInt reads an integer of the YAML file. It accepts no string and no
// number with a decimal point, such as 3.0: the loader does not coerce.
func configInt(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case uint64:
		return int64(v), v <= math.MaxInt64
	}
	return 0, false
}

// sortedMapKeys returns the keys of the map in sorted order.
func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
