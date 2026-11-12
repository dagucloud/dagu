// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin || (windows && (amd64 || arm64))

package desktop

import "fmt"

// columnIndexes maps each wanted column name to its grid column, by the
// header that names it, comparing as a person reads. A column with no
// header is a miss.
func columnIndexes(columns, headers []string) ([]int, error) {
	byName := make(map[string]int, len(headers))
	for i, head := range headers {
		byName[NormalizeText(head)] = i
	}
	indexes := make([]int, len(columns))
	for i, column := range columns {
		index, ok := byName[NormalizeText(column)]
		if !ok {
			return nil, fmt.Errorf("%w: no column is headed %q", ErrNotFound, column)
		}
		indexes[i] = index
	}
	return indexes, nil
}
