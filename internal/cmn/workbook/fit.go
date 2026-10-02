// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import "encoding/json"

// FitRows keeps the longest prefix of rows whose JSON encoding fits the
// budget in bytes, and reports whether anything was dropped. A budget of
// zero or less means no limit.
func FitRows(rows []Row, budget int) ([]Row, bool) {
	if budget <= 0 || len(rows) == 0 {
		return rows, false
	}
	if encodedSize(rows) <= budget {
		return rows, false
	}
	// Encoded size grows with the prefix length, so the largest fitting
	// prefix can be found by bisection instead of dropping rows one by one.
	lo, hi := 0, len(rows)-1
	for lo < hi {
		mid := lo + (hi-lo+1)/2
		if encodedSize(rows[:mid]) <= budget {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return rows[:lo], true
}

func encodedSize(rows []Row) int {
	data, err := json.Marshal(rows)
	if err != nil {
		return 0
	}
	return len(data)
}
