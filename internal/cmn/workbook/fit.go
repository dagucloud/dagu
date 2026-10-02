// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import "encoding/json"

// FitRows drops rows from the end until the JSON encoding of rows fits the
// budget in bytes. It reports whether anything was dropped. A budget of zero
// or less means no limit.
func FitRows(rows []Row, budget int) ([]Row, bool) {
	if budget <= 0 || len(rows) == 0 {
		return rows, false
	}
	size := encodedSize(rows)
	if size <= budget {
		return rows, false
	}
	// Shrink proportionally first, then one row at a time.
	keep := len(rows) * budget / size
	if keep >= len(rows) {
		keep = len(rows) - 1
	}
	rows = rows[:keep]
	for len(rows) > 0 && encodedSize(rows) > budget {
		rows = rows[:len(rows)-1]
	}
	return rows, true
}

func encodedSize(rows []Row) int {
	data, err := json.Marshal(rows)
	if err != nil {
		return 0
	}
	return len(data)
}
