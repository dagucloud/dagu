// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"encoding/json"
	"math"
)

// FitRows keeps the longest prefix of rows whose JSON encoding fits the
// budget in bytes, and reports whether anything was dropped. A budget of
// zero or less means no limit.
func FitRows(rows []Row, budget int) ([]Row, bool) {
	return FitJSON(rows, budget)
}

// FitJSON keeps the longest prefix of items whose JSON encoding fits the
// budget in bytes, and reports whether anything was dropped. A budget of
// zero or less means no limit.
func FitJSON[T any](items []T, budget int) ([]T, bool) {
	if budget <= 0 || len(items) == 0 {
		return items, false
	}
	if encodedSize(items) <= budget {
		return items, false
	}
	// Encoded size grows with the prefix length, so the largest fitting
	// prefix can be found by bisection instead of dropping items one by one.
	lo, hi := 0, len(items)-1
	for lo < hi {
		mid := lo + (hi-lo+1)/2
		if encodedSize(items[:mid]) <= budget {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return items[:lo], true
}

// encodedSize is the JSON size of items; a value that cannot be encoded
// counts as larger than any budget, so it is never reported as fitting.
func encodedSize[T any](items []T) int {
	data, err := json.Marshal(items)
	if err != nil {
		return math.MaxInt
	}
	return len(data)
}
